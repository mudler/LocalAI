// SPDX-License-Identifier: MIT

// Package gmailtest is a fake of the part of the Gmail v1 REST API that the
// Gmail add-on uses, like gdrivetest for Google Drive. It checks the access
// token of each call against the tokens it knows and their scopes, serves
// messages as MIME trees in the shape of the real API (base64url bodies,
// attachment IDs, nested multipart), reads the q expression of a search with
// a small parser so a test sees whether a value broke out of its quotes,
// pages its lists, keeps drafts and sent mail, and can answer with a rate
// limit, a server failure or a 401. It keeps the calls so a test can see what
// was asked and with which token.
package gmailtest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The scopes the fake checks.
const (
	ScopeReadonly = "https://www.googleapis.com/auth/gmail.readonly"
	ScopeCompose  = "https://www.googleapis.com/auth/gmail.compose"
)

// Header is one header of a message or a part.
type Header struct{ Name, Value string }

// Part is one node of the MIME tree of a message.
type Part struct {
	MimeType string
	Filename string
	Headers  []Header
	// Data is the decoded body of a leaf part.
	Data []byte
	// AttachmentID, when set, makes the API leave Data out of the message and
	// serve it from the attachment endpoint.
	AttachmentID string
	Parts        []*Part
}

// Message is a message of the fake.
type Message struct {
	ID       string
	ThreadID string
	Labels   []string
	Internal time.Time
	Snippet  string
	Payload  *Part
}

// Label is a label of the fake.
type Label struct{ ID, Name, Type string }

// Draft is a draft of the fake.
type Draft struct {
	ID      string
	Message Message
	// Raw is the RFC 822 text the add-on sent.
	Raw string
}

// Call is one request the fake served.
type Call struct {
	Method, Path, Query, Token, ContentType string
	// Body is the request body, cut at 64 KiB.
	Body string
}

type principal struct {
	email  string
	scopes map[string]bool
}

type injected struct {
	status     int
	reason     string
	retryAfter string
	times      int // 0: always
	pathPrefix string
}

// Server is a fake API.
type Server struct {
	*httptest.Server

	mu         sync.Mutex
	tokens     map[string]*principal
	auth       func(token string) (email string, scopes []string, ok bool)
	messages   map[string]*Message
	order      []string
	labels     []Label
	drafts     map[string]*Draft
	sent       []Draft
	calls      []Call
	inject     []*injected
	seq        int
	dropWrites bool
	now        func() time.Time
}

// New starts a fake API.
func New() *Server {
	s := &Server{now: time.Now}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	s.reset()
	return s
}

func (s *Server) reset() {
	s.tokens, s.messages, s.drafts = map[string]*principal{}, map[string]*Message{}, map[string]*Draft{}
	s.order, s.sent, s.calls, s.inject, s.seq = nil, nil, nil, nil, 0
	s.auth, s.dropWrites = nil, false
	s.labels = []Label{{"INBOX", "INBOX", "system"}, {"SENT", "SENT", "system"}, {"DRAFT", "DRAFT", "system"}, {"SPAM", "SPAM", "system"},
		{"TRASH", "TRASH", "system"}, {"UNREAD", "UNREAD", "system"}, {"STARRED", "STARRED", "system"}}
}

// Reset removes every message, label, draft, token, call and injected answer,
// so one server can serve many specs.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
}

// DropWrites makes the fake apply each change and then cut the connection
// without an answer.
func (s *Server) DropWrites(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropWrites = on
}

// SetAuthenticator makes the fake ask f about a token it does not know.
func (s *Server) SetAuthenticator(f func(token string) (email string, scopes []string, ok bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auth = f
}

// AddToken makes a token valid for an account with the scopes.
func (s *Server) AddToken(token, email string, scopes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &principal{email: email, scopes: map[string]bool{}}
	for _, sc := range scopes {
		p.scopes[sc] = true
	}
	s.tokens[token] = p
}

// RevokeToken makes a token unknown, as the service does once it is revoked.
func (s *Server) RevokeToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// AddLabel adds a user label.
func (s *Server) AddLabel(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labels = append(s.labels, Label{id, name, "user"})
}

// AddMessage adds m. A missing ID, thread or time is made; a message with no
// labels is in the inbox.
func (s *Server) AddMessage(m Message) *Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.add(m)
}

func (s *Server) add(m Message) *Message {
	s.seq++
	if m.ID == "" {
		m.ID = fmt.Sprintf("m%04d", s.seq)
	}
	if m.ThreadID == "" {
		m.ThreadID = "t-" + m.ID
	}
	if m.Labels == nil {
		m.Labels = []string{"INBOX"}
	}
	if m.Internal.IsZero() {
		m.Internal = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(s.seq) * time.Hour)
	}
	c := m
	s.messages[m.ID] = &c
	s.order = append(s.order, m.ID)
	return &c
}

// Calls returns the requests served so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Drafts returns the drafts that exist.
func (s *Server) Drafts() []Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Draft
	for _, d := range s.drafts {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AddDraft adds a draft that a person wrote, with the raw text of its message.
func (s *Server) AddDraft(id, raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putDraft(id, "", raw)
}

// Sent returns the messages that were sent.
func (s *Server) Sent() []Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Draft(nil), s.sent...)
}

// Inject makes the next times calls under pathPrefix ("" for any) answer with
// status and the error reason. retryAfter, when not empty, is sent in the
// Retry-After header. times 0 means every call.
func (s *Server) Inject(pathPrefix string, status int, reason, retryAfter string, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inject = append(s.inject, &injected{status: status, reason: reason, retryAfter: retryAfter, times: times, pathPrefix: pathPrefix})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, reason, message string) {
	reply(w, status, map[string]any{"error": map[string]any{
		"code": status, "message": message,
		"errors": []map[string]string{{"reason": reason, "message": message}},
	}})
}

func (s *Server) record(r *http.Request, token string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if len(body) > 64<<10 {
		body = body[:64<<10]
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.calls = append(s.calls, Call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Token: token, ContentType: r.Header.Get("Content-Type"), Body: string(body)})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.record(r, token)
	for i, in := range s.inject {
		if !strings.HasPrefix(r.URL.Path, in.pathPrefix) {
			continue
		}
		if in.times > 0 {
			in.times--
			if in.times == 0 {
				s.inject = append(s.inject[:i], s.inject[i+1:]...)
			}
		}
		if in.retryAfter != "" {
			w.Header().Set("Retry-After", in.retryAfter)
		}
		fail(w, in.status, in.reason, "injected")
		return
	}
	p, ok := s.tokens[token]
	if !ok && s.auth != nil {
		if email, scopes, valid := s.auth(token); valid {
			p, ok = &principal{email: email, scopes: map[string]bool{}}, true
			for _, sc := range scopes {
				p.scopes[sc] = true
			}
		}
	}
	if !ok {
		fail(w, http.StatusUnauthorized, "authError", "Invalid Credentials")
		return
	}
	if s.dropWrites && r.Method == http.MethodPost {
		s.route(httptest.NewRecorder(), r, p)
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return
	}
	s.route(w, r, p)
}

const base = "/gmail/v1/users/me"

func (s *Server) route(w http.ResponseWriter, r *http.Request, p *principal) {
	path := strings.TrimPrefix(r.URL.Path, base)
	if path == r.URL.Path {
		fail(w, http.StatusNotFound, "notFound", "no such path")
		return
	}
	seg := strings.Split(strings.Trim(path, "/"), "/")
	read := p.scopes[ScopeReadonly] || p.scopes[ScopeCompose]
	switch {
	case path == "/profile" && r.Method == http.MethodGet:
		if !read {
			fail(w, http.StatusForbidden, "insufficientPermissions", "Insufficient Permission")
			return
		}
		reply(w, 200, map[string]any{"emailAddress": p.email, "messagesTotal": len(s.messages), "threadsTotal": len(s.messages), "historyId": "1"})
	case path == "/messages" && r.Method == http.MethodGet:
		s.list(w, r, p)
	case len(seg) == 2 && seg[0] == "messages" && r.Method == http.MethodGet:
		s.getMessage(w, r, p, seg[1])
	case len(seg) == 4 && seg[0] == "messages" && seg[2] == "attachments" && r.Method == http.MethodGet:
		s.attachment(w, p, seg[1], seg[3])
	case len(seg) == 2 && seg[0] == "threads" && r.Method == http.MethodGet:
		s.thread(w, r, p, seg[1])
	case path == "/labels" && r.Method == http.MethodGet:
		if !p.scopes[ScopeReadonly] {
			fail(w, http.StatusForbidden, "insufficientPermissions", "Insufficient Permission")
			return
		}
		reply(w, 200, map[string]any{"labels": s.labels})
	case path == "/drafts" && r.Method == http.MethodPost:
		s.createDraft(w, r, p)
	case path == "/drafts/send" && r.Method == http.MethodPost:
		s.sendDraft(w, r, p)
	case len(seg) == 2 && seg[0] == "drafts" && r.Method == http.MethodGet:
		s.getDraft(w, r, p, seg[1])
	default:
		fail(w, http.StatusNotFound, "notFound", "no such path")
	}
}

func hasLabel(m *Message, l string) bool {
	for _, x := range m.Labels {
		if x == l {
			return true
		}
	}
	return false
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, p *principal) {
	if !p.scopes[ScopeReadonly] {
		fail(w, http.StatusForbidden, "insufficientPermissions", "Insufficient Permission")
		return
	}
	q := r.URL.Query()
	terms := ParseQuery(q.Get("q"))
	max, _ := strconv.Atoi(q.Get("maxResults"))
	if max <= 0 {
		max = 100
	}
	start := 0
	if t := q.Get("pageToken"); t != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(t, "p"))
		if err != nil || !strings.HasPrefix(t, "p") {
			fail(w, http.StatusBadRequest, "invalidArgument", "Invalid pageToken")
			return
		}
		start = n
	}
	var hits []*Message
	ids := append([]string(nil), s.order...)
	sort.SliceStable(ids, func(i, j int) bool { return s.messages[ids[i]].Internal.After(s.messages[ids[j]].Internal) })
	for _, id := range ids {
		m := s.messages[id]
		if hasLabel(m, "DRAFT") {
			continue
		}
		if q.Get("includeSpamTrash") != "true" && (hasLabel(m, "SPAM") || hasLabel(m, "TRASH")) {
			continue
		}
		ok := true
		for _, l := range q["labelIds"] {
			ok = ok && hasLabel(m, l)
		}
		if ok && matches(m, terms) {
			hits = append(hits, m)
		}
	}
	out := map[string]any{"resultSizeEstimate": len(hits)}
	var items []map[string]string
	for i := start; i < len(hits) && i < start+max; i++ {
		items = append(items, map[string]string{"id": hits[i].ID, "threadId": hits[i].ThreadID})
	}
	if items != nil {
		out["messages"] = items
	}
	if start+max < len(hits) {
		out["nextPageToken"] = "p" + strconv.Itoa(start+max)
	}
	reply(w, 200, out)
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request, p *principal, id string) {
	if !p.scopes[ScopeReadonly] {
		fail(w, http.StatusForbidden, "insufficientPermissions", "Insufficient Permission")
		return
	}
	m, ok := s.messages[id]
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "Requested entity was not found.")
		return
	}
	reply(w, 200, render(m, r.URL.Query().Get("format"), r.URL.Query()["metadataHeaders"]))
}

func (s *Server) thread(w http.ResponseWriter, r *http.Request, p *principal, id string) {
	if !p.scopes[ScopeReadonly] {
		fail(w, http.StatusForbidden, "insufficientPermissions", "Insufficient Permission")
		return
	}
	var msgs []any
	for _, mid := range s.order {
		if m := s.messages[mid]; m.ThreadID == id && !hasLabel(m, "DRAFT") {
			msgs = append(msgs, render(m, r.URL.Query().Get("format"), r.URL.Query()["metadataHeaders"]))
		}
	}
	if msgs == nil {
		fail(w, http.StatusNotFound, "notFound", "Requested entity was not found.")
		return
	}
	reply(w, 200, map[string]any{"id": id, "messages": msgs})
}

func findAttachment(pt *Part, id string) *Part {
	if pt == nil {
		return nil
	}
	if pt.AttachmentID == id {
		return pt
	}
	for _, c := range pt.Parts {
		if f := findAttachment(c, id); f != nil {
			return f
		}
	}
	return nil
}

func (s *Server) attachment(w http.ResponseWriter, p *principal, mid, aid string) {
	if !p.scopes[ScopeReadonly] {
		fail(w, http.StatusForbidden, "insufficientPermissions", "Insufficient Permission")
		return
	}
	m, ok := s.messages[mid]
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "Requested entity was not found.")
		return
	}
	pt := findAttachment(m.Payload, aid)
	if pt == nil {
		fail(w, http.StatusNotFound, "notFound", "Invalid attachment token")
		return
	}
	reply(w, 200, map[string]any{"size": len(pt.Data), "data": enc(pt.Data)})
}

func enc(b []byte) string { return base64.URLEncoding.EncodeToString(b) }

func header(m *Message, name string) string {
	if m.Payload == nil {
		return ""
	}
	for _, h := range m.Payload.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func renderPart(pt *Part, id string, withData bool) map[string]any {
	out := map[string]any{"partId": id, "mimeType": pt.MimeType, "filename": pt.Filename}
	var hs []map[string]string
	for _, h := range pt.Headers {
		hs = append(hs, map[string]string{"name": h.Name, "value": h.Value})
	}
	out["headers"] = hs
	body := map[string]any{"size": len(pt.Data)}
	if pt.AttachmentID != "" {
		body["attachmentId"] = pt.AttachmentID
	} else if withData && len(pt.Data) > 0 {
		body["data"] = enc(pt.Data)
	}
	out["body"] = body
	if len(pt.Parts) > 0 {
		var ps []any
		for i, c := range pt.Parts {
			ps = append(ps, renderPart(c, id+"."+strconv.Itoa(i), withData))
		}
		out["parts"] = ps
	}
	return out
}

// render is a message in the format the call asks for: full, metadata (the
// named headers only, no body) or minimal (no payload).
func render(m *Message, format string, only []string) map[string]any {
	out := map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": m.Labels, "snippet": m.Snippet,
		"internalDate": strconv.FormatInt(m.Internal.UnixMilli(), 10)}
	switch format {
	case "minimal":
		return out
	case "metadata":
		var hs []map[string]string
		if m.Payload != nil {
			for _, h := range m.Payload.Headers {
				for _, o := range only {
					if strings.EqualFold(h.Name, o) {
						hs = append(hs, map[string]string{"name": h.Name, "value": h.Value})
					}
				}
			}
		}
		out["payload"] = map[string]any{"mimeType": m.Payload.MimeType, "headers": hs}
	default:
		if m.Payload != nil {
			out["payload"] = renderPart(m.Payload, "0", true)
		}
	}
	return out
}

func (s *Server) putDraft(id, threadID, raw string) *Draft {
	s.seq++
	if id == "" {
		id = fmt.Sprintf("r%04d", s.seq)
	}
	msg := Message{ID: "d-" + id, ThreadID: threadID, Labels: []string{"DRAFT"}}
	parsed, err := mail.ReadMessage(strings.NewReader(raw))
	pt := &Part{MimeType: "text/plain"}
	if err == nil {
		for _, k := range []string{"To", "Cc", "Subject", "In-Reply-To", "References", "Content-Type", "Content-Transfer-Encoding"} {
			if v := parsed.Header.Get(k); v != "" {
				pt.Headers = append(pt.Headers, Header{k, v})
			}
		}
		body, _ := io.ReadAll(parsed.Body)
		if strings.EqualFold(parsed.Header.Get("Content-Transfer-Encoding"), "base64") {
			if d, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(body)), "")); err == nil {
				body = d
			}
		}
		pt.Data = body
	}
	msg.Payload = pt
	m := s.add(msg)
	d := &Draft{ID: id, Message: *m, Raw: raw}
	s.drafts[id] = d
	return d
}

func (s *Server) createDraft(w http.ResponseWriter, r *http.Request, p *principal) {
	if !p.scopes[ScopeCompose] {
		fail(w, http.StatusForbidden, "insufficientPermissions", "Insufficient Permission")
		return
	}
	var body struct {
		Message struct {
			Raw      string `json:"raw"`
			ThreadID string `json:"threadId"`
		} `json:"message"`
	}
	if json.Unmarshal(readBody(r), &body) != nil {
		fail(w, http.StatusBadRequest, "invalidArgument", "bad json")
		return
	}
	raw, err := base64.URLEncoding.DecodeString(padded(body.Message.Raw))
	if err != nil || body.Message.Raw == "" {
		fail(w, http.StatusBadRequest, "invalidArgument", "Invalid raw")
		return
	}
	d := s.putDraft("", body.Message.ThreadID, string(raw))
	reply(w, 200, map[string]any{"id": d.ID, "message": map[string]any{"id": d.Message.ID, "threadId": d.Message.ThreadID}})
}

func padded(s string) string {
	for len(s)%4 != 0 {
		s += "="
	}
	return s
}

func readBody(r *http.Request) []byte {
	b, _ := io.ReadAll(r.Body)
	return b
}

func (s *Server) getDraft(w http.ResponseWriter, r *http.Request, p *principal, id string) {
	if !p.scopes[ScopeReadonly] && !p.scopes[ScopeCompose] {
		fail(w, http.StatusForbidden, "insufficientPermissions", "Insufficient Permission")
		return
	}
	d, ok := s.drafts[id]
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "Requested entity was not found.")
		return
	}
	m := s.messages[d.Message.ID]
	reply(w, 200, map[string]any{"id": d.ID, "message": render(m, r.URL.Query().Get("format"), nil)})
}

func (s *Server) sendDraft(w http.ResponseWriter, r *http.Request, p *principal) {
	if !p.scopes[ScopeCompose] {
		fail(w, http.StatusForbidden, "insufficientPermissions", "Insufficient Permission")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(readBody(r), &body) != nil {
		fail(w, http.StatusBadRequest, "invalidArgument", "bad json")
		return
	}
	d, ok := s.drafts[body.ID]
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "Requested entity was not found.")
		return
	}
	m := s.messages[d.Message.ID]
	m.Labels = []string{"SENT"}
	delete(s.drafts, body.ID)
	d.Message = *m
	s.sent = append(s.sent, *d)
	reply(w, 200, map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": m.Labels})
}
