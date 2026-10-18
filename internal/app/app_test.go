package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hwclient/internal/fakehw"
)

// harness is a full App in front of a fake service, driven over real HTTP
// from loopback, as the UI on the same machine would.
type harness struct {
	fake *fakehw.Server
	app  *App
	base string
}

// newHarness starts an App whose upstream is a fakehw server.
func newHarness(t *testing.T, seedSession bool) *harness {
	t.Helper()
	fake, transport, stop := fakehw.Start()
	t.Cleanup(stop)

	dataDir := t.TempDir()
	if seedSession {
		if err := os.WriteFile(filepath.Join(dataDir, stateFile), fake.SeedState(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	instance, err := New(Options{DataDir: dataDir, Listen: "127.0.0.1:0", Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	base, err := instance.URL()
	if err != nil {
		t.Fatal(err)
	}
	return &harness{fake: fake, app: instance, base: base}
}

// do sends a request the way the page does (with the client header) and
// returns the status and body.
func (h *harness) do(t *testing.T, method, path, contentType string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, h.base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(ClientHeader, "1")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// apiReply is the service's envelope.
type apiReply struct {
	OK    bool            `json:"ok"`
	Cause string          `json:"cause"`
	Body  json.RawMessage `json:"body"`
}

func (h *harness) api(t *testing.T, method, path, contentType string, body []byte) apiReply {
	t.Helper()
	status, data := h.do(t, method, path, contentType, body)
	var reply apiReply
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatalf("%s %s: status %d, undecodable %q", method, path, status, data)
	}
	return reply
}

const formType = "application/x-www-form-urlencoded"

// newPubID runs message/init for the first fake contact.
func (h *harness) newPubID(t *testing.T) string {
	t.Helper()
	reply := h.api(t, http.MethodGet, "/hw/api/message/init?inmate_id=1000001&visitor_id="+fakehw.VisitorID, "", nil)
	var body struct {
		PubID string `json:"pubid"`
	}
	if json.Unmarshal(reply.Body, &body); !reply.OK || body.PubID == "" {
		t.Fatalf("message/init failed: %+v", reply)
	}
	return body.PubID
}

func TestListContactsAndReadThread(t *testing.T) {
	h := newHarness(t, true)
	reply := h.api(t, http.MethodPost, "/hw/api/app/inmates-by-visitor", formType, []byte("id="+fakehw.VisitorID))
	var contacts []struct {
		ID       string `json:"id"`
		FullName string `json:"full_name"`
	}
	if json.Unmarshal(reply.Body, &contacts); !reply.OK || len(contacts) != 2 || contacts[0].FullName != "Alex Example" {
		t.Fatalf("contacts = %+v (%+v)", contacts, reply)
	}
	reply = h.api(t, http.MethodGet, "/hw/api/app/visitor/chat?inmate_id=1000001&page=1&limit=3", "", nil)
	var thread []fakehw.Message
	if json.Unmarshal(reply.Body, &thread); !reply.OK || len(thread) != 3 || thread[0].Type != "video" {
		t.Fatalf("thread page = %+v (%+v)", thread, reply)
	}
}

func TestSendTextGifAndVoice(t *testing.T) {
	h := newHarness(t, true)
	before := len(h.fake.Messages())

	text := url.Values{"type": {"text"}, "body": {"hello there"}, "elapsed_time": {"0"}, "gallery_url": {""},
		"inmate_id": {"1000001"}, "visitor_id": {fakehw.VisitorID}, "pubid": {h.newPubID(t)}, "sender": {"visitor"}}
	if reply := h.api(t, http.MethodPost, "/hw/api/message/send", formType, []byte(text.Encode())); !reply.OK {
		t.Fatalf("text send failed: %+v", reply)
	}

	gif := url.Values{"type": {"gif"}, "body": {""}, "elapsed_time": {"0"}, "gallery_url": {"https://media.example/a.gif"},
		"inmate_id": {"1000001"}, "visitor_id": {fakehw.VisitorID}, "pubid": {h.newPubID(t)}, "sender": {"visitor"}}
	if reply := h.api(t, http.MethodPost, "/hw/api/message/send", formType, []byte(gif.Encode())); !reply.OK {
		t.Fatalf("gif send failed: %+v", reply)
	}

	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	for name, value := range map[string]string{"type": "voice", "body": "", "elapsed_time": "12.5", "inmate_id": "1000001", "sender": "visitor", "pubid": h.newPubID(t)} {
		form.WriteField(name, value)
	}
	part, _ := form.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="voice"; filename="clip.m4a"`},
		"Content-Type":        {"audio/m4a"},
	})
	part.Write(bytes.Repeat([]byte{0x42}, 2048))
	form.Close()
	if reply := h.api(t, http.MethodPost, "/hw/api/message/send", form.FormDataContentType(), upload.Bytes()); !reply.OK {
		t.Fatalf("voice send failed: %+v", reply)
	}

	messages := h.fake.Messages()[before:]
	if len(messages) != 3 || messages[0].Body != "hello there" || messages[1].ImageURL != "https://media.example/a.gif" || messages[2].Type != "voice" {
		t.Errorf("stored messages = %+v", messages)
	}
	if h.fake.Voice.ContentType != "audio/m4a" || h.fake.Voice.Size != 2048 {
		t.Errorf("voice part arrived as %+v", h.fake.Voice)
	}
}

func TestDeadSessionIsRenewedTransparently(t *testing.T) {
	h := newHarness(t, true)
	before := h.app.server.Service().Store.Snapshot().PHPSESSID
	h.fake.ExpireAll()
	reply := h.api(t, http.MethodPost, "/hw/api/app/inmates-by-visitor", formType, []byte("id="+fakehw.VisitorID))
	if !reply.OK {
		t.Fatalf("call after expiry failed: %+v", reply)
	}
	if after := h.app.server.Service().Store.Snapshot().PHPSESSID; after == before || after == "" {
		t.Errorf("session was not renewed: %q -> %q", before, after)
	}
}

func TestNoSessionGivesAClearLocalError(t *testing.T) {
	h := newHarness(t, false)
	status, data := h.do(t, http.MethodGet, "/hw/api/app/visitor/chat?inmate_id=1&page=1&limit=1", "", nil)
	if status != http.StatusServiceUnavailable || !strings.Contains(string(data), `"local":true`) || !strings.Contains(string(data), "no session yet") {
		t.Errorf("status %d, body %s", status, data)
	}
}

func TestForwarderRefusesAuthAndNonAPIPaths(t *testing.T) {
	h := newHarness(t, true)
	// The last two are redirected to their clean form by the router, which is
	// then refused or is not an API path at all. Either way nothing is forwarded.
	cases := map[string]string{
		"/hw/api/app/login/":              "403",
		"/hw/api/app/login":               "403",
		"/hw/api/app/refetch-session/v2":  "403",
		"/hw/api/app/refetch-session/v2/": "403",
		"/hw/account/logout":              "403",
		"/hw/api/../account/logout":       "307",
		"/hw/api//app/login/":             "307",
	}
	for path, want := range cases {
		// Go's client would normalize dot segments away, so send the raw path.
		conn, err := net.Dial("tcp", strings.TrimPrefix(h.base, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(conn, "POST "+path+" HTTP/1.1\r\nHost: localhost\r\n"+ClientHeader+": 1\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		raw, _ := io.ReadAll(conn)
		conn.Close()
		if !strings.HasPrefix(string(raw), "HTTP/1.1 "+want) {
			t.Errorf("%s: got %q, want %s", path, strings.SplitN(string(raw), "\r\n", 2)[0], want)
		}
	}
}

func TestStateChangingRequestsNeedTheClientHeader(t *testing.T) {
	h := newHarness(t, true)
	// A form posted by a foreign page arrives without the custom header.
	resp, err := http.Post(h.base+"/hw/api/message/send", formType, strings.NewReader("type=text"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status %d, want 403", resp.StatusCode)
	}
}

func TestLANCallersNeedPairing(t *testing.T) {
	h := newHarness(t, true)
	handler := h.app.server.Handler()
	request := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		// An origin-form target: an absolute one would be a proxy request.
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host, req.RemoteAddr = "192.168.1.5:8080", "192.168.1.20:50000"
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	for _, path := range []string{"/app/status", "/app/config", "/_/sessid", "/hw/api/app/visitor/chat?inmate_id=1000001&page=1&limit=1"} {
		if rec := request(path, nil); rec.Code != http.StatusForbidden {
			t.Errorf("unpaired LAN caller got %d for %s", rec.Code, path)
		}
	}
	// The setup files stay public: the phone needs them before anything else.
	for _, path := range []string{"/proxy.pac", "/ca.pem"} {
		if rec := request(path, nil); rec.Code != http.StatusOK {
			t.Errorf("%s: %d, want 200 for everyone", path, rec.Code)
		}
	}
	paired := request(PairURL("", h.app.pairingSecret()), nil)
	cookies := paired.Result().Cookies()
	if paired.Code != http.StatusSeeOther || len(cookies) != 1 {
		t.Fatalf("pairing: %d, cookies %v", paired.Code, cookies)
	}
	if rec := request("/app/status", cookies[0]); rec.Code != http.StatusOK {
		t.Errorf("paired LAN caller got %d", rec.Code)
	}
}

func TestStatusAndCA(t *testing.T) {
	h := newHarness(t, true)
	status, data := h.do(t, http.MethodGet, "/app/status", "", nil)
	var reply statusReply
	if err := json.Unmarshal(data, &reply); err != nil || status != http.StatusOK {
		t.Fatalf("status %d: %s", status, data)
	}
	if !reply.Session.HaveSession || !reply.Session.CanRenew || reply.VisitorID != fakehw.VisitorID || len(reply.CAFingerprint) != 64 {
		t.Errorf("status = %+v", reply)
	}
	if len(reply.PACURLs) != len(reply.PairingURLs) {
		t.Errorf("PAC URLs %v and pairing URLs %v differ in count", reply.PACURLs, reply.PairingURLs)
	}
	if strings.Contains(string(data), h.app.server.Service().Store.Snapshot().PHPSESSID) {
		t.Error("status leaks the session id")
	}
	code, pem := h.do(t, http.MethodGet, "/ca.pem", "", nil)
	if code != http.StatusOK || !strings.Contains(string(pem), "BEGIN CERTIFICATE") || strings.Contains(string(pem), "PRIVATE") {
		t.Errorf("CA download: %d %q", code, pem)
	}
}

func TestConfigHidesAndKeepsTheBucketSecret(t *testing.T) {
	h := newHarness(t, true)
	put := func(body string) (int, configReply, string) {
		status, data := h.do(t, http.MethodPut, "/app/config", "application/json", []byte(body))
		var reply configReply
		json.Unmarshal(data, &reply)
		return status, reply, string(data)
	}
	status, reply, raw := put(`{"visitor_id":" 77 ","media":{"region":"us-east-1","bucket":"b","access_key_id":"id","secret_access_key":"hunter2"}}`)
	if status != http.StatusOK || reply.VisitorID != "77" || !reply.MediaSecretSet || strings.Contains(raw, "hunter2") {
		t.Fatalf("first update: %d %s", status, raw)
	}
	// Saving again without retyping the secret must not erase it.
	if status, reply, raw = put(`{"visitor_id":"","media":{"region":"eu-west-1","bucket":"b","access_key_id":"id","secret_access_key":""}}`); status != http.StatusOK || !reply.MediaSecretSet {
		t.Fatalf("second update: %d %s", status, raw)
	}
	if stored := h.app.config.Get(); stored.Media.SecretAccessKey != "hunter2" || stored.Media.Region != "eu-west-1" || stored.VisitorID != "" {
		t.Errorf("stored config = %+v", stored)
	}
	if status, _, raw = put(`{"visitor_id":"abc","media":{}}`); status != http.StatusBadRequest {
		t.Errorf("non-numeric visitor id: %d %s", status, raw)
	}
}

func TestUpload(t *testing.T) {
	h := newHarness(t, true)
	if status, data := h.do(t, http.MethodPost, "/app/upload?name=a.gif", "image/gif", []byte("GIF89a")); status != http.StatusConflict {
		t.Fatalf("upload without storage: %d %s", status, data)
	}

	var stored []byte
	var storedPath string
	bucket := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		stored, _ = io.ReadAll(r.Body)
		storedPath = r.URL.Path
	}))
	defer bucket.Close()
	body := `{"visitor_id":"","media":{"endpoint":"` + bucket.URL + `","path_style":true,"region":"us-east-1","bucket":"b","access_key_id":"id","secret_access_key":"key","key_prefix":"hw/"}}`
	if status, data := h.do(t, http.MethodPut, "/app/config", "application/json", []byte(body)); status != http.StatusOK {
		t.Fatalf("saving storage settings: %d %s", status, data)
	}
	status, data := h.do(t, http.MethodPost, "/app/upload?name=My+Photo.GIF", "image/gif", []byte("GIF89a"))
	var reply struct {
		URL string `json:"url"`
	}
	json.Unmarshal(data, &reply)
	if status != http.StatusOK || string(stored) != "GIF89a" || !strings.HasPrefix(storedPath, "/b/hw/") || !strings.HasSuffix(reply.URL, ".gif") || strings.Contains(reply.URL, "Photo") {
		t.Errorf("upload: %d %s; stored %q at %q", status, data, stored, storedPath)
	}
}
