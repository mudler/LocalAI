package e2e_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The Telegram connector as a transport: bindings, who is let in, what a
// message gets, how replies come back and what the stored token looks like.
// The fake Bot API is the only stub; the chat, the agent and the model are the
// real ones. These specs go through the HTTP routes and the Bot API only, so
// they hold when the connector moves into an add-on.
var _ = Describe("Chats Telegram connector", Label("Chats", "Telegram"), func() {
	type obj = map[string]any
	var seq int

	decode := func(out []byte) obj {
		var m obj
		ExpectWithOffset(1, json.Unmarshal(out, &m)).To(Succeed(), string(out))
		return m
	}
	nextID := func() int { seq += 100; return seq }

	newAgent := func() string {
		code, out := chatsDo(http.MethodPost, chatsBase()+"/agents", obj{"name": fmt.Sprintf("tg-agent-%d", time.Now().UnixNano()), "model": "mock-model"})
		ExpectWithOffset(1, code).To(Equal(http.StatusCreated), string(out))
		id := decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/agents/"+id, nil) })
		return id
	}
	bind := func(token string, agent string, extra obj) string {
		// The specs written before the reply modes count the answers as the
		// messages the bot sends; the live modes have their own specs.
		body := obj{"name": fmt.Sprintf("Bot %d", time.Now().UnixNano()), "token": token, "agent_id": agent, "enabled": true, "reply_mode": "off"}
		for k, v := range extra {
			body[k] = v
		}
		code, out := chatsDo(http.MethodPost, chatsBase()+"/connectors", body)
		ExpectWithOffset(1, code).To(Equal(http.StatusCreated), string(out))
		id := decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/connectors/"+id, nil) })
		return id
	}
	sent := func(token string) func() int { return func() int { return len(telegram.sentBy(token)) } }
	senderChats := func(bindID string) []obj {
		code, out := chatsDo(http.MethodGet, chatsBase(), nil)
		ExpectWithOffset(1, code).To(Equal(http.StatusOK), string(out))
		var mine []obj
		for _, c := range decode(out)["conversations"].([]any) {
			c := c.(obj)
			if c["origin"] == "connector" && c["origin_id"] == bindID {
				mine = append(mine, c)
			}
		}
		return mine
	}

	It("keeps the bot token sealed: no API returns it and no file holds it in clear text", func() {
		token := "7001:SEALED-token-xyz"
		id := bind(token, newAgent(), obj{"enabled": false})
		for _, p := range []string{"/connectors", "/connectors/" + id} {
			code, out := chatsDo(http.MethodGet, chatsBase()+p, nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(string(out)).NotTo(ContainSubstring("SEALED-token-xyz"))
		}
		code, out := chatsDo(http.MethodGet, chatsBase()+"/connectors/"+id, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(decode(out)).To(HaveKeyWithValue("has_token", true))

		// An update without a token keeps the stored one: the test call still finds the bot.
		code, out = chatsDo(http.MethodPut, chatsBase()+"/connectors/"+id, obj{"name": "Renamed", "agent_id": decode(out)["agent_id"], "enabled": false})
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(decode(out)).To(HaveKeyWithValue("has_token", true))
		code, out = chatsDo(http.MethodPost, chatsBase()+"/connectors/"+id+"/test", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))

		// Nothing under the data path holds the token, and neither does the key file's neighbour.
		var hits []string
		_ = filepath.WalkDir(filepath.Join(tmpDir, "data"), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if raw, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(raw), "SEALED-token-xyz") {
				hits = append(hits, p)
			}
			return nil
		})
		Expect(hits).To(BeEmpty(), "the token is in clear text on disk")
		key, err := os.Stat(filepath.Join(tmpDir, "data", "connectors.key"))
		if err == nil {
			Expect(key.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		}
	})

	It("accepts only the allowlist by default, logs the others and lets the owner allow one from the log", func() {
		token := "7002:allowlist"
		id := bind(token, newAgent(), obj{"allow": []string{"21"}})
		telegram.say(token, nextID(), 22, "stranger here")
		telegram.say(token, nextID(), 21, "friend here")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(1))

		code, out := chatsDo(http.MethodGet, chatsBase()+"/connectors/"+id+"/senders", nil)
		Expect(code).To(Equal(http.StatusOK))
		got := map[string]bool{}
		for _, s := range decode(out)["senders"].([]any) {
			got[s.(obj)["id"].(string)] = s.(obj)["allowed"].(bool)
		}
		Expect(got).To(Equal(map[string]bool{"21": true, "22": false}))
		Expect(telegram.sentBy(token)[0].ChatID).To(Equal(int64(21)), "the stranger was answered")

		code, out = chatsDo(http.MethodPost, chatsBase()+"/connectors/"+id+"/senders/22/allow", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		telegram.say(token, nextID(), 22, "now allowed")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(2))
		code, _ = chatsDo(http.MethodPost, chatsBase()+"/connectors/"+id+"/senders/999/allow", nil)
		Expect(code).To(Equal(http.StatusBadRequest), "a sender who never wrote cannot be allowed")
	})

	It("gives each sender a chat of their own and keeps one chat for one sender", func() {
		token := "7003:chats"
		id := bind(token, newAgent(), obj{"accept_any": true, "confirm_accept_any": "accept any message"})
		telegram.say(token, nextID(), 31, "first from 31")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(1))
		telegram.say(token, nextID(), 32, "first from 32")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(2))
		telegram.say(token, nextID(), 31, "second from 31")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(3))
		chats := senderChats(id)
		Expect(chats).To(HaveLen(2), "one chat per sender, not one per message")
		for _, c := range chats {
			Eventually(func() bool { return chatsIdle(c["id"].(string)) }, 90*time.Second, 200*time.Millisecond).Should(BeTrue())
		}
		Consistently(sent(token), 2*time.Second, 200*time.Millisecond).Should(Equal(3), "a turn was answered twice")
		counts := map[int]int{}
		for _, c := range chats {
			counts[len(userTexts(fullTranscript(c["id"].(string))))]++
		}
		Expect(counts).To(Equal(map[int]int{2: 1, 1: 1}))
	})

	It("keeps a second message in the transcript of a chat that has an agent", func() {
		code, out := chatsDo(http.MethodPost, chatsBase(), obj{"agent_id": newAgent()})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		id := decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/"+id, nil) })
		chatsSend(id, "alpha message")
		Eventually(func() bool { return chatsIdle(id) }, 90*time.Second, 200*time.Millisecond).Should(BeTrue())
		chatsSend(id, "beta message")
		Eventually(func() int { return len(userTexts(fullTranscript(id))) }, 30*time.Second, 200*time.Millisecond).Should(Equal(2))
		Eventually(func() bool { return chatsIdle(id) }, 90*time.Second, 200*time.Millisecond).Should(BeTrue())
	})

	It("starts a new chat for each sender when the agent of the binding changes", func() {
		token := "7004:agentchange"
		a1, a2 := newAgent(), newAgent()
		id := bind(token, a1, obj{"allow": []string{"41"}})
		telegram.say(token, nextID(), 41, "with agent one")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(1))
		code, out := chatsDo(http.MethodPut, chatsBase()+"/connectors/"+id, obj{"name": "x", "agent_id": a2, "enabled": true, "allow": []string{"41"}})
		Expect(code).To(Equal(http.StatusOK), string(out))
		telegram.say(token, nextID(), 41, "with agent two")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(2))
		Expect(senderChats(id)).To(HaveLen(2))
	})

	It("answers a message with no text, a message that is too long and nothing for a group", func() {
		token := "7005:content"
		bind(token, newAgent(), obj{"accept_any": true, "confirm_accept_any": "accept any message"})
		telegram.queue(token, obj{"update_id": nextID(), "message": obj{"chat": obj{"id": 51, "type": "private"}, "from": obj{"id": 51}, "photo": []any{}}})
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(1))
		Expect(telegram.sentBy(token)[0].Text).To(ContainSubstring("only read text"))
		telegram.say(token, nextID(), 51, strings.Repeat("x", 8001))
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(2))
		Expect(telegram.sentBy(token)[1].Text).To(ContainSubstring("too long"))
		telegram.queue(token, obj{"update_id": nextID(), "message": obj{"chat": obj{"id": -900, "type": "group"}, "from": obj{"id": 51}, "text": "hello group"}})
		telegram.say(token, nextID(), 51, "private again")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(3))
		for _, s := range telegram.sentBy(token) {
			Expect(s.ChatID).NotTo(Equal(int64(-900)), "the bot answered in a group")
		}
	})

	It("limits a sender to 20 messages a minute and says so once", func() {
		token := "7006:rate"
		id := bind(token, newAgent(), obj{"accept_any": true, "confirm_accept_any": "accept any message"})
		for i := 0; i < 23; i++ {
			telegram.say(token, nextID(), 61, fmt.Sprintf("flood %d", i))
		}
		Eventually(func() int {
			code, out := chatsDo(http.MethodGet, chatsBase()+"/connectors/"+id+"/senders", nil)
			if code != http.StatusOK {
				return 0
			}
			return len(decode(out)["senders"].([]any))
		}, 60*time.Second, 200*time.Millisecond).Should(Equal(1))
		Eventually(func() int {
			n := 0
			for _, s := range telegram.sentBy(token) {
				if strings.Contains(s.Text, "Too many messages") {
					n++
				}
			}
			return n
		}, 120*time.Second, 200*time.Millisecond).Should(Equal(1), "one notice for the flood")
		chats := senderChats(id)
		Expect(chats).To(HaveLen(1))
		Eventually(func() int { return len(userTexts(fullTranscript(chats[0]["id"].(string)))) }, 60*time.Second, 200*time.Millisecond).
			Should(BeNumerically("<=", 20))
	})

	It("shows typing while it works", func() {
		token := "7007:typing"
		bind(token, newAgent(), obj{"allow": []string{"71"}})
		telegram.say(token, nextID(), 71, "hello")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(1))
		Expect(telegram.typingCount(token)).To(BeNumerically(">=", 1), "a turn shorter than the typing period shows typing")
	})

	It("switches a binding off, with a notification, when Telegram refuses its token", func() {
		token := "7008:refused"
		id := bind(token, newAgent(), obj{"allow": []string{"81"}})
		telegram.refuseToken(token, 401)
		DeferCleanup(func() { telegram.refuseToken(token, 0) })
		Eventually(func() bool {
			code, out := chatsDo(http.MethodGet, chatsBase()+"/connectors/"+id, nil)
			return code == http.StatusOK && decode(out)["enabled"] == false
		}, 60*time.Second, 200*time.Millisecond).Should(BeTrue(), "the binding stayed on with a refused token")
		code, out := chatsDo(http.MethodGet, chatsBase()+"/notifications", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(string(out)).To(ContainSubstring("was switched off"))
	})

	It("stops reading when disabled and goes on from the stored offset when enabled again", func() {
		token := "7009:toggle"
		id := bind(token, newAgent(), obj{"allow": []string{"91"}})
		telegram.say(token, nextID(), 91, "one")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(1))
		code, out := chatsDo(http.MethodPost, chatsBase()+"/connectors/"+id+"/disable", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		telegram.say(token, nextID(), 91, "while off")
		Consistently(sent(token), 3*time.Second, 200*time.Millisecond).Should(Equal(1))
		code, out = chatsDo(http.MethodPost, chatsBase()+"/connectors/"+id+"/enable", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(2))
		// "one" is not read twice: the second answer is for the message sent while off.
		Expect(senderChats(id)).To(HaveLen(1))
	})

	It("refuses a binding for an agent without a model, and one without an agent", func() {
		code, out := chatsDo(http.MethodPost, chatsBase()+"/agents", obj{"name": "no-model-agent"})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		agent := decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/agents/"+agent, nil) })
		code, out = chatsDo(http.MethodPost, chatsBase()+"/connectors", obj{"name": "x", "token": "7010:x", "agent_id": agent})
		Expect(code).To(Equal(http.StatusBadRequest), string(out))
		code, out = chatsDo(http.MethodPost, chatsBase()+"/connectors", obj{"name": "x", "token": "7010:x"})
		Expect(code).To(Equal(http.StatusBadRequest), "a binding without an agent: "+string(out))
	})

	It("shows the answer in a draft as it is written, and sends the answer after it", func() {
		token := "7011:stream"
		bind(token, newAgent(), obj{"allow": []string{"111"}, "reply_mode": "stream"})
		telegram.say(token, nextID(), 111, "MOCK_LONG_STREAM_SHORT please")
		Eventually(func() string { return telegram.lastDraft(token) }, 60*time.Second, 100*time.Millisecond).
			Should(And(ContainSubstring("tick"), HaveSuffix("▉")), "a preview of the answer while it is written")
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(1))
		Expect(telegram.sentBy(token)[0].Text).To(And(ContainSubstring("tick"), Not(ContainSubstring("▉"))), "the answer as a message")
		Consistently(sent(token), 2*time.Second, 200*time.Millisecond).Should(Equal(1), "no working message")
		// The drafts of one answer share an ID and go at most every 400 ms.
		drafts := telegram.draftsBy(token)
		Expect(len(drafts)).To(BeNumerically(">=", 2))
		for i := 1; i < len(drafts); i++ {
			Expect(drafts[i].ID).To(Equal(drafts[0].ID))
			Expect(drafts[i].At.Sub(drafts[i-1].At)).To(BeNumerically(">=", 370*time.Millisecond), "at most one preview each 400 ms")
		}
	})

	It("follows a reply to an answer into its chat in the thread mode, and starts a chat for any other message", func() {
		token := "7012:thread"
		id := bind(token, newAgent(), obj{"allow": []string{"121"}, "conversation": "thread"})
		first := nextID()
		telegram.queue(token, obj{"update_id": first, "message": obj{"message_id": first, "chat": obj{"id": 121, "type": "private"}, "from": obj{"id": 121, "first_name": "E2E"}, "text": "question one"}})
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(1))
		answer := telegram.sentBy(token)[0].ID
		second := nextID()
		telegram.queue(token, obj{"update_id": second, "message": obj{"message_id": second, "chat": obj{"id": 121, "type": "private"}, "from": obj{"id": 121, "first_name": "E2E"}, "text": "question two"}})
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(2))
		third := nextID()
		telegram.queue(token, obj{"update_id": third, "message": obj{"message_id": third, "chat": obj{"id": 121, "type": "private"}, "from": obj{"id": 121, "first_name": "E2E"},
			"text": "more on one", "reply_to_message": obj{"message_id": answer, "from": obj{"id": 7012, "is_bot": true}}}})
		Eventually(sent(token), 90*time.Second, 200*time.Millisecond).Should(Equal(3))
		chats := senderChats(id)
		Expect(chats).To(HaveLen(2))
		texts := map[string]bool{}
		for _, c := range chats {
			Eventually(func() bool { return chatsIdle(c["id"].(string)) }, 90*time.Second, 200*time.Millisecond).Should(BeTrue())
			texts[strings.Join(userTexts(fullTranscript(c["id"].(string))), "|")] = true
		}
		Expect(texts).To(HaveKey("question one|more on one"))
	})

	It("sends the file a turn writes after the answer", func() {
		token := "7013:files"
		bind(token, newAgent(), obj{"allow": []string{"131"}})
		args := base64.URLEncoding.EncodeToString([]byte(`{"path":"/out/e2e-report.csv","content":"a,b\n1,2\n"}`))
		telegram.say(token, nextID(), 131, "ADDON_TOOL_CALL:write_file:"+args)
		Eventually(func() int { return len(telegram.filesBy(token)) }, 90*time.Second, 200*time.Millisecond).Should(Equal(1))
		f := telegram.filesBy(token)[0]
		Expect([]string{f.Method, f.Name, string(f.Data)}).To(Equal([]string{"sendDocument", "e2e-report.csv", "a,b\n1,2\n"}))
		Expect(sent(token)()).To(BeNumerically(">=", 1), "the answer came first")
	})

	It("refuses to save a token that Telegram refuses", func() {
		token := "7014:typo"
		telegram.refuseToken(token, 401)
		DeferCleanup(func() { telegram.refuseToken(token, 0) })
		code, out := chatsDo(http.MethodPost, chatsBase()+"/connectors", obj{"name": "Typo", "token": token, "agent_id": newAgent()})
		Expect(code).To(Equal(http.StatusBadRequest), string(out))
		Expect(string(out)).To(ContainSubstring("refused the bot token"))
	})
})

// fullTranscript reads every line of a chat: one read is cut at 200 lines, and an
// agent on the mock model makes many tool lines for each message.
func fullTranscript(id string) []chatMessage {
	var all []chatMessage
	var after int64
	for {
		code, out := chatsDo(http.MethodGet, fmt.Sprintf("%s/%s/messages?after=%d&limit=1000", chatsBase(), id, after), nil)
		ExpectWithOffset(1, code).To(Equal(http.StatusOK), string(out))
		var r struct {
			Messages []chatMessage `json:"messages"`
		}
		ExpectWithOffset(1, json.Unmarshal(out, &r)).To(Succeed())
		if len(r.Messages) == 0 {
			return all
		}
		all = append(all, r.Messages...)
		after = r.Messages[len(r.Messages)-1].Seq
	}
}
