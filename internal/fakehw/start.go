package fakehw

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"

	"hwclient/internal/session"
)

// loopbackHost is left alone by the Transport Start returns, so tests can
// still reach other local servers (a fake bucket, for example).
const loopbackHost = "127.0.0.1"

// Start runs a fake on a local TLS listener. The returned transport sends
// every upstream connection to it, whatever host was asked for, and trusts
// its certificate. Call stop when done.
func Start() (fake *Server, transport *http.Transport, stop func()) {
	fake = New()
	listener := httptest.NewTLSServer(fake)
	transport = listener.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "example.com" // A SAN on httptest's certificate.
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if host, _, _ := net.SplitHostPort(address); host != loopbackHost {
			address = listener.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	return fake, transport, listener.Close
}

// SeedState returns a credentials file as the store would hold it after the
// app was opened through the proxy once.
func (s *Server) SeedState() []byte {
	state, err := json.Marshal(session.Creds{
		PHPSESSID: s.Mint(),
		Remember:  map[string]string{"asps": RememberValue, "esur": RememberValue},
		VisitorID: VisitorID,
	})
	if err != nil {
		panic(err) // A fixed struct of strings always encodes.
	}
	return state
}
