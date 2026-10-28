package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	localaiapp "github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/tests/studiobot"
)

// fakeTelegram is a Bot API server for the connector. The application is built
// with the Telegram add-on transport on its address (see BeforeSuite), so a message queued here reaches a real
// chat on the mock model and the answer comes back here. Updates are kept per
// bot token, as Telegram does, so each binding reads only its own.
type fakeTelegram struct {
	srv     *httptest.Server
	restore func()

	mu      sync.Mutex
	pending map[string][]map[string]any
	sent    []tgSent
	typing  map[string]int
	refuse  map[string]int // token -> error code answered to getMe and getUpdates
}

// tgSent is one sendMessage the fake received.
type tgSent struct {
	Token  string
	ChatID int64
	Text   string
}

var telegram *fakeTelegram

func startFakeTelegram() {
	f := &fakeTelegram{pending: map[string][]map[string]any{}, typing: map[string]int{}, refuse: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		path := strings.TrimPrefix(r.URL.Path, "/bot")
		cut := strings.LastIndex(path, "/")
		token, method := path[:cut], path[cut+1:]
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		code := f.refuse[token]
		f.mu.Unlock()
		if code != 0 && (method == "getMe" || method == "getUpdates") {
			_, _ = w.Write([]byte(fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"refused"}`, code)))
			return
		}
		switch method {
		case "getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"username":"e2e_bot"}}`))
		case "getUpdates":
			off := int64(body["offset"].(float64))
			deadline := time.Now().Add(300 * time.Millisecond)
			for {
				f.mu.Lock()
				var out []map[string]any
				for _, u := range f.pending[token] {
					if int64(u["update_id"].(int)) >= off {
						out = append(out, u)
					}
				}
				f.mu.Unlock()
				if len(out) > 0 || time.Now().After(deadline) {
					if out == nil {
						out = []map[string]any{}
					}
					b, _ := json.Marshal(map[string]any{"ok": true, "result": out})
					_, _ = w.Write(b)
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		case "sendMessage":
			f.mu.Lock()
			chat, _ := body["chat_id"].(float64)
			f.sent = append(f.sent, tgSent{Token: token, ChatID: int64(chat), Text: body["text"].(string)})
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		case "sendChatAction":
			f.mu.Lock()
			f.typing[token]++
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	f.restore = localaiapp.UseConnectorTransportForTests(studiobot.NewBotTransport(&transport.API{BaseURL: f.srv.URL}))
	telegram = f
}

func stopFakeTelegram() {
	if telegram != nil {
		telegram.restore()
		telegram.srv.Close()
	}
}

// say queues a private text message from a person.
func (f *fakeTelegram) say(token string, id int, from int64, text string) {
	f.queue(token, map[string]any{"update_id": id, "message": map[string]any{
		"chat": map[string]any{"id": from, "type": "private"}, "from": map[string]any{"id": from, "first_name": "E2E"}, "text": text}})
}

// queue queues any update for a bot.
func (f *fakeTelegram) queue(token string, update map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[token] = append(f.pending[token], update)
}

// sentBy lists what a bot sent.
func (f *fakeTelegram) sentBy(token string) []tgSent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tgSent
	for _, s := range f.sent {
		if s.Token == token {
			out = append(out, s)
		}
	}
	return out
}

func (f *fakeTelegram) typingCount(token string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.typing[token]
}

func (f *fakeTelegram) refuseToken(token string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code == 0 {
		delete(f.refuse, token)
		return
	}
	f.refuse[token] = code
}

// A person who messages a bound bot gets an answer from the agent on the mock
// model, and a stranger gets nothing.
var _ = Describe("Chats connectors", Label("Chats"), func() {
	type obj = map[string]any
	decode := func(out []byte) obj {
		var m obj
		Expect(json.Unmarshal(out, &m)).To(Succeed(), string(out))
		return m
	}

	It("answers an allowed Telegram sender with the agent and ignores a stranger", func() {
		code, out := chatsDo(http.MethodPost, chatsBase()+"/agents", obj{"name": "Bot agent e2e", "model": "mock-model"})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		agentID := decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/agents/"+agentID, nil) })

		code, out = chatsDo(http.MethodPost, chatsBase()+"/connectors", obj{
			"name": "Bot e2e", "kind": "telegram", "token": "42:E2E-token", "agent_id": agentID, "enabled": true, "allow": []string{"11"},
		})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		Expect(string(out)).NotTo(ContainSubstring("E2E-token"))
		bindID := decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/connectors/"+bindID, nil) })

		code, out = chatsDo(http.MethodPost, chatsBase()+"/connectors/"+bindID+"/test", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(decode(out)).To(HaveKeyWithValue("bot_username", "e2e_bot"))

		telegram.say("42:E2E-token", 1, 99, "let me in")
		telegram.say("42:E2E-token", 2, 11, "hello bot")
		Eventually(func() int { return len(telegram.sentBy("42:E2E-token")) }, 90*time.Second, 200*time.Millisecond).Should(Equal(1))
		Expect(telegram.sentBy("42:E2E-token")[0].Text).NotTo(BeEmpty())

		code, out = chatsDo(http.MethodGet, chatsBase()+"/connectors/"+bindID+"/senders", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		senders := decode(out)["senders"].([]any)
		Expect(senders).To(HaveLen(2))
		allowed := map[string]bool{}
		for _, s := range senders {
			allowed[s.(obj)["id"].(string)] = s.(obj)["allowed"].(bool)
		}
		Expect(allowed).To(Equal(map[string]bool{"99": false, "11": true}))
	})
})
