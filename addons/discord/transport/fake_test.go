// SPDX-License-Identifier: MIT
package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mudler/LocalAI/addons/discord/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/gomega"
)

// secretToken is a made-up token. No test may let it out in an error.
const secretToken = "MTAwMDAwMDAwMDAwMDAwMDAw.GabcDE.SECRET-token-value"

const (
	botID   = "100000000000000001"
	guildID = "200000000000000001"
	chanID  = "300000000000000001"
	threadI = "300000000000000002"
	userID  = "400000000000000001"
	dmID    = "500000000000000001"
)

// restCall is a request the fake REST server got.
type restCall struct {
	Method string
	Path   string // without /api/vN
	Query  url.Values
	Header http.Header
	Body   map[string]any
	// File is the file of a multipart upload.
	File struct {
		Name string
		Data []byte
	}
}

// reply is the answer of the fake REST server.
type reply struct {
	Status int
	Body   string
	Header map[string]string
}

// handshake is a packet a client sent on the Gateway: identify or resume.
type handshake struct {
	Op   int
	Data map[string]any
}

// fakeDiscord is Discord in one test server: the REST API, the CDN and the
// Gateway websocket. Each REST call is recorded; the answer of a route is set
// with on. The Gateway speaks hello, identify/resume, ready/resumed and
// heartbeat acknowledgements, and sends the events that a test dispatches.
type fakeDiscord struct {
	srv *httptest.Server

	mu     sync.Mutex
	calls  []restCall
	on     map[string]func(restCall) reply
	nextID int
	// history is the messages of a channel, newest first.
	history map[string][]map[string]any
	// messages are single messages by channel/id.
	messages map[string]map[string]any
	files    map[string][]byte
	// closeIdentify, when not zero, closes a link with this code after identify.
	closeIdentify int
	intentsOff    bool

	conn       *websocket.Conn
	wmu        sync.Mutex
	shakes     []handshake
	seq        int
	sessions   int
	heartbeats atomic.Int32
}

func newFakeDiscord() *fakeDiscord {
	f := &fakeDiscord{on: map[string]func(restCall) reply{}, history: map[string][]map[string]any{}, messages: map[string]map[string]any{}, files: map[string][]byte{}, nextID: 900000}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeDiscord) Close() { f.srv.Close() }

var apiPrefix = regexp.MustCompile(`^/api/v\d+`)

func (f *fakeDiscord) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/ws") {
		f.gateway(w, r)
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/cdn/"); ok {
		f.mu.Lock()
		f.calls = append(f.calls, restCall{Method: r.Method, Path: "/cdn/" + rest, Header: r.Header.Clone()})
		data, found := f.files[rest]
		f.mu.Unlock()
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
		return
	}
	c := restCall{Method: r.Method, Path: apiPrefix.ReplaceAllString(r.URL.Path, ""), Query: r.URL.Query(), Header: r.Header.Clone()}
	raw, _ := io.ReadAll(r.Body)
	if mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "multipart/form-data" {
		mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(p)
			if p.FormName() == "payload_json" {
				_ = json.Unmarshal(data, &c.Body)
			} else {
				c.File.Name, c.File.Data = p.FileName(), data
			}
		}
	} else if len(raw) > 0 {
		_ = json.Unmarshal(raw, &c.Body)
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	h := f.on[c.Method+" "+shape(c.Path)]
	f.mu.Unlock()
	rep := f.defaults(c)
	if h != nil {
		rep = h(c)
	}
	for k, v := range rep.Header {
		w.Header().Set(k, v)
	}
	if rep.Status == 0 {
		rep.Status = 200
	}
	w.WriteHeader(rep.Status)
	_, _ = io.WriteString(w, rep.Body)
}

// shape turns the IDs of a path into :id, so a route is one key.
func shape(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		if s != "" && strings.Trim(s, "0123456789") == "" {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (f *fakeDiscord) defaults(c restCall) reply {
	switch c.Method + " " + shape(c.Path) {
	case "GET /gateway":
		return reply{Body: jsonOf(map[string]any{"url": "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/ws"})}
	case "GET /users/@me":
		return reply{Body: jsonOf(map[string]any{"id": botID, "username": "localai-bot", "bot": true})}
	case "GET /applications/@me":
		flags := 1 << 19
		if f.intentsOff {
			flags = 0
		}
		return reply{Body: jsonOf(map[string]any{"id": botID, "flags": flags})}
	case "GET /channels/:id":
		id := strings.TrimPrefix(c.Path, "/channels/")
		if id == threadI {
			return reply{Body: jsonOf(map[string]any{"id": threadI, "type": 11, "parent_id": chanID, "guild_id": guildID})}
		}
		return reply{Body: jsonOf(map[string]any{"id": id, "type": 0, "guild_id": guildID})}
	case "POST /channels/:id/messages":
		f.mu.Lock()
		f.nextID++
		id := f.nextID
		f.mu.Unlock()
		return reply{Body: jsonOf(map[string]any{"id": strconv.Itoa(id)})}
	case "PATCH /channels/:id/messages/:id":
		return reply{Body: `{}`}
	case "GET /channels/:id/messages":
		ch := strings.Split(c.Path, "/")[2]
		f.mu.Lock()
		list := f.history[ch]
		f.mu.Unlock()
		if before := c.Query.Get("before"); before != "" {
			for i, m := range list {
				if m["id"] == before {
					list = list[i+1:]
					break
				}
			}
		}
		limit, _ := strconv.Atoi(c.Query.Get("limit"))
		if limit > 0 && len(list) > limit {
			list = list[:limit]
		}
		return reply{Body: jsonOf(list)}
	case "GET /channels/:id/messages/:id":
		parts := strings.Split(c.Path, "/")
		f.mu.Lock()
		m := f.messages[parts[2]+"/"+parts[4]]
		f.mu.Unlock()
		if m == nil {
			return reply{Status: 404, Body: `{"code":10008,"message":"Unknown Message"}`}
		}
		return reply{Body: jsonOf(m)}
	case "POST /users/@me/channels":
		return reply{Body: jsonOf(map[string]any{"id": dmID, "type": 1})}
	}
	return reply{Status: 404, Body: `{"code":0,"message":"404: Not Found"}`}
}

// called returns the recorded calls of a route, such as "POST /channels/:id/messages".
func (f *fakeDiscord) called(route string) []restCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []restCall
	for _, c := range f.calls {
		if c.Method+" "+shape(c.Path) == route {
			out = append(out, c)
		}
	}
	return out
}

// api is the REST client of the add-on, pointed at the fake.
func (f *fakeDiscord) api() *transport.API {
	return &transport.API{BaseURL: f.srv.URL + "/api/v10", CDNHosts: []string{strings.TrimPrefix(f.srv.URL, "http://")}}
}

// redirect sends the requests of discordgo, which go to discord.com, to the fake.
type redirect struct{ to *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.to.Scheme, r.to.Host
	return http.DefaultTransport.RoundTrip(req)
}

// newGateway is a Gateway for tests: fast backoff and heartbeat, over the fake.
func (f *fakeDiscord) newGateway(stateDir string) *transport.Gateway {
	u, _ := url.Parse(f.srv.URL)
	return &transport.Gateway{API: f.api(), Client: &http.Client{Transport: redirect{u}, Timeout: 10 * time.Second},
		Backoff: 20 * time.Millisecond, IdleAfter: time.Minute, StateDir: stateDir}
}

// runtime is a Runtime over the fake, with the gateway of newGateway().
func (f *fakeDiscord) runtime(host *memHost) *transport.Runtime {
	rt := transport.New(f.api())
	rt.Gateway = f.newGateway("")
	if host != nil {
		rt.Host = host
	}
	return rt
}

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func (f *fakeDiscord) send(c *websocket.Conn, v any) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_ = c.WriteJSON(v)
}

// gateway serves one websocket: hello, then a loop that answers the packets
// of the client.
func (f *fakeDiscord) gateway(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conn = c
	f.mu.Unlock()
	f.send(c, map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 100}})
	for {
		var p struct {
			Op int `json:"op"`
			D  any `json:"d"`
		}
		if err := c.ReadJSON(&p); err != nil {
			return
		}
		switch p.Op {
		case 1:
			f.heartbeats.Add(1)
			f.send(c, map[string]any{"op": 11})
		case 2, 6:
			f.mu.Lock()
			d, _ := p.D.(map[string]any)
			f.shakes = append(f.shakes, handshake{Op: p.Op, Data: d})
			code := f.closeIdentify
			if p.Op == 2 {
				f.sessions++
			}
			sess := f.sessions
			f.seq++
			seq := f.seq
			f.mu.Unlock()
			if code != 0 && p.Op == 2 {
				f.wmu.Lock()
				_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, "closed"))
				f.wmu.Unlock()
				return
			}
			if p.Op == 2 {
				f.send(c, map[string]any{"op": 0, "t": "READY", "s": seq, "d": map[string]any{"v": 10, "session_id": fmt.Sprintf("sess-%d", sess),
					"user": map[string]any{"id": botID, "username": "localai-bot", "bot": true}, "guilds": []any{}, "application": map[string]any{"id": botID}}})
			} else {
				f.send(c, map[string]any{"op": 0, "t": "RESUMED", "s": seq, "d": map[string]any{}})
			}
		}
	}
}

// dispatch sends an event to the connected client.
func (f *fakeDiscord) dispatch(t string, d any) {
	f.mu.Lock()
	c := f.conn
	f.seq++
	seq := f.seq
	f.mu.Unlock()
	Expect(c).NotTo(BeNil(), "no client is connected")
	f.send(c, map[string]any{"op": 0, "t": t, "s": seq, "d": d})
}

// drop closes the websocket without a close frame, as a network loss does.
func (f *fakeDiscord) drop() {
	f.mu.Lock()
	c := f.conn
	f.mu.Unlock()
	if c != nil {
		_ = c.UnderlyingConn().Close()
	}
}

func (f *fakeDiscord) handshakes() []handshake {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]handshake(nil), f.shakes...)
}

// author is the user of a message.
func author(id, name string, bot bool) map[string]any {
	return map[string]any{"id": id, "username": name, "global_name": strings.ToUpper(name[:1]) + name[1:], "bot": bot}
}

var snow atomic.Int64

func nextSnow() string { return strconv.FormatInt(700000000000000000+snow.Add(1), 10) }

// msg is a MESSAGE_CREATE payload. mentionBot adds the bot to the mentions.
type msg struct {
	channel, guild, sender, text string
	mentionBot                   bool
	bot, webhook                 bool
	reply                        string // the author of the message this one replies to
	attachments                  []map[string]any
	typ                          int
}

func (m msg) payload() map[string]any {
	p := map[string]any{"id": nextSnow(), "channel_id": m.channel, "content": m.text, "type": m.typ,
		"author": author(m.sender, "ada", m.bot), "mentions": []any{}, "attachments": []any{}}
	if m.guild != "" {
		p["guild_id"] = m.guild
	}
	if m.mentionBot {
		p["mentions"] = []any{author(botID, "localai-bot", true)}
	}
	if m.webhook {
		p["webhook_id"] = nextSnow()
	}
	if m.reply != "" {
		p["type"] = 19
		p["message_reference"] = map[string]any{"message_id": nextSnow(), "channel_id": m.channel}
		p["referenced_message"] = map[string]any{"id": nextSnow(), "channel_id": m.channel, "author": author(m.reply, "someone", m.reply == botID)}
	}
	if len(m.attachments) > 0 {
		p["attachments"] = m.attachments
	}
	return p
}

// memHost is the host side of a file handle in memory: it keeps the chunks that
// the add-on puts, and serves the bytes of src to its gets.
type memHost struct {
	got bytes.Buffer
	src []byte
}

func (h *memHost) Do(_ context.Context, _, _ string, args, out any) error {
	switch a := args.(type) {
	case contract.ConnectorFilePut:
		if a.Offset == int64(h.got.Len()) {
			h.got.Write(a.Data)
		}
		*(out.(*contract.ConnectorFilePutResult)) = contract.ConnectorFilePutResult{Received: int64(h.got.Len())}
	case contract.ConnectorFileGet:
		end := min(a.Offset+a.Length, int64(len(h.src)))
		*(out.(*contract.ConnectorFileGetResult)) = contract.ConnectorFileGetResult{Data: h.src[a.Offset:end], EOF: end == int64(len(h.src))}
	}
	return nil
}
