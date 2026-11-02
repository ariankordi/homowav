package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// CallTimeout bounds one Call attempt, including a voice upload.
	CallTimeout = 2 * time.Minute
	// maxCallReply caps a buffered API reply. Thread pages are a few hundred KiB.
	maxCallReply = 32 << 20
)

// ErrNoSession means nothing has been captured, so there is nothing to send
// and nothing to renew with.
var ErrNoSession = errors.New("session: no session yet; open the app once through the proxy, or sign in under Settings")

// Reply is a buffered upstream response.
type Reply struct {
	Status int
	Header http.Header
	Body   []byte
}

// Call sends one API request as the logged-in app and returns the reply.
// pathAndQuery starts with "/". header carries the caller's content headers;
// Cookie and User-Agent are always set here. If the reply looks like a dead
// session, Call gets a working one (shared with the phone through the hook)
// and retries once, so callers never handle logouts themselves.
func (s *Service) Call(ctx context.Context, method, pathAndQuery string, header http.Header, body []byte) (*Reply, error) {
	if s.Client == nil || s.BaseURL == "" {
		return nil, errors.New("session: Service.Client and BaseURL are not configured")
	}
	used := s.Store.Snapshot().PHPSESSID
	if used == "" {
		renewed, err := s.Renewer.Renew(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w (%v)", ErrNoSession, err)
		}
		used = renewed
	}
	reply, env, ok, err := s.send(ctx, method, pathAndQuery, header, body, used)
	if err != nil {
		return nil, err
	}
	if ok && env.maybeStaleSession() {
		// Ambiguous failures are rate-limited inside Recover, so a request that
		// is simply invalid cannot mint a new session on every attempt.
		if next, recoverErr := s.Renewer.Recover(ctx, used, !env.loggedOut()); recoverErr == nil {
			retried, retriedEnv, retriedOK, retryErr := s.send(ctx, method, pathAndQuery, header, body, next)
			if retryErr == nil && (!retriedOK || !retriedEnv.failed()) {
				s.Log.Info("call recovered", "path", pathAndQuery, "failed", Fingerprint(used), "session", Fingerprint(next))
				reply, env, ok, used = retried, retriedEnv, retriedOK, next
			}
		}
	}
	if ok && env.succeeded() {
		s.noteAccepted(used, reply.Header, env)
	}
	return reply, nil
}

// send performs one attempt with the given session.
func (s *Service) send(ctx context.Context, method, pathAndQuery string, header http.Header, body []byte, session string) (*Reply, envelope, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, s.BaseURL+pathAndQuery, bytes.NewReader(body))
	if err != nil {
		return nil, envelope{}, false, err
	}
	req.Header = header.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	creds := s.Store.Snapshot()
	creds.PHPSESSID = session
	req.Header.Set("Cookie", creds.CookieHeader())
	req.Header.Set("User-Agent", firstNonEmpty(creds.RefetchUserAgent, defaultRefetchUserAgent))

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, envelope{}, false, fmt.Errorf("calling %s: %w", req.URL.Path, err)
	}
	defer resp.Body.Close()
	replyBody, err := io.ReadAll(io.LimitReader(resp.Body, maxCallReply))
	if err != nil {
		return nil, envelope{}, false, fmt.Errorf("reading reply of %s: %w", req.URL.Path, err)
	}
	env, ok := parseEnvelope(resp.Header.Get("Content-Type"), replyBody)
	return &Reply{Status: resp.StatusCode, Header: resp.Header, Body: replyBody}, env, ok, nil
}

// noteAccepted records that the server accepted session, plus any cookies the
// reply set, exactly as the hook does for the app's own traffic.
func (s *Service) noteAccepted(session string, header http.Header, env envelope) {
	now := time.Now()
	obs := observation{session: session, remember: map[string]string{}}
	obs.addResponse(header, env, now)
	if err := s.Store.Update(func(c *Creds) bool { return obs.apply(c, now) }); err != nil {
		s.Log.Error("saving credentials", "err", err)
	}
}
