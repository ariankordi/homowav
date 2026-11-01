// Command hwclient runs the session-keeping proxy for the HomeWAV app and
// the web UI on top of it.
//
// By default it lives in the tray (Windows) or menu bar (macOS), opens the UI
// in the browser and logs to hwclient.log in the data folder. Started again
// while running, it only opens the browser. With -headless it is a plain
// server logging to the terminal, for servers or debugging.
//
// The UI's Settings page walks through the phone setup: trust the CA, set the
// PAC URL, open the app once.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"hwclient/internal/app"
	"hwclient/internal/session"
)

const (
	// secretEnv lets the pairing secret stay out of the process list.
	secretEnv = "HWCLIENT_API_SECRET"
	// logFileName is written in the data folder in tray mode, where there is
	// no terminal (on Windows the program is built without a console).
	logFileName = "hwclient.log"
	// maxLogSize starts the log over once it grows past this, at startup.
	maxLogSize = 5 << 20
)

// version is stamped by the release build (-ldflags "-X main.version=v1.2.3").
var version = "dev"

// flags is everything parsed from the command line.
type flags struct {
	options     app.Options
	headless    bool
	noBrowser   bool
	showVersion bool
}

func parseFlags() flags {
	parsed := flags{}
	options := &parsed.options
	flag.StringVar(&options.Listen, "listen", app.DefaultListen, "address for the proxy, the web UI and the local API")
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
	flag.BoolVar(&parsed.headless, "headless", false, "no tray icon; log to the terminal")
	flag.BoolVar(&parsed.noBrowser, "no-browser", false, "do not open the browser on start (tray mode)")
	flag.BoolVar(&parsed.showVersion, "version", false, "print the version and exit")
	flag.Parse()
	options.AccessLog = !*noAccessLog
	options.SecretOverride = os.Getenv(secretEnv)
	return parsed
}

func main() {
	parsed := parseFlags()
	if parsed.showVersion {
		fmt.Println(app.Name, version)
		return
	}
	if parsed.options.DataDir == "" {
		dir, err := app.DefaultDataDir()
		if err != nil {
			fail(err)
		}
		parsed.options.DataDir = dir
	}
	if parsed.headless {
		runHeadless(parsed.options)
		return
	}
	runTray(parsed)
}

// runHeadless serves until interrupted, logging to the terminal.
func runHeadless(options app.Options) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logger.Info("starting", "version", version)
	options.Logger = logger
	instance, err := app.New(options)
	if err != nil {
		fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if url, err := instance.URL(); err == nil {
			logger.Info("web UI ready", "url", url)
		}
	}()
	if err := instance.Run(ctx); err != nil {
		fail(err)
	}
}

// openLog opens the log file in the data folder, starting it over when large.
// Logs also go to stderr, which helps when tray mode is started from a terminal.
func openLog(dataDir string) (io.Writer, func(), error) {
	if err := os.MkdirAll(dataDir, session.PrivateDirMode); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dataDir, logFileName)
	mode := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogSize {
		mode |= os.O_TRUNC
	}
	file, err := os.OpenFile(path, mode, session.PrivateFileMode)
	if err != nil {
		return nil, nil, err
	}
	// The file comes first: io.MultiWriter stops at the first failing writer,
	// and a Windows GUI program has no usable stderr.
	return io.MultiWriter(file, os.Stderr), func() { file.Close() }, nil
}

// fail reports a startup error and exits. In tray mode on Windows nobody sees
// stderr, so the log file (when open) is the place to look.
func fail(err error) {
	fmt.Fprintln(os.Stderr, app.Name+":", err)
	os.Exit(1)
}
