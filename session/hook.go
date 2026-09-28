package session

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"homowav/proxykit"
)

// maxRecoveries bounds retries per request: one with a session another
// request already renewed, and one after our own refetch.
const maxRecoveries = 2

// Service ties the Store and Renewer to the proxy hook and the local API.
type Service struct {
	Store   *Store
	Renewer *Renewer
	Log     *slog.Logger
}

// Hook is the proxykit hook for the API host. For JSON API calls it records
// credentials the server accepted, and if the server reports a logout it
// renews the session and replays the request before the app sees the failure.
// Everything else is forwarded untouched.
func (s *Service) Hook(f *proxykit.Flow) {
	if !strings.HasPrefix(f.Request.URL.Path, apiPrefix) {
		return
	}
	requestBody, err := f.RequestBody()
	if err != nil {
		return // Too large to replay; let it stream through untouched.
	}
	res, err := f.Forward()
	if err != nil {
		return // proxykit turns this into a 502.
	}
	env, ok := inspect(res)
	switch {
	case !ok:
	case env.succeeded():
		s.commit(f, requestBody, res.Header, env)
	case env.loggedOut():
		s.recover(f, requestBody)
	}
}

// recover swaps in a working PHPSESSID and replays the request. If renewal
// fails, the logged-out reply goes to the app so its own recovery runs.
func (s *Service) recover(f *proxykit.Flow, requestBody []byte) {
	failed := requestCookie(f.Request, sessionCookie)
	for range maxRecoveries {
		session, err := s.Renewer.Recover(f.Request.Context(), failed)
		if err != nil {
			return
		}
		replaceCookie(f.Request.Header, sessionCookie, session)
		res, err := f.Reforward()
		if err != nil {
			f.Log.Warn("replay after renewal failed", "err", err)
			return
		}
		env, ok := inspect(res)
		if !ok || !env.loggedOut() {
			if ok && env.succeeded() {
				s.commit(f, requestBody, res.Header, env)
				// Hand the app the working session so its next call succeeds directly.
				if !setsCookie(res.Header, sessionCookie) {
					res.Header.Add("Set-Cookie", (&http.Cookie{Name: sessionCookie, Value: session, Path: "/"}).String())
				}
				f.Log.Info("request recovered", "failed", Fingerprint(failed), "session", Fingerprint(session))
			}
			return
		}
		failed = session
	}
}

// commit stores what a successful exchange revealed.
func (s *Service) commit(f *proxykit.Flow, requestBody []byte, header http.Header, env envelope) {
	now := time.Now()
	obs := observeRequest(f.Request, requestBody)
	obs.addResponse(header, env, now)
	var changed bool
	err := s.Store.Update(func(c *Creds) bool {
		changed = obs.apply(c, now)
		return changed
	})
	if err != nil {
		f.Log.Error("saving credentials", "err", err)
	}
	if changed {
		f.Log.Info("credentials updated", "session", Fingerprint(obs.session), "jwt", Fingerprint(obs.jwtAccess))
	}
}

// inspect buffers a response body and decodes the API envelope, if it is one.
func inspect(res *proxykit.Response) (envelope, bool) {
	body, err := res.Body()
	if err != nil {
		return envelope{}, false
	}
	return parseEnvelope(res.Header.Get("Content-Type"), body)
}

// requestCookie returns the named cookie's value, or "".
func requestCookie(r *http.Request, name string) string {
	if cookie, err := r.Cookie(name); err == nil {
		return cookie.Value
	}
	return ""
}

// replaceCookie sets name=value in the Cookie header, keeping every other
// cookie byte-for-byte. Multiple Cookie lines (as HTTP/2 allows) are merged.
func replaceCookie(header http.Header, name, value string) {
	parts := []string{name + "=" + value}
	for _, line := range header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part != "" && !strings.HasPrefix(part, name+"=") {
				parts = append(parts, part)
			}
		}
	}
	header.Set("Cookie", strings.Join(parts, "; "))
}

// setsCookie reports whether a response already sets the named cookie.
func setsCookie(header http.Header, name string) bool {
	for _, line := range header.Values("Set-Cookie") {
		if strings.HasPrefix(line, name+"=") {
			return true
		}
	}
	return false
}
