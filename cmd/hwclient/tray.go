package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fyne.io/systray"

	"hwclient/internal/app"
)

const (
	// probeTimeout bounds the check for an already running instance.
	probeTimeout = 2 * time.Second
	// stopTimeout bounds how long Quit waits for the server to shut down.
	stopTimeout = 10 * time.Second
	// statusPath answers on a running instance; see internal/app/api.go.
	statusPath = "/app/status"
)

// runTray runs the server behind a tray / menu bar icon. systray.Run must
// own the main thread (a macOS requirement), so the server runs beside it.
func runTray(parsed flags) {
	writer, closeLog, err := openLog(parsed.options.DataDir)
	if err != nil {
		fail(err)
	}
	defer closeLog()
	logger := slog.New(slog.NewTextHandler(writer, nil))
	logger.Info("starting", "version", version)

	options := parsed.options
	options.Logger = logger
	instance, err := app.New(options)
	if err != nil {
		logger.Error("fatal", "err", err)
		fail(err)
	}

	// A second start (double-clicking the app again) just shows the UI. The
	// check comes after app.New, which resolves the port saved in settings.
	if url, running := runningInstance(instance.Listen()); running {
		logger.Info("already running; opening the browser", "url", url)
		if err := openURL(url); err != nil {
			logger.Error("cannot open the browser", "err", err)
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	// restarting is only written before stopped is closed, and read after.
	restarting := false
	onReady := func() {
		setupMenu(instance, logger)
		go func() {
			defer close(stopped)
			err := instance.Run(ctx)
			switch {
			case errors.Is(err, app.ErrRestart):
				restarting = true
			case err != nil:
				logger.Error("fatal", "err", err)
			}
			// Whether it failed or was asked to stop, the icon goes with it.
			systray.Quit()
		}()
		if !parsed.noBrowser {
			go openUI(instance, logger)
		}
		// Ctrl-C in a terminal, or a logout, quits like the menu item does.
		go func() {
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
			<-signals
			systray.Quit()
		}()
	}
	onExit := func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(stopTimeout):
			logger.Warn("server did not stop in time")
		}
	}
	systray.Run(onReady, onExit)
	if restarting {
		// The page that asked for the restart reconnects by itself, so the
		// new copy does not open another browser tab.
		closeLog()
		if err := relaunch([]string{"-no-browser"}); err != nil {
			fail(err)
		}
	}
}

// setupMenu sets the icon and the menu, and handles clicks until the app quits.
func setupMenu(instance *app.App, logger *slog.Logger) {
	systray.SetTemplateIcon(templateIcon, trayIcon())
	systray.SetTooltip(app.Name)
	open := systray.AddMenuItem("Open "+app.Name, "Open the web UI in the browser")
	folder := systray.AddMenuItem("Show data folder", "Settings, certificates and the log file")
	restart := systray.AddMenuItem("Restart", "Stop and start again, for example after changing the port")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Stop "+app.Name)
	// Clicking the icon itself opens the UI where the platform allows it (Windows).
	systray.SetOnTapped(func() { openUI(instance, logger) })
	go func() {
		for {
			select {
			case <-open.ClickedCh:
				openUI(instance, logger)
			case <-folder.ClickedCh:
				if err := openPath(instance.DataDir()); err != nil {
					logger.Error("cannot open the data folder", "err", err)
				}
			case <-restart.ClickedCh:
				instance.RequestRestart()
			case <-quit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

// openUI opens the browser once the server is listening.
func openUI(instance *app.App, logger *slog.Logger) {
	url, err := instance.URL()
	if err == nil {
		err = openURL(url)
	}
	if err != nil {
		logger.Error("cannot open the browser", "err", err)
	}
}

// runningInstance checks whether this program already answers on the listen
// port, and returns its UI address if so. Another program on the port is
// not mistaken for it: the status reply has to parse.
func runningInstance(listen string) (string, bool) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", false
	}
	base := "http://" + net.JoinHostPort("localhost", port)
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Get(base + statusPath)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var status struct {
		Session json.RawMessage `json:"session"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&status) != nil || status.Session == nil {
		return "", false
	}
	return base, true
}
