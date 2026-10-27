package e2echats_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const strongPassword = "E2e-Chats-Pass-123!"

// register creates an account and returns a client that acts as that user.
// The first account on a fresh server is the administrator.
func register(base, email string) (*client, string) {
	anon := &client{base: base}
	body, _ := json.Marshal(map[string]string{"email": email, "password": strongPassword, "name": email})
	resp, err := http.Post(base+"/api/auth/register", "application/json", strings.NewReader(string(body)))
	Expect(err).ToNot(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode).To(BeNumerically("<", 300), "register "+email)
	var cookie string
	for _, ck := range resp.Cookies() {
		if ck.Name == "session" || strings.Contains(ck.Name, "session") {
			cookie = ck.Name + "=" + ck.Value
		}
	}
	Expect(cookie).ToNot(BeEmpty(), "a session cookie")
	anon.cookie = cookie
	// Act with an API key, which is the way a script does it and needs no CSRF token.
	var key struct {
		Key  string `json:"key"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	code, raw := anon.do(http.MethodPost, "/api/auth/api-keys", map[string]string{"name": "e2e"})
	Expect(code).To(BeNumerically("<", 300), string(raw))
	Expect(json.Unmarshal(raw, &key)).To(Succeed())
	Expect(key.Key).ToNot(BeEmpty())
	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		ID string `json:"id"`
	}
	code, raw = anon.do(http.MethodGet, "/api/auth/me", nil)
	Expect(code).To(Equal(http.StatusOK), string(raw))
	_ = json.Unmarshal(raw, &me)
	id := me.ID
	if id == "" {
		id = me.User.ID
	}
	return &client{base: base, bearer: key.Key}, id
}

// Authentication on, SQLite auth database: who may use the chats, and whose
// chat is whose. Spec: sections 3 (every record is scoped by user), 4.6, 4.11.
var _ = Describe("Chats with authentication", Ordered, func() {
	var (
		in             *instance
		admin, bob     *client
		adminID, bobID string
	)
	BeforeAll(func() {
		in = newInstance()
		in.Start("--auth-enabled")
		admin, adminID = register(in.URL(), "admin@example.test")
		bob, bobID = register(in.URL(), "bob@example.test")
	})
	AfterAll(func() { in.Cleanup() })
	BeforeEach(func() { llm.Reset() })

	It("refuses anonymous callers", func() {
		anon := &client{base: in.URL()}
		code, _ := anon.do(http.MethodGet, "/api/chats", nil)
		Expect(code).To(Equal(http.StatusUnauthorized))
		code, _ = anon.do(http.MethodPost, "/api/chats", map[string]string{"model": "fake"})
		Expect(code).To(Equal(http.StatusUnauthorized))
		code, _ = (&client{base: in.URL(), bearer: "not-a-key"}).do(http.MethodGet, "/api/chats", nil)
		Expect(code).To(Equal(http.StatusUnauthorized))
	})

	It("lets a user with the default permissions use chats, and keeps each user's chats private", func() {
		mine := admin.create("fake")
		Expect(admin.say(mine.ID, "private to the admin ADMIN-SECRET")).To(Equal("done"))
		theirs := bob.create("fake")

		var list struct {
			Conversations []chatInfo `json:"conversations"`
		}
		Expect(bob.get("/api/chats?limit=200", &list)).To(Equal(http.StatusOK))
		var ids []string
		for _, x := range list.Conversations {
			ids = append(ids, x.ID)
		}
		Expect(ids).To(ContainElement(theirs.ID))
		Expect(ids).ToNot(ContainElement(mine.ID))

		// Every route answers 404 for another user's chat, as for a missing one.
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, ""}, {http.MethodDelete, ""}, {http.MethodGet, "/messages"}, {http.MethodPost, "/stop"},
			{http.MethodPost, "/send-now"}, {http.MethodGet, "/events"}, {http.MethodDelete, "/pending/p1"},
			{http.MethodGet, "/tools"}, {http.MethodGet, "/addons"},
		} {
			code, raw := bob.do(r.method, "/api/chats/"+mine.ID+r.path, nil)
			Expect(code).To(Equal(http.StatusNotFound), r.method+" "+r.path+" "+string(raw))
			Expect(string(raw)).ToNot(ContainSubstring("ADMIN-SECRET"))
		}
		code, _ := bob.do(http.MethodPost, "/api/chats/"+mine.ID+"/messages", map[string]string{"text": "intruder"})
		Expect(code).To(Equal(http.StatusNotFound))
		Expect(texts(admin.transcript(mine.ID), "user")).To(Equal([]string{"private to the admin ADMIN-SECRET"}))
		Expect(admin.info(mine.ID).ID).To(Equal(mine.ID), "the chat is intact")
	})

	It("keeps the files of each user apart", func() {
		code, raw := admin.do(http.MethodPut, "/api/chats/files/content?path=/who.txt", []byte("admin file"))
		Expect(code).To(BeNumerically("<", 300), string(raw))
		code, _ = bob.do(http.MethodGet, "/api/chats/files/content?path=/who.txt", nil)
		Expect(code).To(Equal(http.StatusNotFound))
		code, raw = bob.do(http.MethodPut, "/api/chats/files/content?path=/who.txt", []byte("bob file"))
		Expect(code).To(BeNumerically("<", 300), string(raw))
		code, raw = admin.do(http.MethodGet, "/api/chats/files/content?path=/who.txt", nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(raw)).To(Equal("admin file"))
		code, _ = bob.do(http.MethodGet, "/api/chats/files/content?path=/../who.txt", nil)
		Expect(code).To(BeNumerically(">=", 400))
	})

	It("does not let a user run a turn on another user's model allowlist", func() {
		code, raw := admin.do(http.MethodPut, "/api/auth/admin/users/"+bobID+"/models", map[string]any{"enabled": true, "models": []string{"mock-model"}})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		code, raw = bob.do(http.MethodPost, "/api/chats", map[string]string{"model": "fake"})
		Expect(code).To(Equal(http.StatusForbidden), string(raw))
		code, raw = bob.do(http.MethodPost, "/api/chats", map[string]string{"model": "mock-model"})
		Expect(code).To(Equal(http.StatusCreated), string(raw))
		code, _ = admin.do(http.MethodPut, "/api/auth/admin/users/"+bobID+"/models", map[string]any{"enabled": false})
		Expect(code).To(Equal(http.StatusOK))
	})

	It("needs the chats feature permission for every chats route", func() {
		own := bob.create("fake")
		code, raw := admin.do(http.MethodPut, "/api/auth/admin/users/"+bobID+"/permissions", map[string]bool{"chats": false})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, "/api/chats"}, {http.MethodGet, "/api/chats/" + own.ID}, {http.MethodGet, "/api/chats/" + own.ID + "/messages"},
			{http.MethodGet, "/api/chats/files"}, {http.MethodGet, "/api/chats/notifications"}, {http.MethodGet, "/api/chats/agents"},
			{http.MethodGet, "/api/chats/triggers"}, {http.MethodGet, "/api/chats/skills"},
		} {
			code, raw := bob.do(r.method, r.path, nil)
			Expect(code).To(Equal(http.StatusForbidden), r.method+" "+r.path+" "+string(raw))
		}
		code, _ = bob.do(http.MethodPost, "/api/chats", map[string]string{"model": "fake"})
		Expect(code).To(Equal(http.StatusForbidden))
		// The plain API is a different feature: it still works.
		code, raw = bob.do(http.MethodPost, "/v1/chat/completions", map[string]any{"model": "fake", "messages": []map[string]string{{"role": "user", "content": "plain"}}})
		Expect(code).To(Equal(http.StatusOK), string(raw))

		code, _ = admin.do(http.MethodPut, "/api/auth/admin/users/"+bobID+"/permissions", map[string]bool{"chats": true})
		Expect(code).To(Equal(http.StatusOK))
		code, _ = bob.do(http.MethodGet, "/api/chats/"+own.ID, nil)
		Expect(code).To(Equal(http.StatusOK), "the chat was kept while the permission was off")
	})

	It("starts the LocalAI assistant for an administrator only", func() {
		code, raw := bob.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake", "assistant": true})
		Expect(code).To(Equal(http.StatusForbidden), string(raw))
		Expect(errType(raw)).To(Equal("authorization_error"))

		var ch chatInfo
		code, raw = admin.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake", "assistant": true})
		Expect(code).To(Equal(http.StatusCreated), string(raw))
		Expect(json.Unmarshal(raw, &ch)).To(Succeed())
		Expect(ch.Assistant).To(BeTrue())

		code, raw = admin.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake", "assistant": true, "agent_id": "some-agent"})
		Expect(code).To(Equal(http.StatusBadRequest), string(raw))

		code, raw = admin.do(http.MethodPost, "/api/settings", map[string]any{"localai_assistant_enabled": false})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		code, raw = admin.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake", "assistant": true})
		Expect(code).To(Equal(http.StatusServiceUnavailable), string(raw))
		Expect(errType(raw)).To(Equal("assistant_off"))
		code, _ = admin.do(http.MethodPost, "/api/settings", map[string]any{"localai_assistant_enabled": true})
		Expect(code).To(Equal(http.StatusOK))
	})

	It("keeps chats, transcripts and the owner after a restart on the SQLite database", func() {
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "stored reply"} })
		ch := bob.create("fake")
		bob.say(ch.ID, "remember me")
		before := bob.transcript(ch.ID)

		in.Restart("--auth-enabled")

		after := bob.transcript(ch.ID) // the API key is stored in the database too
		Expect(after).To(Equal(before))
		code, _ := admin.do(http.MethodGet, "/api/chats/"+ch.ID, nil)
		Expect(code).To(Equal(http.StatusNotFound), "still another user's chat after the restart")
		Expect(bob.info(ch.ID).LastTurn).To(Equal("done"))
		_ = adminID
	})

	It("ends a turn that the process left running when it crashed, and keeps what was waiting", func() {
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Hang: true} })
		ch := bob.create("fake")
		bob.send(ch.ID, "CRASH-1 hang")
		Eventually(func() int { return llm.CountWith("CRASH-1") }, 60*time.Second).Should(Equal(1))
		_, pid := bob.send(ch.ID, "CRASH-1 queued")
		Expect(pid).ToNot(BeEmpty())

		in.Kill()
		llm.Reset()
		in.Start("--auth-enabled")

		i := bob.info(ch.ID)
		Expect(i.Running).To(BeFalse())
		Expect(i.LastTurn).To(Equal("interrupted"))
		Expect(i.Pending).To(HaveLen(1), "the message sent during the turn is waiting again")
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "back"} })
		code, raw := bob.do(http.MethodPost, "/api/chats/"+ch.ID+"/send-now", nil)
		Expect(code).To(Equal(http.StatusOK), string(raw))
		Eventually(func() []string { return texts(bob.transcript(ch.ID), "assistant") }, 30*time.Second).Should(ContainElement("back"))
	})
})

// A static API key and no accounts: one anonymous owner behind a shared key.
var _ = Describe("Chats with a static API key", func() {
	It("needs the key and keeps working with it", func() {
		in := newInstance()
		defer in.Cleanup()
		in.Start("--api-keys", "e2e-static-key")
		anon := &client{base: in.URL()}
		code, _ := anon.do(http.MethodGet, "/api/chats", nil)
		Expect(code).To(Equal(http.StatusUnauthorized))
		keyed := &client{base: in.URL(), bearer: "e2e-static-key"}
		llm.Reset()
		ch := keyed.create("fake")
		Expect(keyed.say(ch.ID, "with a key")).To(Equal("done"))
		es := keyed.events(ch.ID)
		defer es.Close()
		status, _ := es.Status()
		Expect(status).To(Equal(http.StatusOK))
		code, _ = anon.do(http.MethodGet, "/api/chats/"+ch.ID, nil)
		Expect(code).To(Equal(http.StatusUnauthorized))
	})
})
