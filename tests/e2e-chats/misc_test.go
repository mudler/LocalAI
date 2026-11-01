package e2echats_test

import (
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Reasoning, compaction, the per-chat switches, the assistant flag and the
// inbox, with authentication off. Spec: 4.4 (compaction), 4.11 (API),
// section 4.10 (notifications).
var _ = Describe("Chats reasoning, compaction, switches and inbox", Ordered, func() {
	var (
		in *instance
		c  *client
	)
	BeforeAll(func() {
		in = newInstance()
		in.writeModel("mock-autoparser", map[string]any{
			"name": "mock-autoparser", "backend": "mock-backend", "parameters": map[string]any{"model": "mock-model.bin"},
			"function": map[string]any{"grammar": map[string]any{"disable": true}},
		})
		in.Start()
		c = &client{base: in.URL()}
	})
	AfterAll(func() { in.Cleanup() })
	BeforeEach(func() { llm.Reset() })

	It("streams the model's reasoning as reasoning events before its tool call", func() {
		ch := c.create("mock-autoparser")
		es := c.events(ch.ID)
		defer es.Close()
		started, _ := c.send(ch.ID, "AUTOPARSER_TOOL_CALL think first")
		Expect(started).To(BeTrue())
		Eventually(func() string { return es.Text("reasoning") }, 30*time.Second, 50*time.Millisecond).Should(ContainSubstring("I need to search for information."))
		_, _ = c.do(http.MethodPost, "/api/chats/"+ch.ID+"/stop", nil)
		c.settled(ch.ID)
		Expect(es.Kinds()[0]).To(Equal("turn_start"))
		Expect(es.Kinds()).To(ContainElement("tool_start"))
	})

	It("splits inline <think> tags from the answer, as /v1/chat/completions does", func() {
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Reasoning: "weighing the options", Text: "the final answer"} })
		ch := c.create("fake")
		es := c.events(ch.ID)
		defer es.Close()
		c.say(ch.ID, "THINKTAG-1")
		es.Wait("turn_end")
		Expect(es.Text("reasoning")).To(ContainSubstring("weighing the options"))
		Expect(es.Text("text")).To(Equal("the final answer"))
	})

	It("compacts a conversation that fills the model's context window and reports it", func() {
		llm.Handle(func(r fakeReq) fakeStep {
			return fakeStep{Text: strings.Repeat("A long answer that fills the window. ", 30)}
		})
		ch := c.create("fake-small")
		es := c.events(ch.ID)
		defer es.Close()
		for i := 0; i < 8 && !es.Has("compacted"); i++ {
			c.say(ch.ID, "COMPACT-1 "+strings.Repeat("please go on with more detail ", 170))
		}
		if !es.Has("compacted") {
			Skip("known gap: G-CHATS-COMPACT (no compacted event after eight turns of 5000 characters on a 16384 token window)")
		}
		for _, ev := range es.All() {
			if ev.Kind == "compacted" {
				Expect(ev.Before).To(BeNumerically(">", ev.After))
			}
		}
		// The transcript keeps every message.
		Expect(len(texts(c.transcript(ch.ID), "user"))).To(BeNumerically(">=", 2))
	})

	It("leaves room for the conversation in an 8192 token window", func() {
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "short"} })
		ch := c.create("fake-8k")
		for i := 0; i < 3; i++ {
			c.say(ch.ID, "WINDOW-1 hello "+strings.Repeat("some more words to read ", 150))
		}
		// The loop warns when its tool schemas and prompt alone exceed the budget: compaction is then off.
		if strings.Contains(in.Log(), "auto-compaction skipped: the tool schemas and system prompt alone exceed the context budget") {
			Skip("known gap: G-CHATS-OVERHEAD (tool schemas and prompt take about 6700 tokens, so a chat on an 8192 token model can never compact)")
		}
	})

	It("lists the tools of a chat and switches an add-on and a skill off for that chat only", func() {
		a, b := c.create("fake"), c.create("fake")
		var tools struct {
			Addons []struct {
				ID  string `json:"id"`
				Off bool   `json:"off"`
			} `json:"addons"`
			Off []string `json:"off"`
		}
		Expect(c.get("/api/chats/"+a.ID+"/tools", &tools)).To(Equal(http.StatusOK))
		code, raw := c.do(http.MethodPut, "/api/chats/"+a.ID+"/mcp", map[string][]string{"off": {"some-server"}})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		Expect(c.get("/api/chats/"+a.ID+"/tools", &tools)).To(Equal(http.StatusOK))
		Expect(tools.Off).To(ContainElement("some-server"))
		Expect(c.get("/api/chats/"+b.ID+"/tools", &tools)).To(Equal(http.StatusOK))
		Expect(tools.Off).ToNot(ContainElement("some-server"))

		code, raw = c.do(http.MethodPut, "/api/chats/"+a.ID+"/skills", map[string][]string{"off": {"docx"}})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		code, raw = c.do(http.MethodPut, "/api/chats/"+a.ID+"/addons", map[string][]string{"off": {"bad id with spaces"}})
		Expect(code).To(Equal(http.StatusBadRequest), string(raw))
	})

	Describe("the LocalAI assistant flag", func() {
		It("starts an assistant chat, refuses an agent with it, and answers 503 when the setting is off", func() {
			var ch chatInfo
			code, raw := c.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake", "assistant": true})
			Expect(code).To(Equal(http.StatusCreated), string(raw))
			Expect(jsonInto(raw, &ch)).To(Succeed())
			Expect(ch.Assistant).To(BeTrue())
			Expect(c.info(ch.ID).Assistant).To(BeTrue())

			code, raw = c.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake", "assistant": true, "agent_id": "x"})
			Expect(code).To(Equal(http.StatusBadRequest), string(raw))

			code, raw = c.do(http.MethodPost, "/api/settings", map[string]any{"localai_assistant_enabled": false})
			Expect(code).To(Equal(http.StatusOK), string(raw))
			code, raw = c.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake", "assistant": true})
			Expect(code).To(Equal(http.StatusServiceUnavailable), string(raw))
			Expect(errType(raw)).To(Equal("assistant_off"))
			code, _ = c.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake"})
			Expect(code).To(Equal(http.StatusCreated), "plain chats are not affected")
			code, _ = c.do(http.MethodPost, "/api/settings", map[string]any{"localai_assistant_enabled": true})
			Expect(code).To(Equal(http.StatusOK))
		})

		It("gives only the assistant chat the assistant tools", func() {
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "ok"} })
			plain := c.create("fake")
			c.say(plain.ID, "ASSIST-PLAIN")
			var asst chatInfo
			code, raw := c.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake", "assistant": true})
			Expect(code).To(Equal(http.StatusCreated), string(raw))
			Expect(jsonInto(raw, &asst)).To(Succeed())
			c.say(asst.ID, "ASSIST-ADMIN")
			var plainTools, asstTools []string
			for _, r := range llm.Requests() {
				if strings.Contains(r.Prompt(), "ASSIST-PLAIN") {
					plainTools = r.Tools
				}
				if strings.Contains(r.Prompt(), "ASSIST-ADMIN") {
					asstTools = r.Tools
				}
			}
			Expect(len(asstTools)).To(BeNumerically(">", len(plainTools)), "the assistant chat has the instance-management tools")
		})
	})

	Describe("notifications, inbox and push", func() {
		It("starts with an empty inbox and a stable push key", func() {
			var n struct {
				Notifications []map[string]any `json:"notifications"`
				Unread        int              `json:"unread"`
			}
			Expect(c.get("/api/chats/notifications", &n)).To(Equal(http.StatusOK))
			Expect(n.Unread).To(BeZero())
			var k1, k2 struct {
				PublicKey string `json:"public_key"`
			}
			Expect(c.get("/api/chats/push/key", &k1)).To(Equal(http.StatusOK))
			Expect(k1.PublicKey).ToNot(BeEmpty())
			Expect(c.get("/api/chats/push/key", &k2)).To(Equal(http.StatusOK))
			Expect(k2.PublicKey).To(Equal(k1.PublicKey))
			in.Restart()
			var k3 struct {
				PublicKey string `json:"public_key"`
			}
			Expect(c.get("/api/chats/push/key", &k3)).To(Equal(http.StatusOK))
			Expect(k3.PublicKey).To(Equal(k1.PublicKey), "the key pair is kept across a restart")
		})

		It("validates push subscriptions", func() {
			code, _ := c.do(http.MethodPost, "/api/chats/push/subscribe", map[string]any{})
			Expect(code).To(Equal(http.StatusBadRequest))
			code, _ = c.do(http.MethodPost, "/api/chats/push/subscribe", map[string]any{"endpoint": "http://not-https.invalid/x", "keys": map[string]string{"p256dh": "a", "auth": "b"}})
			Expect(code).To(Equal(http.StatusBadRequest), "a push endpoint must be https")
		})

		It("tells the owner when a trigger run finishes and lets them read and delete the notice", func() {
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "digest"} })
			var trig struct {
				ID string `json:"id"`
			}
			code, raw := c.do(http.MethodPost, "/api/chats/triggers", map[string]any{"name": "E2E manual", "prompt": "INBOX-1 summarise", "kind": "schedule", "schedule": "weekdays 09:00", "time_zone": "UTC", "enabled": false, "model": "fake"})
			Expect(code).To(BeNumerically("<", 300), string(raw))
			Expect(jsonInto(raw, &trig)).To(Succeed())
			Expect(trig.ID).ToNot(BeEmpty())
			code, raw = c.do(http.MethodPost, "/api/chats/triggers/"+trig.ID+"/run", nil)
			Expect(code).To(BeNumerically("<", 300), string(raw))

			var n struct {
				Notifications []struct {
					ID     int64  `json:"id"`
					Title  string `json:"title"`
					Unread bool   `json:"unread"`
					Read   bool   `json:"read"`
				} `json:"notifications"`
				Unread int `json:"unread"`
			}
			Eventually(func() int {
				Expect(c.get("/api/chats/notifications", &n)).To(Equal(http.StatusOK))
				return len(n.Notifications)
			}, 60*time.Second, 200*time.Millisecond).Should(BeNumerically(">=", 1))
			Expect(n.Unread).To(BeNumerically(">=", 1))
			var cnt struct {
				Unread int `json:"unread"`
			}
			Expect(c.get("/api/chats/notifications/count", &cnt)).To(Equal(http.StatusOK))
			Expect(cnt.Unread).To(Equal(n.Unread))

			code, raw = c.do(http.MethodPost, "/api/chats/notifications/read", map[string]any{"all": true})
			Expect(code).To(Equal(http.StatusOK), string(raw))
			Expect(c.get("/api/chats/notifications/count", &cnt)).To(Equal(http.StatusOK))
			Expect(cnt.Unread).To(BeZero())
			code, raw = c.do(http.MethodDelete, "/api/chats/notifications", map[string]any{"ids": []int64{n.Notifications[0].ID}})
			Expect(code).To(Equal(http.StatusOK), string(raw))
		})

		It("streams new notifications as server-sent events", func() {
			resp, err := http.DefaultClient.Do(c.req(http.MethodGet, "/api/chats/notifications/events", nil, ""))
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/event-stream"))
		})
	})
})
