package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The chats API against the whole server: HTTP, the conversation service, the
// agent loop, the in-memory call into the chat route and the mock backend.
// Scenarios that need a scripted model (sub-agents, compaction, restart, two
// frontends) live next to the code in core/services/studio, where the model
// can be scripted; this file proves the wiring a user meets.

type chatMessage struct {
	Seq  int64  `json:"seq"`
	Role string `json:"role"`
	Text string `json:"text"`
	// Attachments are the files of a user line; Text holds only its words.
	Attachments []struct {
		Name string `json:"name"`
	} `json:"attachments"`
}

func chatsBase() string { return strings.TrimSuffix(apiURL, "/v1") + "/api/chats" }

func chatsDo(method, url string, body any) (int, []byte) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		Expect(err).ToNot(HaveOccurred())
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	Expect(err).ToNot(HaveOccurred())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	return resp.StatusCode, out
}

func chatsCreate() string {
	code, out := chatsDo(http.MethodPost, chatsBase(), map[string]string{"model": "mock-model"})
	Expect(code).To(Equal(http.StatusCreated), string(out))
	var c struct {
		ID string `json:"id"`
	}
	Expect(json.Unmarshal(out, &c)).To(Succeed())
	Expect(c.ID).ToNot(BeEmpty())
	return c.ID
}

func chatsSend(id, text string) {
	code, out := chatsDo(http.MethodPost, chatsBase()+"/"+id+"/messages", map[string]string{"text": text})
	Expect(code).To(Equal(http.StatusOK), string(out))
}

func chatsTranscript(id string) []chatMessage {
	code, out := chatsDo(http.MethodGet, chatsBase()+"/"+id+"/messages", nil)
	Expect(code).To(Equal(http.StatusOK), string(out))
	var r struct {
		Messages []chatMessage `json:"messages"`
	}
	Expect(json.Unmarshal(out, &r)).To(Succeed())
	return r.Messages
}

func chatsIdle(id string) bool {
	code, out := chatsDo(http.MethodGet, chatsBase()+"/"+id, nil)
	Expect(code).To(Equal(http.StatusOK), string(out))
	var r struct {
		Running  bool   `json:"running"`
		LastTurn string `json:"last_turn"`
	}
	Expect(json.Unmarshal(out, &r)).To(Succeed())
	return !r.Running && r.LastTurn != ""
}

func userTexts(msgs []chatMessage) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == "user" {
			out = append(out, m.Text)
		}
	}
	return out
}

var _ = Describe("Chats API", Label("Chats"), func() {
	It("answers a message through the real chat route and keeps the transcript", func() {
		id := chatsCreate()
		chatsSend(id, "hello there")
		Eventually(func() bool { return chatsIdle(id) }, 30*time.Second, 200*time.Millisecond).Should(BeTrue())

		msgs := chatsTranscript(id)
		Expect(userTexts(msgs)).To(Equal([]string{"hello there"}))
		var answered bool
		for _, m := range msgs {
			if m.Role == "assistant" && strings.TrimSpace(m.Text) != "" {
				answered = true
			}
		}
		Expect(answered).To(BeTrue(), fmt.Sprintf("no assistant text in %+v", msgs))
	})

	It("keeps messages sent while a turn runs, in the order they were sent", func() {
		id := chatsCreate()
		chatsSend(id, "MOCK_SLOW_STREAM first")
		chatsSend(id, "second")
		chatsSend(id, "third")
		Eventually(func() bool { return chatsIdle(id) }, 60*time.Second, 200*time.Millisecond).Should(BeTrue())

		users := userTexts(chatsTranscript(id))
		Expect(users).To(Equal([]string{"MOCK_SLOW_STREAM first", "second", "third"}))
	})

	It("keeps a chat out of reach of a name that does not exist", func() {
		code, _ := chatsDo(http.MethodGet, chatsBase()+"/does-not-exist", nil)
		Expect(code).To(Equal(http.StatusNotFound))
	})

	It("no longer serves the removed agent, job and conversation APIs", func() {
		root := strings.TrimSuffix(apiURL, "/v1")
		for _, path := range []string{"/api/agents", "/api/conversations"} {
			code, _ := chatsDo(http.MethodGet, root+path, nil)
			Expect(code).To(Equal(http.StatusNotFound), path)
		}
	})

	It("imports the chats that the old page kept in the browser, once", func() {
		stored := map[string]any{"chats": []any{map[string]any{"id": "c1", "name": "Old trip", "model": "mock-model",
			"createdAt": 1700000000000, "updatedAt": 1700000100000,
			"history": []any{map[string]any{"role": "user", "content": "plan a trip"}, map[string]any{"role": "assistant", "content": "ok"}}}}}
		code, out := chatsDo(http.MethodPost, chatsBase()+"/import/browser", stored)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(string(out)).To(ContainSubstring(`"imported":1`))
		code, out = chatsDo(http.MethodPost, chatsBase()+"/import/browser", stored)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(string(out)).To(ContainSubstring(`"imported":0`))
		Expect(string(out)).To(ContainSubstring(`"skipped":1`))
	})

	It("deletes a chat", func() {
		id := chatsCreate()
		code, _ := chatsDo(http.MethodDelete, chatsBase()+"/"+id, nil)
		Expect(code).To(BeNumerically("<", 300))
		code, _ = chatsDo(http.MethodGet, chatsBase()+"/"+id, nil)
		Expect(code).To(Equal(http.StatusNotFound))
	})
})

// The chat completions endpoint is not touched by chats. The golden file holds
// its streaming output for a fixed request, with the per-request id and time
// removed; set UPDATE_GOLDEN=1 to record it again.
var _ = Describe("Chat completions stream", Label("Chats"), func() {
	idRe := regexp.MustCompile(`"id":"[^"]*"`)
	createdRe := regexp.MustCompile(`"created":\d+`)

	It("is byte-identical to the recorded output", func() {
		body := `{"model":"mock-model","stream":true,"messages":[{"role":"user","content":"hello"}]}`
		resp, err := http.Post(apiURL+"/chat/completions", "application/json", strings.NewReader(body))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK), string(raw))

		got := createdRe.ReplaceAll(idRe.ReplaceAll(raw, []byte(`"id":"X"`)), []byte(`"created":0`))
		const golden = "testdata/chat_completions_stream.golden"
		if os.Getenv("UPDATE_GOLDEN") != "" {
			Expect(os.WriteFile(golden, got, 0o644)).To(Succeed())
		}
		want, err := os.ReadFile(golden)
		Expect(err).ToNot(HaveOccurred(), "record it with UPDATE_GOLDEN=1")
		Expect(string(got)).To(Equal(string(want)))
	})
})

// The files of a user, through the whole server: HTTP, the files service, the
// workspace store and the blob store. Skipped when the server has no workspace
// (a build without SQLite, or no data path).
var _ = Describe("Chats files", Label("Chats"), func() {
	filesURL := func(path string) string { return chatsBase() + "/files" + path }
	query := func(p string) string { return "?path=" + url.QueryEscape(p) }

	BeforeEach(func() {
		code, body := chatsDo(http.MethodGet, filesURL(""), nil)
		if code == http.StatusConflict {
			Skip("files are off on this server: " + string(body))
		}
		Expect(code).To(Equal(http.StatusOK), string(body))
	})

	It("keeps a file, finds it, deletes it into the Trash and restores it", func() {
		name := fmt.Sprintf("/E2E/note-%d.txt", time.Now().UnixNano())
		req, err := http.NewRequest(http.MethodPut, filesURL("/content")+query(name), strings.NewReader("zeppelin schedule"))
		Expect(err).ToNot(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		code, out := chatsDo(http.MethodGet, filesURL("/content")+query(name), nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(out)).To(Equal("zeppelin schedule"))

		code, out = chatsDo(http.MethodGet, filesURL("/search")+"?q=zeppelin", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(string(out)).To(ContainSubstring(name))

		code, _ = chatsDo(http.MethodDelete, filesURL("")+query(name), nil)
		Expect(code).To(Equal(http.StatusOK))
		code, _ = chatsDo(http.MethodGet, filesURL("/content")+query(name), nil)
		Expect(code).To(Equal(http.StatusNotFound))

		_, out = chatsDo(http.MethodGet, filesURL("/trash"), nil)
		var trash struct {
			Entries []struct {
				Rev   int64    `json:"rev"`
				Paths []string `json:"paths"`
			} `json:"entries"`
		}
		Expect(json.Unmarshal(out, &trash)).To(Succeed())
		var rev int64
		for _, e := range trash.Entries {
			for _, p := range e.Paths {
				if p == name {
					rev = e.Rev
				}
			}
		}
		Expect(rev).ToNot(BeZero(), string(out))
		code, out = chatsDo(http.MethodPost, filesURL("/undo"), map[string]any{"rev": rev})
		Expect(code).To(Equal(http.StatusOK), string(out))
		code, out = chatsDo(http.MethodGet, filesURL("/content")+query(name), nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(out)).To(Equal("zeppelin schedule"))
	})

	It("saves a file attached to a message in the chat's folder", func() {
		id := chatsCreate()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		Expect(mw.WriteField("text", "read the attachment")).To(Succeed())
		fw, err := mw.CreateFormFile("files", "attached.txt")
		Expect(err).ToNot(HaveOccurred())
		_, _ = fw.Write([]byte("kestrel migration data"))
		Expect(mw.Close()).To(Succeed())
		resp, err := http.Post(chatsBase()+"/"+id+"/messages", mw.FormDataContentType(), &buf)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Eventually(func() bool { return chatsIdle(id) }, 30*time.Second, 200*time.Millisecond).Should(BeTrue())

		code, out := chatsDo(http.MethodGet, filesURL("/search")+"?q=kestrel", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(string(out)).To(ContainSubstring("attached.txt"))
		Expect(string(out)).To(ContainSubstring("/Chats/"))
	})

	It("refuses a path that leaves the user's home", func() {
		for _, p := range []string{"/../etc/passwd", "/a/../../b"} {
			code, _ := chatsDo(http.MethodGet, filesURL("/content")+query(p), nil)
			Expect(code).To(Equal(http.StatusBadRequest), p)
		}
	})
})

// Skills through the whole server: written over HTTP, found by a chat, switched
// off for it, read back. Skipped when the server has no workspace.
var _ = Describe("Chats skills", Label("Chats"), func() {
	skillsURL := func(p string) string { return chatsBase() + "/skills" + p }

	BeforeEach(func() {
		code, body := chatsDo(http.MethodGet, skillsURL(""), nil)
		if code == http.StatusConflict {
			Skip("files are off on this server: " + string(body))
		}
		Expect(code).To(Equal(http.StatusOK), string(body))
	})

	type skill struct {
		Name    string `json:"name"`
		Layer   string `json:"layer"`
		Enabled bool   `json:"enabled"`
		Path    string `json:"path"`
	}
	list := func(chat string) map[string]skill {
		u := skillsURL("")
		if chat != "" {
			u += "?chat=" + chat
		}
		code, out := chatsDo(http.MethodGet, u, nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		var res struct {
			Skills []skill `json:"skills"`
		}
		Expect(json.Unmarshal(out, &res)).To(Succeed())
		m := map[string]skill{}
		for _, s := range res.Skills {
			m[s.Name] = s
		}
		return m
	}

	It("writes a skill, offers it to a chat, switches it off for that chat only, and deletes it", func() {
		name := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
		code, out := chatsDo(http.MethodPost, skillsURL(""), map[string]any{"layer": "personal", "name": name, "description": "Walk through the release checklist", "body": "Tick every box."})
		Expect(code).To(Equal(http.StatusOK), string(out))

		Expect(list("")[name].Layer).To(Equal("personal"))
		Expect(list("")[name].Path).To(ContainSubstring("/workspace/home/.skills/" + name))

		chat := chatsCreate()
		other := chatsCreate()
		Expect(list(chat)[name].Enabled).To(BeTrue())
		code, out = chatsDo(http.MethodPut, chatsBase()+"/"+chat+"/skills", map[string]any{"off": []string{name}})
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(list(chat)[name].Enabled).To(BeFalse())
		Expect(list(other)[name].Enabled).To(BeTrue(), "a switch belongs to one chat")

		// A turn with the skill switched off still works.
		chatsSend(chat, "hello")
		Eventually(func() bool { return chatsIdle(chat) }, 30*time.Second, 200*time.Millisecond).Should(BeTrue())

		code, _ = chatsDo(http.MethodDelete, skillsURL("/personal/"+name), nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(list("")).NotTo(HaveKey(name))
	})

	It("refuses a skill name that is not valid", func() {
		code, _ := chatsDo(http.MethodPost, skillsURL(""), map[string]any{"layer": "personal", "name": "Not Valid", "description": "d", "body": "b"})
		Expect(code).To(Equal(http.StatusBadRequest))
	})
})

// Saved agents, triggers and the inbox through the whole server: a trigger that
// the owner runs by hand is a chat of the owner on the mock model.
var _ = Describe("Chats agents, triggers and inbox", Label("Chats"), func() {
	type obj = map[string]any
	decode := func(out []byte) obj {
		var m obj
		Expect(json.Unmarshal(out, &m)).To(Succeed(), string(out))
		return m
	}

	It("starts a chat with a saved agent and answers it", func() {
		code, out := chatsDo(http.MethodPost, chatsBase()+"/agents", obj{"name": "Helper e2e", "instructions": "Be brief.", "model": "mock-model"})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		agentID := decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/agents/"+agentID, nil) })

		code, out = chatsDo(http.MethodPost, chatsBase(), obj{"agent_id": agentID})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		chat := decode(out)
		Expect(chat).To(HaveKeyWithValue("agent_id", agentID))
		Expect(chat).To(HaveKeyWithValue("model", "mock-model"))
		id := chat["id"].(string)
		chatsSend(id, "hello")
		Eventually(func() bool { return chatsIdle(id) }, 60*time.Second, 200*time.Millisecond).Should(BeTrue())
		Expect(userTexts(chatsTranscript(id))).To(Equal([]string{"hello"}))
	})

	It("runs a trigger by hand as a chat of its owner and tells the owner", func() {
		code, out := chatsDo(http.MethodPost, chatsBase()+"/triggers", obj{
			"name": "Digest e2e", "prompt": "Say hello.", "kind": "schedule", "schedule": "daily 08:00", "time_zone": "UTC",
			"enabled": true, "model": "mock-model",
		})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		trgID := decode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/triggers/"+trgID, nil) })

		code, out = chatsDo(http.MethodGet, chatsBase()+"/notifications/count", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		before := int(decode(out)["unread"].(float64))

		code, out = chatsDo(http.MethodPost, chatsBase()+"/triggers/"+trgID+"/run", nil)
		Expect(code).To(Equal(http.StatusAccepted), string(out))
		var run obj
		Eventually(func() string {
			code, out := chatsDo(http.MethodGet, chatsBase()+"/triggers/"+trgID+"/runs", nil)
			Expect(code).To(Equal(http.StatusOK), string(out))
			runs := decode(out)["runs"].([]any)
			if len(runs) == 0 {
				return ""
			}
			run = runs[0].(obj)
			return run["status"].(string)
		}, 90*time.Second, 200*time.Millisecond).Should(Equal("done"))

		chatID := run["chat_id"].(string)
		code, out = chatsDo(http.MethodGet, chatsBase()+"/"+chatID, nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(decode(out)).To(And(HaveKeyWithValue("origin", "trigger"), HaveKeyWithValue("origin_id", trgID)))
		Expect(userTexts(chatsTranscript(chatID))).To(ContainElement(ContainSubstring("Say hello.")))

		// Opening the chat marked its notification read, so the count is as before.
		code, out = chatsDo(http.MethodGet, chatsBase()+"/notifications?limit=50", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		found := false
		for _, n := range decode(out)["notifications"].([]any) {
			if n.(obj)["link"] == "/app/chats/"+chatID {
				found = true
				Expect(n.(obj)["kind"]).To(Equal("run_ended"))
			}
		}
		Expect(found).To(BeTrue(), "no notification for the run")
		code, out = chatsDo(http.MethodGet, chatsBase()+"/notifications/count", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(int(decode(out)["unread"].(float64))).To(Equal(before))
	})
})
