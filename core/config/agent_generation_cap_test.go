package config_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/mudler/cogito/clients"
	"github.com/sashabaranov/go-openai"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent.max_generation_tokens", func() {
	var (
		srv  *httptest.Server
		mu   sync.Mutex
		caps []int
	)

	BeforeEach(func() {
		caps = nil
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				MaxTokens int `json:"max_tokens"`
			}
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			mu.Lock()
			caps = append(caps, body.MaxTokens)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`))
		}))
		DeferCleanup(srv.Close)
	})

	// agentLLM builds the client the way the agent job paths do: the model
	// config comes from YAML, the client points at the LocalAI API, and the
	// config's cap is applied before the loop runs.
	agentLLM := func(modelYAML string) *clients.LocalAIClient {
		var cfg config.ModelConfig
		Expect(yaml.Unmarshal([]byte(modelYAML), &cfg)).To(Succeed())
		llm := clients.NewLocalAILLM(cfg.Name, "", srv.URL)
		cfg.ApplyAgentGenerationCap(llm)
		return llm
	}

	sentCap := func(llm *clients.LocalAIClient, req openai.ChatCompletionRequest) int {
		req.Messages = []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}}
		_, _, err := llm.CreateChatCompletion(context.Background(), req)
		Expect(err).ToNot(HaveOccurred())
		mu.Lock()
		defer mu.Unlock()
		Expect(caps).ToNot(BeEmpty())
		return caps[len(caps)-1]
	}

	It("sends the configured cap on every agent completion", func() {
		llm := agentLLM("name: m\nagent:\n  max_generation_tokens: 2048\n")
		Expect(sentCap(llm, openai.ChatCompletionRequest{})).To(Equal(2048))
		Expect(sentCap(llm, openai.ChatCompletionRequest{})).To(Equal(2048))
	})

	It("keeps the client default when the model sets no cap", func() {
		bare := sentCap(clients.NewLocalAILLM("m", "", srv.URL), openai.ChatCompletionRequest{})
		Expect(bare).To(BeNumerically(">", 0))
		llm := agentLLM("name: m\n")
		Expect(sentCap(llm, openai.ChatCompletionRequest{})).To(Equal(bare))
	})

	It("lets a request that carries its own cap win, so cogito's length retry can raise it", func() {
		llm := agentLLM("name: m\nagent:\n  max_generation_tokens: 2048\n")
		Expect(sentCap(llm, openai.ChatCompletionRequest{MaxTokens: 4096})).To(Equal(4096))
	})
})
