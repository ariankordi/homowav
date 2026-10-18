// Package session keeps a HomeWAV visitor session alive for other local tools.
//
// It watches the app's own traffic (through a proxykit hook), keeps the latest
// credentials that the server accepted, renews the PHP session with the
// long-lived "remember" cookies when the server reports a logout, and serves
// the credentials on local /_/ endpoints. It has no flags and no globals, so a
// CLI and a future web UI can both embed it.
package session

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"sync"
	"time"
)

// Creds is everything needed to act as the logged-in app. The zero value
// means nothing has been captured yet.
type Creds struct {
	// PHPSESSID is the short-lived session cookie that actually authorizes API calls.
	PHPSESSID string `json:"phpsessid,omitempty"`
	// Remember holds the long-lived cookies (asps, esur) that refetch-session
	// exchanges for a new PHPSESSID.
	Remember map[string]string `json:"remember,omitempty"`
	// RememberExpires is when the remember cookies lapse (the server slides it
	// forward on every refetch).
	RememberExpires time.Time `json:"remember_expires,omitzero"`
	// JWTAccess and JWTRefresh come from the login response body. The server
	// did not appear to require the access token in the capture.
	JWTAccess  string    `json:"jwt_access,omitempty"`
	JWTRefresh string    `json:"jwt_refresh,omitempty"`
	JWTExpires time.Time `json:"jwt_expires,omitzero"`
	// VoipToken is sent as "token" in refetch-session requests.
	VoipToken string `json:"voip_token,omitempty"`
	// RefetchUserAgent is the User-Agent the app used for refetch-session.
	RefetchUserAgent string `json:"refetch_user_agent,omitempty"`

	// VisitorID is the account's numeric id, seen in the app's own requests.
	// It is an identifier, not a secret; API calls need it as a parameter.
	VisitorID string `json:"visitor_id,omitempty"`

	// SessionUpdated is when PHPSESSID last changed.
	SessionUpdated time.Time `json:"session_updated,omitzero"`
	// SessionValidated is when the server last accepted PHPSESSID. It is
	// not persisted on its own, only alongside other changes.
	SessionValidated time.Time `json:"session_validated,omitzero"`
}

// clone returns a deep copy so callers can never mutate the store's map.
func (c Creds) clone() Creds {
	c.Remember = maps.Clone(c.Remember)
	return c
}

// CookieHeader renders the credentials as a Cookie request header value.
func (c Creds) CookieHeader() string {
	header := ""
	add := func(name, value string) {
		if value == "" {
			return
		}
		if header != "" {
			header += "; "
		}
		header += name + "=" + value
	}
	add(sessionCookie, c.PHPSESSID)
	for _, name := range rememberCookies {
		add(name, c.Remember[name])
	}
	return header
}

// Store holds the current Creds, optionally persisted to a JSON file.
// All methods are safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	creds Creds
	path  string
	// onChange, if set, is called (outside the lock) after every change.
	onChange func(Creds)
}

// NewStore loads path if it exists. An empty path keeps everything in memory.
func NewStore(path string) (*Store, error) {
	store := &Store{path: path}
	if path == "" {
		return store, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}
	if err := json.Unmarshal(data, &store.creds); err != nil {
		return nil, fmt.Errorf("parsing state %s: %w", path, err)
	}
	return store, nil
}

// OnChange registers fn to be called with a copy of the Creds after every
// change. Intended for UIs; set it before traffic starts.
func (s *Store) OnChange(fn func(Creds)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// Snapshot returns a copy of the current credentials.
func (s *Store) Snapshot() Creds {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.creds.clone()
}

// Update applies edit to the credentials and persists the result if edit
// reports a change. edit receives a copy and must not retain it.
func (s *Store) Update(edit func(c *Creds) (changed bool)) error {
	s.mu.Lock()
	next := s.creds.clone()
	if !edit(&next) {
		s.mu.Unlock()
		return nil
	}
	s.creds = next
	err := s.saveLocked()
	notify, snapshot := s.onChange, next.clone()
	s.mu.Unlock()
	if notify != nil {
		notify(snapshot)
	}
	return err
}

// saveLocked writes the state file atomically, with a private mode because it
// holds live credentials. The caller holds s.mu.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.creds, "", "\t")
	if err != nil {
		return err
	}
	if err := WriteAtomic(s.path, append(data, '\n'), PrivateFileMode); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	return nil
}

// Fingerprint returns a short, stable, non-reversible tag for a secret so
// logs can show whether two values match without revealing either one.
func Fingerprint(secret string) string {
	if secret == "" {
		return "-"
	}
	sum := md5.Sum([]byte(secret))
	return hex.EncodeToString(sum[:4])
}
