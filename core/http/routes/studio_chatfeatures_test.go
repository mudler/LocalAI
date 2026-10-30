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
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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
func newLLMStudioServer(llm *fakeLLM) *studioServer { return newLLMStudioServerWith(llm, nil) }

func newLLMStudioServerWith(llm *fakeLLM, mod func(*studio.Deps)) *studioServer {
	return newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
		p := provider.New()
		p.Transport().SetHandler(llm)
		d.Provider, d.Harness = p, nil
		if mod != nil {
			mod(d)
		}
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
	defer func() { _ = resp.Body.Close() }()
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

var _ = Describe("Chats sub-agent API", func() {
	It("lists, reads and stops the sub-agents of a chat, and announces them in the event stream", func() {
		never := make(chan struct{})
		DeferCleanup(func() { close(never) })
		h := &studiotest.ToolHarness{Script: func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"dig deeper","wait":false}`}}
			}
			return []studiotest.ToolStep{{Block: never}}
		}}
		s := newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			d.Harness = h
			d.HostTools = func(context.Context, workspace.Actor) ([]session.Tool, error) {
				return []session.Tool{{Name: "search", Description: "search", Schema: json.RawMessage(`{"type":"object"}`),
					Run: func(context.Context, json.RawMessage) (string, error) { return "found", nil }}}, nil
			}
		})
		id := s.create("alice")
		events := s.readEvents("alice", id, func() {
			defer GinkgoRecover()
			s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "go"})
		}, func(kind string) bool { return kind == "sub_agent_started" })
		started := events[len(events)-1]
		Expect(started["ids"]).To(HaveLen(1))
		child := started["ids"].([]any)[0].(string)
		s.waitIdle("alice", id)

		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/agents", nil))
		Expect(list).To(HaveKeyWithValue("background_limit", BeNumerically("==", 4)))
		Eventually(func() any {
			return decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/agents", nil))["running"]
		}, 20*time.Second).Should(BeNumerically("==", 1))
		runs := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/agents", nil))["sub_agents"].([]any)
		Expect(runs[0].(map[string]any)).To(And(HaveKeyWithValue("id", child), HaveKeyWithValue("running", true)))

		Eventually(func() []string {
			rec := s.do("alice", http.MethodGet, "/api/chats/"+id+"/agents/"+child+"/messages", nil)
			Expect(rec.Code).To(Equal(http.StatusOK))
			var texts []string
			for _, m := range decodeJSON(rec)["messages"].([]any) {
				texts = append(texts, m.(map[string]any)["text"].(string))
			}
			return texts
		}, 20*time.Second).Should(ContainElement("dig deeper"))

		// Only the owner, and only through the child's own parent.
		Expect(s.do("bob", http.MethodGet, "/api/chats/"+id+"/agents", nil).Code).To(Equal(http.StatusNotFound))
		other := s.create("alice")
		Expect(s.do("alice", http.MethodGet, "/api/chats/"+other+"/agents/"+child+"/messages", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+other+"/agents/"+child+"/stop", nil).Code).To(Equal(http.StatusNotFound))

		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/agents/"+child+"/stop", nil).Code).To(Equal(http.StatusOK))
		Eventually(func() any {
			runs := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/agents", nil))["sub_agents"].([]any)
			return runs[0].(map[string]any)["last_turn"]
		}, 20*time.Second).Should(Equal("stopped"))
	})
})

var _ = Describe("Chats trigger proposals in the chat", func() {
	It("makes a proposal record the chat stream carries, and lets the person edit, confirm or dismiss it", func() {
		h := &studiotest.ToolHarness{Script: func(_ workspace.Actor, text string) []studiotest.ToolStep {
			name := strings.TrimPrefix(text, "propose ")
			return []studiotest.ToolStep{{Tool: "propose_trigger", Args: fmt.Sprintf(`{"name":%q,"prompt":"Summarize the news","when":"daily 08:00","time_zone":"UTC"}`, name)}}
		}}
		s := newStudioServerWith(newTestFiles(), func(d *studio.Deps) { d.Harness = h })
		id := s.create("alice")
		events := s.readEvents("alice", id, func() {
			defer GinkgoRecover()
			s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "propose Morning news"})
		}, func(kind string) bool { return kind == "turn_end" })

		var proposalID string
		var sawCard bool
		for _, ev := range events {
			if ev["kind"] == "trigger_proposed" {
				Expect(ev["text"]).To(Equal("Morning news"))
				proposalID = ev["ids"].([]any)[0].(string)
			}
			if tool, ok := ev["tool"].(map[string]any); ok && ev["kind"] == "tool_end" {
				card := tool["card"].(map[string]any)
				Expect(card).To(HaveKeyWithValue("kind", "trigger_proposal"))
				sawCard = card["id"] == proposalID
			}
		}
		Expect(proposalID).NotTo(BeEmpty(), "no trigger_proposed event in %v", events)
		Expect(sawCard).To(BeTrue(), "tool_end carries no card for the proposal")

		// The card stays in the transcript, so a reload still shows the proposal.
		var cardID string
		for _, m := range s.transcript("alice", id) {
			if card, ok := m["card"].(map[string]any); ok && card["kind"] == "trigger_proposal" {
				cardID = card["id"].(string)
			}
		}
		Expect(cardID).To(Equal(proposalID))

		// It is a record of this chat, and nothing runs yet.
		other := s.create("alice")
		mine := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/proposals?chat="+id, nil))["triggers"].([]any)
		Expect(mine).To(HaveLen(1))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/proposals?chat="+other, nil))["triggers"]).To(BeEmpty())
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers", nil))["triggers"]).To(BeEmpty())

		// Edit: replaces the fields, stays a proposal.
		url := "/api/chats/triggers/proposals/" + proposalID
		Expect(s.do("bob", http.MethodPut, url, map[string]any{"name": "x"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodPut, url, map[string]any{"name": "", "prompt": "p", "kind": "schedule", "schedule": "daily 09:00"}).Code).To(Equal(http.StatusBadRequest))
		edit := s.do("alice", http.MethodPut, url, map[string]any{"name": "Evening news", "prompt": "Summarize the evening news", "kind": "schedule", "schedule": "daily 19:30", "time_zone": "UTC", "model": "test-model", "enabled": true})
		Expect(edit.Code).To(Equal(http.StatusOK), edit.Body.String())
		Expect(decodeJSON(edit)).To(And(HaveKeyWithValue("name", "Evening news"), HaveKeyWithValue("enabled", false)))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers", nil))["triggers"]).To(BeEmpty())
		Expect(decodeJSON(s.do("alice", http.MethodGet, url[:len(url)-len(proposalID)-1], nil))["triggers"]).To(HaveLen(1))

		confirm := s.do("alice", http.MethodPost, url+"/confirm", map[string]any{"time_zone": "UTC"})
		Expect(confirm.Code).To(Equal(http.StatusOK), confirm.Body.String())
		Expect(decodeJSON(confirm)).To(And(HaveKeyWithValue("name", "Evening news"), HaveKeyWithValue("enabled", true)))
		Expect(s.do("alice", http.MethodPut, url, map[string]any{"name": "x", "prompt": "p", "kind": "schedule", "schedule": "daily 09:00"}).Code).To(Equal(http.StatusNotFound))

		// The answer is kept: the receipt reads it back, and only its owner.
		got := s.do("alice", http.MethodGet, url, nil)
		Expect(got.Code).To(Equal(http.StatusOK), got.Body.String())
		Expect(decodeJSON(got)).To(And(HaveKeyWithValue("state", "accepted"), HaveKeyWithValue("can_undo", true), HaveKey("answered_at")))
		Expect(s.do("bob", http.MethodGet, url, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPost, url+"/undo", nil).Code).To(Equal(http.StatusNotFound))

		// Undo takes the trigger off the page and keeps the receipt.
		undo := s.do("alice", http.MethodPost, url+"/undo", nil)
		Expect(undo.Code).To(Equal(http.StatusOK), undo.Body.String())
		Expect(decodeJSON(undo)).To(HaveKeyWithValue("state", "undone"))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers", nil))["triggers"]).To(BeEmpty())
		Expect(s.do("alice", http.MethodPost, url+"/undo", nil).Code).To(Equal(http.StatusConflict))

		// Scheduling it again, then declining it, leaves a declined line.
		again := s.do("alice", http.MethodPost, url+"/reopen", nil)
		Expect(again.Code).To(Equal(http.StatusOK), again.Body.String())
		Expect(decodeJSON(again)).To(HaveKeyWithValue("state", "pending"))
		Expect(s.do("alice", http.MethodDelete, url, nil).Code).To(Equal(http.StatusNoContent))
		Expect(decodeJSON(s.do("alice", http.MethodGet, url, nil))).To(HaveKeyWithValue("state", "declined"))
	})
})

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	Expect(err).NotTo(HaveOccurred())
	return b
}

// swaggerPaths returns the path and method pairs of the committed swagger.json.
func swaggerPaths() map[string]bool {
	GinkgoHelper()
	raw, err := os.ReadFile("../../../swagger/swagger.json")
	Expect(err).NotTo(HaveOccurred())
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	Expect(json.Unmarshal(raw, &doc)).To(Succeed())
	out := map[string]bool{}
	for path, ops := range doc.Paths {
		for method := range ops {
			out[strings.ToUpper(method)+" "+path] = true
		}
	}
	return out
}

// featureOfChatsRoute is the feature that gates a path under /api/chats: the
// automation parts have their own, off by default; the rest is chats.
func featureOfChatsRoute(path string) string {
	for prefix, feature := range map[string]string{
		"/api/chats/agents":     auth.FeatureAgents,
		"/api/chats/triggers":   auth.FeatureTriggers,
		"/api/chats/connectors": auth.FeatureConnectors,
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return feature
		}
	}
	return auth.FeatureChats
}

var _ = Describe("Chats route contract", func() {
	// The change feed of the files (/api/workspace) is part of the chats surface.
	chatsSurface := func(p string) bool {
		return strings.HasPrefix(p, "/api/chats") || strings.HasPrefix(p, "/api/workspace")
	}
	It("gates and documents every route that is registered under /api/chats and /api/workspace", func() {
		s := newStudioServer()
		gated := map[string]string{}
		for _, entry := range auth.RouteFeatureRegistry {
			if chatsSurface(entry.Pattern) {
				gated[entry.Method+" "+entry.Pattern] = entry.Feature
			}
		}
		documented := swaggerPaths()
		param := regexp.MustCompile(`:([A-Za-z_]+)`)
		count := 0
		for _, r := range s.e.Routes() {
			if !chatsSurface(r.Path) {
				continue
			}
			// The administrator views have no feature: they need the admin role
			// (studio_admin_test.go checks that). They are still documented and
			// need a login.
			isAdmin := strings.HasPrefix(r.Path, "/api/chats/admin/")
			if !isAdmin {
				count++
			}
			if isAdmin {
				Expect(documented).To(HaveKey(r.Method+" "+param.ReplaceAllString(r.Path, "{$1}")), "an admin route missing from swagger.json; run make swagger")
				Expect(s.do("", r.Method, strings.NewReplacer(":id", "c1").Replace(r.Path), nil).Code).To(Equal(http.StatusUnauthorized), r.Method+" "+r.Path)
				continue
			}
			Expect(gated).To(HaveKey(r.Method+" "+r.Path), "a chats route with no entry in the feature registry would pass ungated")
			Expect(gated[r.Method+" "+r.Path]).To(Equal(featureOfChatsRoute(r.Path)), r.Path)
			Expect(documented).To(HaveKey(r.Method+" "+param.ReplaceAllString(r.Path, "{$1}")), "a chats route missing from swagger.json; run make swagger")
			// Every route needs a login, whatever the feature says.
			Expect(s.do("", r.Method, strings.NewReplacer(":id", "c1", ":pid", "p1", ":seq", "1", ":agent_id", "a1", ":layer", "personal", ":name", "n1", ":sender", "s1").Replace(r.Path), nil).Code).
				To(Equal(http.StatusUnauthorized), r.Method+" "+r.Path)
		}
		Expect(count).To(Equal(len(gated)), "the registry holds exactly the registered chats routes")
	})
})
