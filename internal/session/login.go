package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Facts about the app's login, from a capture of iOS app 2.28.0.
const (
	// AppVersion is the app version hwclient presents itself as.
	AppVersion = "2.28.0"
	// apiUserAgent is what the app sends on API calls (refetch uses another).
	apiUserAgent = "iPhone14,5 IOS17.0 Build 2.28.0(493)"
	// loginUsernameParam carries the username in the query, not the form.
	loginUsernameParam = "username"
	// loginDeviceType is the platform the app reports. hwclient presents
	// itself as the iOS app, whose traffic is the one that was captured.
	loginDeviceType = "ios"
	// fcmField is the push token the app sends with login.
	fcmField = "fcm_token"
	// loginCooldown spaces out automatic logins, so a run of failures never
	// becomes a run of password attempts.
	loginCooldown = time.Minute
)

// ErrLoginRefused means the service turned down the username and password.
// Automatic logins stop until different credentials are saved, because
// repeated attempts can lock the account.
var ErrLoginRefused = errors.New("session: the service refused the username or password")

// AccountSource returns the saved username and password, or empty strings.
type AccountSource func() (username, password string)

// Login signs in with a username and password, as the app's login screen
// does, and stores the session, remember cookies and JWTs it returns.
func (r *Renewer) Login(ctx context.Context, username, password string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, err := r.loginLocked(ctx, username, password)
	if err == nil {
		r.lastFailure, r.lastErr, r.lastSuccess = time.Time{}, nil, time.Now()
	}
	return session, err
}

// LoginState reports whether a saved account can be used for automatic
// logins, and the reason when it was refused.
func (r *Renewer) LoginState() (saved bool, refused error) {
	if r.Account == nil {
		return false, nil
	}
	username, password := r.Account()
	if username == "" || password == "" {
		return false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refusedAccount == accountKey(username, password) {
		return true, ErrLoginRefused
	}
	return true, nil
}

// autoLoginLocked signs in with the saved account after a renewal failed.
// tried is false when there is nothing to try (no account saved, or it was
// refused before, or the cooldown has not passed). The caller holds r.mu.
func (r *Renewer) autoLoginLocked(ctx context.Context) (session string, tried bool, err error) {
	if r.Account == nil {
		return "", false, nil
	}
	username, password := r.Account()
	if username == "" || password == "" || r.refusedAccount == accountKey(username, password) {
		return "", false, nil
	}
	if !r.lastLogin.IsZero() && time.Since(r.lastLogin) < loginCooldown {
		return "", false, nil
	}
	r.log.Info("renewal failed; signing in with the saved password")
	session, err = r.loginLocked(ctx, username, password)
	return session, true, err
}

// loginLocked performs POST /api/app/login/ and stores what it returns. The
// caller holds r.mu.
func (r *Renewer) loginLocked(ctx context.Context, username, password string) (string, error) {
	if username == "" || password == "" {
		return "", errors.New("session: a username and a password are both needed")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refetchTimeout)
	defer cancel()
	r.lastLogin = time.Now()
	creds := r.store.Snapshot()
	form := url.Values{
		"device_type":  {loginDeviceType},
		"password":     {password},
		"version":      {AppVersion},
		loginVoipField: {creds.VoipToken},
	}
	// The push token is only sent when the app's own one is known: an empty
	// one might replace the phone's registration and silence notifications.
	if creds.FCMToken != "" {
		form.Set(fcmField, creds.FCMToken)
	}
	target := r.baseURL + loginPath + "?" + url.Values{loginUsernameParam: {username}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US;q=1.0")
	req.Header.Set("Authorization", "Bearer")
	req.Header.Set("User-Agent", apiUserAgent)
	req.Header.Set("X-App-Version", AppVersion)
	req.Header.Set("X-HomeWAV-App", "app_type_visitor")
	req.Header.Set("Date", strconv.FormatInt(time.Now().Unix(), 10))

	r.attempts.Add(1)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRefetchBody))
	if err != nil {
		return "", fmt.Errorf("login: reading reply: %w", err)
	}
	env, ok := parseEnvelope(resp.Header.Get("Content-Type"), body)
	if resp.StatusCode != http.StatusOK || !ok {
		return "", fmt.Errorf("login: HTTP %d with no usable reply", resp.StatusCode)
	}
	if !env.succeeded() {
		// A reply that says no is about the credentials, not the network.
		r.refusedAccount = accountKey(username, password)
		return "", fmt.Errorf("%w: %s", ErrLoginRefused, env.Cause)
	}

	now := time.Now()
	obs := observation{remember: map[string]string{}, voipToken: creds.VoipToken}
	obs.addResponse(resp.Header, env, now)
	var account struct {
		VisitorID string `json:"visitor_id"`
	}
	if json.Unmarshal(env.Body, &account) == nil && isDigits(account.VisitorID) {
		obs.visitorID = account.VisitorID
	}
	if obs.session == "" {
		return "", errors.New("login: reply set no PHPSESSID")
	}
	if err := r.store.Update(func(c *Creds) bool { return obs.apply(c, now) }); err != nil {
		r.log.Error("saving the session from login", "err", err)
	}
	r.refusedAccount = ""
	r.log.Info("signed in", "new", Fingerprint(obs.session))
	return obs.session, nil
}

// accountKey identifies a username and password without keeping the
// password itself in memory longer than the AccountSource does.
func accountKey(username, password string) string {
	sum := sha256.Sum256([]byte(username + "\x00" + password))
	return string(sum[:])
}
