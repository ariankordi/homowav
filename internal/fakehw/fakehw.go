// Package fakehw is an in-memory stand-in for the HomeWAV API, for tests and
// for running the UI without touching the real service.
//
// The request and reply shapes follow captures of the iOS app 2.28.0 and the
// visitor web client. Every name, id and message in here is made up.
package fakehw

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Fixed values of the fake account.
const (
	// VisitorID is the fake account's id.
	VisitorID = "9000001"
	// RememberValue is the value of both remember cookies.
	RememberValue = "fake-remember"
	// SessionCookie authorizes calls, as on the real service.
	SessionCookie = "PHPSESSID"
	// LoggedOutCause is the real service's wording for a dead session.
	LoggedOutCause = "You must be logged in to access the requested page"
	// rememberMaxAge is the 30 days the real service sends.
	rememberMaxAge = 2592000
	// maxUpload caps a parsed multipart body.
	maxUpload = 32 << 20
	// textPrice and mediaPrice are the fake per-message prices.
	textPrice  = "0.25"
	mediaPrice = "0.50"
	// Username and Password are the fake account's login.
	Username = "sam"
	Password = "correct horse"
	// loginRefusedCause is a made-up refusal; the real wording is not known.
	loginRefusedCause = "Invalid username or password"
	// PrisonID is the fake facility both contacts are in.
	PrisonID = "8000001"
	// onlineColor and offlineColor are the heartbeat's presence values.
	onlineColor  = "green"
	offlineColor = "red"
	// readStatus marks a message the recipient has seen.
	readStatus = "read"
	// tinyGIF is a 1x1 image, so media bubbles have something to show.
	tinyGIF = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
)

// Message is one chat message in the shape /api/app/visitor/chat returns.
type Message struct {
	ID               string `json:"id"`
	PubID            string `json:"pubid"`
	InmateID         string `json:"inmate_id"`
	VisitorID        string `json:"visitor_id"`
	Sender           string `json:"sender"`
	Body             string `json:"body"`
	Type             string `json:"type"`
	Status           string `json:"status"`
	Created          string `json:"created"`
	CreatedTimestamp int64  `json:"created_timestamp"`
	SenderName       string `json:"sender_name"`
	// StreamURL is a URL string for media and false otherwise, as on the real service.
	StreamURL any    `json:"stream_url"`
	ImageURL  string `json:"image_url,omitempty"`
	// Duration is only present in the reply to a send.
	Duration string `json:"duration,omitempty"`
}

// Server is the fake. Mount it on an httptest TLS server.
type Server struct {
	mu       sync.Mutex
	sessions map[string]bool
	minted   int
	messages []Message
	pubids   map[string]bool
	nextID   int
	// rememberRevoked makes refetch-session refuse the remember cookies, as
	// the real service does when it logs the account out for good.
	rememberRevoked bool
	// logins counts login attempts, right or wrong.
	logins int

	// AutoReply, when set, makes the contact answer each text after this
	// long, so the UI's check for new messages has something to find.
	AutoReply time.Duration
	// SingleContact leaves only the first contact on the account, the
	// common case the UI opens straight into.
	SingleContact bool

	// Voice records the last uploaded voice part, for assertions.
	Voice struct {
		ContentType string
		Size        int
	}
}

// New returns a fake with two contacts and a short thread for the first.
func New() *Server {
	server := &Server{sessions: map[string]bool{}, pubids: map[string]bool{}, nextID: 1000}
	base := time.Now().Add(-48 * time.Hour)
	seed := []Message{
		{Sender: "inmate", Type: "text", Body: "Hey, did you get my letter?", Status: readStatus},
		{Sender: "visitor", Type: "text", Body: "Yes! Reading it tonight.", Status: "read"},
		{Sender: "visitor", Type: "gif", ImageURL: tinyGIF, Status: "read"},
		{Sender: "inmate", Type: "voice", StreamURL: "https://media.invalid/voice.aac", Status: "read"},
		{Sender: "visitor", Type: "image", ImageURL: tinyGIF, Status: "approved"},
		{Sender: "visitor", Type: "video", StreamURL: "https://media.invalid/clip.mp4", Status: "pending"},
	}
	for index, message := range seed {
		message.InmateID = "1000001"
		server.appendLocked(message, base.Add(time.Duration(index)*time.Hour))
	}
	return server
}

// Mint creates a valid session and returns its id.
func (s *Server) Mint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mintLocked()
}

func (s *Server) mintLocked() string {
	s.minted++
	id := fmt.Sprintf("fake-session-%d", s.minted)
	s.sessions[id] = true
	return id
}

// ExpireAll logs every session out, as the real service does after idling.
func (s *Server) ExpireAll() {
	s.mu.Lock()
	s.sessions = map[string]bool{}
	s.mu.Unlock()
}

// Messages returns a copy of everything in the fake's threads.
// RevokeRemember makes the remember cookies stop working, so only a login
// with the password gets a new session.
func (s *Server) RevokeRemember() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rememberRevoked = true
}

// Logins reports how many login attempts were made.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.messages...)
}

// appendLocked stores a message, filling in the generated fields.
func (s *Server) appendLocked(message Message, at time.Time) Message {
	s.nextID++
	message.ID = strconv.Itoa(s.nextID)
	if message.PubID == "" {
		message.PubID = fmt.Sprintf("%032x", s.nextID)
	}
	message.VisitorID = VisitorID
	message.Created = at.Format(time.DateTime)
	message.CreatedTimestamp = at.Unix()
	if message.StreamURL == nil {
		message.StreamURL = false
	}
	if message.Sender == "visitor" {
		message.SenderName = "Sam Visitor"
	} else {
		message.SenderName = "Alex Example"
	}
	s.messages = append(s.messages, message)
	return message
}

func (s *Server) authorized(r *http.Request) bool {
	cookie, err := r.Cookie(SessionCookie)
	s.mu.Lock()
	defer s.mu.Unlock()
	return err == nil && s.sessions[cookie.Value]
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	json.NewEncoder(w).Encode(value)
}

func ok(w http.ResponseWriter, body any) { writeJSON(w, map[string]any{"ok": true, "body": body}) }

func fail(w http.ResponseWriter, cause string) {
	writeJSON(w, map[string]any{"ok": false, "cause": cause})
}

// ServeHTTP routes the handful of endpoints the client uses.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/app/refetch-session/v2" {
		s.refetch(w, r)
		return
	}
	if r.URL.Path == "/api/app/login/" {
		s.login(w, r)
		return
	}
	// message/init is the one call the real service answers without a session.
	if r.URL.Path == "/api/message/init" {
		s.initMessage(w, r)
		return
	}
	if !s.authorized(r) {
		fail(w, LoggedOutCause)
		return
	}
	switch r.URL.Path {
	case "/api/app/inmates-by-visitor":
		s.inmates(w, r)
	case "/api/app/visitor/chat":
		s.chat(w, r)
	case "/api/message/send":
		s.send(w, r)
	case "/api/visitor/heartbeat":
		s.heartbeat(w)
	case "/api/app/prisons/" + PrisonID:
		s.prison(w)
	default:
		fail(w, "Invalid request")
	}
}

// login answers like the real one: the username is in the query, the
// password in the form, and the session and remember cookies are set.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.logins++
	s.mu.Unlock()
	if r.URL.Query().Get("username") != Username || r.PostFormValue("password") != Password {
		fail(w, loginRefusedCause)
		return
	}
	// A login issues new remember cookies, which work again.
	s.mu.Lock()
	s.rememberRevoked = false
	s.mu.Unlock()
	s.setSessionCookies(w)
	ok(w, map[string]any{"visitor_id": VisitorID, "username": Username, "jwt_access": "", "jwt_refresh": "fake-refresh"})
}

// setSessionCookies gives the client a new session and fresh remember cookies.
func (s *Server) setSessionCookies(w http.ResponseWriter) {
	w.Header().Add("Set-Cookie", SessionCookie+"="+s.Mint()+"; path=/")
	for _, name := range []string{"asps", "esur"} {
		w.Header().Add("Set-Cookie", fmt.Sprintf("%s=%s; Max-Age=%d; path=/", name, RememberValue, rememberMaxAge))
	}
}

func (s *Server) refetch(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	revoked := s.rememberRevoked
	s.mu.Unlock()
	if cookie, err := r.Cookie("asps"); err != nil || cookie.Value != RememberValue || revoked {
		fail(w, LoggedOutCause)
		return
	}
	s.setSessionCookies(w)
	ok(w, map[string]any{"is_logged_in": true})
}

// prison answers /api/app/prisons/<id> with the fields the UI reads, in the
// real shape: messaging_price holds numbers, the other prices strings. As at
// the real facility it was copied from, a GIF costs less than an image.
func (s *Server) prison(w http.ResponseWriter) {
	ok(w, map[string]any{
		"id": PrisonID, "name": "Example County Detention Center",
		"messaging_price":                map[string]float64{"video": 0.1, "library": 0.1, "image": 0.25, "gif": 0.1, "text": 0.03},
		"price_per_voice_message_minute": "0.02",
		"price_per_emoji_symbol":         "0.00",
		"enable_emoji_price":             "0",
		"price_per_s3_video_message":     nil,
	})
}

func (s *Server) inmates(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("id") != VisitorID {
		fail(w, "Invalid request")
		return
	}
	contact := func(id, first, last string, textLimit string, gif bool) map[string]any {
		return map[string]any{
			"id": id, "visitor_id": VisitorID, "first_name": first, "last_name": last,
			"full_name": first + " " + last, "prison_id": PrisonID, "prison_name": "Example County Detention Center",
			"relationship": "Friend", "approved": "yes", "credit_balance": "12.50", "bonus_balance": "0.00",
			"prison_price_per_text_message": textPrice, "prison_price_per_video_message": mediaPrice,
			"prison_max_text_message_length": textLimit, "prison_max_voice_message_length": "60",
			"prison_max_video_message_length": "30", "can_text_message": true, "can_video_message": true,
			"messages_disabled": nil, "chat_disabled": nil,
			"messaging_enabled": map[string]bool{"video": true, "library": true, "image": true, "gif": gif, "text": true, "voice": true},
		}
	}
	contacts := []any{
		contact("1000001", "Alex", "Example", "160", true),
		contact("1000002", "Jordan", "Sample", "300", false),
	}
	if s.SingleContact {
		contacts = contacts[:1]
	}
	ok(w, contacts)
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	limit, _ := strconv.Atoi(query.Get("limit"))
	if page < 1 || limit < 1 {
		fail(w, "Invalid request")
		return
	}
	s.mu.Lock()
	var thread []Message
	// Newest first, like the real service.
	for index := len(s.messages) - 1; index >= 0; index-- {
		if s.messages[index].InmateID == query.Get("inmate_id") {
			thread = append(thread, s.messages[index])
		}
	}
	s.mu.Unlock()
	start := min((page-1)*limit, len(thread))
	end := min(start+limit, len(thread))
	pageItems := thread[start:end]
	if pageItems == nil {
		pageItems = []Message{}
	}
	ok(w, pageItems)
}

func (s *Server) initMessage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("inmate_id") == "" || r.URL.Query().Get("visitor_id") != VisitorID {
		fail(w, "Invalid request")
		return
	}
	s.mu.Lock()
	s.nextID++
	pubid := fmt.Sprintf("%032x", s.nextID)
	s.pubids[pubid] = true
	s.mu.Unlock()
	ok(w, map[string]any{"pubid": pubid, "stream_name": pubid + "-st", "protocol": "rtmps"})
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	// ParseMultipartForm also handles urlencoded bodies.
	if err := r.ParseMultipartForm(maxUpload); err != nil && err != http.ErrNotMultipart {
		fail(w, "Invalid request")
		return
	}
	message := Message{
		PubID:    r.FormValue("pubid"),
		InmateID: r.FormValue("inmate_id"),
		Sender:   r.FormValue("sender"),
		Type:     r.FormValue("type"),
		Body:     r.FormValue("body"),
		Status:   "approved",
	}
	s.mu.Lock()
	known := s.pubids[message.PubID]
	delete(s.pubids, message.PubID)
	s.mu.Unlock()
	if !known || message.InmateID == "" || message.Sender != "visitor" {
		fail(w, "Invalid request")
		return
	}
	switch message.Type {
	case "text":
		if message.Body == "" {
			fail(w, "Message is empty")
			return
		}
	case "voice":
		file, header, err := r.FormFile("voice")
		if err != nil {
			fail(w, "No recording")
			return
		}
		size, _ := io.Copy(io.Discard, file)
		file.Close()
		s.mu.Lock()
		s.Voice.ContentType, s.Voice.Size = header.Header.Get("Content-Type"), int(size)
		s.mu.Unlock()
		message.StreamURL = "https://media.invalid/" + message.PubID + ".aac"
		message.Duration = r.FormValue("elapsed_time")
	case "gif", "image":
		if r.FormValue("gallery_url") == "" {
			fail(w, "No media")
			return
		}
		message.ImageURL = r.FormValue("gallery_url")
	case "video":
		if r.FormValue("gallery_url") == "" {
			fail(w, "No media")
			return
		}
		message.StreamURL = r.FormValue("gallery_url")
	default:
		fail(w, "Invalid request")
		return
	}
	s.mu.Lock()
	stored := s.appendLocked(message, time.Now())
	s.mu.Unlock()
	if s.AutoReply > 0 && message.Type == "text" {
		time.AfterFunc(s.AutoReply, func() { s.reply(message.InmateID, "Got it: "+message.Body) })
	}
	ok(w, sendReply(stored))
}

// sendReply is the shape the real service answers a send with: the stored
// message without created_timestamp (only "created", in the facility's local
// time), plus a summary of the chat.
func sendReply(stored Message) map[string]any {
	encoded, _ := json.Marshal(stored)
	var reply map[string]any
	json.Unmarshal(encoded, &reply)
	delete(reply, "created_timestamp")
	reply["chat"] = map[string]any{"chatId": stored.InmateID, "isOccupantOnline": isOnline(stored.InmateID), "isVisitorOnline": true}
	return reply
}

// reply stores a message from a contact, as if they had answered.
func (s *Server) reply(inmateID, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLocked(Message{InmateID: inmateID, Sender: "inmate", Type: "text", Body: body, Status: "approved"}, time.Now())
}

// isOnline makes the first contact online and everyone else offline.
func isOnline(inmateID string) bool { return inmateID == "1000001" }

// heartbeat answers the visitor app's presence poll: who is online, the
// unread count (a string, as on the real service) and balances.
func (s *Server) heartbeat(w http.ResponseWriter) {
	s.mu.Lock()
	unread := 0
	for _, message := range s.messages {
		if message.Sender == "inmate" && message.Status != readStatus {
			unread++
		}
	}
	s.mu.Unlock()
	status, suspended, balances := map[string]string{}, map[string]bool{}, map[string]string{}
	for _, id := range []string{"1000001", "1000002"} {
		status[id] = offlineColor
		if isOnline(id) {
			status[id] = onlineColor
		}
		suspended[id] = false
		balances[id] = "12.50"
	}
	ok(w, map[string]any{
		"status": status, "is_occupant_suspended": suspended, "calls": []any{},
		"forceLogout": false, "forceLogoutRemainingSeconds": -1,
		"unread_messages_count": strconv.Itoa(unread), "credit_balances": balances,
		"is_disapproved": false, "disapproval_reason": []any{}, "is_fraud": false,
	})
}
