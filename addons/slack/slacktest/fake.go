// SPDX-License-Identifier: MIT

// Package slacktest is a fake Slack for tests: the Web API methods that the
// Slack add-on calls, the downloads of files, and Socket Mode over a
// websocket. A test registers apps, pushes events and reads what the bot
// posted. Nothing leaves the process.
package slacktest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// App is one Slack app with its bot.
type App struct {
	Bot, AppToken string
	UserID, User  string
	BotID, Team   string
	TeamID        string
}

// Post is a message that a bot posted or edited.
type Post struct {
	Token, Channel, Thread, Text, TS string
	// Edited is set for chat.update.
	Edited bool
}

// Server is the fake. Its zero value is not usable: call New.
type Server struct {
	srv *httptest.Server
	// URL is the base of the Web API, for transport.API.BaseURL.
	URL string
	// FilesHost is the host of the downloads, for transport.API.FilesHost.
	FilesHost string

	mu       sync.Mutex
	apps     map[string]*App // by bot token and by app token
	tickets  map[string]string
	conns    map[string][]*conn // by app token
	pending  map[string][]any
	posts    []Post
	acks     []string
	users    map[string]map[string]any
	files    map[string]file
	refuse   map[string]string
	limit    map[string]int
	calls    map[string]int
	seq      int
	links    int
	upgrader websocket.Upgrader
}

type file struct {
	name, mime string
	data       []byte
}

type conn struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (c *conn) send(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.WriteJSON(v)
}

// New starts a fake Slack. Close it when the test ends.
func New() *Server {
	s := &Server{apps: map[string]*App{}, tickets: map[string]string{}, conns: map[string][]*conn{}, pending: map[string][]any{},
		users: map[string]map[string]any{}, files: map[string]file{}, refuse: map[string]string{}, limit: map[string]int{}, calls: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.api)
	mux.HandleFunc("/link", s.link)
	mux.HandleFunc("/files/", s.download)
	s.srv = httptest.NewServer(mux)
	s.URL = s.srv.URL + "/api"
	u, _ := url.Parse(s.srv.URL)
	s.FilesHost = u.Host
	return s
}

// Close stops the server and its links.
func (s *Server) Close() {
	s.mu.Lock()
	for _, cs := range s.conns {
		for _, c := range cs {
			_ = c.ws.Close()
		}
	}
	s.mu.Unlock()
	s.srv.Close()
}

// AddApp registers an app. Socket Mode is on unless the test turns it off.
func (s *Server) AddApp(a App) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.UserID == "" {
		a.UserID = "UBOT"
	}
	if a.User == "" {
		a.User = "localai"
	}
	if a.TeamID == "" {
		a.TeamID = "T1"
	}
	if a.Team == "" {
		a.Team = "Test team"
	}
	s.apps[a.Bot] = &a
	s.apps[a.AppToken] = &a
}

// AddUser registers a person of the workspace.
func (s *Server) AddUser(id, name, real string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[id] = map[string]any{"id": id, "name": name, "profile": map[string]any{"real_name": real, "display_name": ""}}
}

// AddFile registers a file that a message can carry.
func (s *Server) AddFile(id, name, mime string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[id] = file{name: name, mime: mime, data: data}
}

// Refuse makes every method answer a token with the Slack error code (such
// as invalid_auth). An empty code stops it.
func (s *Server) Refuse(token, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if code == "" {
		delete(s.refuse, token)
		return
	}
	s.refuse[token] = code
}

// RateLimitOnce makes the next call of a method answer 429 with Retry-After.
func (s *Server) RateLimitOnce(method string, seconds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limit[method] = seconds
}

// Calls is how many times a method was called.
func (s *Server) Calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

// Links is how many Socket Mode links were opened.
func (s *Server) Links() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.links
}

// Connected tells whether an app has an open link.
func (s *Server) Connected(appToken string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns[appToken]) > 0
}

// Push sends an event to the app over Socket Mode, or keeps it until a link
// opens. It returns the event ID.
func (s *Server) Push(appToken string, ev map[string]any) string {
	s.mu.Lock()
	s.seq++
	id := fmt.Sprintf("Ev%04d", s.seq)
	s.mu.Unlock()
	s.PushEnvelope(appToken, id, ev)
	return id
}

// PushEnvelope sends an event with a given event ID, as Slack does when it
// sends an event again.
func (s *Server) PushEnvelope(appToken, eventID string, ev map[string]any) {
	s.mu.Lock()
	s.seq++
	env := map[string]any{"envelope_id": fmt.Sprintf("env-%d", s.seq), "type": "events_api", "accepts_response_payload": false,
		"payload": map[string]any{"type": "event_callback", "event_id": eventID, "team_id": "T1", "event": ev}}
	cs := s.conns[appToken]
	if len(cs) == 0 {
		s.pending[appToken] = append(s.pending[appToken], env)
		s.mu.Unlock()
		return
	}
	c := cs[len(cs)-1]
	s.mu.Unlock()
	_ = c.send(env)
}

// Disconnect sends a disconnect frame with a reason (refresh_requested,
// link_disabled) on the links of an app.
func (s *Server) Disconnect(appToken, reason string) {
	s.mu.Lock()
	cs := append([]*conn(nil), s.conns[appToken]...)
	s.mu.Unlock()
	for _, c := range cs {
		_ = c.send(map[string]any{"type": "disconnect", "reason": reason})
	}
}

// Acks are the envelope IDs that the bot acknowledged.
func (s *Server) Acks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.acks...)
}

// Posts are the messages that a bot token posted or edited, in order.
func (s *Server) Posts(token string) []Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Post
	for _, p := range s.posts {
		if p.Token == token {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) reply(w http.ResponseWriter, v map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	_ = r.ParseForm()
	token := bearer(r)
	s.mu.Lock()
	s.calls[method]++
	if sec, ok := s.limit[method]; ok {
		delete(s.limit, method)
		s.mu.Unlock()
		w.Header().Set("Retry-After", strconv.Itoa(sec))
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	code := s.refuse[token]
	app := s.apps[token]
	s.mu.Unlock()
	switch {
	case code != "":
		s.reply(w, map[string]any{"ok": false, "error": code})
		return
	case token == "":
		s.reply(w, map[string]any{"ok": false, "error": "not_authed"})
		return
	case app == nil:
		s.reply(w, map[string]any{"ok": false, "error": "invalid_auth"})
		return
	}
	isApp := token == app.AppToken
	if (method == "apps.connections.open") != isApp {
		s.reply(w, map[string]any{"ok": false, "error": "not_allowed_token_type"})
		return
	}
	switch method {
	case "auth.test":
		s.reply(w, map[string]any{"ok": true, "user_id": app.UserID, "user": app.User, "bot_id": app.BotID, "team_id": app.TeamID, "team": app.Team})
	case "apps.connections.open":
		s.mu.Lock()
		s.seq++
		ticket := fmt.Sprintf("ticket-%d", s.seq)
		s.tickets[ticket] = app.AppToken
		s.mu.Unlock()
		s.reply(w, map[string]any{"ok": true, "url": "ws" + strings.TrimPrefix(s.srv.URL, "http") + "/link?ticket=" + ticket})
	case "chat.postMessage", "chat.update":
		channel, text := r.Form.Get("channel"), r.Form.Get("text")
		if channel == "" || text == "" {
			s.reply(w, map[string]any{"ok": false, "error": "no_text"})
			return
		}
		s.mu.Lock()
		ts := r.Form.Get("ts")
		if method == "chat.postMessage" {
			s.seq++
			ts = fmt.Sprintf("%d.%06d", time.Now().Unix(), s.seq)
		}
		thread := r.Form.Get("thread_ts")
		if method == "chat.update" {
			// An edit keeps the thread of the message it edits.
			for _, p := range s.posts {
				if p.TS == ts && p.Channel == channel {
					thread = p.Thread
				}
			}
		}
		s.posts = append(s.posts, Post{Token: token, Channel: channel, Thread: thread, Text: text, TS: ts, Edited: method == "chat.update"})
		s.mu.Unlock()
		s.reply(w, map[string]any{"ok": true, "channel": channel, "ts": ts})
	case "users.info":
		s.mu.Lock()
		u, ok := s.users[r.Form.Get("user")]
		s.mu.Unlock()
		if !ok {
			s.reply(w, map[string]any{"ok": false, "error": "user_not_found"})
			return
		}
		s.reply(w, map[string]any{"ok": true, "user": u})
	case "files.info":
		id := r.Form.Get("file")
		s.mu.Lock()
		f, ok := s.files[id]
		s.mu.Unlock()
		if !ok {
			s.reply(w, map[string]any{"ok": false, "error": "file_not_found"})
			return
		}
		s.reply(w, map[string]any{"ok": true, "file": map[string]any{"id": id, "name": f.name, "mimetype": f.mime, "size": len(f.data),
			"url_private_download": s.srv.URL + "/files/" + id}})
	default:
		s.reply(w, map[string]any{"ok": false, "error": "unknown_method"})
	}
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	app := s.apps[bearer(r)]
	f, ok := s.files[strings.TrimPrefix(r.URL.Path, "/files/")]
	s.mu.Unlock()
	if app == nil || bearer(r) != app.Bot {
		// Slack answers its sign-in page to a request without a token.
		_, _ = w.Write([]byte("<!DOCTYPE html><html></html>"))
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", f.mime)
	_, _ = w.Write(f.data)
}

func (s *Server) link(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	appToken, ok := s.tickets[r.URL.Query().Get("ticket")]
	delete(s.tickets, r.URL.Query().Get("ticket"))
	s.mu.Unlock()
	if !ok {
		http.Error(w, "bad ticket", http.StatusUnauthorized)
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &conn{ws: ws}
	if c.send(map[string]any{"type": "hello", "num_connections": 1}) != nil {
		_ = ws.Close()
		return
	}
	s.mu.Lock()
	s.links++
	s.conns[appToken] = append(s.conns[appToken], c)
	pending := s.pending[appToken]
	delete(s.pending, appToken)
	s.mu.Unlock()
	for _, env := range pending {
		_ = c.send(env)
	}
	defer func() {
		s.mu.Lock()
		cs := s.conns[appToken]
		for i, x := range cs {
			if x == c {
				s.conns[appToken] = append(cs[:i:i], cs[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		_ = ws.Close()
	}()
	for {
		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}
		if err := ws.ReadJSON(&ack); err != nil {
			return
		}
		s.mu.Lock()
		s.acks = append(s.acks, ack.EnvelopeID)
		s.mu.Unlock()
	}
}

// DM is the event of a direct message from a person.
func DM(channel, user, text string) map[string]any {
	return map[string]any{"type": "message", "channel_type": "im", "channel": channel, "user": user, "text": text, "ts": ts()}
}

// Mention is the event of a mention of the bot in a channel. thread is empty
// for a mention outside a thread.
func Mention(channel, user, botUser, text, thread string) map[string]any {
	ev := map[string]any{"type": "app_mention", "channel": channel, "user": user, "text": "<@" + botUser + "> " + text, "ts": ts()}
	if thread != "" {
		ev["thread_ts"] = thread
	}
	return ev
}

// Reply is the event of a message in a thread of a channel, without a mention.
func Reply(channel, user, text, thread string) map[string]any {
	return map[string]any{"type": "message", "channel_type": "channel", "channel": channel, "user": user, "text": text, "ts": ts(), "thread_ts": thread}
}

var tsSeq struct {
	sync.Mutex
	n int
}

func ts() string {
	tsSeq.Lock()
	defer tsSeq.Unlock()
	tsSeq.n++
	return fmt.Sprintf("%d.%06d", time.Now().Unix(), tsSeq.n)
}
