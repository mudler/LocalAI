package agentpool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/agentpool"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AgentJobService generation cap", func() {
	var (
		tempDir    string
		mcpFixture *httptest.Server
		llmAPI     *httptest.Server
		mu         sync.Mutex
		sentCaps   []int
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "agent_jobs_cap_test")
		Expect(err).NotTo(HaveOccurred())

		srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v0.0.1"}, nil)
		srv.AddTool(&mcp.Tool{
			Name:        "noop",
			Description: "fixture tool",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
		mcpFixture = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))

		// Stands in for the LocalAI API the job calls back into: it records the
		// output cap of every completion and fails it, since this spec pins the
		// request rather than the run.
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
		_ = os.RemoveAll(tempDir)
	})

	runJob := func(name, agentYAML string) []int {
		body := fmt.Sprintf("name: %s\n%smcp:\n  remote: |\n    {\"mcpServers\": {\"fixture\": {\"url\": %q}}}\n", name, agentYAML, mcpFixture.URL)
		Expect(os.WriteFile(filepath.Join(tempDir, name+".yaml"), []byte(body), 0644)).To(Succeed())

		systemState := &system.SystemState{}
		systemState.Model.ModelsPath = tempDir
		appConfig := config.NewApplicationConfig(
			config.WithDynamicConfigDir(tempDir),
			config.WithContext(context.Background()),
		)
		appConfig.SystemState = systemState
		u, err := url.Parse(llmAPI.URL)
		Expect(err).NotTo(HaveOccurred())
		appConfig.APIAddress = u.Host

		configLoader := config.NewModelConfigLoader(tempDir)
		Expect(configLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())
		service := agentpool.NewAgentJobService(appConfig, model.NewModelLoader(systemState), configLoader, templates.NewEvaluator(tempDir))

		task := schema.Task{ID: "task-" + name, Name: name, Model: name, Prompt: "do it", Enabled: true}
		job := schema.Job{ID: "job-" + name, TaskID: task.ID}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = service.ExecuteJobInternal(job, task, ctx)

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
