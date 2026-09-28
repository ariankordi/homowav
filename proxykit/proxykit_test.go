package proxykit

import (
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testBodyLimit is small so over-limit behavior is cheap to exercise.
const testBodyLimit = 16

// upstreamServer echoes request details and serves a few fixed paths.
// It counts hits so tests can prove the upstream was (not) contacted.
func upstreamHandler(hits *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("X-Upstream-Host", r.Host)
		w.Header().Set("X-Upstream-Proto", r.Proto)
		switch r.URL.Path {
		case "/big":
			io.WriteString(w, strings.Repeat("x", 1000))
		case "/echo-len":
			body, _ := io.ReadAll(r.Body)
			io.WriteString(w, strings.Repeat("y", len(body)))
		case "/gzip":
			// Only compress when asked, like real servers.
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				io.WriteString(w, "plain-because-not-asked")
				return
			}
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			io.WriteString(gz, "hello from gzip")
			gz.Close()
		default:
			body, _ := io.ReadAll(r.Body)
			w.Write(append([]byte("upstream:"), body...))
		}
	})
}

// startEntry serves p's entry handler on a plaintext test server.
func startEntry(t *testing.T, p *Proxy) *httptest.Server {
	t.Helper()
	entry := httptest.NewServer(p.EntryHandler(p.PACHandler("")))
	t.Cleanup(func() { entry.Close(); p.Close() })
	return entry
}

// plainProxyClient sends absolute-URI requests through entry. Compression is
// disabled so the test sees exactly what the proxy wrote.
func plainProxyClient(entry *httptest.Server) *http.Client {
	proxyURL, _ := url.Parse(entry.URL)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableCompression: true}}
}

// reader returns a body-reading helper bound to t. It is a closure so call
// sites can pass a (resp, err) pair straight from client.Get/Post.
func reader(t *testing.T) func(*http.Response, error) string {
	return func(resp *http.Response, err error) string {
		t.Helper()
		return readBody(t, resp, err)
	}
}

func readBody(t *testing.T, resp *http.Response, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestFlowPaths(t *testing.T) {
	var hits atomic.Int64
	upstream := httptest.NewServer(upstreamHandler(&hits))
	defer upstream.Close()

	var tappedReq, tappedResp string
	var bigErr error
	var bigLen int
	p := &Proxy{Hosts: NewHostTable(), TLSConfig: &tls.Config{}, BodyLimit: testBodyLimit}
	p.Hosts.Handle("127.0.0.1", func(f *Flow) {
		switch f.Request.URL.Path {
		case "/tap":
			reqBody, _ := f.RequestBody()
			tappedReq = string(reqBody)
			res, _ := f.Forward()
			respBody, _ := res.Body()
			tappedResp = string(respBody)
		case "/modify":
			res, _ := f.Forward()
			body, _ := res.Body()
			res.SetBody(bytes.ToUpper(body))
			res.Header.Set("X-Modified", "1")
		case "/local":
			f.Respond(http.StatusTeapot, http.Header{"X-Local": {"1"}}, []byte("local"))
		case "/big":
			res, _ := f.Forward()
			body, err := res.Body()
			bigErr, bigLen = err, len(body)
		case "/echo-len":
			f.RequestBody()
		case "/panic":
			panic("boom")
		}
	})
	entry := startEntry(t, p)
	client := plainProxyClient(entry)
	base := upstream.URL // http://127.0.0.1:port

	t.Run("passthrough", func(t *testing.T) {
		got := reader(t)(client.Post(base+"/pass", "text/plain", strings.NewReader("abc")))
		if got != "upstream:abc" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("gzip is decoded for the client", func(t *testing.T) {
		resp, err := client.Get(base + "/gzip")
		if got := reader(t)(resp, err); got != "hello from gzip" {
			t.Fatalf("got %q", got)
		}
		if enc := resp.Header.Get("Content-Encoding"); enc != "" {
			t.Fatalf("Content-Encoding leaked: %q", enc)
		}
	})
	t.Run("tap", func(t *testing.T) {
		got := reader(t)(client.Post(base+"/tap", "text/plain", strings.NewReader("req")))
		if got != "upstream:req" || tappedReq != "req" || tappedResp != "upstream:req" {
			t.Fatalf("got %q, tapped %q / %q", got, tappedReq, tappedResp)
		}
	})
	t.Run("modify", func(t *testing.T) {
		resp, err := client.Post(base+"/modify", "text/plain", strings.NewReader("abc"))
		if got := reader(t)(resp, err); got != "UPSTREAM:ABC" || resp.Header.Get("X-Modified") != "1" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("custom reply skips upstream", func(t *testing.T) {
		before := hits.Load()
		resp, err := client.Get(base + "/local")
		if got := reader(t)(resp, err); got != "local" || resp.StatusCode != http.StatusTeapot {
			t.Fatalf("got %d %q", resp.StatusCode, got)
		}
		if hits.Load() != before {
			t.Fatal("upstream was contacted")
		}
	})
	t.Run("response over limit still streams fully", func(t *testing.T) {
		got := reader(t)(client.Get(base + "/big"))
		if len(got) != 1000 || !errors.Is(bigErr, ErrBodyTooLarge) || bigLen != testBodyLimit {
			t.Fatalf("client got %d bytes, hook got %d bytes err=%v", len(got), bigLen, bigErr)
		}
	})
	t.Run("request over limit still forwards fully", func(t *testing.T) {
		got := reader(t)(client.Post(base+"/echo-len", "text/plain", strings.NewReader(strings.Repeat("z", 500))))
		if len(got) != 500 {
			t.Fatalf("upstream saw %d bytes, want 500", len(got))
		}
	})
	t.Run("hook panic becomes 500", func(t *testing.T) {
		resp, err := client.Get(base + "/panic")
		reader(t)(resp, err)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status %d", resp.StatusCode)
		}
	})
	t.Run("unlisted plain host forwarded untouched", func(t *testing.T) {
		unlisted := strings.Replace(base, "127.0.0.1", "localhost", 1)
		got := reader(t)(client.Get(unlisted + "/tap"))
		if got != "upstream:" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("pac reflects live table", func(t *testing.T) {
		p.Hosts.Handle("*.example.com", nil)
		pac := reader(t)(http.Get(entry.URL + "/proxy.pac"))
		wantProxy := `"PROXY ` + strings.TrimPrefix(entry.URL, "http://") + `"`
		if !strings.Contains(pac, `dnsDomainIs(host, ".example.com")`) || !strings.Contains(pac, wantProxy) {
			t.Fatalf("pac:\n%s", pac)
		}
		p.Hosts.Remove("*.example.com")
		if pac = reader(t)(http.Get(entry.URL + "/proxy.pac")); strings.Contains(pac, "example.com") {
			t.Fatalf("removed host still in pac:\n%s", pac)
		}
	})
}

// selfSignedCert makes a throwaway cert valid for 127.0.0.1, standing in for
// the user-provided static cert.
func selfSignedCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "proxykit test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func TestConnect(t *testing.T) {
	var hits atomic.Int64
	upstream := httptest.NewUnstartedServer(upstreamHandler(&hits))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()

	mitmCert, mitmPool := selfSignedCert(t)
	upstreamTransport := upstream.Client().Transport.(*http.Transport).Clone()
	upstreamTransport.ForceAttemptHTTP2 = true

	var hooked atomic.Int64
	p := &Proxy{
		Hosts:     NewHostTable(),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{mitmCert}},
		// The proxy must trust the test upstream's own self-signed cert.
		Transport: upstreamTransport,
	}
	entry := startEntry(t, p)
	proxyURL, _ := url.Parse(entry.URL)

	clientTrusting := func(pool *x509.CertPool) *http.Client {
		return &http.Client{Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			TLSClientConfig:   &tls.Config{RootCAs: pool},
			ForceAttemptHTTP2: true,
		}}
	}

	t.Run("listed host is intercepted over h2", func(t *testing.T) {
		p.Hosts.Handle("127.0.0.1", func(f *Flow) {
			hooked.Add(1)
			res, _ := f.Forward()
			res.Header.Set("X-Hooked", "1")
		})
		defer p.Hosts.Remove("127.0.0.1")
		// Client only trusts the MITM cert, so success proves interception.
		resp, err := clientTrusting(mitmPool).Post(upstream.URL+"/x", "text/plain", strings.NewReader("hi"))
		got := reader(t)(resp, err)
		if got != "upstream:hi" || resp.Header.Get("X-Hooked") != "1" {
			t.Fatalf("got %q headers %v", got, resp.Header)
		}
		if resp.Proto != "HTTP/2.0" {
			t.Fatalf("client<->proxy proto %s, want HTTP/2.0", resp.Proto)
		}
		if upstreamProto := resp.Header.Get("X-Upstream-Proto"); upstreamProto != "HTTP/2.0" {
			t.Fatalf("proxy<->upstream proto %s, want HTTP/2.0", upstreamProto)
		}
	})
	t.Run("unlisted host is tunneled blind", func(t *testing.T) {
		before := hooked.Load()
		upstreamPool := x509.NewCertPool()
		upstreamPool.AddCert(upstream.Certificate())
		// Client only trusts the upstream's real cert, so success proves no MITM.
		got := reader(t)(clientTrusting(upstreamPool).Get(upstream.URL + "/y"))
		if got != "upstream:" || hooked.Load() != before {
			t.Fatalf("got %q, hooked delta %d", got, hooked.Load()-before)
		}
	})
}

func TestHostTable(t *testing.T) {
	table := NewHostTable()
	table.Handle("*.example.com", nil)
	table.Handle("API.example.com", func(*Flow) {})
	cases := map[string]bool{
		"a.example.com":        true,
		"a.b.example.com:8443": true,
		"example.com":          false,
		"notexample.com":       false,
		"api.example.com.":     true,
	}
	for host, want := range cases {
		if _, got := table.Lookup(host); got != want {
			t.Errorf("Lookup(%q) = %v, want %v", host, got, want)
		}
	}
	if hook, _ := table.Lookup("api.example.com"); hook == nil {
		t.Error("exact match should win over wildcard")
	}
}

func TestUpstreamAuthority(t *testing.T) {
	cases := []struct{ host, connectTarget, want string }{
		{"Example.com:8444", "", "example.com"},           // Direct mode: listener port ignored.
		{"example.com", "example.com:443", "example.com"}, // Normal CONNECT.
		{"example.com", "example.com:8443", "example.com:8443"},
		{"other.example.com", "example.com:443", "other.example.com"}, // Coalesced vhost.
		{"[::1]:9000", "", "[::1]"},
	}
	for _, c := range cases {
		if got := upstreamAuthority(c.host, c.connectTarget); got != c.want {
			t.Errorf("upstreamAuthority(%q, %q) = %q, want %q", c.host, c.connectTarget, got, c.want)
		}
	}
}
