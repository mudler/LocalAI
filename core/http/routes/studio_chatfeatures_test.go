// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeLLM is the model behind the chat route: an OpenAI-compatible handler
// that records what the loop sends and answers "Echo: <last user text>".
type fakeLLM struct {
	mu   sync.Mutex
	reqs []map[string]any
	// hold, when set, makes a request wait until it is closed or sent a token.
	hold chan struct{}
}

func (f *fakeLLM) requests() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.reqs...)
}

func (f *fakeLLM) last() map[string]any {
	reqs := f.requests()
	Expect(reqs).NotTo(BeEmpty())
	return reqs[len(reqs)-1]
}

// userTexts returns the text of each user message of a request.
func userTexts(req map[string]any) []string {
	var out []string
	for _, m := range req["messages"].([]any) {
		msg := m.(map[string]any)
		if msg["role"] != "user" {
			continue
		}
		switch c := msg["content"].(type) {
		case string:
			out = append(out, c)
		case []any:
			for _, part := range c {
				if p := part.(map[string]any); p["type"] == "text" {
					out = append(out, p["text"].(string))
				}
			}
		}
	}
	return out
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req map[string]any
	if json.Unmarshal(raw, &req) != nil || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-r.Context().Done():
			return
		}
	}
	texts := userTexts(req)
	answer := "Echo: "
	if len(texts) > 0 {
		answer += texts[len(texts)-1]
	}
	if req["stream"] != true {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer}}},
			"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	}
	chunk(map[string]any{"object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": answer}}}})
	chunk(map[string]any{"object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	if so, ok := req["stream_options"].(map[string]any); ok && so["include_usage"] == true {
		chunk(map[string]any{"object": "chat.completion.chunk", "choices": []any{},
			"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110, "timing_token_generation": 500.0}})
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// newLLMStudioServer is a chats server whose loop is the real nib harness and
// whose model is llm.
func newLLMStudioServer(llm *fakeLLM) *studioServer {
	return newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
		p := provider.New()
		p.Transport().SetHandler(llm)
		d.Provider, d.Harness = p, nil
	})
}

func (s *studioServer) waitIdle(user, id string) {
	GinkgoHelper()
	Eventually(func() bool {
		d := s.detail(user, id)
		return !d["running"].(bool) && d["last_turn"] != nil
	}, 30*time.Second, 50*time.Millisecond).Should(BeTrue())
}

// say sends a message and waits for the turn to end.
func (s *studioServer) say(user, id, text string) {
	GinkgoHelper()
	rec := s.do(user, http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": text})
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	s.waitIdle(user, id)
}

func (s *studioServer) transcript(user, id string) []map[string]any {
	GinkgoHelper()
	rec := s.do(user, http.MethodGet, "/api/chats/"+id+"/messages", nil)
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	var out []map[string]any
	for _, m := range decodeJSON(rec)["messages"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

func roleTexts(msgs []map[string]any, role string) []string {
	var out []string
	for _, m := range msgs {
		if m["role"] == role {
			out = append(out, m["text"].(string))
		}
	}
	return out
}

var _ = Describe("Chats per-chat settings", func() {
	var (
		llm *fakeLLM
		s   *studioServer
	)
	BeforeEach(func() {
		llm = &fakeLLM{}
		studioModels = []string{"other-model"}
		DeferCleanup(func() { studioModels = nil })
		s = newLLMStudioServer(llm)
	})

	It("stores the settings on the chat and applies them to the next turn", func() {
		id := s.create("alice")
		rec := s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"settings": map[string]any{
			"system_prompt": "ALWAYS-ANSWER-IN-HAIKU", "temperature": 0.25, "top_p": 0.5, "top_k": 7, "reasoning_effort": "low", "context_size": 8192,
		}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["settings"]).To(HaveKeyWithValue("top_k", BeNumerically("==", 7)))
		Expect(s.detail("alice", id)["settings"]).To(HaveKeyWithValue("system_prompt", "ALWAYS-ANSWER-IN-HAIKU"))

		s.say("alice", id, "hello")
		req := llm.last()
		Expect(req).To(HaveKeyWithValue("temperature", BeNumerically("~", 0.25, 1e-6)))
		Expect(req).To(HaveKeyWithValue("top_p", BeNumerically("~", 0.5, 1e-6)))
		Expect(req).To(HaveKeyWithValue("top_k", BeNumerically("==", 7)))
		Expect(req).To(HaveKeyWithValue("reasoning_effort", "low"))
		system := req["messages"].([]any)[0].(map[string]any)
		Expect(system["role"]).To(Equal("system"))
		Expect(system["content"]).To(ContainSubstring("ALWAYS-ANSWER-IN-HAIKU"))

		// Clearing the settings returns the model's defaults.
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"settings": map[string]any{}}).Code).To(Equal(http.StatusOK))
		s.say("alice", id, "again")
		Expect(llm.last()).NotTo(HaveKey("top_k"))
	})

	It("refuses settings out of range and unknown fields to change", func() {
		id := s.create("alice")
		for _, body := range []map[string]any{
			{"settings": map[string]any{"temperature": 3}},
			{"settings": map[string]any{"top_p": 0}},
			{"settings": map[string]any{"top_k": -1}},
			{"settings": map[string]any{"reasoning_effort": "extreme"}},
			{"settings": map[string]any{"context_size": 10}},
			{},
		} {
			Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, body).Code).To(Equal(http.StatusBadRequest), fmt.Sprint(body))
		}
	})

	It("renames a chat, moves it to another model, and keeps both private", func() {
		id := s.create("alice")
		rec := s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "Trip plan", "model": "other-model"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(And(HaveKeyWithValue("title", "Trip plan"), HaveKeyWithValue("model", "other-model")))
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"model": "no-such-model"}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("bob", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "mine now"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "   "}).Code).To(Equal(http.StatusBadRequest))

		s.say("alice", id, "hi")
		Expect(llm.last()).To(HaveKeyWithValue("model", "other-model"))
	})

	It("refuses a new title or model while a turn runs, but takes settings", func() {
		llm.hold = make(chan struct{})
		id := s.create("alice")
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "slow"}).Code).To(Equal(http.StatusOK))
		Eventually(func() int { return len(llm.requests()) }, 20*time.Second).Should(Equal(1))
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "x"}).Code).To(Equal(http.StatusConflict))
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"model": "other-model"}).Code).To(Equal(http.StatusConflict))
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"settings": map[string]any{"temperature": 0.1}}).Code).To(Equal(http.StatusOK))
		close(llm.hold)
		s.waitIdle("alice", id)
	})
})

// readEvents reads the server-sent events of a chat until stop returns true,
// and returns them in order as kind and decoded data.
func (s *studioServer) readEvents(user, id string, start func(), stop func(kind string) bool) []map[string]any {
	GinkgoHelper()
	srv := httptest.NewServer(s.e)
	DeferCleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/chats/"+id+"/events", nil)
	Expect(err).NotTo(HaveOccurred())
	req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	go start()
	var (
		out  []map[string]any
		kind string
	)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			kind = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var data map[string]any
			Expect(json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)).To(Succeed())
			out = append(out, data)
			if stop(kind) {
				return out
			}
		}
	}
	Fail("the event stream ended before the expected event")
	return nil
}

var _ = Describe("Chats context meter", func() {
	It("reports the tokens used, the limit and the speed with each model request", func() {
		llm := &fakeLLM{}
		s := newLLMStudioServer(llm)
		id := s.create("alice")
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"settings": map[string]any{"context_size": 4096}}).Code).To(Equal(http.StatusOK))
		events := s.readEvents("alice", id, func() {
			defer GinkgoRecover()
			s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "count me"})
		}, func(kind string) bool { return kind == "turn_end" })

		var usage map[string]any
		for _, ev := range events {
			if ev["kind"] == "usage" {
				usage = ev["usage"].(map[string]any)
			}
		}
		Expect(usage).NotTo(BeNil(), "no usage event in %v", events)
		Expect(usage).To(HaveKeyWithValue("used", BeNumerically("==", 100)))
		Expect(usage).To(HaveKeyWithValue("limit", BeNumerically("==", 4096)))
		Expect(usage).To(HaveKeyWithValue("completion", BeNumerically("==", 10)))
		Expect(usage).To(HaveKeyWithValue("tokens_per_second", BeNumerically("==", 20)))
	})
})

var _ = Describe("Chats transcript edits", func() {
	var (
		llm *fakeLLM
		s   *studioServer
		id  string
	)
	BeforeEach(func() {
		llm = &fakeLLM{}
		s = newLLMStudioServer(llm)
		id = s.create("alice")
		s.say("alice", id, "first question")
		s.say("alice", id, "second question")
	})

	It("regenerates the last answer and the model sees only the messages that stay", func() {
		before := len(llm.requests())
		rec := s.do("alice", http.MethodPost, "/api/chats/"+id+"/regenerate", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("started", true))
		s.waitIdle("alice", id)
		Expect(len(llm.requests())).To(BeNumerically(">", before))

		msgs := s.transcript("alice", id)
		Expect(roleTexts(msgs, "user")).To(Equal([]string{"first question", "second question"}))
		Expect(roleTexts(msgs, "assistant")).To(Equal([]string{"Echo: first question", "Echo: second question"}))
		// The model got the first exchange once, then the second question once.
		Expect(userTexts(llm.last())).To(Equal([]string{"first question", "second question"}))
	})

	It("edits a sent message and runs the chat again from there", func() {
		var firstSeq float64
		for _, m := range s.transcript("alice", id) {
			if m["role"] == "user" {
				firstSeq = m["seq"].(float64)
				break
			}
		}
		url := fmt.Sprintf("/api/chats/%s/messages/%d/edit", id, int64(firstSeq))
		rec := s.do("alice", http.MethodPost, url, map[string]any{"text": "edited question"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		s.waitIdle("alice", id)

		msgs := s.transcript("alice", id)
		Expect(roleTexts(msgs, "user")).To(Equal([]string{"edited question"}))
		Expect(roleTexts(msgs, "assistant")).To(Equal([]string{"Echo: edited question"}))
		Expect(userTexts(llm.last())).To(Equal([]string{"edited question"}), "the removed turns must not reach the model")
	})

	It("refuses to edit a line that is not a user message, and a missing one", func() {
		var assistantSeq float64
		for _, m := range s.transcript("alice", id) {
			if m["role"] == "assistant" {
				assistantSeq = m["seq"].(float64)
			}
		}
		Expect(s.do("alice", http.MethodPost, fmt.Sprintf("/api/chats/%s/messages/%d/edit", id, int64(assistantSeq)), map[string]any{"text": "x"}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages/9999/edit", map[string]any{"text": "x"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages/1/edit", map[string]any{"text": " "}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("bob", http.MethodPost, "/api/chats/"+id+"/regenerate", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("refuses to regenerate an empty chat and a running one", func() {
		empty := s.create("alice")
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+empty+"/regenerate", nil).Code).To(Equal(http.StatusBadRequest))

		llm.hold = make(chan struct{})
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "slow"}).Code).To(Equal(http.StatusOK))
		Eventually(func() bool { return s.detail("alice", id)["running"].(bool) }, 20*time.Second).Should(BeTrue())
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/regenerate", nil).Code).To(Equal(http.StatusConflict))
		close(llm.hold)
		s.waitIdle("alice", id)
	})

	It("forks a chat from a message, keeping the original whole", func() {
		var seqs []float64
		for _, m := range s.transcript("alice", id) {
			if m["role"] == "assistant" {
				seqs = append(seqs, m["seq"].(float64))
			}
		}
		rec := s.do("alice", http.MethodPost, "/api/chats/"+id+"/fork", map[string]any{"from_seq": int64(seqs[0]), "title": "Branch"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		fork := decodeJSON(rec)
		Expect(fork).To(HaveKeyWithValue("title", "Branch"))
		forkID := fork["id"].(string)
		Expect(forkID).NotTo(Equal(id))

		Expect(roleTexts(s.transcript("alice", forkID), "user")).To(Equal([]string{"first question"}))
		Expect(roleTexts(s.transcript("alice", id), "user")).To(Equal([]string{"first question", "second question"}))

		s.say("alice", forkID, "a different second question")
		Expect(userTexts(llm.last())).To(Equal([]string{"first question", "a different second question"}))
		Expect(s.do("bob", http.MethodPost, "/api/chats/"+id+"/fork", nil).Code).To(Equal(http.StatusNotFound))

		whole := s.do("alice", http.MethodPost, "/api/chats/"+id+"/fork", nil)
		Expect(whole.Code).To(Equal(http.StatusCreated))
		Expect(decodeJSON(whole)["title"]).To(HaveSuffix("(fork)"))
	})

	It("finds chats by title and by transcript, only the user's own", func() {
		other := s.create("alice")
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+other, map[string]any{"title": "Zebra planning"}).Code).To(Equal(http.StatusOK))
		bob := s.create("bob")
		s.say("bob", bob, "second question of bob")

		byText := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/search?q=SECOND%20quest", nil))["hits"].([]any)
		Expect(byText).To(HaveLen(1))
		hit := byText[0].(map[string]any)
		Expect(hit["chat"].(map[string]any)["id"]).To(Equal(id))
		Expect(hit["snippet"]).To(ContainSubstring("second question"))
		Expect(hit["seq"]).To(BeNumerically(">", 0))

		byTitle := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/search?q=zebra", nil))["hits"].([]any)
		Expect(byTitle).To(HaveLen(1))
		Expect(byTitle[0].(map[string]any)["in_title"]).To(BeTrue())

		Expect(s.do("alice", http.MethodGet, "/api/chats/search", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/search?q=nothing-matches", nil))["hits"]).To(BeEmpty())
	})

	It("exports a chat as Markdown and JSON, and imports the JSON as a new chat the model can continue", func() {
		md := s.do("alice", http.MethodGet, "/api/chats/"+id+"/export", nil)
		Expect(md.Code).To(Equal(http.StatusOK))
		Expect(md.Header().Get("Content-Type")).To(HavePrefix("text/markdown"))
		Expect(md.Header().Get("Content-Disposition")).To(ContainSubstring("attachment"))
		Expect(md.Body.String()).To(And(ContainSubstring("## You"), ContainSubstring("first question"), ContainSubstring("## Assistant"), ContainSubstring("Echo: second question")))

		js := s.do("alice", http.MethodGet, "/api/chats/"+id+"/export?format=json", nil)
		Expect(js.Code).To(Equal(http.StatusOK))
		exported := decodeJSON(js)
		Expect(exported).To(HaveKeyWithValue("version", BeNumerically("==", 1)))
		Expect(s.do("alice", http.MethodGet, "/api/chats/"+id+"/export?format=pdf", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("bob", http.MethodGet, "/api/chats/"+id+"/export", nil).Code).To(Equal(http.StatusNotFound))

		imp := s.do("bob", http.MethodPost, "/api/chats/import", map[string]any{"chat": exported})
		Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
		newID := decodeJSON(imp)["id"].(string)
		Expect(roleTexts(s.transcript("bob", newID), "user")).To(Equal([]string{"first question", "second question"}))

		s.say("bob", newID, "third question")
		Expect(userTexts(llm.last())).To(Equal([]string{"first question", "second question", "third question"}))

		bad := map[string]any{"chat": map[string]any{"version": 2, "model": "test-model"}}
		Expect(s.do("bob", http.MethodPost, "/api/chats/import", bad).Code).To(Equal(http.StatusBadRequest))
		noModel := map[string]any{"chat": map[string]any{"version": 1}}
		Expect(s.do("bob", http.MethodPost, "/api/chats/import", noModel).Code).To(Equal(http.StatusBadRequest))
		unknown := map[string]any{"chat": exported, "model": "nope"}
		Expect(s.do("bob", http.MethodPost, "/api/chats/import", unknown).Code).To(Equal(http.StatusBadRequest))
	})
})
