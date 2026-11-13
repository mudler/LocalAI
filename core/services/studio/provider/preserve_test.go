package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/provider"
)

// bodyOf sends one request through a client with params and returns the JSON
// body the route got.
func bodyOf(params provider.Params) map[string]any {
	var (
		mu  sync.Mutex
		got map[string]any
	)
	p := provider.New()
	p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(raw, &got)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	}))
	_, _, err := p.LLMWith(nil, "m", params).CreateChatCompletion(context.Background(), userRequest("hello"))
	Expect(err).NotTo(HaveOccurred())
	mu.Lock()
	defer mu.Unlock()
	return got
}

var _ = Describe("PreserveThinking", func() {
	// A chat template that keeps the reasoning of the last turn only (the
	// Qwen 3 family) renders every earlier answer again without it when a new
	// turn starts, so the prompt changes from the first answer of the turn
	// before and the server evaluates everything after it again. A chat keeps
	// its whole history, so it asks the template to keep the reasoning.
	It("asks the chat template to keep the reasoning of earlier turns", func() {
		body := bodyOf(provider.Params{PreserveThinking: true})
		Expect(body).To(HaveKeyWithValue("chat_template_kwargs", HaveKeyWithValue("preserve_thinking", true)))
	})

	It("sends no template argument without it", func() {
		body := bodyOf(provider.Params{ReasoningEffort: "high"})
		Expect(body).NotTo(HaveKey("chat_template_kwargs"))
	})
})
