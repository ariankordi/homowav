package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// refetchTimeout bounds one refetch-session call.
	refetchTimeout = 15 * time.Second
	// failureBackoff stops a burst of logged-out requests from each hammering
	// refetch-session after it has just failed.
	failureBackoff = 5 * time.Second
	// maxRefetchBody caps the refetch reply; it is a user record, a few KiB.
	maxRefetchBody = 1 << 20
	// defaultRefetchUserAgent is what app 2.28.0 sent, used until one is observed.
	defaultRefetchUserAgent = "HomeWAV/2.28.0 (com.homewav.app; build:493; iOS 17.0.0) Alamofire/5.10.2"
)

// ErrNoRememberCookies means renewal is impossible until the app has been
// used through the proxy at least once.
var ErrNoRememberCookies = errors.New("session: no remember cookies captured yet; open the app once through the proxy")

// Renewer exchanges the remember cookies for a fresh PHPSESSID. Concurrent
// callers are serialized, so a burst of logged-out requests costs one refetch.
type Renewer struct {
	store   *Store
	client  *http.Client
	baseURL string
	log     *slog.Logger

	mu          sync.Mutex
	lastFailure time.Time
	lastErr     error
	attempts    atomic.Int64
}

// NewRenewer sends refetches to https://host through transport, which should
// be the same one the proxy uses (so tests and custom dialers apply to both).
func NewRenewer(store *Store, transport http.RoundTripper, host string, log *slog.Logger) *Renewer {
	return &Renewer{
		store:   store,
		client:  &http.Client{Transport: transport, Timeout: refetchTimeout},
		baseURL: "https://" + host,
		log:     log,
	}
}

// Attempts reports how many refetch requests have been sent (for tests and status).
func (r *Renewer) Attempts() int64 { return r.attempts.Load() }

// Recover returns a PHPSESSID to retry with after failed was rejected as
// logged out. If the store already holds a different session (another
// request renewed it, or the app logged in again), that one is returned
// without contacting the server.
func (r *Renewer) Recover(ctx context.Context, failed string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.store.Snapshot().PHPSESSID; current != "" && current != failed {
		return current, nil
	}
	return r.refetchLocked(ctx)
}

// Renew always asks the server for a new PHPSESSID.
func (r *Renewer) Renew(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refetchLocked(ctx)
}

// KeepAlive renews every interval until ctx ends. Each refetch also slides the
// remember cookies' 30-day expiry forward, so the session survives even if the
// app is never opened.
func (r *Renewer) KeepAlive(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if len(r.store.Snapshot().Remember) == 0 {
				continue
			}
			if _, err := r.Renew(ctx); err != nil {
				r.log.Warn("keepalive renewal failed", "err", err)
			}
		}
	}
}

// refetchLocked runs one refetch unless one just failed. The caller holds r.mu.
func (r *Renewer) refetchLocked(ctx context.Context) (string, error) {
	if !r.lastFailure.IsZero() && time.Since(r.lastFailure) < failureBackoff {
		return "", r.lastErr
	}
	// Detach from the triggering client: if the phone gives up mid-refetch, the
	// requests queued behind it still want the result.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refetchTimeout)
	defer cancel()
	session, err := r.refetch(ctx)
	if err != nil {
		r.lastFailure, r.lastErr = time.Now(), err
		r.log.Warn("session renewal failed", "err", err)
		return "", err
	}
	r.lastFailure, r.lastErr = time.Time{}, nil
	return session, nil
}

// refetch performs POST /api/app/refetch-session/v2 the way the app does and
// stores the resulting credentials.
func (r *Renewer) refetch(ctx context.Context) (string, error) {
	creds := r.store.Snapshot()
	if len(creds.Remember) == 0 {
		return "", ErrNoRememberCookies
	}
	form := url.Values{
		"jwtAccess":      {creds.JWTAccess},
		"jwtRefresh":     {creds.JWTRefresh},
		refetchVoipField: {creds.VoipToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+refetchPath, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", firstNonEmpty(creds.RefetchUserAgent, defaultRefetchUserAgent))
	req.Header.Set("Authorization", strings.TrimSpace("Bearer "+creds.JWTAccess))
	req.Header.Set("Refresh-Token", creds.JWTRefresh)
	// Only the remember cookies: the capture shows refetch works without a PHPSESSID.
	req.Header.Set("Cookie", Creds{Remember: creds.Remember}.CookieHeader())

	r.attempts.Add(1)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("refetch-session: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRefetchBody))
	if err != nil {
		return "", fmt.Errorf("refetch-session: reading reply: %w", err)
	}
	env, ok := parseEnvelope(resp.Header.Get("Content-Type"), body)
	if resp.StatusCode != http.StatusOK || !ok || !env.succeeded() {
		return "", fmt.Errorf("refetch-session: HTTP %d, ok=%v, cause=%q", resp.StatusCode, ok && env.succeeded(), env.Cause)
	}

	now := time.Now()
	obs := observation{remember: map[string]string{}}
	obs.addResponse(resp.Header, env, now)
	if obs.session == "" {
		return "", errors.New("refetch-session: reply set no PHPSESSID")
	}
	if err := r.store.Update(func(c *Creds) bool { return obs.apply(c, now) }); err != nil {
		r.log.Error("saving renewed session", "err", err)
	}
	r.log.Info("session renewed", "old", Fingerprint(creds.PHPSESSID), "new", Fingerprint(obs.session))
	return obs.session, nil
}
