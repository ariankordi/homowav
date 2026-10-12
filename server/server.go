// Package server assembles the proxy, the session hook and the local
// endpoints into one runnable unit. It is what the CLI runs and what a future
// web UI embeds: construct a Config in code, optionally add routes with
// Handle, then Run. Package app does exactly that for the full program.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"hwclient/access"
	"hwclient/proxykit"
	"hwclient/session"
)

const (
	// DefaultListen is the plaintext port the phone's PAC URL points at.
	DefaultListen = ":8080"
	// shutdownGrace is how long in-flight requests get when Run's context ends.
	shutdownGrace = 5 * time.Second
	// readHeaderTimeout stops idle half-open connections from piling up.
	readHeaderTimeout = 30 * time.Second
	// pacPath is where the phone fetches its proxy configuration.
	pacPath = "/proxy.pac"
)

// Config describes one server. Only a certificate source is required.
type Config struct {
	// Listen is the plaintext address serving the PAC, CONNECT and /_/ endpoints.
	Listen string
	// HTTPSListen optionally accepts direct TLS (hosts-file or DNS override setups).
	HTTPSListen string
	// Certificate is presented for the intercepted host; the phone must trust its issuer.
	Certificate tls.Certificate
	// GetCertificate supplies the certificate instead, for sources that
	// re-issue it over time (see package certs). It wins over Certificate.
	GetCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	// PACAddr is the "host:port" written into the PAC. Empty uses the Host the phone fetched it with.
	PACAddr string
	// StatePath persists credentials across restarts. Empty keeps them in memory only.
	StatePath string
	// Gate guards the /_/ credential endpoints. Nil admits loopback callers only.
	Gate func(http.Handler) http.Handler
	// OpenProxy also tunnels and forwards hosts that are not intercepted. It
	// is off by default: the PAC never sends other hosts here, and an open
	// proxy lets any LAN client reach this machine's loopback services.
	OpenProxy bool
	// Host is the API host to intercept. Empty means session.DefaultHost.
	Host string
	// KeepAlive renews the session this often even if the app is idle. Zero disables it.
	KeepAlive time.Duration
	// AccessLog logs one line per request.
	AccessLog bool
	// Logger receives all logs. Nil discards them.
	Logger *slog.Logger
	// Transport sends upstream requests. Nil uses proxykit.NewTransport().
	Transport http.RoundTripper
}

// Server is a configured, not yet running, hwclient instance.
type Server struct {
	cfg     Config
	proxy   *proxykit.Proxy
	service *session.Service
	local   *http.ServeMux

	mu   sync.Mutex
	addr net.Addr
}

// New validates cfg, loads saved credentials, and wires everything together.
func New(cfg Config) (*Server, error) {
	if len(cfg.Certificate.Certificate) == 0 && cfg.GetCertificate == nil {
		return nil, errors.New("server: Config.Certificate or Config.GetCertificate is required")
	}
	if cfg.Gate == nil {
		cfg.Gate = access.Gate{}.Wrap
	}
	if cfg.Listen == "" {
		cfg.Listen = DefaultListen
	}
	if cfg.Host == "" {
		cfg.Host = session.DefaultHost
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Transport == nil {
		cfg.Transport = proxykit.NewTransport()
	}

	store, err := session.NewStore(cfg.StatePath)
	if err != nil {
		return nil, err
	}
	service := &session.Service{
		Store:   store,
		Renewer: session.NewRenewer(store, cfg.Transport, cfg.Host, cfg.Logger),
		Log:     cfg.Logger,
		Client:  &http.Client{Transport: cfg.Transport},
		BaseURL: "https://" + cfg.Host,
	}
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cfg.Certificate}}
	if cfg.GetCertificate != nil {
		tlsConfig = &tls.Config{GetCertificate: cfg.GetCertificate}
	}
	proxy := &proxykit.Proxy{
		Hosts:      proxykit.NewHostTable(),
		TLSConfig:  tlsConfig,
		Transport:  cfg.Transport,
		Logger:     cfg.Logger,
		AccessLog:  cfg.AccessLog,
		ListedOnly: !cfg.OpenProxy,
	}
	proxy.Hosts.Handle(cfg.Host, service.Hook)

	local := http.NewServeMux()
	local.Handle("GET "+pacPath, proxy.PACHandler(cfg.PACAddr))
	local.Handle("/_/", cfg.Gate(service.Handler()))
	return &Server{cfg: cfg, proxy: proxy, service: service, local: local}, nil
}

// Service exposes the credential store, renewer and status for embedding UIs.
func (s *Server) Service() *session.Service { return s.service }

// Handle adds a local route (for example a web UI) next to the PAC and /_/
// endpoints. Call it before Run. Patterns follow http.ServeMux.
func (s *Server) Handle(pattern string, handler http.Handler) { s.local.Handle(pattern, handler) }

// Handler is the full entry-port handler, for callers that run their own listener.
func (s *Server) Handler() http.Handler { return s.proxy.EntryHandler(s.local) }

// Addr is the bound entry address once Run is listening, or nil.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Run listens and serves until ctx ends or a listener fails, then shuts down.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.addr = listener.Addr()
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	failed := make(chan error, 2)

	entry := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: readHeaderTimeout}
	go func() { failed <- fmt.Errorf("entry listener: %w", entry.Serve(listener)) }()
	s.cfg.Logger.Info("listening", "addr", listener.Addr().String(), "pac", pacPath, "host", s.cfg.Host)

	if s.cfg.HTTPSListen != "" {
		tlsListener, err := net.Listen("tcp", s.cfg.HTTPSListen)
		if err != nil {
			entry.Close()
			return err
		}
		defer tlsListener.Close()
		go func() { failed <- fmt.Errorf("direct HTTPS listener: %w", s.proxy.ServeTLS(tlsListener)) }()
		s.cfg.Logger.Info("direct HTTPS listening", "addr", tlsListener.Addr().String())
	}
	if s.cfg.KeepAlive > 0 {
		go s.service.Renewer.KeepAlive(ctx, s.cfg.KeepAlive)
	}

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-failed:
	}
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer stop()
	entry.Shutdown(shutdownCtx)
	s.proxy.Close()
	return runErr
}
