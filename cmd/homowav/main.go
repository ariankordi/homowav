// Command homowav runs the session-keeping proxy for the HomeWAV app.
//
// Point the phone at http://<this-box>:<port>/proxy.pac (Wi-Fi > Configure
// Proxy > Automatic), trust the CA that signed -cert, and open the app once.
// Afterwards `curl localhost:8080/_/sessid` prints a working session.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"homowav/server"
	"homowav/session"
)

// secretEnv lets the API secret stay out of the process list.
const secretEnv = "HOMOWAV_API_SECRET"

// defaultKeepAlive refreshes well inside the remember cookies' 30-day window.
const defaultKeepAlive = 12 * time.Hour

func main() {
	listen := flag.String("listen", server.DefaultListen, "plaintext address for the PAC, CONNECT and /_/ endpoints")
	httpsListen := flag.String("https-listen", "", "optional direct-HTTPS address (hosts-file/DNS-override setups)")
	certPath := flag.String("cert", "", "PEM certificate for the intercepted host (required)")
	keyPath := flag.String("key", "", "PEM private key for -cert (required)")
	statePath := flag.String("state", "", "JSON file that keeps credentials across restarts (recommended)")
	pacAddr := flag.String("pac-addr", "", `"host:port" written into the PAC (default: the Host the phone used)`)
	host := flag.String("host", session.DefaultHost, "API host to intercept")
	keepAlive := flag.Duration("keepalive", defaultKeepAlive, "renew the session this often even when the app is idle (0 disables)")
	noAccessLog := flag.Bool("no-access-log", false, "disable per-request access logs")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger, *certPath, *keyPath, server.Config{
		Listen:      *listen,
		HTTPSListen: *httpsListen,
		StatePath:   *statePath,
		PACAddr:     *pacAddr,
		APISecret:   os.Getenv(secretEnv),
		Host:        *host,
		KeepAlive:   *keepAlive,
		AccessLog:   !*noAccessLog,
		Logger:      logger,
	}); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// run loads the certificate, then serves until SIGINT/SIGTERM.
func run(logger *slog.Logger, certPath, keyPath string, cfg server.Config) error {
	if certPath == "" || keyPath == "" {
		return errors.New("-cert and -key are required")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("loading cert: %w", err)
	}
	cfg.Certificate = cert
	if cfg.StatePath == "" {
		logger.Warn("no -state file: credentials are lost on restart")
	}
	if cfg.APISecret == "" {
		logger.Info("/_/ endpoints are loopback-only; set " + secretEnv + " to allow other devices")
	}

	srv, err := server.New(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Run(ctx)
}
