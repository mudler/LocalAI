package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// offerLLM records the tools of every chat completion it receives and answers
// with plain text, so the run ends after the first model turn.
type offerLLM struct {
	mu      sync.Mutex
	offered []string
}

func (m *offerLLM) Ask(_ context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f.AddMessage(cogito.AssistantMessageRole, "done"), nil
}

func (m *offerLLM) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	m.mu.Lock()
	for _, t := range req.Tools {
		if t.Function != nil {
			m.offered = append(m.offered, t.Function.Name)
		}
	}
	m.mu.Unlock()
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "done"}}},
	}}, cogito.LLMUsage{}, nil
}

var _ = Describe("mcpJobSessionOption", func() {
	It("does not offer a session tool of the add-on namespace to the job model", func() {
		srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v0.0.1"}, nil)
		for _, name := range []string{"echo", "addon__a__t"} {
			srv.AddTool(&mcp.Tool{
				Name:        name,
				Description: "fixture tool " + name,
				InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
			})
		}
		ts := httptest.NewServer(mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil))
		DeferCleanup(ts.Close)

		ctx := context.Background()
		client := mcp.NewClient(&mcp.Implementation{Name: "job", Version: "v0.0.1"}, nil)
		session, err := client.Connect(ctx, &mcp.SSEClientTransport{Endpoint: ts.URL}, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = session.Close() })

		llm := &offerLLM{}
		fragment := cogito.NewEmptyFragment().AddMessage("user", "hi")
		_, err = cogito.ExecuteTools(llm, fragment, cogito.WithContext(ctx), mcpJobSessionOption([]*mcp.ClientSession{session}))
		_ = err // a text-only answer may end the run with "no tool"; the offer is what is asserted

		llm.mu.Lock()
		defer llm.mu.Unlock()
		offered := append([]string(nil), llm.offered...)
		sort.Strings(offered)
		Expect(offered).To(ContainElement("echo"), "the fixture session must reach the model")
		Expect(offered).NotTo(ContainElement("addon__a__t"))
	})
})
