package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"hwclient/internal/session"
)

const (
	// caPath is the public download of the CA certificate.
	caPath = "/ca.pem"
	// caContentType makes iOS offer to install the file as a profile.
	caContentType = "application/x-x509-ca-cert"
	// maxConfigBody caps a settings update.
	maxConfigBody = 64 << 10
	// uploadNameParam carries the original file name of an upload.
	uploadNameParam = "name"
)

// statusReply is everything the UI needs to draw its frame.
type statusReply struct {
	Session session.Status `json:"session"`
	// VisitorID is the override from settings, or else the captured id.
	VisitorID string `json:"visitor_id"`
	// APIHost is the intercepted host, for display.
	APIHost string `json:"api_host"`
	// PACURLs are the proxy configuration URLs to enter on the phone.
	PACURLs []string `json:"pac_urls"`
	// PairingURLs admit another device's browser when opened there.
	PairingURLs []string `json:"pairing_urls"`
	// CAURL is the CA download, empty when an external certificate is used.
	CAURL string `json:"ca_url"`
	// CAFingerprint is the CA's SHA-256, to compare with the phone's display.
	CAFingerprint string `json:"ca_fingerprint"`
	// MediaConfigured reports whether uploads can work.
	MediaConfigured bool `json:"media_configured"`
}

// configReply is the settings as the page may see them: the bucket secret is
// replaced by a flag, so it cannot be read back out.
type configReply struct {
	VisitorID string `json:"visitor_id"`
	Media     Media  `json:"media"`
	// MediaSecretSet reports a stored secret without revealing it.
	MediaSecretSet bool `json:"media_secret_set"`
}

// configUpdate is what the page sends. An empty bucket secret keeps the stored one.
type configUpdate struct {
	VisitorID string `json:"visitor_id"`
	Media     Media  `json:"media"`
}

// api serves the routes under /app/.
func (a *App) api() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app/status", a.handleStatus)
	mux.HandleFunc("POST /app/renew", a.handleRenew)
	mux.HandleFunc("GET /app/config", a.handleGetConfig)
	mux.HandleFunc("PUT /app/config", a.handlePutConfig)
	mux.HandleFunc("POST /app/upload", a.handleUpload)
	return mux
}

func (a *App) handleStatus(w http.ResponseWriter, _ *http.Request) {
	settings := a.config.Get()
	status := a.server.Service().Status()
	reply := statusReply{
		Session:         status,
		VisitorID:       firstNonEmpty(settings.VisitorID, status.VisitorID),
		APIHost:         a.options.Host,
		PACURLs:         []string{},
		PairingURLs:     []string{},
		MediaConfigured: settings.Media.Configured(),
	}
	for _, base := range a.lanBases() {
		reply.PACURLs = append(reply.PACURLs, base+pacPath)
		reply.PairingURLs = append(reply.PairingURLs, PairURL(base, a.pairingSecret()))
	}
	if a.certs != nil {
		reply.CAURL = caPath
		reply.CAFingerprint, _ = a.certs.CAFingerprint()
	}
	writeJSON(w, http.StatusOK, reply)
}

func (a *App) handleRenew(w http.ResponseWriter, r *http.Request) {
	if _, err := a.server.Service().Renewer.Renew(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.handleStatus(w, r)
}

func (a *App) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.visibleConfig())
}

// visibleConfig strips the bucket secret.
func (a *App) visibleConfig() configReply {
	settings := a.config.Get()
	reply := configReply{VisitorID: settings.VisitorID, Media: settings.Media, MediaSecretSet: settings.Media.SecretAccessKey != ""}
	reply.Media.SecretAccessKey = ""
	return reply
}

func (a *App) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var update configUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConfigBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, "settings are not valid JSON: "+err.Error())
		return
	}
	update.VisitorID = strings.TrimSpace(update.VisitorID)
	if update.VisitorID != "" && !isDigits(update.VisitorID) {
		writeError(w, http.StatusBadRequest, "the visitor id must be a number")
		return
	}
	trimMedia(&update.Media)
	err := a.config.Update(func(c *Config) {
		if update.Media.SecretAccessKey == "" {
			update.Media.SecretAccessKey = c.Media.SecretAccessKey
		}
		c.VisitorID, c.Media = update.VisitorID, update.Media
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.visibleConfig())
}

// trimMedia removes stray whitespace from pasted values.
func trimMedia(media *Media) {
	for _, field := range []*string{&media.Endpoint, &media.Region, &media.Bucket, &media.AccessKeyID, &media.SecretAccessKey, &media.PublicBaseURL, &media.KeyPrefix, &media.ACL} {
		*field = strings.TrimSpace(*field)
	}
}

// handleUpload stores the request body in the configured bucket and returns
// its public URL. The body is the file itself; its name comes in the query.
// It is streamed to the bucket as it arrives, so its size is checked from
// Content-Length before anything is read. net/http stops the body at that
// length, so a client cannot send more than it announced.
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	media := a.config.Get().Media
	store, err := NewS3(media, a.options.Transport)
	if err != nil {
		writeError(w, http.StatusConflict, "no media storage is set up yet (see Settings)")
		return
	}
	switch size := r.ContentLength; {
	case size < 0:
		// Browsers always send the length of a file body; S3 needs it up front.
		writeError(w, http.StatusLengthRequired, "the upload has no Content-Length")
		return
	case size == 0:
		writeError(w, http.StatusBadRequest, "the file is empty")
		return
	case size > MaxUploadSize:
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file is too large (the limit is %d GiB)", MaxUploadSize>>30))
		return
	}
	key, err := NewMediaKey(media.KeyPrefix, r.URL.Query().Get(uploadNameParam), time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body := newDeadlineReader(r.Body, http.NewResponseController(w), a.uploadStall)
	publicURL, err := store.Put(r.Context(), key, r.Header.Get("Content-Type"), body, r.ContentLength)
	body.finish(err == nil)
	if err != nil {
		a.log.Warn("upload failed", "err", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": publicURL})
}

// deadlineReader reads a request body and pushes the connection's read
// deadline forward before every read. A sender that goes quiet then fails
// the read after timeout, instead of holding the upload open forever. Only a
// deadline can do this: canceling the outgoing request does not interrupt a
// read that is waiting on the browser.
type deadlineReader struct {
	source     io.Reader
	controller *http.ResponseController
	timeout    time.Duration
	// unsupported is set when the connection has no deadlines (it then
	// behaves like a plain reader).
	unsupported bool
}

func newDeadlineReader(source io.Reader, controller *http.ResponseController, timeout time.Duration) *deadlineReader {
	return &deadlineReader{source: source, controller: controller, timeout: timeout}
}

// Read implements io.Reader.
func (r *deadlineReader) Read(buffer []byte) (int, error) {
	if !r.unsupported && r.controller.SetReadDeadline(time.Now().Add(r.timeout)) != nil {
		r.unsupported = true
	}
	return r.source.Read(buffer)
}

// finish removes the deadline after a complete upload, so it cannot outlive
// it. After a failed one it expires the deadline instead: net/http drains an
// unread body before replying, and that must not wait on a stalled sender.
// The connection is then closed after the reply.
func (r *deadlineReader) finish(succeeded bool) {
	if r.unsupported {
		return
	}
	if succeeded {
		r.controller.SetReadDeadline(time.Time{})
	} else {
		r.controller.SetReadDeadline(time.Now())
	}
}

// serveCA offers the CA certificate. It is public information, and the phone
// has to fetch it before it trusts anything from us.
func (a *App) serveCA(w http.ResponseWriter, _ *http.Request) {
	if a.certs == nil {
		http.Error(w, "this instance uses an external certificate; get its CA from whoever issued it", http.StatusNotFound)
		return
	}
	pem, err := a.certs.CAPEM()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", caContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="hwclient-ca.pem"`)
	w.Write(pem)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

// writeError uses the same {ok, cause} shape as the service, so the page has
// one error path.
func writeError(w http.ResponseWriter, status int, cause string) {
	writeJSON(w, status, map[string]any{"ok": false, "cause": cause, "local": true})
}

func isDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
