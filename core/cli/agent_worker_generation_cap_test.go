package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("handleMCPCIJob generation cap", func() {
	var (
		mcpFixture *httptest.Server
		llmAPI     *httptest.Server
		mu         sync.Mutex
		sentCaps   []int
	)

	BeforeEach(func() {
		srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v0.0.1"}, nil)
		srv.AddTool(&mcp.Tool{
			Name:        "noop",
			Description: "fixture tool",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
		mcpFixture = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))

		// The LLM API records the output cap of every completion the job sends
		// and then fails it: what this spec pins is the request, not the run.
		sentCaps = nil
		llmAPI = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				MaxTokens int `json:"max_tokens"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				mu.Lock()
				sentCaps = append(sentCaps, body.MaxTokens)
				mu.Unlock()
			}
			http.Error(w, "fixture", http.StatusBadRequest)
		}))
	})

	AfterEach(func() {
		mcpTools.CloseMCPSessions("capped")
		mcpTools.CloseMCPSessions("uncapped")
		llmAPI.Close()
		mcpFixture.Close()
	})

	runJob := func(name, agentYAML string) []int {
		var cfg config.ModelConfig
		Expect(yaml.Unmarshal([]byte(fmt.Sprintf("name: %s\n%smcp:\n  remote: |\n    {\"mcpServers\": {\"fixture\": {\"url\": %q}}}\n", name, agentYAML, mcpFixture.URL)), &cfg)).To(Succeed())

		payload, err := json.Marshal(jobs.JobEvent{
			JobID:       "job-" + name,
			TaskID:      "task-" + name,
			Job:         &jobs.JobRecord{ID: "job-" + name},
			Task:        &jobs.TaskRecord{ID: "task-" + name, Model: name, Prompt: "do it"},
			ModelConfig: &cfg,
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(handleMCPCIJob(context.Background(), payload, llmAPI.URL, "", testutil.NewFakeBus(), 2*time.Second)).To(Succeed())

		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), sentCaps...)
	}

	It("sends agent.max_generation_tokens as the output cap of the job's completions", func() {
		caps := runJob("capped", "agent:\n  max_generation_tokens: 1234\n")
		Expect(caps).ToNot(BeEmpty())
		Expect(caps).To(HaveEach(1234))
	})

	It("leaves the client default in place when the model sets no cap", func() {
		caps := runJob("uncapped", "")
		Expect(caps).ToNot(BeEmpty())
		Expect(caps).ToNot(ContainElement(1234))
		Expect(caps).To(HaveEach(BeNumerically(">", 0)))
	})
})
