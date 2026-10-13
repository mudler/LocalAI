package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	compressionservice "github.com/mudler/LocalAI/core/services/compression"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// errStopAfterTools ends the chat handler right after the MCP block: context
// compression runs after tool injection and before any backend is touched, so
// the specs can read the final tool list without loading a model.
var errStopAfterTools = errors.New("stop after tool injection")

type stopCompressor struct{}

func (stopCompressor) Transform(context.Context, config.CompressionConfig, int, string, []schema.Message) ([]schema.Message, *compressionservice.Metadata, error) {
	return nil, nil, errStopAfterTools
}

// mcpAutoFixture is a remote MCP server with one tool that counts every HTTP
// request it receives, so a spec can prove no connection was attempted.
type mcpAutoFixture struct {
	url      string
	requests atomic.Int32
}

func newMCPAutoFixture() *mcpAutoFixture {
	fx := &mcpAutoFixture{}
	srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v0.0.1"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:        "lookup",
		Description: "fixture tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.requests.Add(1)
		handler.ServeHTTP(w, r)
	}))
	DeferCleanup(server.Close)
	fx.url = server.URL
	return fx
}

// chat sends a plain chat request (no MCP metadata) through ChatEndpoint to a
// model with mcp.auto and returns the tool names that reached the backend stage.
func (fx *mcpAutoFixture) chat(appConfig *config.ApplicationConfig, db *gorm.DB, user *auth.User) []string {
	modelName := "mcp-auto-gate-" + uuid.NewString()
	DeferCleanup(func() { mcpTools.CloseMCPSessions(modelName) })

	cfg := &config.ModelConfig{Name: modelName}
	cfg.MCP.Servers = `{"mcpServers": {"fixture": {"url": "` + fx.url + `"}}}`
	cfg.MCP.Auto = true
	cfg.Compression.Enabled = true

	input := &schema.OpenAIRequest{
		PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: modelName}},
		Messages:          []schema.Message{{Role: "user", Content: "hi", StringContent: "hi"}},
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	c := e.NewContext(req, httptest.NewRecorder())
	c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
	c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
	if user != nil {
		c.Set("auth_user", user)
	}

	var dbs []*gorm.DB
	if db != nil {
		dbs = append(dbs, db)
	}
	handler := ChatEndpoint(nil, nil, nil, appConfig, nil, nil, stopCompressor{}, dbs...)
	Expect(handler(c)).To(MatchError(ContainSubstring(errStopAfterTools.Error())))

	var names []string
	for _, t := range input.Tools {
		names = append(names, t.Function.Name)
	}
	return names
}

// mcp.auto turns a plain chat request into a server-side MCP request, so it
// must honour the instance switch the dedicated MCP routes honour.
var _ = Describe("ChatEndpoint mcp.auto instance gate", func() {
	It("enables the model's servers when MCP is enabled", func() {
		fx := newMCPAutoFixture()
		Expect(fx.chat(&config.ApplicationConfig{}, nil, nil)).To(ContainElement("lookup"))
		Expect(fx.requests.Load()).To(BeNumerically(">", 0))
	})

	It("stays a plain chat when MCP is disabled on the instance", func() {
		fx := newMCPAutoFixture()
		Expect(fx.chat(&config.ApplicationConfig{DisableMCP: true}, nil, nil)).To(BeEmpty())
		Expect(fx.requests.Load()).To(BeZero(), "no MCP server may be contacted")
	})
})
