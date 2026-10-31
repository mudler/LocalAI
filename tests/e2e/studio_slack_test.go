package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	slack "github.com/mudler/LocalAI/addons/slack/transport"
)

// slackFake is a fake Slack (Web API and Socket Mode) with the real runtime
// of the Slack add-on over it. The application serves the add-on through the
// connector transport of the suite (see startFakeTelegram).
var slackFake struct {
	srv *slacktest.Server
	rt  *slack.Runtime
}

func startFakeSlack() {
	slackFake.srv = slacktest.New()
	slackFake.rt = slack.New(&slack.API{BaseURL: slackFake.srv.URL, FilesHost: slackFake.srv.FilesHost})
}

func stopFakeSlack() {
	if slackFake.rt != nil {
		slackFake.rt.Close()
		slackFake.srv.Close()
	}
}

// The Slack connector end to end: a binding made over the HTTP routes, events
// pushed over Socket Mode, the chat on the mock model, and the answers posted
// back through the Web API. The fake Slack is the only stub.
var _ = Describe("Chats Slack connector", Label("Chats", "Slack"), func() {
	type obj = map[string]any
	f := func() *slacktest.Server { return slackFake.srv }

	decode := func(out []byte) obj {
		var m obj
		ExpectWithOffset(1, json.Unmarshal(out, &m)).To(Succeed(), string(out))
		return m
	}
	newAgent := func() string {
		code, out := chatsDo(http.MethodPost, chatsBase()+"/agents", obj{"name": fmt.Sprintf("slack-agent-%d", time.Now().UnixNano()), "model": "mock-model"})
		ExpectWithOffset(1, code).To(Equal(http.StatusCreated), string(out))
		id := decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/agents/"+id, nil) })
		return id
	}
	// app registers a Slack app with fresh tokens and binds it.
	app := func(extra obj) (bot, appToken, id string) {
		n := time.Now().UnixNano()
		bot, appToken = fmt.Sprintf("xoxb-e2e-%d", n), fmt.Sprintf("xapp-e2e-%d", n)
		f().AddApp(slacktest.App{Bot: bot, AppToken: appToken, UserID: "UBOT", User: "localai"})
		// The specs written before the reply modes read each answer as one
		// post; the live modes have their own specs.
		body := obj{"kind": "slack", "name": fmt.Sprintf("App %d", n), "token": bot + " " + appToken, "agent_id": newAgent(), "enabled": true, "reply_mode": "off"}
		for k, v := range extra {
			body[k] = v
		}
		code, out := chatsDo(http.MethodPost, chatsBase()+"/connectors", body)
		ExpectWithOffset(1, code).To(Equal(http.StatusCreated), string(out))
		id = decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/connectors/"+id, nil) })
		EventuallyWithOffset(1, func() bool { return f().Connected(appToken) }, 30*time.Second, 100*time.Millisecond).Should(BeTrue(), "no Socket Mode link")
		return bot, appToken, id
	}
	// answers are the posts of a bot that are not the "working" message.
	answers := func(bot string) func() []slacktest.Post {
		return func() []slacktest.Post {
			var out []slacktest.Post
			for _, p := range f().Posts(bot) {
				if p.Text != slack.Working {
					out = append(out, p)
				}
			}
			return out
		}
	}
	chatsOf := func(id string) []obj {
		code, out := chatsDo(http.MethodGet, chatsBase(), nil)
		ExpectWithOffset(1, code).To(Equal(http.StatusOK), string(out))
		var mine []obj
		for _, c := range decode(out)["conversations"].([]any) {
			if c := c.(obj); c["origin"] == "connector" && c["origin_id"] == id {
				mine = append(mine, c)
			}
		}
		return mine
	}

	BeforeEach(func() {
		f().AddUser("U100", "ada", "Ada")
		f().AddUser("U200", "bo", "Bo")
	})

	It("checks both tokens, and keeps them sealed: no API returns them and no file holds them", func() {
		bot, appToken, id := app(obj{"allow": []string{"U100"}})
		code, out := chatsDo(http.MethodPost, chatsBase()+"/connectors/"+id+"/test", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(decode(out)["bot_username"]).To(Equal("localai (Test team)"))
		for _, p := range []string{"/connectors", "/connectors/" + id} {
			_, out := chatsDo(http.MethodGet, chatsBase()+p, nil)
			Expect(string(out)).NotTo(ContainSubstring(bot))
			Expect(string(out)).NotTo(ContainSubstring(appToken))
		}
		var hits []string
		_ = filepath.WalkDir(filepath.Join(tmpDir, "data"), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if raw, rerr := os.ReadFile(p); rerr == nil && (strings.Contains(string(raw), bot) || strings.Contains(string(raw), appToken)) {
				hits = append(hits, p)
			}
			return nil
		})
		Expect(hits).To(BeEmpty(), "a Slack token is in clear text on disk")
		// A token that Slack refuses fails the test with the reason of the platform.
		f().Refuse(appToken, "invalid_auth")
		DeferCleanup(func() { f().Refuse(appToken, "") })
		code, _ = chatsDo(http.MethodPost, chatsBase()+"/connectors/"+id+"/test", nil)
		Expect(code).To(Equal(http.StatusBadGateway))
	})

	It("answers a direct message of a listed person and ignores a stranger", func() {
		bot, appToken, id := app(obj{"allow": []string{"U100"}})
		f().Push(appToken, slacktest.DM("D200", "U200", "let me in"))
		f().Push(appToken, slacktest.DM("D100", "U100", "hello **bot**"))
		Eventually(answers(bot), 90*time.Second, 200*time.Millisecond).Should(HaveLen(1))
		Expect(answers(bot)()[0].Channel).To(Equal("D100"))
		Expect(answers(bot)()[0].Thread).To(BeEmpty())
		code, out := chatsDo(http.MethodGet, chatsBase()+"/connectors/"+id+"/senders", nil)
		Expect(code).To(Equal(http.StatusOK))
		got := map[string]bool{}
		for _, s := range decode(out)["senders"].([]any) {
			got[s.(obj)["id"].(string)] = s.(obj)["allowed"].(bool)
		}
		Expect(got).To(Equal(map[string]bool{"U100": true, "U200": false}))
		// The owner allows the stranger from the log.
		code, out = chatsDo(http.MethodPost, chatsBase()+"/connectors/"+id+"/senders/U200/allow", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		f().Push(appToken, slacktest.DM("D200", "U200", "now allowed"))
		Eventually(answers(bot), 90*time.Second, 200*time.Millisecond).Should(HaveLen(2))
	})

	It("answers a mention in its thread, and keeps one chat for everyone in the thread", func() {
		bot, appToken, id := app(obj{"allow": []string{"C500"}})
		mention := slacktest.Mention("C500", "U100", "UBOT", "summarise this", "")
		thread := mention["ts"].(string)
		f().Push(appToken, mention)
		Eventually(answers(bot), 90*time.Second, 200*time.Millisecond).Should(HaveLen(1))
		Expect(answers(bot)()[0].Thread).To(Equal(thread))
		f().Push(appToken, slacktest.Reply("C500", "U200", "and a follow-up", thread))
		Eventually(answers(bot), 90*time.Second, 200*time.Millisecond).Should(HaveLen(2))
		Expect(answers(bot)()[1].Thread).To(Equal(thread))
		chats := chatsOf(id)
		Expect(chats).To(HaveLen(1), "one chat per thread")
		Eventually(func() int { return len(userTexts(fullTranscript(chats[0]["id"].(string)))) }, 30*time.Second, 200*time.Millisecond).Should(Equal(2))
		// A channel that is not listed is not answered, and chatter is not read.
		f().Push(appToken, slacktest.Mention("C999", "U200", "UBOT", "other channel", ""))
		f().Push(appToken, map[string]any{"type": "message", "channel_type": "channel", "channel": "C500", "user": "U100", "text": "chatter", "ts": "1.1"})
		Consistently(answers(bot), 3*time.Second, 200*time.Millisecond).Should(HaveLen(2))
	})

	It("hands a file of a message to the chat", func() {
		bot, appToken, id := app(obj{"allow": []string{"U100"}})
		f().AddFile("F777", "brief.txt", "text/plain", []byte("the brief"))
		ev := slacktest.DM("D100", "U100", "read the brief")
		ev["files"] = []any{map[string]any{"id": "F777"}}
		f().Push(appToken, ev)
		Eventually(answers(bot), 90*time.Second, 200*time.Millisecond).Should(HaveLen(1))
		chats := chatsOf(id)
		Expect(chats).To(HaveLen(1))
		// The file is an attachment of the user line, apart from its words.
		Eventually(func() []string {
			var names []string
			for _, m := range fullTranscript(chats[0]["id"].(string)) {
				if m.Role == "user" {
					for _, a := range m.Attachments {
						names = append(names, a.Name)
					}
				}
			}
			return names
		}, 30*time.Second, 200*time.Millisecond).Should(ContainElement("brief.txt"))
		Expect(userTexts(fullTranscript(chats[0]["id"].(string)))).To(Equal([]string{"read the brief"}))
	})

	It("switches a binding off, with a notification, when Slack refuses its token", func() {
		bot, appToken, id := app(obj{"allow": []string{"U100"}})
		f().Refuse(bot, "token_revoked")
		DeferCleanup(func() { f().Refuse(bot, "") })
		f().Disconnect(appToken, "refresh_requested")
		Eventually(func() bool {
			code, out := chatsDo(http.MethodGet, chatsBase()+"/connectors/"+id, nil)
			return code == http.StatusOK && decode(out)["enabled"] == false
		}, 60*time.Second, 200*time.Millisecond).Should(BeTrue(), "the binding stayed on with a refused token")
		code, out := chatsDo(http.MethodGet, chatsBase()+"/notifications", nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(out)).To(ContainSubstring("was switched off"))
	})
})
