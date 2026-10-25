package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	defer resp.Body.Close()
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
		defer resp.Body.Close()
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
