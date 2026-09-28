package session

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
)

// renewParam on any value endpoint forces a refetch before answering.
const renewParam = "renew"

// Status describes the captured credentials without revealing them.
type Status struct {
	HaveSession        bool      `json:"have_session"`
	SessionFingerprint string    `json:"session_fingerprint"`
	SessionUpdated     time.Time `json:"session_updated,omitzero"`
	SessionValidated   time.Time `json:"session_validated,omitzero"`
	CanRenew           bool      `json:"can_renew"`
	RememberExpires    time.Time `json:"remember_expires,omitzero"`
	HaveJWT            bool      `json:"have_jwt"`
	JWTExpires         time.Time `json:"jwt_expires,omitzero"`
	JWTExpired         bool      `json:"jwt_expired"`
	HaveVoipToken      bool      `json:"have_voip_token"`
	RenewAttempts      int64     `json:"renew_attempts"`
}

// Status summarizes the current credentials for display.
func (s *Service) Status() Status {
	c := s.Store.Snapshot()
	return Status{
		HaveSession:        c.PHPSESSID != "",
		SessionFingerprint: Fingerprint(c.PHPSESSID),
		SessionUpdated:     c.SessionUpdated,
		SessionValidated:   c.SessionValidated,
		CanRenew:           len(c.Remember) > 0,
		RememberExpires:    c.RememberExpires,
		HaveJWT:            c.JWTAccess != "",
		JWTExpires:         c.JWTExpires,
		JWTExpired:         !c.JWTExpires.IsZero() && time.Now().After(c.JWTExpires),
		HaveVoipToken:      c.VoipToken != "",
		RenewAttempts:      s.Renewer.Attempts(),
	}
}

// Handler serves the local credential endpoints:
//
//	GET /_/sessid   PHPSESSID as text/plain
//	GET /_/token    JWT access token as text/plain (may be expired; see /_/session)
//	GET /_/cookie   a ready-to-use Cookie header value
//	GET /_/session  JSON Status, no secrets
//
// Add ?renew=1 to a value endpoint to fetch a new session first. Callers must
// be on loopback, or send "Authorization: Bearer <secret>" when secret is set.
func (s *Service) Handler(secret string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /_/sessid", s.valueHandler(func(c Creds) string { return c.PHPSESSID }))
	mux.Handle("GET /_/token", s.valueHandler(func(c Creds) string { return c.JWTAccess }))
	mux.Handle("GET /_/cookie", s.valueHandler(Creds.CookieHeader))
	mux.HandleFunc("GET /_/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "\t")
		encoder.Encode(s.Status())
	})
	return requireLocalOrSecret(secret, mux)
}

// valueHandler serves one credential as text/plain with no trailing newline,
// so $(curl -s .../_/sessid) needs no trimming.
func (s *Service) valueHandler(value func(Creds) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Query().Has(renewParam) {
			if _, err := s.Renewer.Renew(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}
		v := value(s.Store.Snapshot())
		if v == "" {
			http.Error(w, "not captured yet: open the app through the proxy", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, v)
	})
}

// requireLocalOrSecret admits loopback callers, and others only with the
// shared secret. The PAC is served elsewhere and stays public for the phone.
func requireLocalOrSecret(secret string, next http.Handler) http.Handler {
	want := []byte("Bearer " + secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized := secret != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) == 1
		if !isLoopback(r.RemoteAddr) && !authorized {
			http.Error(w, "forbidden: credential endpoints are loopback-only unless an API secret is set", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
