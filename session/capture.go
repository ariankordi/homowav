package session

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Facts about the HomeWAV API, from a capture of iOS app 2.28.0.
const (
	// DefaultHost is the API host the app talks to.
	DefaultHost = "app.homewav.com"
	// sessionCookie is the PHP session cookie that authorizes calls.
	sessionCookie = "PHPSESSID"
	// apiPrefix scopes the hook: only JSON API paths are inspected.
	apiPrefix = "/api/"
	// refetchPath exchanges the remember cookies for a new PHPSESSID.
	refetchPath = "/api/app/refetch-session/v2"
	// loginPath checks the password. Its failures are the user's, never a stale session.
	loginPath = "/api/app/login/"
	// loggedOutCause is the "cause" of an {"ok":false} reply to an expired
	// session. The server sends it with HTTP 200, never 401.
	loggedOutCause = "You must be logged in to access the requested page"
	// permissionCause means the session is fine but the account may not do this.
	permissionCause = "You do not have permission to view this page"
	// jwtParts is the number of dot-separated segments in a JWT.
	jwtParts = 3
	// Form fields that carry the voip token: login uses one name, refetch the other.
	loginVoipField   = "voipToken"
	refetchVoipField = "token"
	// inmatesPath lists the account's contacts; its form field "id" is the visitor id.
	inmatesPath = "/api/app/inmates-by-visitor"
	// inmatesVisitorField is that form field.
	inmatesVisitorField = "id"
	// visitorIDParam names the visitor id everywhere else (message/init's
	// query, message/send's form).
	visitorIDParam = "visitor_id"
)

// rememberCookies are the long-lived (30-day) cookies that refetch-session accepts.
var rememberCookies = []string{"asps", "esur"}

// envelope is the {ok, cause, body} wrapper the JSON API uses for every reply.
type envelope struct {
	OK    *bool           `json:"ok"`
	Cause string          `json:"cause"`
	Body  json.RawMessage `json:"body"`
}

// succeeded reports an explicit {"ok": true}.
func (e envelope) succeeded() bool { return e.OK != nil && *e.OK }

// loggedOut reports the specific "session expired" failure.
func (e envelope) loggedOut() bool { return e.OK != nil && !*e.OK && e.Cause == loggedOutCause }

// failed reports an explicit {"ok": false}.
func (e envelope) failed() bool { return e.OK != nil && !*e.OK }

// maybeStaleSession reports a failure that a dead session could cause. The
// server does not always say "logged in": a dead session on
// /api/app/prisons/<id> (opening a conversation) gives "Invalid request".
// Permission errors are the account's, so a new session cannot fix them.
func (e envelope) maybeStaleSession() bool { return e.failed() && e.Cause != permissionCause }

// isAuthPath reports the calls that create sessions, which are never retried
// with a different session (a wrong password must not look like a logout).
func isAuthPath(path string) bool { return path == loginPath || path == refetchPath }

// parseEnvelope decodes body if the content type is JSON and it has an "ok" field.
func parseEnvelope(contentType string, body []byte) (envelope, bool) {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType != "application/json" {
		return envelope{}, false
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil || env.OK == nil {
		return envelope{}, false
	}
	return env, true
}

// observation is what one request/response exchange revealed. Values are
// only committed to the Store once the server accepted the request.
type observation struct {
	session          string
	remember         map[string]string
	rememberExpires  time.Time
	jwtAccess        string
	jwtRefresh       string
	voipToken        string
	refetchUserAgent string
	visitorID        string
}

// observeRequest reads credentials the app sent. body is the buffered
// request body (may be nil).
func observeRequest(r *http.Request, body []byte) observation {
	obs := observation{remember: map[string]string{}}
	for _, cookie := range r.Cookies() {
		obs.noteCookie(cookie.Name, cookie.Value)
	}
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && isJWT(token) {
		obs.jwtAccess = token
	}
	visitorID := r.URL.Query().Get(visitorIDParam)
	if isForm(r.Header.Get("Content-Type")) {
		if form, err := url.ParseQuery(string(body)); err == nil {
			obs.voipToken = firstNonEmpty(form.Get(loginVoipField), form.Get(refetchVoipField))
			visitorID = firstNonEmpty(visitorID, form.Get(visitorIDParam))
			if r.URL.Path == inmatesPath {
				visitorID = firstNonEmpty(visitorID, form.Get(inmatesVisitorField))
			}
		}
	}
	if isDigits(visitorID) {
		obs.visitorID = visitorID
	}
	if r.URL.Path == refetchPath {
		obs.refetchUserAgent = r.Header.Get("User-Agent")
	}
	return obs
}

// addResponse layers what a successful response set on top of the request's values.
func (o *observation) addResponse(header http.Header, env envelope, now time.Time) {
	for _, line := range header.Values("Set-Cookie") {
		cookie, err := http.ParseSetCookie(line)
		if err != nil || cookie.MaxAge < 0 {
			continue // Deletions carry no credential.
		}
		if o.noteCookie(cookie.Name, cookie.Value) && cookie.Name != sessionCookie {
			o.rememberExpires = cookieExpiry(cookie, now)
		}
	}
	// Only login returns JWTs in the body; the field names are its own.
	var tokens struct {
		JWTAccess  string `json:"jwt_access"`
		JWTRefresh string `json:"jwt_refresh"`
	}
	if json.Unmarshal(env.Body, &tokens) == nil {
		if isJWT(tokens.JWTAccess) {
			o.jwtAccess = tokens.JWTAccess
		}
		o.jwtRefresh = firstNonEmpty(tokens.JWTRefresh, o.jwtRefresh)
	}
}

// noteCookie records a cookie if it is one we track and reports whether it was.
func (o *observation) noteCookie(name, value string) bool {
	if value == "" {
		return false
	}
	if name == sessionCookie {
		o.session = value
		return true
	}
	for _, remembered := range rememberCookies {
		if name == remembered {
			o.remember[name] = value
			return true
		}
	}
	return false
}

// apply merges the observation into c, which the server just accepted, so a
// matching PHPSESSID is marked validated. Returns whether anything worth
// persisting changed (a validation timestamp alone is not).
func (o observation) apply(c *Creds, now time.Time) (changed bool) {
	set := func(dst *string, value string) {
		if value != "" && *dst != value {
			*dst, changed = value, true
		}
	}
	if o.session != "" && o.session != c.PHPSESSID {
		c.PHPSESSID, c.SessionUpdated, changed = o.session, now, true
	}
	if o.session != "" && o.session == c.PHPSESSID {
		c.SessionValidated = now
	}
	if len(o.remember) > 0 && !maps.Equal(o.remember, pick(c.Remember, o.remember)) {
		if c.Remember == nil {
			c.Remember = map[string]string{}
		}
		maps.Copy(c.Remember, o.remember)
		changed = true
	}
	if !o.rememberExpires.IsZero() && !o.rememberExpires.Equal(c.RememberExpires) {
		c.RememberExpires, changed = o.rememberExpires, true
	}
	if o.jwtAccess != "" && o.jwtAccess != c.JWTAccess {
		c.JWTAccess, c.JWTExpires, changed = o.jwtAccess, jwtExpiry(o.jwtAccess), true
	}
	set(&c.JWTRefresh, o.jwtRefresh)
	set(&c.VoipToken, o.voipToken)
	set(&c.RefetchUserAgent, o.refetchUserAgent)
	set(&c.VisitorID, o.visitorID)
	return changed
}

// pick returns the entries of m whose keys appear in keys, for comparison.
func pick(m, keys map[string]string) map[string]string {
	out := make(map[string]string, len(keys))
	for key := range keys {
		if value, ok := m[key]; ok {
			out[key] = value
		}
	}
	return out
}

// cookieExpiry prefers Max-Age over Expires, as browsers do (RFC 6265 5.3).
func cookieExpiry(cookie *http.Cookie, now time.Time) time.Time {
	if cookie.MaxAge > 0 {
		return now.Add(time.Duration(cookie.MaxAge) * time.Second)
	}
	return cookie.Expires
}

// isJWT reports whether token has the three-segment JWT shape. The app sends
// "Bearer" with no real token before login, which must not be captured.
func isJWT(token string) bool {
	return strings.Count(token, ".") == jwtParts-1 && len(token) > jwtParts
}

// jwtExpiry reads the "exp" claim without verifying the signature (we only
// display it; the server is the one that enforces it). Zero if unreadable.
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != jwtParts {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

// isDigits reports a non-empty all-digit string, the only shape ids take.
func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isForm(contentType string) bool {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	return mediaType == "application/x-www-form-urlencoded"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// IsAuthPath reports the API calls that create or replace sessions. Local
// tools must never send these: only the app logs in.
func IsAuthPath(path string) bool { return isAuthPath(path) }
