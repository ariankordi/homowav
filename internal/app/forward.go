// This file is the API base the web UI talks to: it forwards requests
// to the HomeWAV API with the captured session attached.
//
// The page builds every request itself. This side adds the credentials (which
// therefore never reach the browser), the headers the app sends, and recovery
// from a dead session. It is deliberately not a general proxy: only API paths
// are forwarded, and never the calls that log in or replace the session.

package app

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"hwclient/internal/session"
)

const (
	// apiPrefix is the only part of the service that is forwarded.
	apiPrefix = "/api/"
	// maxForwardBody caps a forwarded request. It is held in memory, because
	// Call may resend it after recovering the session. The biggest forwarded
	// body is a voice message (a few MiB of m4a); photos and videos go to the
	// media bucket instead, and only their URL is forwarded.
	maxForwardBody = 64 << 20
	// Headers the iOS app 2.28.0 sends on API calls. The app puts unix
	// seconds in Date, which is unusual but is what the service sees from it.
	appVersion       = "2.28.0"
	appVersionHeader = "X-App-Version"
	appKindHeader    = "X-HomeWAV-App"
	appKind          = "app_type_visitor"
)

// forwardedHeaders are the request headers the page controls.
var forwardedHeaders = []string{"Content-Type", "Accept", "Accept-Language"}

// Forwarder is an http.Handler for the API base. Mount it with
// http.StripPrefix so it sees paths starting with /api/.
type Forwarder struct {
	// Service supplies the session and performs the call.
	Service *session.Service
	// Log receives one line per failed call. Nil discards them.
	Log *slog.Logger
}

// ServeHTTP forwards one request.
func (f *Forwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !allowedPath(r.URL.Path) {
		writeFailure(w, http.StatusForbidden, "hwclient: this path is not forwarded")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxForwardBody))
	if err != nil {
		writeFailure(w, http.StatusRequestEntityTooLarge, "hwclient: request body is too large")
		return
	}
	header := http.Header{}
	for _, name := range forwardedHeaders {
		if value := r.Header.Get(name); value != "" {
			header.Set(name, value)
		}
	}
	header.Set(appVersionHeader, appVersion)
	header.Set(appKindHeader, appKind)
	header.Set("Date", strconv.FormatInt(time.Now().Unix(), 10))

	target := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	reply, err := f.Service.Call(r.Context(), r.Method, target, header, body)
	switch {
	case errors.Is(err, session.ErrNoSession):
		writeFailure(w, http.StatusServiceUnavailable, "hwclient: no session yet. Open the app once through the proxy (see Settings).")
		return
	case err != nil:
		if f.Log != nil {
			f.Log.Warn("upstream call failed", "path", r.URL.Path, "err", err)
		}
		writeFailure(w, http.StatusBadGateway, "hwclient: could not reach the service: "+err.Error())
		return
	}
	if contentType := reply.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(reply.Status)
	w.Write(reply.Body)
}

// allowedPath admits clean /api/ paths other than the session-creating calls.
func allowedPath(requestPath string) bool {
	// Trailing slashes are significant to the service but must not be a way
	// around the check, so the comparison ignores them.
	trimmed := strings.TrimRight(requestPath, "/")
	if !strings.HasPrefix(requestPath, apiPrefix) || session.IsAuthPath(trimmed) || session.IsAuthPath(trimmed+"/") {
		return false
	}
	return path.Clean(requestPath) == trimmed
}

// writeFailure answers in the service's own {ok, cause} shape, so the page
// has one error path for both sides.
func writeFailure(w http.ResponseWriter, status int, cause string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "cause": cause, "local": true})
}
