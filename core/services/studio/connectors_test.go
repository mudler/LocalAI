package studio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

const botToken = "777:TOKEN-must-never-leak"

// scriptedBot is a Bot API server with queued updates and recorded answers.
type scriptedBot struct {
	srv *httptest.Server

	mu      sync.Mutex
	updates []map[string]any // pending, by update_id
	sent    []string         // sendMessage texts
	sentTo  []int64
	typing  int
	fail    map[string]func() (int, string) // method -> forced answer
	offsets []int64
	polls   int
}

func newScriptedBot(t *testing.T) *scriptedBot {
	t.Helper()
	b := &scriptedBot{fail: map[string]func() (int, string){}}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
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
			b.sent = append(b.sent, body["text"].(string))
			b.sentTo = append(b.sentTo, int64(body["chat_id"].(float64)))
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		case "sendChatAction":
			b.typing++
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	t.Cleanup(b.srv.Close)
	return b
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

func (b *scriptedBot) photo(id int, from int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updates = append(b.updates, map[string]any{"update_id": id, "message": map[string]any{
		"chat": map[string]any{"id": from, "type": "private"}, "from": map[string]any{"id": from, "first_name": "P"}, "photo": []any{map[string]any{"file_id": "x"}}}})
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
	conns *studio.Connectors
	logs  *bytes.Buffer
	agent agents.Agent
}

// newConnRig builds a frontend with a connector manager on the shared stores of sh.
func newConnRig(t *testing.T, sh *shared, store connectors.Store, bot *scriptedBot, script func(workspace.Actor, string) []studiotest.ToolStep, lead func(context.Context) bool) *connRig {
	t.Helper()
	r := sh.rig(t, script, studio.TriggerOptions{})
	logs := &bytes.Buffer{}
	c, err := studio.NewConnectors(r.svc, studio.ConnectorOptions{
		Store: store, Telegram: &connectors.Telegram{BaseURL: bot.srv.URL, Client: bot.srv.Client()},
		Clock: sh.clock.Now, Tick: 20 * time.Millisecond, Poll: 0, Backoff: 10 * time.Millisecond, RatePerMinute: 5, Lead: lead,
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
	return &connRig{trigRig: r, bot: bot, conns: c, logs: logs}
}

func connStore(t *testing.T) connectors.Store {
	t.Helper()
	s, err := connectors.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (c *connRig) bind(t *testing.T, in studio.ConnectorInput) connectors.Binding {
	t.Helper()
	if c.agent.ID == "" {
		ag, err := c.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Support", Model: "m", Addons: []string{"office"}})
		if err != nil {
			t.Fatal(err)
		}
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
	b, err := c.conns.CreateConnector(context.Background(), alice, in)
	if err != nil {
		t.Fatal(err)
	}
	return b.Binding
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	until(t, what, f)
}

func connSay(text string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: text}} }
}

func TestAnAllowedSenderGetsTheAnswerOfTheBoundAgentInAChatOfTheirOwn(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("Hello from the agent."), nil)
	b := r.bind(t, studio.ConnectorInput{Allow: []string{"11"}})
	bot.say(1, 11, "Ada", "ada", "hi there")
	waitFor(t, "the answer", func() bool { return len(bot.answers()) == 1 })
	if got := bot.answers()[0]; got != "All caught up."[:0]+"Hello from the agent." {
		t.Fatalf("answer = %q", got)
	}
	chats, _ := r.svc.List(context.Background(), alice, 10)
	if len(chats) != 1 || chats[0].Origin != session.OriginConnector || chats[0].OriginID != b.ID || chats[0].AgentID != r.agent.ID || !strings.HasPrefix(chats[0].Title, "Telegram · Ada") {
		t.Fatalf("chats = %+v", chats)
	}
	if sent := r.h.Sent(chats[0].ID); len(sent) != 1 || sent[0] != "hi there" {
		t.Fatalf("the chat got %q", sent)
	}
	// Only the agent's grants: the office tool, no propose_trigger.
	names := r.h.ToolNames(chats[0].ID)
	if !hasName(names, "addon__office__read-doc") || hasName(names, "addon__sandbox__bash") || hasName(names, "propose_trigger") {
		t.Fatalf("tools = %v", names)
	}
	// A second message continues the same chat.
	bot.say(2, 11, "Ada", "ada", "and then?")
	waitFor(t, "the second answer", func() bool { return len(bot.answers()) == 2 })
	if again, _ := r.svc.List(context.Background(), alice, 10); len(again) != 1 {
		t.Fatalf("a second chat was made: %d", len(again))
	}
}

func hasName(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestAStrangerAGroupAndAnEditAreIgnoredButLogged(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	st := connStore(t)
	r := newConnRig(t, sh, st, bot, connSay("x"), nil)
	b := r.bind(t, studio.ConnectorInput{Allow: []string{"11"}})
	bot.say(1, 99, "Stranger", "eve", "let me in")
	bot.group(2, 11, "an allowed person in a group")
	bot.say(3, 11, "Ada", "ada", "real message")
	waitFor(t, "the real answer", func() bool { return len(bot.answers()) == 1 })
	if chats, _ := r.svc.List(context.Background(), alice, 10); len(chats) != 1 {
		t.Fatalf("chats = %d, want only the allowed sender's", len(chats))
	}
	senders, _ := st.Senders(context.Background(), b.ID, 0)
	byID := map[string]connectors.Sender{}
	for _, s := range senders {
		byID[s.ID] = s
	}
	if s, ok := byID["99"]; !ok || s.Allowed || s.Count != 1 || s.Name != "Stranger" {
		t.Fatalf("stranger row = %+v %v", s, ok)
	}
	if s := byID["11"]; !s.Allowed {
		t.Fatalf("allowed row = %+v", s)
	}
	for _, a := range bot.answers() {
		if strings.Contains(a, "let me in") {
			t.Fatal("the stranger was answered")
		}
	}
}

func TestAcceptAnyGivesEachSenderAChatOfTheirOwn(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	r.bind(t, studio.ConnectorInput{AcceptAny: true})
	bot.say(1, 1, "One", "", "a")
	bot.say(2, 2, "Two", "", "b")
	waitFor(t, "two answers", func() bool { return len(bot.answers()) == 2 })
	chats, _ := r.svc.List(context.Background(), alice, 10)
	if len(chats) != 2 {
		t.Fatalf("chats = %d, want one for each sender", len(chats))
	}
	seen := map[string]bool{}
	for _, c := range chats {
		seen[strings.Join(r.h.Sent(c.ID), "|")] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("the chats mixed the senders: %v", seen)
	}
}

func TestASenderWhoWritesTooFastIsDroppedWithOneNotice(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	r.bind(t, studio.ConnectorInput{AcceptAny: true}) // the rig allows 5 a minute
	for i := 1; i <= 9; i++ {
		bot.say(i, 5, "Fast", "", fmt.Sprintf("m%d", i))
	}
	waitFor(t, "the notice", func() bool {
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
	if notices != 1 {
		t.Fatalf("%d notices, want one a minute", notices)
	}
	chats, _ := r.svc.List(context.Background(), alice, 10)
	if len(chats) != 1 || len(r.h.Sent(chats[0].ID)) > 5 {
		t.Fatalf("more than the limit reached the model: %d chats", len(chats))
	}
}

func TestATooLongMessageAndAPhotoGetTheirSentencesAndNoRun(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	r.bind(t, studio.ConnectorInput{AcceptAny: true})
	bot.say(1, 5, "Big", "", strings.Repeat("x", 8001))
	bot.photo(2, 5)
	waitFor(t, "both sentences", func() bool { return len(bot.answers()) == 2 })
	a := strings.Join(bot.answers(), "\n")
	if !strings.Contains(a, "too long") || !strings.Contains(a, "only read text") {
		t.Fatalf("answers = %q", a)
	}
	if chats, _ := r.svc.List(context.Background(), alice, 10); len(chats) != 0 {
		t.Fatalf("a refused message made %d chats", len(chats))
	}
}

func TestARefusedModelRequestIsAnsweredWithTheFixedSentence(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	script := func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Err: errors.New("the model refused the request " + session.MarkQuota)}}
	}
	r := newConnRig(t, sh, connStore(t), bot, script, nil)
	r.bind(t, studio.ConnectorInput{AcceptAny: true})
	bot.say(1, 5, "Q", "", "hi")
	waitFor(t, "the answer", func() bool { return len(bot.answers()) == 1 })
	if got := bot.answers()[0]; got != session.ReasonQuota {
		t.Fatalf("answer = %q", got)
	}
}

func TestALongAnswerIsSplitAndNeverCutInTheMiddleOfAWord(t *testing.T) {
	long := strings.Repeat("word ", 1900) // 9500 characters
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay(long), nil)
	r.bind(t, studio.ConnectorInput{AcceptAny: true})
	bot.say(1, 5, "L", "", "go")
	waitFor(t, "three parts", func() bool { return len(bot.answers()) == 3 })
	total := 0
	for _, a := range bot.answers() {
		if len([]rune(a)) > 4096 || strings.HasPrefix(a, "ord") || strings.HasSuffix(a, "wor") {
			t.Fatalf("a bad part of %d characters: %q...", len(a), a[:10])
		}
		total += len(strings.Fields(a))
	}
	if total != 1900 {
		t.Fatalf("%d words came back, want 1900", total)
	}
}

func TestTheOffsetMovesPastHandledUpdatesAndARestartDoesNotReplayThem(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	st := connStore(t)
	r := newConnRig(t, sh, st, bot, connSay("ok"), nil)
	b := r.bind(t, studio.ConnectorInput{AcceptAny: true})
	bot.say(1, 5, "A", "", "one")
	bot.say(2, 6, "B", "", "two") // another sender: two quick messages of one sender may share a turn
	waitFor(t, "two answers", func() bool { return len(bot.answers()) == 2 })
	waitFor(t, "the offset", func() bool { off, ok, _ := st.Offset(context.Background(), b.ID); return ok && off == 3 })
	r.conns.Close()
	// A new process on the same store must not answer the old updates again.
	r2 := newConnRig(t, sh, st, bot, connSay("ok"), nil)
	_ = r2
	bot.say(3, 5, "A", "", "three")
	waitFor(t, "the third answer", func() bool { return len(bot.answers()) == 3 })
	time.Sleep(100 * time.Millisecond)
	if n := len(bot.answers()); n != 3 {
		t.Fatalf("%d answers, an old update was replayed", n)
	}
}

func TestADeletedAgentDisablesTheBindingAndTellsTheOwner(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	st := connStore(t)
	r := newConnRig(t, sh, st, bot, connSay("ok"), nil)
	b := r.bind(t, studio.ConnectorInput{AcceptAny: true})
	if err := r.svc.DeleteAgent(context.Background(), alice, r.agent.ID); err != nil {
		t.Fatal(err)
	}
	bot.say(1, 5, "A", "", "hi")
	waitFor(t, "the binding to be disabled", func() bool { got, _ := st.GetBinding(context.Background(), b.ID); return !got.Enabled })
	waitFor(t, "the notification", func() bool {
		for _, n := range r.notes(t) {
			if n.Kind == notify.KindNeedsAttention && strings.Contains(n.Body, "agent") {
				return true
			}
		}
		return false
	})
	for _, a := range bot.answers() {
		if strings.Contains(a, "Traceback") || strings.Contains(a, botToken) {
			t.Fatalf("answer = %q", a)
		}
	}
}

func TestTelegramRefusingTheTokenOrAnotherPollerDisablesTheBinding(t *testing.T) {
	for name, code := range map[string]int{"unauthorized": 401, "conflict": 409} {
		t.Run(name, func(t *testing.T) {
			sh, bot := newShared(t), newScriptedBot(t)
			bot.fail["getUpdates"] = func() (int, string) {
				return code, fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"x"}`, code)
			}
			st := connStore(t)
			r := newConnRig(t, sh, st, bot, connSay("ok"), nil)
			b := r.bind(t, studio.ConnectorInput{AcceptAny: true})
			waitFor(t, "the binding to be disabled", func() bool { got, _ := st.GetBinding(context.Background(), b.ID); return !got.Enabled })
			waitFor(t, "the notification", func() bool { return len(r.notes(t)) > 0 })
		})
	}
}

func TestATooManyRequestsAnswerMakesTheLoopWaitAndRecover(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
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
	st := connStore(t)
	r := newConnRig(t, sh, st, bot, connSay("ok"), nil)
	b := r.bind(t, studio.ConnectorInput{AcceptAny: true})
	waitFor(t, "three polls", func() bool { mu.Lock(); defer mu.Unlock(); return n >= 3 })
	if got, _ := st.GetBinding(context.Background(), b.ID); !got.Enabled {
		t.Fatal("a 429 disabled the binding")
	}
}

func TestDisablingOrDeletingABindingStopsItsLoopAtOnce(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	b := r.bind(t, studio.ConnectorInput{AcceptAny: true})
	waitFor(t, "polling", func() bool { return bot.pollCount() > 2 })
	if _, err := r.conns.SetConnectorEnabled(context.Background(), alice, b.ID, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	n := bot.pollCount()
	time.Sleep(200 * time.Millisecond)
	if bot.pollCount() != n {
		t.Fatal("a disabled binding is still polled")
	}
	if _, err := r.conns.SetConnectorEnabled(context.Background(), alice, b.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "polling again", func() bool { return bot.pollCount() > n })
	if err := r.conns.DeleteConnector(context.Background(), alice, b.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	m := bot.pollCount()
	time.Sleep(200 * time.Millisecond)
	if bot.pollCount() != m {
		t.Fatal("a deleted binding is still polled")
	}
}

func TestOnlyTheLeaderPollsAndANewLeaderContinuesFromTheStoredOffset(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	st := connStore(t)
	var mu sync.Mutex
	leader := "a"
	lead := func(who string) func(context.Context) bool {
		return func(context.Context) bool { mu.Lock(); defer mu.Unlock(); return leader == who }
	}
	a := newConnRig(t, sh, st, bot, connSay("ok"), lead("a"))
	b := newConnRig(t, sh, st, bot, connSay("ok"), lead("b"))
	bind := a.bind(t, studio.ConnectorInput{AcceptAny: true})
	_ = bind
	bot.say(1, 5, "A", "", "one")
	waitFor(t, "the answer", func() bool { return len(bot.answers()) == 1 })
	mu.Lock()
	leader = "b"
	mu.Unlock()
	bot.say(2, 5, "A", "", "two")
	waitFor(t, "the second answer from the new leader", func() bool { return len(bot.answers()) == 2 })
	time.Sleep(100 * time.Millisecond)
	if n := len(bot.answers()); n != 2 {
		t.Fatalf("%d answers: two frontends both answered or an update was replayed", n)
	}
	_ = b
}

func TestTheTokenIsInNoLogLineAndNeverComesBackFromTheService(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	st := connStore(t)
	r := newConnRig(t, sh, st, bot, connSay("ok"), nil)
	b := r.bind(t, studio.ConnectorInput{AcceptAny: true})
	bot.say(1, 5, "A", "", "hi")
	waitFor(t, "an answer", func() bool { return len(bot.answers()) == 1 })
	if strings.Contains(r.logs.String(), "TOKEN-must-never-leak") {
		t.Fatalf("the token is in the log:\n%s", r.logs.String())
	}
	stored, _ := st.GetBinding(context.Background(), b.ID)
	if bytes.Contains(stored.Token, []byte("TOKEN-must-never-leak")) {
		t.Fatal("the token is stored in the clear")
	}
	list, _ := r.conns.ListConnectors(context.Background(), alice)
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "TOKEN-must-never-leak") || len(list) != 1 || !list[0].HasToken {
		t.Fatalf("list = %s", raw)
	}
	// An update without a token keeps the old one.
	if _, err := r.conns.UpdateConnector(context.Background(), alice, b.ID, studio.ConnectorInput{Name: "Renamed", AgentID: r.agent.ID, AcceptAny: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	bot.say(2, 5, "A", "", "still works")
	waitFor(t, "an answer after the update", func() bool { return len(bot.answers()) == 2 })
}

func TestABindingNeedsAnAgentOfItsOwnerThatNamesAModel(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	ctx := context.Background()
	if _, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "x", Token: botToken}); !errors.Is(err, connectors.ErrInvalid) {
		t.Fatalf("no agent: %v", err)
	}
	if _, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "x", Token: botToken, AgentID: "missing"}); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
	bare, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "No model"})
	if _, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "x", Token: botToken, AgentID: bare.ID}); !errors.Is(err, connectors.ErrInvalid) {
		t.Fatalf("agent with no model: %v", err)
	}
	bobs, _ := r.svc.SaveAgent(ctx, "bob", agents.Agent{Name: "Bobs", Model: "m"})
	if _, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "x", Token: botToken, AgentID: bobs.ID}); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another user's agent: %v", err)
	}
	r.bind(t, studio.ConnectorInput{})
	if _, err := r.conns.GetConnector(ctx, "bob", "nope"); !errors.Is(err, connectors.ErrNotFound) {
		t.Fatalf("bob: %v", err)
	}
	if err := func() error {
		_, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "y", AgentID: r.agent.ID})
		return err
	}(); !errors.Is(err, connectors.ErrInvalid) {
		t.Fatalf("no token: %v", err)
	}
}

func TestTestAsksTelegramForTheBotAndAllowAddsASeenSender(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	b := r.bind(t, studio.ConnectorInput{})
	ctx := context.Background()
	me, err := r.conns.TestConnector(ctx, alice, b.ID)
	if err != nil || me.Username != "helper_bot" {
		t.Fatalf("test = %+v %v", me, err)
	}
	bot.say(1, 42, "Newcomer", "new", "let me in")
	waitFor(t, "the sender to be logged", func() bool { s, _ := r.conns.ConnectorSenders(ctx, alice, b.ID); return len(s) == 1 })
	if len(bot.answers()) != 0 {
		t.Fatal("an unknown sender was answered")
	}
	if _, err := r.conns.AllowSender(ctx, alice, b.ID, "42"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.conns.AllowSender(ctx, alice, b.ID, "999"); err == nil {
		t.Fatal("a sender that never wrote was allowed")
	}
	bot.say(2, 42, "Newcomer", "new", "now?")
	waitFor(t, "the answer", func() bool { return len(bot.answers()) == 1 })
}
