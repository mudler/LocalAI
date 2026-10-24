package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// backendCall is what one model call received from the chat endpoint.
type backendCall struct {
	toolChoice string
	grammar    string
}

// The specs run the real ChatEndpoint against a recording backend stub and a
// profile alias resolved by the real loader, so they see the tool_choice and
// grammar every model call of the MCP loop is made with.
var _ = Describe("ChatEndpoint profile require_tool", func() {
	var origInference modelInferenceFunc

	BeforeEach(func() { origInference = backend.ModelInferenceFunc })
	AfterEach(func() { backend.ModelInferenceFunc = origInference })

	// recordBackend answers the n-th call with replies[n] (the last one
	// repeats) and records what each call was made with.
	recordBackend := func(replies ...backend.LLMResponse) *[]backendCall {
		calls := &[]backendCall{}
		backend.ModelInferenceFunc = func(
			ctx context.Context, s string, messages schema.Messages,
			images, videos, audios []string,
			loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader,
			o *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool,
			tools, toolChoice string,
			logprobs, topLogprobs *int,
			logitBias map[string]float64,
			metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			reply := replies[min(len(*calls), len(replies)-1)]
			*calls = append(*calls, backendCall{toolChoice: toolChoice, grammar: c.Grammar})
			return func() (backend.LLMResponse, error) {
				if tokenCallback != nil && reply.Response != "" {
					tokenCallback(reply.Response, backend.TokenUsage{})
				}
				return reply, nil
			}, nil
		}
		return calls
	}

	toolCall := backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Id: "call-1", Name: "lookup", Arguments: "{}"}}}}}
	answer := backend.LLMResponse{Response: "Done.", ChatDeltas: []*pb.ChatDelta{{Content: "Done."}}}

	// resolve writes a target and a profile alias with the fixture MCP server
	// and returns the alias resolved the way the request middleware does.
	resolve := func(profile, mcpURL string, tokenizerTemplate bool) (*config.ModelConfig, string) {
		dir := GinkgoT().TempDir()
		name := "require-tool-" + uuid.NewString()
		target := "name: " + name + "-base\nbackend: llama-cpp\nparameters:\n  model: base.gguf\n"
		if tokenizerTemplate {
			target += "template:\n  use_tokenizer_template: true\n"
		}
		alias := "name: " + name + "\nalias: " + name + "-base\nprofile:\n" + profile
		if mcpURL != "" {
			alias += "  mcp:\n    auto: true\n    remote: |\n      {\"mcpServers\": {\"fixture\": {\"url\": \"" + mcpURL + "\"}}}\n"
		}
		Expect(os.WriteFile(filepath.Join(dir, "base.yaml"), []byte(target), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "alias.yaml"), []byte(alias), 0644)).To(Succeed())
		cl := config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())
		cfg, ok := cl.GetModelConfig(name)
		Expect(ok).To(BeTrue())
		resolved, _, err := cl.ResolveAlias(&cfg)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { mcpTools.CloseMCPSessions(resolved.MCPSessionKey()) })
		return resolved, dir
	}

	const requireTool = "  require_tool: true\n"
	const plainProfile = "  system_prompt: plain persona\n"

	type request struct {
		stream     bool
		toolChoice any
		messages   []schema.Message
	}

	run := func(cfg *config.ModelConfig, dir string, r request) *httptest.ResponseRecorder {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		messages := r.messages
		if messages == nil {
			messages = userMessages("Is this a good olive oil?")
		}
		input := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "alias"}},
			Messages:          messages,
			Stream:            r.stream,
			ToolsChoice:       r.toolChoice,
			Context:           ctx,
			Cancel:            cancel,
		}
		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")), rec)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
		Expect(ChatEndpoint(nil, nil, templates.NewEvaluator(dir), config.NewApplicationConfig(), nil, nil, nil)(c)).To(Succeed())
		return rec
	}

	choices := func(calls *[]backendCall) []string {
		var out []string
		for _, c := range *calls {
			out = append(out, c.toolChoice)
		}
		return out
	}

	for _, stream := range []bool{false, true} {
		Context(map[bool]string{false: "non-streaming", true: "streaming"}[stream], func() {
			It("forces a tool on the first call and releases it on every follow-up call", func() {
				fx := newMCPAutoFixture()
				calls := recordBackend(toolCall, toolCall, answer)
				cfg, dir := resolve(requireTool, fx.url, true)
				run(cfg, dir, request{stream: stream})
				Expect(choices(calls)).To(Equal([]string{`"required"`, "", ""}))
				Expect(fx.requests.Load()).To(BeNumerically(">", 0))
			})

			It("hands the client's own tool_choice back on the follow-up calls", func() {
				fx := newMCPAutoFixture()
				calls := recordBackend(toolCall, answer)
				cfg, dir := resolve(requireTool, fx.url, true)
				run(cfg, dir, request{stream: stream, toolChoice: "auto"})
				Expect(choices(calls)).To(Equal([]string{`"required"`, `"auto"`}))
			})

			It("leaves tool_choice alone without the switch", func() {
				fx := newMCPAutoFixture()
				calls := recordBackend(toolCall, answer)
				cfg, dir := resolve(plainProfile, fx.url, true)
				run(cfg, dir, request{stream: stream})
				Expect(choices(calls)).To(Equal([]string{"", ""}))
			})
		})
	}

	It("does not force a tool when the profile offers no server-side tools", func() {
		calls := recordBackend(answer)
		cfg, dir := resolve(requireTool, "", true)
		run(cfg, dir, request{})
		Expect(choices(calls)).To(Equal([]string{""}))
	})

	It("keeps a tool_choice the client set explicitly", func() {
		fx := newMCPAutoFixture()
		calls := recordBackend(answer)
		cfg, dir := resolve(requireTool, fx.url, true)
		run(cfg, dir, request{toolChoice: "none"})
		Expect(choices(calls)).To(Equal([]string{`"none"`}))
	})

	// A request that ends with a tool result is the client returning the
	// result of a tool it ran itself. Forcing another call there would loop
	// between client and server.
	It("does not force a tool on a request that returns a client tool result", func() {
		fx := newMCPAutoFixture()
		calls := recordBackend(answer)
		cfg, dir := resolve(requireTool, fx.url, true)
		run(cfg, dir, request{messages: []schema.Message{
			{Role: "user", Content: "Look it up.", StringContent: "Look it up."},
			{Role: "assistant", ToolCalls: []schema.ToolCall{{ID: "c1", Type: "function", FunctionCall: schema.FunctionCall{Name: "client_tool", Arguments: "{}"}}}},
			{Role: "tool", Content: "result", StringContent: "result", ToolCallID: "c1"},
		}})
		Expect(choices(calls)).To(Equal([]string{""}))
	})

	// Models without a tokenizer template get their tool-call grammar from
	// LocalAI. There "required" means: the grammar leaves out the no-action
	// function the model would otherwise answer with directly.
	It("drops the no-action function from the first call's grammar only", func() {
		fx := newMCPAutoFixture()
		calls := recordBackend(toolCall, answer)
		cfg, dir := resolve(requireTool, fx.url, false)
		run(cfg, dir, request{})
		Expect(*calls).To(HaveLen(2))
		Expect((*calls)[0].grammar).To(ContainSubstring("lookup"))
		Expect((*calls)[0].grammar).NotTo(ContainSubstring("answer"))
		Expect((*calls)[1].grammar).To(ContainSubstring("lookup"))
		Expect((*calls)[1].grammar).To(ContainSubstring("answer"))
	})

	It("keeps the no-action function in the grammar without the switch", func() {
		fx := newMCPAutoFixture()
		calls := recordBackend(toolCall, answer)
		cfg, dir := resolve(plainProfile, fx.url, false)
		run(cfg, dir, request{})
		Expect(*calls).To(HaveLen(2))
		Expect((*calls)[0].grammar).To(ContainSubstring("answer"))
	})
})
