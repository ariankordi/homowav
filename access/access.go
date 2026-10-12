// Package access decides who may use the local endpoints: the credential
// API, the upstream forwarder and the settings API.
//
// Three callers are admitted:
//
//   - a process on this machine (loopback), addressed by a loopback name;
//   - a script sending "Authorization: Bearer <secret>";
//   - a browser on the LAN that opened the pairing link once (cookie).
//
// Browsers additionally have to prove a request was made by our own page,
// because a session cookie or a loopback address alone would let any website
// the user visits send requests here.
package access

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ClientHeader must be present on state-changing requests. A foreign page
	// cannot add a custom header without a CORS preflight, which is never granted.
	ClientHeader = "X-HW-Client"
	// PairCookie carries the secret for a paired browser.
	PairCookie = "hw_pair"
	// PairPath is the public route the pairing link points at.
	PairPath = "/pair"
	// pairParam is the query parameter of the pairing link.
	pairParam = "key"
	// pairCookieLifetime keeps a phone paired for a year.
	pairCookieLifetime = 365 * 24 * time.Hour
)

// Gate admits callers as described in the package comment.
type Gate struct {
	// Secret returns the current pairing secret. An empty secret disables
	// every non-loopback caller.
	Secret func() string
}

// Wrap guards next with the gate.
func (g Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := g.secret()
		byBearer := matches(r.Header.Get("Authorization"), "Bearer "+secret, secret)
		if !byBearer && !g.admitted(r, secret) {
			http.Error(w, "forbidden: open the pairing link from Settings on this device first", http.StatusForbidden)
			return
		}
		// A bearer token already proves the caller is not a foreign web page.
		if !byBearer && !isSafeMethod(r.Method) && !fromOwnPage(r) {
			http.Error(w, "forbidden: request did not come from the hwclient page", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Admitted reports whether r would pass the caller check (not the own-page
// check). Pages use it to decide between the app and a "pair first" notice.
func (g Gate) Admitted(r *http.Request) bool {
	secret := g.secret()
	return matches(r.Header.Get("Authorization"), "Bearer "+secret, secret) || g.admitted(r, secret)
}

// PairHandler serves PairPath: a correct key sets the pairing cookie and
// redirects to the UI.
func (g Gate) PairHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := g.secret()
		if !matches(r.URL.Query().Get(pairParam), secret, secret) {
			http.Error(w, "forbidden: wrong or missing pairing key", http.StatusForbidden)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     PairCookie,
			Value:    secret,
			Path:     "/",
			MaxAge:   int(pairCookieLifetime.Seconds()),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}

// PairURL builds the pairing link for a base such as "http://192.168.1.5:8080".
func PairURL(base, secret string) string {
	return base + PairPath + "?" + url.Values{pairParam: {secret}}.Encode()
}

func (g Gate) secret() string {
	if g.Secret == nil {
		return ""
	}
	return g.Secret()
}

// admitted checks loopback and the pairing cookie.
func (g Gate) admitted(r *http.Request, secret string) bool {
	if cookie, err := r.Cookie(PairCookie); err == nil && matches(cookie.Value, secret, secret) {
		return true
	}
	// The Host check defeats DNS rebinding: a hostile page can make the
	// browser connect to 127.0.0.1, but the Host header keeps its own name.
	return IsLoopback(r.RemoteAddr) && isLoopbackName(r.Host)
}

// matches compares in constant time and never matches while no secret is set.
func matches(got, want, secret string) bool {
	return secret != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// fromOwnPage reports a request made by script on a page this server served.
func fromOwnPage(r *http.Request) bool {
	if r.Header.Get(ClientHeader) == "" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Not a browser, or a same-origin GET-like request.
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host)
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// IsLoopback reports whether a RemoteAddr is on this machine.
func IsLoopback(remoteAddr string) bool {
	ip := net.ParseIP(hostOnly(remoteAddr))
	return ip != nil && ip.IsLoopback()
}

// isLoopbackName reports whether a Host header names this machine directly.
func isLoopbackName(hostport string) bool {
	host := hostOnly(hostport)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hostOnly strips an optional port and IPv6 brackets.
func hostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}
