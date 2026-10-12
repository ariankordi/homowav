// Command hwclient runs the session-keeping proxy for the HomeWAV app and
// the web UI on top of it, without a tray icon. Use it on a server or when
// you want the logs in a terminal.
//
// Open http://localhost:8080 for the UI. Its Settings page walks through the
// phone setup: trust the CA, set the PAC URL, open the app once.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"hwclient/app"
	"hwclient/server"
	"hwclient/session"
)

// secretEnv lets the pairing secret stay out of the process list.
const secretEnv = "HWCLIENT_API_SECRET"

func main() {
	options := app.Options{}
	flag.StringVar(&options.Listen, "listen", server.DefaultListen, "address for the proxy, the web UI and the local API")
	flag.StringVar(&options.HTTPSListen, "https-listen", "", "optional direct-HTTPS address (hosts-file/DNS-override setups)")
	flag.StringVar(&options.DataDir, "data", "", "folder for settings, credentials and certificates (default: the user config directory)")
	flag.StringVar(&options.CertPath, "cert", "", "use this PEM certificate instead of the managed CA (needs -key)")
	flag.StringVar(&options.KeyPath, "key", "", "PEM private key for -cert")
	flag.StringVar(&options.StatePath, "state", "", "credentials file (default: state.json in the data folder)")
	flag.StringVar(&options.PACAddr, "pac-addr", "", `"host:port" written into the PAC (default: the Host the phone used)`)
	flag.StringVar(&options.Host, "host", session.DefaultHost, "API host to intercept")
	flag.DurationVar(&options.KeepAlive, "keepalive", app.DefaultKeepAlive, "renew the session this often even when the app is idle (0 disables)")
	flag.BoolVar(&options.OpenProxy, "open-proxy", false, "also tunnel hosts that are not intercepted (exposes this machine's loopback to the LAN)")
	noAccessLog := flag.Bool("no-access-log", false, "disable per-request access logs")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	options.AccessLog = !*noAccessLog
	options.SecretOverride = os.Getenv(secretEnv)
	options.Logger = logger

	instance, err := app.New(options)
	if err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if url, err := instance.URL(); err == nil {
			logger.Info("web UI ready", "url", url)
		}
	}()
	if err := instance.Run(ctx); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}
