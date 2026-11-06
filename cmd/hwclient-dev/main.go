// Command hwclient-dev runs the real program against an in-memory fake of
// the service, with made-up contacts and messages. Use it to work on the UI:
// nothing it does reaches HomeWAV, costs money, or needs a phone.
//
//	go run ./cmd/hwclient-dev -data scratch/dev-data
//
// Then open the printed URL, or run "npm run dev" in web/ for live reload.
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"hwclient/internal/app"
	"hwclient/internal/fakehw"
)

// devListen differs from the real default, so both can run side by side.
const devListen = "127.0.0.1:8090"

// devAutoReply is how long the fake contact takes to answer a text.
const devAutoReply = 4 * time.Second

// stateFile matches the name package app uses inside the data folder.
const stateFile = "state.json"

func main() {
	listen := flag.String("listen", devListen, "address for the UI and the local API")
	dataDir := flag.String("data", "", "folder for the throwaway settings and certificates (required)")
	oneContact := flag.Bool("one-contact", false, "give the fake account a single contact")
	signedOut := flag.Bool("signed-out", false, "start with no session, to try the first-run screen (fake login: "+fakehw.Username+" / "+fakehw.Password+")")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger, *listen, *dataDir, *oneContact, *signedOut); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, listen, dataDir string, oneContact, signedOut bool) error {
	if dataDir == "" {
		return errors.New("-data is required, for example -data scratch/dev-data")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	fake, transport, stop := fakehw.Start()
	defer stop()
	// Set before anything can call the fake: the fields are not locked.
	fake.AutoReply = devAutoReply
	fake.SingleContact = oneContact
	// Always start from a fresh fake session: the fake forgets them on exit.
	// Signed out means no saved state at all, as on a first run.
	statePath := filepath.Join(dataDir, stateFile)
	if signedOut {
		if err := os.Remove(statePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	} else if err := os.WriteFile(statePath, fake.SeedState(), 0o600); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	instance, err := app.New(app.Options{DataDir: dataDir, Listen: listen, Transport: transport, Logger: logger, AccessLog: true})
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	go func() {
		if url, err := instance.URL(); err == nil {
			logger.Info("dev UI ready (fake service, nothing is sent for real)", "url", url)
		}
	}()
	return instance.Run(ctx)
}
