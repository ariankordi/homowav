package session

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"homowav/proxykit"
)

// Fixed fake-server values. None of these come from a real capture.
const (
	fakeRemember  = "remember-me"
	fakeVoip      = "voip-123"
	permissionMsg = "You do not have permission to view this page"
	rememberAge   = 2592000 // 30 days, as the real server sends.
)

// fakeHomewav mimics the parts of the API the session package relies on.
type fakeHomewav struct {
	mu          sync.Mutex
	valid       map[string]bool
	minted      int
	failRefetch atomic.Bool
	refetches   atomic.Int64
	lastCookie  atomic.Value // string: Cookie header of the last /api/echo call.
}

func newFakeHomewav() *fakeHomewav { return &fakeHomewav{valid: map[string]bool{}} }

// mint issues a new valid session id.
func (h *fakeHomewav) mint() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.minted++
	id := fmt.Sprintf("sess%d", h.minted)
	h.valid[id] = true
	return id
}

// expireAll logs every session out, as the server does after idling.
func (h *fakeHomewav) expireAll() {
	h.mu.Lock()
	h.valid = map[string]bool{}
	h.mu.Unlock()
}

func (h *fakeHomewav) isValid(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	h.mu.Lock()
	defer h.mu.Unlock()
	return err == nil && h.valid[cookie.Value]
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	json.NewEncoder(w).Encode(v)
}

func loggedOutReply(w http.ResponseWriter) {
	reply(w, map[string]any{"ok": false, "cause": loggedOutCause})
}

// setSession adds the Set-Cookie lines the real login/refetch replies carry.
func setSession(w http.ResponseWriter, id string) {
	w.Header().Add("Set-Cookie", sessionCookie+"="+id+"; path=/")
	for _, name := range rememberCookies {
		w.Header().Add("Set-Cookie", fmt.Sprintf("%s=%s; Max-Age=%d; path=/", name, fakeRemember, rememberAge))
	}
}

func (h *fakeHomewav) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/app/login/":
		r.ParseForm()
		if r.Form.Get(loginVoipField) != fakeVoip {
			reply(w, map[string]any{"ok": false, "cause": "bad login"})
			return
		}
		setSession(w, h.mint())
		reply(w, map[string]any{"ok": true, "body": map[string]any{
			"jwt_access": fakeJWT(time.Now().Add(time.Hour)), "jwt_refresh": "refresh-1",
		}})
	case r.URL.Path == refetchPath:
		h.refetches.Add(1)
		asps, err := r.Cookie("asps")
		if h.failRefetch.Load() || err != nil || asps.Value != fakeRemember {
			loggedOutReply(w)
			return
		}
		setSession(w, h.mint())
		reply(w, map[string]any{"ok": true, "body": map[string]any{"is_logged_in": true}})
	case r.URL.Path == "/api/app/prisons/1":
		reply(w, map[string]any{"ok": false, "cause": permissionMsg})
	case r.URL.Path == "/api/binary":
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, "not json")
	default:
		if !h.isValid(r) {
			loggedOutReply(w)
			return
		}
		h.lastCookie.Store(r.Header.Get("Cookie"))
		body, _ := io.ReadAll(r.Body)
		reply(w, map[string]any{"ok": true, "body": map[string]any{"echo": string(body)}})
	}
}

// fakeJWT builds an unsigned token with only an exp claim.
func fakeJWT(exp time.Time) string {
	encode := base64.RawURLEncoding.EncodeToString
	return encode([]byte(`{"alg":"none"}`)) + "." + encode([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix()))) + ".sig"
}

// harness is a proxy with the session hook in front of a fakeHomewav.
type harness struct {
	fake    *fakeHomewav
	service *Service
	client  *http.Client
	path    string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fake := newFakeHomewav()
	upstream := httptest.NewTLSServer(fake)
	t.Cleanup(upstream.Close)

	// Every upstream dial goes to the fake, whatever host the proxy asked for.
	transport := upstream.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "example.com" // A SAN on httptest's certificate.
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := NewStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := &Service{Store: store, Renewer: NewRenewer(store, transport, DefaultHost, logger), Log: logger}

	cert, pool := selfSignedCert(t, DefaultHost)
	proxy := &proxykit.Proxy{
		Hosts:     proxykit.NewHostTable(),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		Transport: transport,
		Logger:    logger,
	}
	proxy.Hosts.Handle(DefaultHost, service.Hook)
	entry := httptest.NewServer(proxy.EntryHandler(nil))
	t.Cleanup(func() { entry.Close(); proxy.Close() })

	proxyURL, _ := url.Parse(entry.URL)
	client := &http.Client{Transport: &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		TLSClientConfig:   &tls.Config{RootCAs: pool},
		ForceAttemptHTTP2: true,
	}}
	return &harness{fake: fake, service: service, client: client, path: statePath}
}

// call sends one API request as the app would and returns the decoded reply.
func (h *harness) call(t *testing.T, path, cookie, body string) (envelope, *http.Response) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "https://"+DefaultHost+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil && resp.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("%s: undecodable reply %q", path, raw)
	}
	return env, resp
}

// login performs the app's login through the proxy and returns the session.
func (h *harness) login(t *testing.T) string {
	t.Helper()
	env, _ := h.call(t, "/api/app/login/?username=someone", "", url.Values{loginVoipField: {fakeVoip}, "password": {"pw"}}.Encode())
	if !env.succeeded() {
		t.Fatalf("login failed: %+v", env)
	}
	return h.service.Store.Snapshot().PHPSESSID
}

func TestLoginIsCapturedAndPersisted(t *testing.T) {
	h := newHarness(t)
	session := h.login(t)
	c := h.service.Store.Snapshot()
	if session == "" || c.Remember["asps"] != fakeRemember || c.Remember["esur"] != fakeRemember {
		t.Fatalf("session/remember not captured: %+v", c)
	}
	if c.VoipToken != fakeVoip || c.JWTRefresh != "refresh-1" || !isJWT(c.JWTAccess) || c.JWTExpires.IsZero() {
		t.Errorf("login body/form not captured: %+v", c)
	}
	if time.Until(c.RememberExpires) < 29*24*time.Hour {
		t.Errorf("remember expiry = %v, want ~30 days out", c.RememberExpires)
	}

	info, err := os.Stat(h.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != stateFileMode {
		t.Errorf("state file mode = %v, want %v", info.Mode().Perm(), os.FileMode(stateFileMode))
	}
	reloaded, err := NewStore(h.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Snapshot(); got.PHPSESSID != session || got.CookieHeader() != c.CookieHeader() {
		t.Errorf("reloaded %+v, want %+v", got, c)
	}
	if strings.Contains(mustRead(t, h.path), "pw") {
		t.Error("password leaked into the state file")
	}
}

func TestFailedRequestIsNotCaptured(t *testing.T) {
	h := newHarness(t)
	h.call(t, "/api/app/prisons/1", "PHPSESSID=unverified", "")
	if got := h.service.Store.Snapshot().PHPSESSID; got != "" {
		t.Errorf("stored %q from a rejected request", got)
	}
}

func TestLogoutIsRecoveredTransparently(t *testing.T) {
	h := newHarness(t)
	stale := h.login(t)
	h.fake.expireAll()

	env, resp := h.call(t, "/api/echo", "PHPSESSID="+stale+"; other=keep", "hello=1")
	if !env.succeeded() || !strings.Contains(string(env.Body), "hello=1") {
		t.Fatalf("client saw %+v, want the replayed success with its body", env)
	}
	if n := h.fake.refetches.Load(); n != 1 {
		t.Errorf("refetches = %d, want 1", n)
	}
	fresh := h.service.Store.Snapshot().PHPSESSID
	if fresh == stale {
		t.Fatal("store kept the expired session")
	}
	if got := resp.Header.Get("Set-Cookie"); !strings.HasPrefix(got, sessionCookie+"="+fresh) {
		t.Errorf("Set-Cookie to app = %q, want the fresh session", got)
	}
	if got, _ := h.fake.lastCookie.Load().(string); got != "PHPSESSID="+fresh+"; other=keep" {
		t.Errorf("replayed Cookie = %q, other cookies must survive", got)
	}
}

func TestStaleAppSessionUsesStoredOneWithoutRefetch(t *testing.T) {
	h := newHarness(t)
	h.login(t)
	env, _ := h.call(t, "/api/echo", "PHPSESSID=old-one", "")
	if !env.succeeded() || h.fake.refetches.Load() != 0 {
		t.Errorf("env=%+v refetches=%d, want success with no refetch", env, h.fake.refetches.Load())
	}
}

func TestBurstCausesOneRefetch(t *testing.T) {
	h := newHarness(t)
	stale := h.login(t)
	h.fake.expireAll()

	const burst = 10
	var wg sync.WaitGroup
	var failures atomic.Int64
	for range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if env, _ := h.call(t, "/api/echo", "PHPSESSID="+stale, ""); !env.succeeded() {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 || h.fake.refetches.Load() != 1 {
		t.Errorf("failures=%d refetches=%d, want 0 and 1", failures.Load(), h.fake.refetches.Load())
	}
}

func TestOtherFailuresPassThrough(t *testing.T) {
	h := newHarness(t)
	session := h.login(t)
	if env, _ := h.call(t, "/api/app/prisons/1", "PHPSESSID="+session, ""); env.Cause != permissionMsg {
		t.Errorf("got %+v, want the permission error untouched", env)
	}
	if _, resp := h.call(t, "/api/binary", "", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("non-JSON status %d", resp.StatusCode)
	}
	if h.fake.refetches.Load() != 0 {
		t.Error("a non-logout failure triggered a refetch")
	}
}

func TestRenewalFailureReturnsOriginalReply(t *testing.T) {
	h := newHarness(t)
	stale := h.login(t)
	h.fake.expireAll()
	h.fake.failRefetch.Store(true)
	if env, _ := h.call(t, "/api/echo", "PHPSESSID="+stale, ""); !env.loggedOut() {
		t.Errorf("got %+v, want the original logged-out reply", env)
	}
}

func TestAPIAccessAndRenew(t *testing.T) {
	h := newHarness(t)
	session := h.login(t)
	const secret = "s3cret"
	api := h.service.Handler(secret)

	get := func(target, remote, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = remote
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("/_/sessid", "127.0.0.1:5000", ""); rec.Code != http.StatusOK || rec.Body.String() != session {
		t.Errorf("loopback sessid = %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/_/sessid", "192.168.1.20:5000", ""); rec.Code != http.StatusForbidden {
		t.Errorf("remote without secret = %d, want 403", rec.Code)
	}
	if rec := get("/_/sessid", "192.168.1.20:5000", "Bearer wrong"); rec.Code != http.StatusForbidden {
		t.Errorf("remote with wrong secret = %d, want 403", rec.Code)
	}
	if rec := get("/_/token", "192.168.1.20:5000", "Bearer "+secret); rec.Code != http.StatusOK || !isJWT(rec.Body.String()) {
		t.Errorf("remote token with secret = %d %q", rec.Code, rec.Body.String())
	}
	rec := get("/_/sessid?renew=1", "[::1]:5000", "")
	if rec.Code != http.StatusOK || rec.Body.String() == session || h.fake.refetches.Load() != 1 {
		t.Errorf("renew = %d %q (refetches %d), want a new session", rec.Code, rec.Body.String(), h.fake.refetches.Load())
	}
	if rec := get("/_/cookie", "127.0.0.1:1", ""); !strings.Contains(rec.Body.String(), "asps="+fakeRemember) {
		t.Errorf("cookie = %q", rec.Body.String())
	}
	status := get("/_/session", "127.0.0.1:1", "").Body.String()
	if strings.Contains(status, rec.Body.String()) || !strings.Contains(status, `"can_renew": true`) {
		t.Errorf("status leaks a secret or lacks fields: %s", status)
	}
}

func TestReplaceCookie(t *testing.T) {
	header := http.Header{"Cookie": {"a=1; PHPSESSID=old", "b=2"}}
	replaceCookie(header, sessionCookie, "new")
	if got := header.Get("Cookie"); got != "PHPSESSID=new; a=1; b=2" || len(header["Cookie"]) != 1 {
		t.Errorf("got %q", header["Cookie"])
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// selfSignedCert makes a throwaway leaf for host and a pool that trusts it.
func selfSignedCert(t *testing.T, host string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}
