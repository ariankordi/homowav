package proxykit

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Defaults used when the corresponding Proxy field is left zero.
const (
	DefaultBodyLimit   = 10 << 20 // 10 MiB: enough for API JSON and HTML pages.
	DefaultDialTimeout = 10 * time.Second
	defaultHTTPSPort   = "443"
)

// Proxy wires a HostTable to an HTTP entry handler and an in-process HTTPS
// interceptor. Configure the exported fields, then mount EntryHandler on a
// plaintext listener (and optionally call ServeTLS for direct HTTPS).
type Proxy struct {
	// Hosts decides which hostnames are intercepted and which hook runs.
	Hosts *HostTable
	// TLSConfig must hold the static certificate(s) presented for intercepted hosts.
	// NextProtos defaults to h2 + http/1.1.
	TLSConfig *tls.Config
	// Transport sends upstream requests. Nil means NewTransport().
	// Supply your own for upstream proxies, custom DNS, or extra root CAs.
	Transport http.RoundTripper
	// BodyLimit caps how much of a body a hook can buffer. Zero means DefaultBodyLimit.
	BodyLimit int64
	// Logger receives access and error logs. Nil discards everything.
	Logger *slog.Logger
	// AccessLog enables one log line per request/tunnel.
	AccessLog bool
	// ListedOnly refuses CONNECT tunnels and plain-HTTP forwarding to hosts
	// that are not in Hosts. Without it the entry port is an open forward
	// proxy, which also lets any LAN client reach this machine's loopback
	// services (including the local endpoints, as a "loopback" caller).
	ListedOnly bool

	startOnce sync.Once
	inbound   *chanListener
	server    *http.Server
	defaultRT http.RoundTripper
}

// NewTransport returns the upstream transport used when Proxy.Transport is nil.
// It tries HTTP/2 via ALPN and lets Go transparently request and decode gzip,
// so hooks always see plain bodies. It deliberately ignores HTTP(S)_PROXY so a
// proxy pointed at itself cannot loop.
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: DefaultDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: DefaultDialTimeout,
	}
}

// EntryHandler is the handler for the plaintext port a client's PAC points at.
// It handles CONNECT (intercept or blind tunnel) and absolute-URI proxy
// requests; everything else (e.g. GET /proxy.pac) goes to local, which may be nil.
func (p *Proxy) EntryHandler(local http.Handler) http.Handler {
	p.start()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodConnect:
			p.handleConnect(w, r)
		case r.URL.IsAbs():
			// Absolute-form ("GET http://host/path") means a proxied plain-HTTP request.
			p.serveFlow(w, r, r.URL.Scheme, true)
		case local != nil:
			p.logAccess("local", r.Method, r.Host+r.URL.Path, r.RemoteAddr)
			local.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// PACHandler serves a PAC file generated from the live HostTable on every
// request. proxyAddr is the "host:port" written into the PAC; if empty, the
// Host the client used to fetch the PAC is used, which is normally correct.
func (p *Proxy) PACHandler(proxyAddr string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr := proxyAddr
		if addr == "" {
			addr = r.Host
		}
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		// Clients cache PACs aggressively; ask them not to so host edits apply sooner.
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, BuildPAC(p.Hosts.Patterns(), addr))
	})
}

// BuildPAC renders a FindProxyForURL that sends patterns to proxyAddr and
// everything else DIRECT. Patterns use HostTable syntax ("a.com", "*.a.com").
func BuildPAC(patterns []string, proxyAddr string) string {
	var conditions []string
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, wildcardPrefix) {
			conditions = append(conditions, "dnsDomainIs(host, "+jsString(pattern[1:])+")")
		} else {
			conditions = append(conditions, "host === "+jsString(pattern))
		}
	}
	match := "false"
	if len(conditions) > 0 {
		match = strings.Join(conditions, " ||\n\t\t")
	}
	return fmt.Sprintf("function FindProxyForURL(url, host) {\n"+
		"\thost = host.toLowerCase();\n"+
		"\tif (%s)\n\t\treturn %s;\n"+
		"\treturn \"DIRECT\";\n}\n", match, jsString("PROXY "+proxyAddr))
}

// jsString quotes s as a JavaScript string literal. JSON encoding is used
// because it also escapes quotes, backslashes, and <, >, & safely.
func jsString(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}

// ServeTLS accepts TLS connections on l and runs them through the hooks, for
// setups where clients reach the proxy directly (hosts file / DNS override)
// instead of via CONNECT. Blocks until l fails or Close is called.
func (p *Proxy) ServeTLS(l net.Listener) error {
	p.start()
	return p.server.Serve(tls.NewListener(l, p.tlsConfig()))
}

// Close stops the in-process HTTPS server. It does not close external listeners.
func (p *Proxy) Close() error {
	p.start()
	return p.server.Close()
}

// start lazily builds the in-process HTTPS server fed by CONNECT.
func (p *Proxy) start() {
	p.startOnce.Do(func() {
		if p.Logger == nil {
			p.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		}
		p.defaultRT = NewTransport()
		p.inbound = newChanListener()
		p.server = &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p.serveFlow(w, r, "https", false)
			}),
			// Surface the CONNECT target to handlers, so the upstream port is right.
			ConnContext:       withConnectTarget,
			ErrorLog:          slog.NewLogLogger(p.Logger.Handler(), slog.LevelDebug),
			ReadHeaderTimeout: 30 * time.Second,
		}
		// tls.NewListener yields *tls.Conn, which is what makes net/http offer HTTP/2 via ALPN.
		go p.server.Serve(tls.NewListener(p.inbound, p.tlsConfig()))
	})
}

// tlsConfig returns TLSConfig with ALPN defaults filled in.
func (p *Proxy) tlsConfig() *tls.Config {
	config := p.TLSConfig.Clone()
	if len(config.NextProtos) == 0 {
		config.NextProtos = []string{"h2", "http/1.1"}
	}
	return config
}

func (p *Proxy) transport() http.RoundTripper {
	if p.Transport != nil {
		return p.Transport
	}
	return p.defaultRT
}

func (p *Proxy) bodyLimit() int64 {
	if p.BodyLimit > 0 {
		return p.BodyLimit
	}
	return DefaultBodyLimit
}

// handleConnect answers CONNECT: listed hosts are handed (still encrypted) to
// the in-process HTTPS server; anything else is tunneled without decryption.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, defaultHTTPSPort)
	}
	_, intercept := p.Hosts.Lookup(target)
	if !intercept && p.ListedOnly {
		p.logAccess("tunnel-refused", "CONNECT", target, r.RemoteAddr)
		http.Error(w, "proxykit: host not served by this proxy", http.StatusForbidden)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		// HTTP/2 CONNECT cannot be hijacked; phones use HTTP/1.1 to proxies anyway.
		http.Error(w, "CONNECT requires HTTP/1.1", http.StatusHTTPVersionNotSupported)
		return
	}

	// Dial before replying so a dead upstream yields a real 502 instead of a reset.
	var upstream net.Conn
	if !intercept {
		dialer := net.Dialer{Timeout: DefaultDialTimeout}
		var err error
		if upstream, err = dialer.DialContext(r.Context(), "tcp", target); err != nil {
			p.logAccess("tunnel-fail", "CONNECT", target, r.RemoteAddr, "err", err)
			http.Error(w, "proxykit: cannot reach "+target, http.StatusBadGateway)
			return
		}
	}

	conn, rw, err := hijacker.Hijack()
	if err != nil {
		if upstream != nil {
			upstream.Close()
		}
		return
	}
	// Clear the server's deadlines; this connection now lives as long as the tunnel.
	conn.SetDeadline(time.Time{})
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		conn.Close()
		if upstream != nil {
			upstream.Close()
		}
		return
	}
	client := withBuffered(conn, rw.Reader)

	if intercept {
		p.logAccess("mitm", "CONNECT", target, r.RemoteAddr)
		if err := p.inbound.push(&connectConn{Conn: client, target: target}); err != nil {
			client.Close()
		}
		return
	}
	p.logAccess("tunnel", "CONNECT", target, r.RemoteAddr)
	tunnel(client, upstream)
}

// serveFlow runs one request through its hook. plainProxy marks absolute-URI
// requests from the entry port, where unlisted hosts are forwarded untouched
// (they are unencrypted anyway); on the HTTPS side an unlisted host is refused.
func (p *Proxy) serveFlow(w http.ResponseWriter, r *http.Request, scheme string, plainProxy bool) {
	hook, listed := p.Hosts.Lookup(r.Host)
	if !listed && !plainProxy {
		http.Error(w, "proxykit: host not intercepted", http.StatusMisdirectedRequest)
		return
	}
	if !listed && p.ListedOnly {
		p.logAccess("forward-refused", r.Method, r.Host, r.RemoteAddr)
		http.Error(w, "proxykit: host not served by this proxy", http.StatusForbidden)
		return
	}

	outgoing := r.Clone(r.Context())
	outgoing.RequestURI = ""
	outgoing.URL.Scheme = scheme
	outgoing.URL.Host = r.Host
	if !plainProxy {
		authority := upstreamAuthority(r.Host, ConnectTarget(r.Context()))
		outgoing.URL.Host, outgoing.Host = authority, authority
	}
	removeHopByHop(outgoing.Header)
	// Dropping the client's value lets the Transport negotiate gzip itself and
	// decode it transparently, so hooks never see compressed bodies.
	outgoing.Header.Del("Accept-Encoding")

	flow := &Flow{
		Request: outgoing,
		Log:     p.Logger.With("host", NormalizeHost(r.Host), "path", r.URL.Path),
		proxy:   p,
		writer:  w,
		start:   time.Now(),
	}
	if hook != nil {
		if !runHook(hook, flow) && !flow.responded {
			flow.Respond(http.StatusInternalServerError, nil, []byte("proxykit: hook panicked\n"))
		}
	}
	flow.finish()
	if p.AccessLog {
		p.Logger.Info("access",
			"client", r.RemoteAddr,
			"method", r.Method,
			"url", scheme+"://"+r.Host+r.URL.RequestURI(),
			"proto", r.Proto,
			"status", flow.status,
			"bytes", flow.written,
			"ms", time.Since(flow.start).Milliseconds())
	}
}

// runHook calls hook and converts a panic into a logged failure so one buggy
// hook cannot take the whole proxy down. Returns false if the hook panicked.
func runHook(hook Hook, flow *Flow) (ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if errors.Is(asError(recovered), http.ErrAbortHandler) {
				panic(recovered)
			}
			flow.Log.Error("hook panicked", "panic", recovered)
			ok = false
		}
	}()
	hook(flow)
	return true
}

func asError(v any) error {
	if err, ok := v.(error); ok {
		return err
	}
	return nil
}

// logAccess writes a non-flow access line (CONNECT decisions, local requests).
func (p *Proxy) logAccess(kind, method, target, client string, extra ...any) {
	if !p.AccessLog {
		return
	}
	p.Logger.Info("access", append([]any{"kind", kind, "client", client, "method", method, "target", target}, extra...)...)
}

// upstreamAuthority picks where an intercepted HTTPS request really goes: the
// Host header's name (correct even if a client coalesces vhosts onto one
// connection) with the CONNECT target's port, or 443 without a CONNECT. The
// Host header's own port is ignored because in direct (ServeTLS) mode it is
// the proxy's listener port, not the origin's.
func upstreamAuthority(host, connectTarget string) string {
	port := defaultHTTPSPort
	if _, targetPort, err := net.SplitHostPort(connectTarget); err == nil {
		port = targetPort
	}
	name := NormalizeHost(host)
	if port != defaultHTTPSPort {
		return net.JoinHostPort(name, port)
	}
	if strings.Contains(name, ":") {
		// Bare IPv6 literal still needs brackets in an authority.
		return "[" + name + "]"
	}
	return name
}

// connectTargetKey is the context key for ConnectTarget.
type connectTargetKey struct{}

// ConnectTarget returns the "host:port" from the CONNECT that carried this
// request, or "" for direct-TLS and plain-proxy requests.
func ConnectTarget(ctx context.Context) string {
	target, _ := ctx.Value(connectTargetKey{}).(string)
	return target
}

// withConnectTarget is the http.Server ConnContext hook that copies the CONNECT
// target from the pushed connection into every request's context.
func withConnectTarget(ctx context.Context, c net.Conn) context.Context {
	if tlsConn, ok := c.(*tls.Conn); ok {
		if connect, ok := tlsConn.NetConn().(*connectConn); ok {
			return context.WithValue(ctx, connectTargetKey{}, connect.target)
		}
	}
	return ctx
}
