package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewServerRequiresCertificate(t *testing.T) {
	if _, err := NewServer(ServerConfig{}); err == nil {
		t.Fatal("NewServer accepted a config without a certificate")
	}
}

func TestRunServesPACAndAPIThenStops(t *testing.T) {
	// httptest's built-in certificate is enough: nothing here dials the intercepted host.
	testServer := httptest.NewTLSServer(http.NotFoundHandler())
	cert := testServer.TLS.Certificates[0]
	testServer.Close()

	srv, err := NewServer(ServerConfig{Listen: "127.0.0.1:0", Certificate: cert})
	if err != nil {
		t.Fatal(err)
	}
	srv.Handle("GET /ui", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ui") }))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	base := waitForAddr(t, srv)

	get := func(path string) string {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	if pac := get(pacPath); !strings.Contains(pac, `host === "app.homewav.com"`) || strings.Count(pac, "host ===") != 1 {
		t.Errorf("PAC should route only the API host:\n%s", pac)
	}
	if status := get("/_/session"); !strings.Contains(status, `"have_session": false`) {
		t.Errorf("status = %s", status)
	}
	if got := get("/ui"); got != "ui" {
		t.Errorf("extra route = %q", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancel", err)
		}
	case <-time.After(shutdownGrace + time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

// waitForAddr polls until Run has bound its listener and returns its base URL.
func waitForAddr(t *testing.T, srv *Server) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if addr := srv.Addr(); addr != nil {
			return "http://" + addr.String()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server never started listening")
	return ""
}
