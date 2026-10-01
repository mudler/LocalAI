package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mcpFixture serves an MCP server over SSE whose tools return fixed results,
// so the executor reaches it through the same transport as a real agent.
type mcpFixture struct {
	server *httptest.Server
	calls  map[string]*atomic.Int32
}

func newMCPFixture(results map[string]string) *mcpFixture {
	srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v0.0.1"}, nil)
	fx := &mcpFixture{calls: map[string]*atomic.Int32{}}
	for name, result := range results {
		counter := &atomic.Int32{}
		fx.calls[name] = counter
		srv.AddTool(&mcp.Tool{
			Name:        name,
			Description: "fixture tool " + name,
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			counter.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: result}}}, nil
		})
	}
	fx.server = httptest.NewServer(mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	return fx
}

func (fx *mcpFixture) close()                   { fx.server.Close() }
func (fx *mcpFixture) callCount(n string) int32 { return fx.calls[n].Load() }

// policyLLM answers each chat completion through respond (plain text answer
// when respond is nil) and records every
// request, so specs can see which tools were offered and which messages the
// executor added.
type policyLLM struct {
	mu       sync.Mutex
	requests []openai.ChatCompletionRequest
	asked    [][]openai.ChatCompletionMessage
	respond  func(req openai.ChatCompletionRequest) openai.ChatCompletionMessage
	answer   string
}

func (m *policyLLM) Ask(_ context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	m.mu.Lock()
	m.asked = append(m.asked, append([]openai.ChatCompletionMessage(nil), f.Messages...))
	m.mu.Unlock()
	return f.AddMessage(cogito.AssistantMessageRole, m.answer), nil
}

func (m *policyLLM) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	msg := openai.ChatCompletionMessage{Role: "assistant", Content: m.answer}
	if m.respond != nil {
		msg = m.respond(req)
	}
	return cogito.LLMReply{
		ChatCompletionResponse: openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{Message: msg}},
		},
	}, cogito.LLMUsage{}, nil
}

// offeredTools returns the sorted tool names of the first request that
// offered tools to the model.
func (m *policyLLM) offeredTools() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, req := range m.requests {
		if len(req.Tools) == 0 {
			continue
		}
		names := []string{}
		for _, t := range req.Tools {
			if t.Function != nil {
				names = append(names, t.Function.Name)
			}
		}
		sort.Strings(names)
		return names
	}
	return nil
}

// nudges counts the user messages carrying prompt in the longest conversation
// the model saw, which is the number of times the gate sent it.
func (m *policyLLM) nudges(prompt string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	best := 0
	count := func(msgs []openai.ChatCompletionMessage) {
		n := 0
		for _, msg := range msgs {
			if msg.Role == "user" && msg.Content == prompt {
				n++
			}
		}
		if n > best {
			best = n
		}
	}
	for _, req := range m.requests {
		count(req.Messages)
	}
	for _, msgs := range m.asked {
		count(msgs)
	}
	return best
}

func toolCallMessage(name string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role: "assistant",
		ToolCalls: []openai.ToolCall{{
			ID:       "call-" + name,
			Type:     openai.ToolTypeFunction,
			Function: openai.FunctionCall{Name: name, Arguments: `{}`},
		}},
	}
}

func lastMessage(req openai.ChatCompletionRequest) openai.ChatCompletionMessage {
	if len(req.Messages) == 0 {
		return openai.ChatCompletionMessage{}
	}
	return req.Messages[len(req.Messages)-1]
}

var _ = Describe("tool policy settings", func() {
	Describe("config parsing", func() {
		It("accepts the tool lists as a comma or newline separated string", func() {
			var cfg AgentConfig
			Expect(ParseConfigJSON(`{"allowed_tools":"a, b\nc,,","excluded_tools":" d \r\n"}`, &cfg)).To(Succeed())
			Expect([]string(cfg.AllowedTools)).To(Equal([]string{"a", "b", "c"}))
			Expect([]string(cfg.ExcludedTools)).To(Equal([]string{"d"}))
		})

		It("accepts the tool lists as a JSON array", func() {
			var cfg AgentConfig
			Expect(ParseConfigJSON(`{"allowed_tools":["a"," b ",""],"excluded_tools":null}`, &cfg)).To(Succeed())
			Expect([]string(cfg.AllowedTools)).To(Equal([]string{"a", "b"}))
			Expect(cfg.ExcludedTools).To(BeEmpty())
		})

		It("rejects a list with non-string entries", func() {
			var cfg AgentConfig
			Expect(ParseConfigJSON(`{"allowed_tools":[1]}`, &cfg)).ToNot(Succeed())
		})

		It("keeps every setting when the config is stored through LocalAGI's config", func() {
			// The REST handlers decode into state.AgentConfig and store its JSON;
			// the distributed dispatcher decodes that JSON into AgentConfig.
			var in state.AgentConfig
			Expect(json.Unmarshal([]byte(`{
				"name": "a",
				"allowed_tools": "search, check_policy",
				"excluded_tools": ["add_memory"],
				"required_tool_before_finish": "check_policy",
				"required_tool_before_finish_prompt": "run it",
				"required_tool_before_finish_attempts": 4
			}`), &in)).To(Succeed())
			stored, err := json.Marshal(in)
			Expect(err).ToNot(HaveOccurred())

			var out AgentConfig
			Expect(ParseConfigJSON(string(stored), &out)).To(Succeed())
			Expect([]string(out.AllowedTools)).To(Equal([]string{"search", "check_policy"}))
			Expect([]string(out.ExcludedTools)).To(Equal([]string{"add_memory"}))
			Expect(out.RequiredToolBeforeFinish).To(Equal("check_policy"))
			Expect(out.RequiredToolBeforeFinishPrompt).To(Equal("run it"))
			Expect(out.RequiredToolBeforeFinishAttempts).To(Equal(4))

			again, err := json.Marshal(out)
			Expect(err).ToNot(HaveOccurred())
			var back state.AgentConfig
			Expect(json.Unmarshal(again, &back)).To(Succeed())
			Expect(back.AllowedTools).To(Equal([]string{"search", "check_policy"}))
			Expect(back.RequiredToolBeforeFinishAttempts).To(Equal(4))
		})
	})

	Describe("config meta", func() {
		It("describes the settings exactly like LocalAGI does", func() {
			upstream := map[string]ConfigField{}
			for _, f := range state.NewAgentConfigMeta(nil, nil, nil, nil).Fields {
				upstream[f.Name] = ConfigField{
					Name: f.Name, Type: string(f.Type), Label: f.Label, DefaultValue: f.DefaultValue,
					Placeholder: f.Placeholder, HelpText: f.HelpText, Min: f.Min, Max: f.Max, Step: f.Step,
					Tags: ConfigFieldTags{Section: f.Tags.Section},
				}
			}
			local := map[string]ConfigField{}
			for _, f := range DefaultConfigMeta().Fields {
				local[f.Name] = f
			}
			for _, name := range []string{
				"allowed_tools", "excluded_tools",
				"required_tool_before_finish", "required_tool_before_finish_prompt", "required_tool_before_finish_attempts",
			} {
				Expect(upstream).To(HaveKey(name))
				Expect(local).To(HaveKeyWithValue(name, upstream[name]), name)
			}
		})
	})

	Describe("tool filter", func() {
		It("keeps the control actions even when they are excluded or not allowed", func() {
			f := newToolFilter([]string{"search"}, []string{"send_message", "stop", "update_state", "search"})
			for _, name := range []string{"send_message", "stop", "update_state"} {
				Expect(f.allows(name)).To(BeTrue(), name)
			}
			Expect(f.allows("search")).To(BeFalse())
			Expect(f.allows("other")).To(BeFalse())
		})
	})

	Describe("ExecuteChatWithLLM", func() {
		var fx *mcpFixture

		BeforeEach(func() {
			fx = newMCPFixture(map[string]string{
				"check_policy": `{"ok":true}`,
				"mcp_allowed":  "allowed result",
				"mcp_blocked":  "blocked result",
			})
		})

		AfterEach(func() { fx.close() })

		baseConfig := func() *AgentConfig {
			return &AgentConfig{
				Name:                "policy-agent",
				Model:               "test-model",
				MCPServers:          []MCPServer{{URL: fx.server.URL}},
				EnableKnowledgeBase: true,
				KBMode:              KBModeTools,
			}
		}

		Context("with allowed and excluded tools", func() {
			It("offers the model only the allowed tools that are not excluded, MCP tools included", func() {
				llm := &policyLLM{answer: "final"}
				cfg := baseConfig()
				cfg.AllowedTools = ToolNames{"mcp_allowed", "search_memory", "add_memory"}
				cfg.ExcludedTools = ToolNames{"add_memory"}

				_, err := ExecuteChatWithLLM(context.Background(), llm, cfg, "hi", Callbacks{})
				Expect(err).ToNot(HaveOccurred())
				Expect(llm.offeredTools()).To(Equal([]string{"mcp_allowed", "search_memory"}))
			})

			It("offers every tool when no list is set", func() {
				llm := &policyLLM{answer: "final"}
				_, err := ExecuteChatWithLLM(context.Background(), llm, baseConfig(), "hi", Callbacks{})
				Expect(err).ToNot(HaveOccurred())
				Expect(llm.offeredTools()).To(Equal([]string{"add_memory", "check_policy", "mcp_allowed", "mcp_blocked", "search_memory"}))
			})

			It("does not run a filtered MCP tool the model calls anyway", func() {
				var calls atomic.Int32
				llm := &policyLLM{answer: "final", respond: func(openai.ChatCompletionRequest) openai.ChatCompletionMessage {
					if calls.Add(1) == 1 {
						return toolCallMessage("mcp_blocked")
					}
					return openai.ChatCompletionMessage{Role: "assistant", Content: "done"}
				}}
				cfg := baseConfig()
				cfg.ExcludedTools = ToolNames{"mcp_blocked"}

				_, _ = ExecuteChatWithLLM(context.Background(), llm, cfg, "hi", Callbacks{})
				Expect(fx.callCount("mcp_blocked")).To(BeZero())
			})
		})

		Context("with a required tool before finish", func() {
			const prompt = "RUN check_policy NOW"

			It("nudges the model until the required tool passes, then returns its answer", func() {
				llm := &policyLLM{answer: "final answer", respond: func(req openai.ChatCompletionRequest) openai.ChatCompletionMessage {
					if last := lastMessage(req); last.Role == "user" && last.Content == prompt {
						return toolCallMessage("check_policy")
					}
					return openai.ChatCompletionMessage{Role: "assistant", Content: "final answer"}
				}}
				cfg := baseConfig()
				cfg.RequiredToolBeforeFinish = "check_policy"
				cfg.RequiredToolBeforeFinishPrompt = prompt

				result, err := ExecuteChatWithLLM(context.Background(), llm, cfg, "hi", Callbacks{})
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal("final answer"))
				Expect(fx.callCount("check_policy")).To(Equal(int32(1)))
				Expect(llm.nudges(prompt)).To(Equal(1))
			})

			It("lets the answer through after the configured number of reminders", func() {
				llm := &policyLLM{answer: "stubborn answer"}
				cfg := baseConfig()
				cfg.RequiredToolBeforeFinish = "check_policy"
				cfg.RequiredToolBeforeFinishPrompt = prompt
				cfg.RequiredToolBeforeFinishAttempts = 2

				result, err := ExecuteChatWithLLM(context.Background(), llm, cfg, "hi", Callbacks{})
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal("stubborn answer"))
				Expect(llm.nudges(prompt)).To(Equal(2))
				Expect(fx.callCount("check_policy")).To(BeZero())
			})

			It("uses three reminders and a prompt naming the tool by default", func() {
				llm := &policyLLM{answer: "stubborn answer"}
				cfg := baseConfig()
				cfg.RequiredToolBeforeFinish = "check_policy"

				_, err := ExecuteChatWithLLM(context.Background(), llm, cfg, "hi", Callbacks{})
				Expect(err).ToNot(HaveOccurred())
				Expect(llm.nudges(requiredFinishPromptFor("check_policy", ""))).To(Equal(3))
				Expect(requiredFinishPromptFor("check_policy", "")).To(ContainSubstring("check_policy"))
			})

			It("keeps nudging when the required tool fails", func() {
				fx.close()
				fx = newMCPFixture(map[string]string{"check_policy": `{"ok":false}`})
				llm := &policyLLM{answer: "final answer", respond: func(req openai.ChatCompletionRequest) openai.ChatCompletionMessage {
					if last := lastMessage(req); last.Role == "user" && last.Content == prompt {
						return toolCallMessage("check_policy")
					}
					return openai.ChatCompletionMessage{Role: "assistant", Content: "final answer"}
				}}
				cfg := baseConfig()
				cfg.RequiredToolBeforeFinish = "check_policy"
				cfg.RequiredToolBeforeFinishPrompt = prompt
				cfg.RequiredToolBeforeFinishAttempts = 2

				_, err := ExecuteChatWithLLM(context.Background(), llm, cfg, "hi", Callbacks{})
				Expect(err).ToNot(HaveOccurred())
				Expect(fx.callCount("check_policy")).To(Equal(int32(2)))
				Expect(llm.nudges(prompt)).To(Equal(2))
			})

			It("does nothing when the agent does not have the required tool", func() {
				llm := &policyLLM{answer: "final"}
				cfg := baseConfig()
				cfg.RequiredToolBeforeFinish = "check_policy"
				cfg.RequiredToolBeforeFinishPrompt = prompt
				cfg.ExcludedTools = ToolNames{"check_policy"}

				result, err := ExecuteChatWithLLM(context.Background(), llm, cfg, "hi", Callbacks{})
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal("final"))
				Expect(llm.nudges(prompt)).To(BeZero())
			})
		})
	})
})

var _ = DescribeTable("requiredToolResultOK",
	func(result string, want bool) {
		Expect(requiredToolResultOK(result)).To(Equal(want))
	},
	Entry("top-level ok true", `{"ok":true}`, true),
	Entry("top-level ok false", `{"ok":false}`, false),
	Entry("ok as a string", `{"ok":"true"}`, false),
	Entry("ok nested in another object", `{"data":{"ok":true}}`, false),
	Entry("object embedded in text", `result: {"ok": true, "n": 1} done`, true),
	Entry("plain text", `"ok": true`, false),
)
