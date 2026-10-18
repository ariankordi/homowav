package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSecret = "s3cret"

// request describes one caller for the table below.
type request struct {
	method, remote, host, auth, cookie, origin string
	clientHeader                               bool
}

func (c request) do(handler http.Handler) int {
	req := httptest.NewRequest(c.method, "http://"+c.host+"/x", nil)
	req.RemoteAddr = c.remote
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: PairCookie, Value: c.cookie})
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.clientHeader {
		req.Header.Set(ClientHeader, "1")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

func TestGate(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	gate := Gate{Secret: func() string { return testSecret }}.Wrap(ok)
	const lan, loop = "192.168.1.20:5000", "127.0.0.1:5000"

	cases := []struct {
		name string
		req  request
		want int
	}{
		{"loopback GET", request{method: "GET", remote: loop, host: "localhost:8080"}, 204},
		{"loopback IPv6 GET", request{method: "GET", remote: "[::1]:5000", host: "[::1]:8080"}, 204},
		{"loopback with foreign Host (DNS rebinding)", request{method: "GET", remote: loop, host: "evil.example:8080"}, 403},
		{"LAN without anything", request{method: "GET", remote: lan, host: "192.168.1.5:8080"}, 403},
		{"LAN wrong bearer", request{method: "GET", remote: lan, host: "192.168.1.5:8080", auth: "Bearer nope"}, 403},
		{"LAN bearer", request{method: "POST", remote: lan, host: "192.168.1.5:8080", auth: "Bearer " + testSecret}, 204},
		{"LAN cookie GET", request{method: "GET", remote: lan, host: "192.168.1.5:8080", cookie: testSecret}, 204},
		{"LAN wrong cookie", request{method: "GET", remote: lan, host: "192.168.1.5:8080", cookie: "nope"}, 403},
		{"POST without client header (form from a foreign page)", request{method: "POST", remote: loop, host: "localhost:8080"}, 403},
		{"POST with header, no Origin", request{method: "POST", remote: loop, host: "localhost:8080", clientHeader: true}, 204},
		{"POST with header and own Origin", request{method: "POST", remote: loop, host: "localhost:8080", clientHeader: true, origin: "http://localhost:8080"}, 204},
		{"POST with header and foreign Origin", request{method: "POST", remote: loop, host: "localhost:8080", clientHeader: true, origin: "http://evil.example"}, 403},
	}
	for _, c := range cases {
		if got := c.req.do(gate); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}

	noSecret := Gate{}.Wrap(ok)
	if got := (request{method: "GET", remote: lan, host: "192.168.1.5:8080", auth: "Bearer "}).do(noSecret); got != 403 {
		t.Errorf("empty secret admitted a LAN caller: %d", got)
	}
}

func TestPairing(t *testing.T) {
	gate := Gate{Secret: func() string { return testSecret }}
	base := "http://192.168.1.5:8080"

	rec := httptest.NewRecorder()
	gate.PairHandler().ServeHTTP(rec, httptest.NewRequest("GET", PairURL(base, testSecret), nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("pairing status = %d", rec.Code)
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, PairCookie+"="+testSecret) || !strings.Contains(setCookie, "HttpOnly") {
		t.Errorf("Set-Cookie = %q", setCookie)
	}

	rec = httptest.NewRecorder()
	gate.PairHandler().ServeHTTP(rec, httptest.NewRequest("GET", PairURL(base, "wrong"), nil))
	if rec.Code != http.StatusForbidden || rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("wrong key: status %d, cookie %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
}
