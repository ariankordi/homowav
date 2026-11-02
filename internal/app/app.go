// Package app assembles the whole program: the session proxy, the settings,
// the certificates, the API base and the web UI. The CLI and the tray command
// both run it, so they differ only in how they start and stop.
//
// Files:
//   - app.go: options, data directory, routes.
//   - server.go: the proxy, session hook and listeners.
//   - api.go: the /app/ settings API used by the web UI.
//   - forward.go: the /hw/ API base that forwards to the service.
//   - access.go: who may use the local endpoints (loopback, pairing, CSRF).
//   - certs.go: the private CA and leaf certificate.
//   - config.go: settings persisted as config.json.
//   - media.go, s3.go: uploads to an S3-compatible bucket.
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"hwclient/internal/session"
	"hwclient/web"
)

// Name is what the user sees: the tray, the CA certificate the phone shows
// when installing it, and logs. The web UI has its own copy (AppName in
// web/src/constants.ts). Internal names (module, binary, data folder, env
// vars) stay "hwclient" so a rename does not move anyone's settings.
const Name = "HW Client"

const (
	// dataDirName is the folder created under the user's config directory.
	dataDirName = "hwclient"
	// File and folder names inside the data directory.
	stateFile  = "state.json"
	configFile = "config.json"
	certsDir   = "certs"
	// apiBase is where the page reaches the service API.
	apiBase = "/hw"
	// DefaultKeepAlive refreshes well inside the remember cookies' 30-day window.
	DefaultKeepAlive = 12 * time.Hour
	// addrWait bounds how long URL waits for the listener to come up.
	addrWait = 5 * time.Second
	// addrPoll is how often URL checks for it.
	addrPoll = 10 * time.Millisecond
)

// Options configures one App. The zero value is usable.
type Options struct {
	// DataDir holds settings, credentials and certificates. Empty uses
	// DefaultDataDir.
	DataDir string
	// Listen is the address for the proxy, the UI and the local API. Empty
	// uses the port saved in settings, or else DefaultListen.
	Listen string
	// CanRestart tells the UI that the launcher relaunches the program when
	// Run returns ErrRestart, so a port change can be applied from the UI.
	CanRestart bool
	// HTTPSListen optionally accepts direct TLS.
	HTTPSListen string
	// CertPath and KeyPath use an existing certificate instead of the managed
	// CA. The UI then cannot offer the CA for download.
	CertPath, KeyPath string
	// StatePath overrides where credentials are kept.
	StatePath string
	// PACAddr is the "host:port" written into the PAC.
	PACAddr string
	// Host is the API host to intercept. Empty means session.DefaultHost.
	Host string
	// KeepAlive renews the session this often. Zero disables it.
	KeepAlive time.Duration
	// AccessLog logs one line per request.
	AccessLog bool
	// SecretOverride replaces the stored pairing secret for this run.
	SecretOverride string
	// OpenProxy also serves hosts that are not intercepted (see ServerConfig).
	OpenProxy bool
	// Logger receives all logs. Nil discards them.
	Logger *slog.Logger
	// Transport sends upstream requests. Nil uses the default.
	Transport http.RoundTripper
}

// App is a configured, not yet running, instance.
type App struct {
	options Options
	server  *Server
	config  *ConfigStore
	// certs is nil when an external certificate is used.
	certs *CertManager
	gate  Gate
	log   *slog.Logger
	// uploadStall ends an upload whose sender has gone quiet this long; see
	// stallTimeout. A field so tests can shorten it.
	uploadStall time.Duration
	// listenFromFlag is set when Options.Listen was given, so the saved port
	// does not apply.
	listenFromFlag bool
	// restart asks Run to stop and return ErrRestart. Buffered, so a request
	// never blocks the handler that makes it.
	restart chan struct{}
}

// ErrRestart is returned by Run after RequestRestart. The launcher is expected
// to start the program again, which picks up settings such as the port.
var ErrRestart = errors.New("app: restart requested")

// DefaultDataDir is the per-user folder for settings and credentials.
func DefaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("app: no user config directory: %w", err)
	}
	return filepath.Join(base, dataDirName), nil
}

// New loads settings and credentials and wires everything together.
func New(options Options) (*App, error) {
	if options.Logger == nil {
		options.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if options.Host == "" {
		options.Host = session.DefaultHost
	}
	if options.DataDir == "" {
		dir, err := DefaultDataDir()
		if err != nil {
			return nil, err
		}
		options.DataDir = dir
	}
	if err := os.MkdirAll(options.DataDir, session.PrivateDirMode); err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	if options.StatePath == "" {
		options.StatePath = filepath.Join(options.DataDir, stateFile)
	}
	settings, err := LoadConfig(filepath.Join(options.DataDir, configFile))
	if err != nil {
		return nil, err
	}
	instance := &App{
		options:        options,
		config:         settings,
		log:            options.Logger,
		uploadStall:    stallTimeout,
		listenFromFlag: options.Listen != "",
		restart:        make(chan struct{}, 1),
	}
	instance.options.Listen = listenAddress(options.Listen, settings.Get().Port)
	options = instance.options
	instance.gate = Gate{Secret: instance.pairingSecret}

	serverConfig := ServerConfig{
		Listen:      options.Listen,
		HTTPSListen: options.HTTPSListen,
		PACAddr:     options.PACAddr,
		StatePath:   options.StatePath,
		Host:        options.Host,
		KeepAlive:   options.KeepAlive,
		AccessLog:   options.AccessLog,
		OpenProxy:   options.OpenProxy,
		Logger:      options.Logger,
		Transport:   options.Transport,
		Gate:        instance.gate.Wrap,
		Account:     instance.savedAccount,
	}
	switch {
	case options.CertPath != "" && options.KeyPath != "":
		certificate, err := tls.LoadX509KeyPair(options.CertPath, options.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("app: loading certificate: %w", err)
		}
		serverConfig.Certificate = certificate
	case options.CertPath != "" || options.KeyPath != "":
		return nil, errors.New("app: a certificate and its key must be given together")
	default:
		manager, err := NewCertManager(filepath.Join(options.DataDir, certsDir), []string{options.Host})
		if err != nil {
			return nil, err
		}
		instance.certs = manager
		serverConfig.GetCertificate = manager.GetCertificate
	}

	instance.server, err = NewServer(serverConfig)
	if err != nil {
		return nil, err
	}
	instance.routes()
	return instance, nil
}

// routes mounts everything next to the PAC and the credential endpoints.
func (a *App) routes() {
	forwarder := &Forwarder{Service: a.server.Service(), Log: a.log}
	a.server.Handle(apiBase+"/", a.gate.Wrap(http.StripPrefix(apiBase, forwarder)))
	a.server.Handle("/app/", a.gate.Wrap(a.api()))
	// Public: the phone fetches these before it can be paired or trusted.
	a.server.Handle("GET "+caPath, http.HandlerFunc(a.serveCA))
	a.server.Handle("GET "+PairPath, a.gate.PairHandler())
	a.server.Handle("/", web.Handler())
}

// savedAccount is the renewer's view of the saved login.
func (a *App) savedAccount() (username, password string) {
	account := a.config.Get().Account
	return account.Username, account.Password
}

// pairingSecret is the gate's view of the secret.
func (a *App) pairingSecret() string {
	if a.options.SecretOverride != "" {
		return a.options.SecretOverride
	}
	return a.config.Get().PairingSecret
}

// DataDir is the folder holding settings, credentials and certificates.
func (a *App) DataDir() string { return a.options.DataDir }

// listenAddress picks the entry address: an explicit one wins, then the
// saved port (on all interfaces, like the default), then DefaultListen.
func listenAddress(explicit string, savedPort int) string {
	switch {
	case explicit != "":
		return explicit
	case savedPort != 0:
		return ":" + strconv.Itoa(savedPort)
	default:
		return DefaultListen
	}
}

// Listen is the address the entry port binds, as resolved from the options
// and settings. The tray uses it to find an instance that is already running.
func (a *App) Listen() string { return a.options.Listen }

// RequestRestart makes Run stop and return ErrRestart. It reports false when
// the launcher cannot relaunch (Options.CanRestart is off).
func (a *App) RequestRestart() bool {
	if !a.options.CanRestart {
		return false
	}
	select {
	case a.restart <- struct{}{}:
	default: // One is already pending.
	}
	return true
}

// Run serves until ctx ends, a listener fails, or a restart is requested
// (then it returns ErrRestart once the server has shut down).
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- a.server.Run(ctx) }()
	select {
	case err := <-result:
		return err
	case <-a.restart:
		a.log.Info("restarting")
		cancel()
		if err := <-result; err != nil {
			return err
		}
		return ErrRestart
	}
}

// URL returns the address of the UI on this machine once Run is listening.
func (a *App) URL() (string, error) {
	deadline := time.Now().Add(addrWait)
	for time.Now().Before(deadline) {
		if port := a.port(); port != 0 {
			return "http://" + net.JoinHostPort("localhost", strconv.Itoa(port)), nil
		}
		time.Sleep(addrPoll)
	}
	return "", errors.New("app: not listening yet")
}

// port is the bound entry port, or 0 before Run is listening.
func (a *App) port() int {
	if addr, ok := a.server.Addr().(*net.TCPAddr); ok {
		return addr.Port
	}
	return 0
}

// lanBases lists "http://ip:port" for every address other devices can use.
func (a *App) lanBases() []string {
	port := a.port()
	if port == 0 {
		return nil
	}
	bases := []string{}
	for _, ip := range lanAddresses() {
		bases = append(bases, "http://"+net.JoinHostPort(ip, strconv.Itoa(port)))
	}
	return bases
}

// lanAddresses returns this machine's private IPv4 addresses.
func lanAddresses() []string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	found := []string{}
	for _, address := range addresses {
		network, ok := address.(*net.IPNet)
		if !ok {
			continue
		}
		ip := network.IP.To4()
		if ip == nil || !ip.IsPrivate() {
			continue // Phones on the same Wi-Fi reach us over private IPv4.
		}
		found = append(found, ip.String())
	}
	return found
}
