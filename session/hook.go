package session

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"hwclient/proxykit"
)

// maxRecoveries bounds retries per request after the first replay: one with a
// session another request already renewed, and one after our own refetch.
const maxRecoveries = 2

// Service ties the Store and Renewer to the proxy hook and the local API.
type Service struct {
	Store   *Store
	Renewer *Renewer
	Log     *slog.Logger
	// Client and BaseURL ("https://host") are used by Call. Leave them zero if
	// only the hook and the credential endpoints are needed.
	Client  *http.Client
	BaseURL string
}

// Hook is the proxykit hook for the API host. For JSON API calls it:
//
//  1. sends the stored session instead of the app's if they differ, so a dead
//     session in the phone's cookie jar costs no failed round trip;
//  2. records credentials the server accepted;
//  3. on a failure a dead session could cause, finds a working session and
//     replays the request before the app sees the failure;
//  4. tells the app the live session with Set-Cookie, so its jar converges.
//
// Everything outside /api/ is forwarded untouched.
func (s *Service) Hook(f *proxykit.Flow) {
	if !strings.HasPrefix(f.Request.URL.Path, apiPrefix) {
		return
	}
	requestBody, err := f.RequestBody()
	if err != nil {
		return // Too large to replay; let it stream through untouched.
	}
	appSession := requestCookie(f.Request, sessionCookie)
	used := appSession
	if stored := s.Store.Snapshot().PHPSESSID; stored != "" && stored != appSession && !isAuthPath(f.Request.URL.Path) {
		replaceCookie(f.Request.Header, sessionCookie, stored)
		used = stored
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
	case env.maybeStaleSession() && !isAuthPath(f.Request.URL.Path):
		var recovered bool
		if used, recovered = s.recover(f, requestBody, appSession, used, !env.loggedOut()); !recovered {
			return // The app gets the failure and runs its own recovery.
		}
		res, _ = f.Forward() // Cached: Reforward left the accepted reply in place.
	default:
		return // A failure that is not about the session.
	}
	s.offerSession(f, res, appSession, used)
}

// recover looks for a session the server accepts and replays the request with
// it, leaving that reply as the flow's response. used is the session the failed
// attempt carried. speculative marks an ambiguous failure (see Renewer.Recover).
// Returns the accepted session, or false if every attempt failed.
func (s *Service) recover(f *proxykit.Flow, requestBody []byte, appSession, used string, speculative bool) (string, bool) {
	failed := used
	// Cheapest first: if we swapped the app's session out and the swap was the
	// problem (for example a stale state file), the app's own may still be live.
	if appSession != "" && appSession != used {
		if s.attempt(f, requestBody, appSession) {
			return appSession, true
		}
		failed = appSession
	}
	for range maxRecoveries {
		session, err := s.Renewer.Recover(f.Request.Context(), failed, speculative)
		if err != nil {
			return used, false
		}
		if s.attempt(f, requestBody, session) {
			f.Log.Info("request recovered", "failed", Fingerprint(used), "session", Fingerprint(session))
			return session, true
		}
		failed = session
	}
	return used, false
}

// attempt replays the request with session and reports whether the server
// accepted it. Replies that are not an {"ok":...} envelope (Pusher's auth
// reply, for one) count as accepted: they are not a session failure.
func (s *Service) attempt(f *proxykit.Flow, requestBody []byte, session string) bool {
	replaceCookie(f.Request.Header, sessionCookie, session)
	res, err := f.Reforward()
	if err != nil {
		f.Log.Warn("replay failed", "err", err)
		return false
	}
	env, ok := inspect(res)
	if ok && env.succeeded() {
		s.commit(f, requestBody, res.Header, env)
	}
	return !ok || !env.failed()
}

// offerSession adds Set-Cookie for the live session when the app sent a
// different one, so the phone's cookie jar stops presenting a dead session.
// It logs what it offers: if the app keeps sending its old value afterwards,
// these lines show it ignores the cookie.
func (s *Service) offerSession(f *proxykit.Flow, res *proxykit.Response, appSession, used string) {
	if used == "" || used == appSession || res.StatusCode != http.StatusOK || setsCookie(res.Header, sessionCookie) {
		return
	}
	res.Header.Add("Set-Cookie", (&http.Cookie{Name: sessionCookie, Value: used, Path: "/"}).String())
	f.Log.Info("offered live session to app", "app_had", Fingerprint(appSession), "live", Fingerprint(used))
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
