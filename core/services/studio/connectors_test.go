package studio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/tests/studiobot"
)

const botToken = "777:TOKEN-must-never-leak"

// scriptedBot is a Bot API server with queued updates and recorded answers.
type scriptedBot struct {
	srv *httptest.Server

	mu       sync.Mutex
	updates  []map[string]any // pending, by update_id
	sent     []string         // sendMessage texts
	sentTo   []int64
	sentIDs  []int64    // the message ID of each sendMessage
	edits    []botEdit  // editMessageText calls
	drafts   []botDraft // sendMessageDraft calls
	modes    []string   // the parse_mode of each sendMessage
	files    []botFile  // sendDocument and sendPhoto calls
	nextID   int64
	typing   int
	typedAt  []time.Time
	fail     map[string]func() (int, string) // method -> forced answer
	offsets  []int64
	polls    int
	inflight int
	media    map[string]botMedia // by file_id: what getFile and the download answer
}

// botMedia is a file of a message that the bot can download.
type botMedia struct {
	Path string
	Size int64 // as getFile tells it; zero: the length of Data
	Data []byte
}

func newScriptedBot() *scriptedBot {
	b := &scriptedBot{fail: map[string]func() (int, string){}, media: map[string]botMedia{}}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/file/bot"+botToken+"/"); ok {
			b.mu.Lock()
			defer b.mu.Unlock()
			for _, m := range b.media {
				if m.Path == rest {
					_, _ = w.Write(m.Data)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body := map[string]any{}
		var upload *botFile
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if r.ParseMultipartForm(4<<20) == nil {
				for k, v := range r.MultipartForm.Value {
					body[k] = v[0]
				}
				for k, fhs := range r.MultipartForm.File {
					fh := fhs[0]
					f, _ := fh.Open()
					data, _ := io.ReadAll(f)
					_ = f.Close()
					upload = &botFile{Field: k, Name: fh.Filename, Data: data}
				}
			}
		} else {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
		}
		path := r.URL.EscapedPath()
		method := path[strings.LastIndex(path, "/")+1:]
		b.mu.Lock()
		defer b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if f := b.fail[method]; f != nil {
			code, out := f()
			w.WriteHeader(code)
			_, _ = w.Write([]byte(out))
			return
		}
		switch method {
		case "getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":777,"username":"helper_bot"}}`))
		case "getUpdates":
			// Telegram allows one reader: a second concurrent poll is refused.
			if b.inflight > 0 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict"}`))
				return
			}
			b.inflight++
			defer func() { b.inflight-- }()
			b.polls++
			off := int64(body["offset"].(float64))
			b.offsets = append(b.offsets, off)
			var out []map[string]any
			for _, u := range b.updates {
				if int64(u["update_id"].(int)) >= off {
					out = append(out, u)
				}
			}
			if out == nil {
				out = []map[string]any{}
				b.mu.Unlock()
				time.Sleep(15 * time.Millisecond)
				b.mu.Lock()
			}
			raw, _ := json.Marshal(map[string]any{"ok": true, "result": out})
			_, _ = w.Write(raw)
		case "sendMessage":
			b.nextID++
			b.sent = append(b.sent, shown(body))
			mode, _ := body["parse_mode"].(string)
			b.modes = append(b.modes, mode)
			b.sentTo = append(b.sentTo, int64(body["chat_id"].(float64)))
			b.sentIDs = append(b.sentIDs, b.nextID)
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d}}`, b.nextID)
		case "editMessageText":
			mode, _ := body["parse_mode"].(string)
			b.edits = append(b.edits, botEdit{ID: int64(body["message_id"].(float64)), Text: shown(body), Mode: mode, At: time.Now()})
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		case "sendMessageDraft":
			b.drafts = append(b.drafts, botDraft{Chat: int64(body["chat_id"].(float64)), ID: int64(body["draft_id"].(float64)), Text: body["text"].(string), At: time.Now()})
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		case "sendDocument", "sendPhoto":
			b.nextID++
			if upload != nil {
				upload.Method = method
				b.files = append(b.files, *upload)
			}
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d}}`, b.nextID)
		case "getFile":
			id, _ := body["file_id"].(string)
			m, ok := b.media[id]
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: invalid file_id"}`))
				return
			}
			size := m.Size
			if size == 0 {
				size = int64(len(m.Data))
			}
			raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"file_id": id, "file_unique_id": "u" + id, "file_size": size, "file_path": m.Path}})
			_, _ = w.Write(raw)
		case "sendChatAction":
			b.typing++
			b.typedAt = append(b.typedAt, time.Now())
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	DeferCleanup(b.srv.Close)
	return b
}

// botEdit is one editMessageText call.
type botEdit struct {
	ID   int64
	Text string
	Mode string
	At   time.Time
}

// botDraft is one sendMessageDraft call.
type botDraft struct {
	Chat, ID int64
	Text     string
	At       time.Time
}

// escapedV2 is a character that MarkdownV2 escapes.
var escapedV2 = regexp.MustCompile("\\\\([_*\\[\\]()~`>#+\\-=|{}.!\\\\])")

// shown is the text of a message as a person reads it: MarkdownV2 without
// its escapes, so the specs compare the words the agent wrote.
func shown(body map[string]any) string {
	t := body["text"].(string)
	if body["parse_mode"] == "MarkdownV2" {
		t = escapedV2.ReplaceAllString(t, "$1")
	}
	return t
}

// botFile is one file the bot sent.
type botFile struct {
	Method, Field, Name string
	Data                []byte
}

func (b *scriptedBot) say(id int, from int64, name, user, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updates = append(b.updates, map[string]any{"update_id": id, "message": map[string]any{
		"chat": map[string]any{"id": from, "type": "private"}, "from": map[string]any{"id": from, "first_name": name, "username": user}, "text": text}})
}

func (b *scriptedBot) group(id int, from int64, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updates = append(b.updates, map[string]any{"update_id": id, "message": map[string]any{
		"chat": map[string]any{"id": -500, "type": "group"}, "from": map[string]any{"id": from, "first_name": "G"}, "text": text}})
}

// message queues a message of a private chat with the fields of extra, such
// as a photo, a voice message or a sticker.
func (b *scriptedBot) message(id int, from int64, extra map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := map[string]any{"message_id": id, "chat": map[string]any{"id": from, "type": "private"}, "from": map[string]any{"id": from, "first_name": "P"}}
	maps.Copy(m, extra)
	b.updates = append(b.updates, map[string]any{"update_id": id, "message": m})
}

func (b *scriptedBot) answers() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.sent...)
}

func (b *scriptedBot) pollCount() int { b.mu.Lock(); defer b.mu.Unlock(); return b.polls }

type connRig struct {
	*trigRig
	bot   *scriptedBot
	tr    *studiobot.BotTransport
	conns *studio.Connectors
	logs  *bytes.Buffer
	agent agents.Agent
}

// newConnRig builds a frontend with a connector manager on the shared stores of sh.
func newConnRig(sh *shared, store connectors.Store, bot *scriptedBot, script func(workspace.Actor, string) []studiotest.ToolStep, lead func(context.Context) bool) *connRig {
	r := newConnRigWith(sh, store, studiobot.NewBotTransport(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()}), script, lead)
	r.bot = bot
	return r
}

// connEditEvery is the edit budget of a binding in the rig: short, so the
// specs of the live reply do not wait.
const connEditEvery = 20 * time.Millisecond

// newConnRigWith builds the rig over any connector transport.
func newConnRigWith(sh *shared, store connectors.Store, tr *studiobot.BotTransport, script func(workspace.Actor, string) []studiotest.ToolStep, lead func(context.Context) bool) *connRig {
	return newConnRigOpts(sh, store, tr, script, lead, nil)
}

// newConnRigOpts is newConnRigWith with options that a spec changes last.
func newConnRigOpts(sh *shared, store connectors.Store, tr *studiobot.BotTransport, script func(workspace.Actor, string) []studiotest.ToolStep, lead func(context.Context) bool, opt func(*studio.ConnectorOptions)) *connRig {
	r := sh.rig(script, studio.TriggerOptions{})
	logs := &bytes.Buffer{}
	o := studio.ConnectorOptions{
		Store: store, Transport: tr,
		Clock: sh.clock.Now, Tick: 20 * time.Millisecond, Poll: 0, Backoff: 10 * time.Millisecond, ConflictWait: 10 * time.Millisecond, RatePerMinute: 5, Lead: lead,
		EditEvery: connEditEvery,
		Logger:    slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if opt != nil {
		opt(&o)
	}
	c, err := studio.NewConnectors(r.svc, o)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(c.Close)
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	go c.Run(ctx)
	return &connRig{trigRig: r, tr: tr, conns: c, logs: logs}
}

func connStore() connectors.Store {
	s, err := connectors.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	return s
}

func (c *connRig) bind(in studio.ConnectorInput) connectors.Binding {
	if c.agent.ID == "" {
		ag, err := c.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Support", Model: "m", Addons: []string{"office"}})
		Expect(err).NotTo(HaveOccurred())
		c.agent = ag
	}
	if in.Name == "" {
		in.Name = "Support bot"
	}
	if in.Token == "" {
		in.Token = botToken
	}
	if in.AgentID == "" {
		in.AgentID = c.agent.ID
	}
	in.Enabled = true
	if in.Reply == "" {
		// The specs written before the reply modes read the answer as the
		// one message the bot sends; the live modes have their own specs.
		in.Reply = connectors.ReplyOff
	}
	b, err := c.conns.CreateConnector(context.Background(), alice, in)
	Expect(err).NotTo(HaveOccurred())
	return b.Binding
}

func waitFor(what string, f func() bool) {
	until(what, f)
}

func connSay(text string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: text}} }
}

var _ = Describe("connectors", func() {
	It("an allowed sender gets the answer of the bound agent in a chat of their own", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("Hello from the agent."), nil)
		b := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		bot.say(1, 11, "Ada", "ada", "hi there")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		got := bot.answers()[0]
		Expect(got).To(Equal("All caught up."[:0]+"Hello from the agent."), "answer = %q", got)
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(1), "chats = %+v", chats)
		Expect(chats[0].Origin).To(Equal(session.OriginConnector), "chats = %+v", chats)
		Expect(chats[0].OriginID).To(Equal(b.ID), "chats = %+v", chats)
		Expect(chats[0].AgentID).To(Equal(r.agent.ID), "chats = %+v", chats)
		Expect(chats[0].Title).To(HavePrefix("Telegram · Ada"), "chats = %+v", chats)
		sent := r.h.Sent(chats[0].ID)
		Expect(sent).To(HaveLen(1), "the chat got %q", sent)
		Expect(sent[0]).To(Equal("hi there"), "the chat got %q", sent)
		// Only the agent's grants: the office tool, no propose_trigger.
		names := r.h.ToolNames(chats[0].ID)
		Expect(hasName(names, "addon__office__read-doc")).To(BeTrue(), "tools = %v", names)
		Expect(hasName(names, "addon__sandbox__bash")).To(BeFalse(), "tools = %v", names)
		Expect(hasName(names, "propose_trigger")).To(BeFalse(), "tools = %v", names)
		// A second message continues the same chat.
		bot.say(2, 11, "Ada", "ada", "and then?")
		waitFor("the second answer", func() bool { return len(bot.answers()) == 2 })
		again, _ := r.svc.List(context.Background(), alice, 10)
		Expect(again).To(HaveLen(1), "a second chat was made: %d", len(again))
	})
})

func hasName(l []string, s string) bool {
	return slices.Contains(l, s)
}

var _ = Describe("connectors", func() {
	It("a stranger a group and an edit are ignored but logged", func() {
		sh, bot := newShared(), newScriptedBot()
		st := connStore()
		r := newConnRig(sh, st, bot, connSay("x"), nil)
		b := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		bot.say(1, 99, "Stranger", "eve", "let me in")
		bot.group(2, 11, "an allowed person in a group")
		bot.say(3, 11, "Ada", "ada", "real message")
		waitFor("the real answer", func() bool { return len(bot.answers()) == 1 })
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(1), "chats = %d, want only the allowed sender's", len(chats))
		senders, _ := st.Senders(context.Background(), b.ID, 0)
		byID := map[string]connectors.Sender{}
		for _, s := range senders {
			byID[s.ID] = s
		}
		s, ok := byID["99"]
		Expect(ok).To(BeTrue(), "stranger row = %+v %v", s, ok)
		Expect(s.Allowed).To(BeFalse(), "stranger row = %+v %v", s, ok)
		Expect(s.Count).To(BeNumerically("==", 1), "stranger row = %+v %v", s, ok)
		Expect(s.Name).To(Equal("Stranger"), "stranger row = %+v %v", s, ok)
		s = byID["11"]
		Expect(s.Allowed).To(BeTrue(), "allowed row = %+v", s)
		for _, a := range bot.answers() {
			Expect(a).NotTo(ContainSubstring("let me in"), "the stranger was answered")
		}
	})

	It("accept any gives each sender a chat of their own", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{AcceptAny: true})
		bot.say(1, 1, "One", "", "a")
		bot.say(2, 2, "Two", "", "b")
		waitFor("two answers", func() bool { return len(bot.answers()) == 2 })
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(2), "chats = %d, want one for each sender", len(chats))
		seen := map[string]bool{}
		for _, c := range chats {
			seen[strings.Join(r.h.Sent(c.ID), "|")] = true
		}
		Expect(seen["a"]).To(BeTrue(), "the chats mixed the senders: %v", seen)
		Expect(seen["b"]).To(BeTrue(), "the chats mixed the senders: %v", seen)
	})

	It("a sender who writes too fast is dropped with one notice", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{AcceptAny: true}) // the rig allows 5 a minute
		for i := 1; i <= 9; i++ {
			bot.say(i, 5, "Fast", "", fmt.Sprintf("m%d", i))
		}
		waitFor("the notice", func() bool {
			for _, a := range bot.answers() {
				if strings.Contains(a, "Too many messages") {
					return true
				}
			}
			return false
		})
		time.Sleep(150 * time.Millisecond)
		notices := 0
		for _, a := range bot.answers() {
			if strings.Contains(a, "Too many messages") {
				notices++
			}
		}
		Expect(notices).To(BeNumerically("==", 1), "%d notices, want one a minute", notices)
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(1), "more than the limit reached the model: %d chats", len(chats))
		Expect(len(r.h.Sent(chats[0].ID))).To(BeNumerically("<=", 5), "more than the limit reached the model: %d chats", len(chats))
	})

	It("a too long message and a sticker get their sentences and no run", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{AcceptAny: true})
		bot.say(1, 5, "Big", "", strings.Repeat("x", 8001))
		bot.message(2, 5, map[string]any{"sticker": map[string]any{"file_id": "s1"}})
		waitFor("both sentences", func() bool { return len(bot.answers()) == 2 })
		a := strings.Join(bot.answers(), "\n")
		Expect(a).To(ContainSubstring("too long"), "answers = %q", a)
		Expect(a).To(ContainSubstring("cannot read this kind of message"), "answers = %q", a)
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(BeEmpty(), "a refused message made %d chats", len(chats))
	})

	It("a refused model request is answered with the fixed sentence", func() {
		sh, bot := newShared(), newScriptedBot()
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Err: errors.New("the model refused the request " + session.MarkQuota)}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{AcceptAny: true})
		bot.say(1, 5, "Q", "", "hi")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal(session.ReasonQuota))
	})

	It("a long answer is split and never cut in the middle of a word", func() {
		long := strings.Repeat("word ", 1900) // 9500 characters
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay(long), nil)
		r.bind(studio.ConnectorInput{AcceptAny: true})
		bot.say(1, 5, "L", "", "go")
		waitFor("three parts", func() bool { return len(bot.answers()) == 3 })
		total := 0
		for _, a := range bot.answers() {
			Expect(len([]rune(a))).To(BeNumerically("<=", 4096), "a bad part of %d characters: %q...", len(a), a[:10])
			Expect(a).NotTo(HavePrefix("ord"), "a bad part of %d characters: %q...", len(a), a[:10])
			Expect(a).NotTo(HaveSuffix("wor"), "a bad part of %d characters: %q...", len(a), a[:10])
			total += len(strings.Fields(a))
		}
		Expect(total).To(BeNumerically("==", 1900), "%d words came back, want 1900", total)
	})

	It("the offset moves past handled updates and a restart does not replay them", func() {
		sh, bot := newShared(), newScriptedBot()
		st := connStore()
		r := newConnRig(sh, st, bot, connSay("ok"), nil)
		b := r.bind(studio.ConnectorInput{AcceptAny: true})
		bot.say(1, 5, "A", "", "one")
		bot.say(2, 6, "B", "", "two") // another sender: two quick messages of one sender may share a turn
		waitFor("two answers", func() bool { return len(bot.answers()) == 2 })
		waitFor("the offset", func() bool { off, ok, _ := st.Offset(context.Background(), b.ID); return ok && off == 3 })
		r.conns.Close()
		// A new process on the same store must not answer the old updates again.
		r2 := newConnRig(sh, st, bot, connSay("ok"), nil)
		_ = r2
		bot.say(3, 5, "A", "", "three")
		waitFor("the third answer", func() bool { return len(bot.answers()) == 3 })
		time.Sleep(100 * time.Millisecond)
		Expect(bot.answers()).To(HaveLen(3))
	})

	It("a deleted agent disables the binding and tells the owner", func() {
		sh, bot := newShared(), newScriptedBot()
		st := connStore()
		r := newConnRig(sh, st, bot, connSay("ok"), nil)
		b := r.bind(studio.ConnectorInput{AcceptAny: true})
		Expect(r.svc.DeleteAgent(context.Background(), alice, r.agent.ID)).To(Succeed())
		bot.say(1, 5, "A", "", "hi")
		waitFor("the binding to be disabled", func() bool { got, _ := st.GetBinding(context.Background(), b.ID); return !got.Enabled })
		waitFor("the notification", func() bool {
			for _, n := range r.notes() {
				if n.Kind == notify.KindNeedsAttention && strings.Contains(n.Body, "agent") {
					return true
				}
			}
			return false
		})
		for _, a := range bot.answers() {
			Expect(a).NotTo(ContainSubstring("Traceback"), "answer = %q", a)
			Expect(a).NotTo(ContainSubstring(botToken), "answer = %q", a)
		}
	})

	DescribeTable("telegram refusing the token or another poller disables the binding",
		func(code int) {
			sh, bot := newShared(), newScriptedBot()
			bot.fail["getUpdates"] = func() (int, string) {
				return code, fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"x"}`, code)
			}
			st := connStore()
			r := newConnRig(sh, st, bot, connSay("ok"), nil)
			b := r.bind(studio.ConnectorInput{AcceptAny: true})
			waitFor("the binding to be disabled", func() bool { got, _ := st.GetBinding(context.Background(), b.ID); return !got.Enabled })
			waitFor("the notification", func() bool { return len(r.notes()) > 0 })
		},
		Entry("unauthorized", 401),
		Entry("conflict", 409),
	)

	It("a too many requests answer makes the loop wait and recover", func() {
		sh, bot := newShared(), newScriptedBot()
		var mu sync.Mutex
		n := 0
		bot.fail["getUpdates"] = func() (int, string) {
			mu.Lock()
			defer mu.Unlock()
			n++
			if n <= 2 {
				return 429, `{"ok":false,"error_code":429,"description":"slow down","parameters":{"retry_after":0}}`
			}
			return 200, `{"ok":true,"result":[]}`
		}
		st := connStore()
		r := newConnRig(sh, st, bot, connSay("ok"), nil)
		b := r.bind(studio.ConnectorInput{AcceptAny: true})
		waitFor("three polls", func() bool { mu.Lock(); defer mu.Unlock(); return n >= 3 })
		got, _ := st.GetBinding(context.Background(), b.ID)
		Expect(got.Enabled).To(BeTrue(), "a 429 disabled the binding")
	})

	It("disabling or deleting a binding stops its loop at once", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		b := r.bind(studio.ConnectorInput{AcceptAny: true})
		waitFor("polling", func() bool { return bot.pollCount() > 2 })
		_, err := r.conns.SetConnectorEnabled(context.Background(), alice, b.ID, false)
		Expect(err).NotTo(HaveOccurred())
		time.Sleep(200 * time.Millisecond)
		n := bot.pollCount()
		time.Sleep(200 * time.Millisecond)
		Expect(bot.pollCount()).To(Equal(n), "a disabled binding is still polled")
		_, err = r.conns.SetConnectorEnabled(context.Background(), alice, b.ID, true)
		Expect(err).NotTo(HaveOccurred())
		waitFor("polling again", func() bool { return bot.pollCount() > n })
		Expect(r.conns.DeleteConnector(context.Background(), alice, b.ID)).To(Succeed())
		time.Sleep(200 * time.Millisecond)
		m := bot.pollCount()
		time.Sleep(200 * time.Millisecond)
		Expect(bot.pollCount()).To(Equal(m), "a deleted binding is still polled")
	})

	It("only the leader polls and a new leader continues from the stored offset", func() {
		sh, bot := newShared(), newScriptedBot()
		st := connStore()
		var mu sync.Mutex
		leader := "a"
		lead := func(who string) func(context.Context) bool {
			return func(context.Context) bool { mu.Lock(); defer mu.Unlock(); return leader == who }
		}
		a := newConnRig(sh, st, bot, connSay("ok"), lead("a"))
		b := newConnRig(sh, st, bot, connSay("ok"), lead("b"))
		bind := a.bind(studio.ConnectorInput{AcceptAny: true})
		_ = bind
		bot.say(1, 5, "A", "", "one")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		mu.Lock()
		leader = "b"
		mu.Unlock()
		bot.say(2, 5, "A", "", "two")
		waitFor("the second answer from the new leader", func() bool { return len(bot.answers()) == 2 })
		time.Sleep(100 * time.Millisecond)
		Expect(bot.answers()).To(HaveLen(2))
		_ = b
	})

	It("the token is in no log line and never comes back from the service", func() {
		sh, bot := newShared(), newScriptedBot()
		st := connStore()
		r := newConnRig(sh, st, bot, connSay("ok"), nil)
		b := r.bind(studio.ConnectorInput{AcceptAny: true})
		bot.say(1, 5, "A", "", "hi")
		waitFor("an answer", func() bool { return len(bot.answers()) == 1 })
		Expect(r.logs.String()).NotTo(ContainSubstring("TOKEN-must-never-leak"), "the token is in the log:\n%s", r.logs.String())
		stored, _ := st.GetBinding(context.Background(), b.ID)
		Expect(bytes.Contains(stored.Token, []byte("TOKEN-must-never-leak"))).To(BeFalse(), "the token is stored in the clear")
		list, _ := r.conns.ListConnectors(context.Background(), alice)
		raw, _ := json.Marshal(list)
		Expect(string(raw)).NotTo(ContainSubstring("TOKEN-must-never-leak"), "list = %s", raw)
		Expect(list).To(HaveLen(1), "list = %s", raw)
		Expect(list[0].HasToken).To(BeTrue(), "list = %s", raw)
		// An update without a token keeps the old one.
		_, err := r.conns.UpdateConnector(context.Background(), alice, b.ID, studio.ConnectorInput{Name: "Renamed", AgentID: r.agent.ID, AcceptAny: true, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		bot.say(2, 5, "A", "", "still works")
		waitFor("an answer after the update", func() bool { return len(bot.answers()) == 2 })
	})

	It("a binding needs an agent of its owner that names a model", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		ctx := context.Background()
		_, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "x", Token: botToken})
		Expect(err).To(MatchError(connectors.ErrInvalid), "no agent: %v", err)
		_, err = r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "x", Token: botToken, AgentID: "missing"})
		Expect(err).To(MatchError(agents.ErrNotFound), "unknown agent: %v", err)
		bare, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "No model"})
		_, err = r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "x", Token: botToken, AgentID: bare.ID})
		Expect(err).To(MatchError(connectors.ErrInvalid), "agent with no model: %v", err)
		bobs, _ := r.svc.SaveAgent(ctx, "bob", agents.Agent{Name: "Bobs", Model: "m"})
		_, err = r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "x", Token: botToken, AgentID: bobs.ID})
		Expect(err).To(MatchError(agents.ErrNotFound), "another user's agent: %v", err)
		r.bind(studio.ConnectorInput{})
		_, err = r.conns.GetConnector(ctx, "bob", "nope")
		Expect(err).To(MatchError(connectors.ErrNotFound), "bob: %v", err)
		_, err = r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "y", AgentID: r.agent.ID})
		Expect(err).To(MatchError(connectors.ErrInvalid), "a connector with no token")
	})

	It("test asks telegram for the bot and allow adds a seen sender", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		b := r.bind(studio.ConnectorInput{})
		ctx := context.Background()
		me, err := r.conns.TestConnector(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred(), "test = %+v %v", me, err)
		Expect(me.Username).To(Equal("helper_bot"), "test = %+v %v", me, err)
		bot.say(1, 42, "Newcomer", "new", "let me in")
		waitFor("the sender to be logged", func() bool { s, _ := r.conns.ConnectorSenders(ctx, alice, b.ID); return len(s) == 1 })
		Expect(bot.answers()).To(BeEmpty(), "an unknown sender was answered")
		_, err = r.conns.AllowSender(ctx, alice, b.ID, "42")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.conns.AllowSender(ctx, alice, b.ID, "999")
		Expect(err).To(HaveOccurred(), "a sender that never wrote was allowed")
		bot.say(2, 42, "Newcomer", "new", "now?")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
	})

	It("forward sends a run notification to the bound chat once", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{NotifyChat: "314"})
		r.bind(studio.ConnectorInput{Name: "No notify"})
		off := r.bind(studio.ConnectorInput{Name: "Off", NotifyChat: "999"})
		_, err := r.conns.SetConnectorEnabled(context.Background(), alice, off.ID, false)
		Expect(err).NotTo(HaveOccurred())
		r.conns.Forward(context.Background(), notify.Notification{Principal: alice, Title: "Nightly failed", Body: "took longer than 15m", Link: "/app/chats/c1"})
		r.conns.Forward(context.Background(), notify.Notification{Principal: "bob", Title: "Bob's", Body: "x"})
		waitFor("the message", func() bool { return len(bot.answers()) == 1 })
		time.Sleep(60 * time.Millisecond)
		bot.mu.Lock()
		defer bot.mu.Unlock()
		Expect(bot.sent).To(HaveLen(1), "sent %q to %v", bot.sent, bot.sentTo)
		Expect(bot.sent[0]).To(ContainSubstring("Nightly failed"), "sent %q to %v", bot.sent, bot.sentTo)
		Expect(bot.sent[0]).To(ContainSubstring("took longer"), "sent %q to %v", bot.sent, bot.sentTo)
		Expect(bot.sent[0]).NotTo(ContainSubstring("/app/chats"), "sent %q to %v", bot.sent, bot.sentTo)
		Expect(bot.sentTo[0]).To(BeNumerically("==", 314), "sent %q to %v", bot.sent, bot.sentTo)
	})

	It("a failed send does not fail the notification", func() {
		sh, bot := newShared(), newScriptedBot()
		bot.fail["sendMessage"] = func() (int, string) { return 400, `{"ok":false,"error_code":400,"description":"chat not found"}` }
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{NotifyChat: "314"})
		// The inbox hook runs after the notification is stored.
		r.inbox.OnAdd(func(n notify.Notification) { r.conns.Forward(context.Background(), n) })
		n, err := r.inbox.Add(context.Background(), notify.Notification{Principal: alice, Title: "T", Body: "B"})
		Expect(err).NotTo(HaveOccurred(), "the notification was not stored")
		Expect(n.ID).NotTo(BeNumerically("==", 0), "the notification was not stored: %v", err)
		got, _ := r.inbox.Count(context.Background(), alice)
		Expect(got).To(BeNumerically("==", 1), "unread = %d", got)
	})

	// Forward adds to the wait group of Close. An Add that runs while Close waits
	// is a misuse of the group (a panic or a goroutine that outlives Close), and
	// nothing may be sent once Close has returned.
	It("forward racing close neither panics nor sends after close", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{NotifyChat: "314"})
		ctx := context.Background()
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						r.conns.Forward(ctx, notify.Notification{Principal: alice, Title: "T", Body: "B"})
					}
				}
			}()
		}
		time.Sleep(20 * time.Millisecond)
		r.conns.Close()
		bot.mu.Lock()
		sentAtClose := len(bot.sent)
		bot.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		close(stop)
		wg.Wait()
		bot.mu.Lock()
		defer bot.mu.Unlock()
		Expect(bot.sent).To(HaveLen(sentAtClose), "%d messages went out after Close returned", len(bot.sent)-sentAtClose)
	})
})
