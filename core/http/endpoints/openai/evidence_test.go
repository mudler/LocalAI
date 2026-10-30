package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func userMessages(texts ...string) []schema.Message {
	var msgs []schema.Message
	for _, t := range texts {
		msgs = append(msgs, schema.Message{Role: "user", Content: t, StringContent: t})
	}
	return msgs
}

var _ = Describe("hasUnsupportedNumber", func() {
	evidence := func(texts ...string) map[string]struct{} {
		return evidenceNumbers("", userMessages(texts...))
	}

	It("flags a number that occurs in no input message", func() {
		Expect(hasUnsupportedNumber("There are 0 open invoices.", evidence("How many open invoices are there?"), false)).To(BeTrue())
	})

	It("does not flag a number the request already contains", func() {
		Expect(hasUnsupportedNumber("Yes, all 9 are open.", evidence("Are the 9 invoices open?"), false)).To(BeFalse())
	})

	It("does not flag an answer without numbers", func() {
		Expect(hasUnsupportedNumber("I cannot tell without looking it up.", evidence("How many?"), false)).To(BeFalse())
	})

	It("matches decimals regardless of the separator", func() {
		Expect(hasUnsupportedNumber("The rate is 3,5 percent.", evidence("Is the rate 3.5%?"), false)).To(BeFalse())
		Expect(hasUnsupportedNumber("The rate is 3.5 percent.", evidence("Ist der Satz 3,5 %?"), false)).To(BeFalse())
	})

	It("flags a decimal whose digits differ from the input", func() {
		Expect(hasUnsupportedNumber("The rate is 3,7 percent.", evidence("Is the rate 3.5%?"), false)).To(BeTrue())
	})

	It("does not treat a number inside a longer one as evidence", func() {
		Expect(hasUnsupportedNumber("There are 9.", evidence("Invoice 19 is open."), false)).To(BeTrue())
		Expect(hasUnsupportedNumber("Invoice 19 is open.", evidence("Invoice 19?"), false)).To(BeFalse())
	})

	It("takes the system prompt as evidence", func() {
		ev := evidenceNumbers("The office opens at 8.", userMessages("When does it open?"))
		Expect(hasUnsupportedNumber("At 8.", ev, false)).To(BeFalse())
		Expect(hasUnsupportedNumber("At 9.", ev, false)).To(BeTrue())
	})

	It("does not flag anything once a tool ran in the request", func() {
		Expect(hasUnsupportedNumber("There are 7 open invoices.", evidence("How many open invoices?"), true)).To(BeFalse())
		Expect(hasUnsupportedNumber("There are 7 open invoices.", evidence("How many open invoices?"), false)).To(BeTrue())
	})
})

var _ = Describe("evidenceGate.apply", func() {
	gate := func() *evidenceGate {
		return &evidenceGate{note: "NOTE", evidence: evidenceNumbers("", userMessages("How many?"))}
	}
	answer := func(content string) []schema.Choice {
		return []schema.Choice{{Message: &schema.Message{Role: "assistant", Content: &content}}}
	}
	contentOf := func(choices []schema.Choice) string {
		return *(choices[0].Message.Content.(*string))
	}

	It("appends the note to an answer with an invented number", func() {
		choices := answer("There are 0.")
		gate().apply(choices)
		Expect(contentOf(choices)).To(Equal("There are 0.\n\nNOTE"))
	})

	It("leaves an answer without numbers alone", func() {
		choices := answer("Let me check.")
		gate().apply(choices)
		Expect(contentOf(choices)).To(Equal("Let me check."))
	})

	It("leaves tool calls for the client alone", func() {
		content := "Calling 2 tools."
		choices := []schema.Choice{{Message: &schema.Message{Content: &content, ToolCalls: []schema.ToolCall{{ID: "x"}}}}}
		gate().apply(choices)
		Expect(contentOf(choices)).To(Equal("Calling 2 tools."))
	})

	It("does nothing when the check is off", func() {
		var g *evidenceGate
		choices := answer("There are 0.")
		g.apply(choices)
		Expect(contentOf(choices)).To(Equal("There are 0."))
	})
})

// The endpoint specs run the real ChatEndpoint against a stubbed backend and a
// profile alias resolved by the real loader, so they cover the wiring from the
// alias YAML to the response the client sees.
var _ = Describe("ChatEndpoint evidence check", func() {
	const note = "UNVERIFIED"

	var origInference modelInferenceFunc

	BeforeEach(func() { origInference = backend.ModelInferenceFunc })
	AfterEach(func() { backend.ModelInferenceFunc = origInference })

	// stubBackend answers the n-th inference call with replies[n] (the last one
	// repeats), streaming the text through the token callback as well.
	stubBackend := func(replies ...backend.LLMResponse) {
		call := 0
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
			reply := replies[min(call, len(replies)-1)]
			call++
			return func() (backend.LLMResponse, error) {
				if tokenCallback != nil && reply.Response != "" {
					tokenCallback(reply.Response, backend.TokenUsage{})
				}
				return reply, nil
			}, nil
		}
	}

	// resolve writes a target and an alias to a fresh model dir and returns the
	// alias resolved the way the request middleware resolves it.
	resolve := func(aliasProfile, mcpURL string) *config.ModelConfig {
		dir := GinkgoT().TempDir()
		name := "evidence-" + uuid.NewString()
		target := "name: " + name + "-base\nbackend: llama-cpp\nparameters:\n  model: base.gguf\ntemplate:\n  use_tokenizer_template: true\n"
		alias := "name: " + name + "\nalias: " + name + "-base\n" + aliasProfile
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
		return resolved
	}

	const withCheck = "profile:\n  evidence_check:\n    numbers: true\n    note: " + note + "\n"
	const withoutCheck = "profile:\n  system_prompt: plain persona\n"

	run := func(cfg *config.ModelConfig, stream bool, question string) *httptest.ResponseRecorder {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		input := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "alias"}},
			Messages:          userMessages(question),
			Stream:            stream,
			Context:           ctx,
			Cancel:            cancel,
		}
		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")), rec)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
		Expect(ChatEndpoint(nil, nil, nil, config.NewApplicationConfig(), nil, nil, nil)(c)).To(Succeed())
		return rec
	}

	answerOf := func(rec *httptest.ResponseRecorder) string {
		var resp schema.OpenAIResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed(), rec.Body.String())
		Expect(resp.Choices).To(HaveLen(1))
		s, ok := resp.Choices[0].Message.Content.(string)
		Expect(ok).To(BeTrue())
		return s
	}

	// streamedAnswer joins the content deltas of an SSE body.
	streamedAnswer := func(rec *httptest.ResponseRecorder) string {
		var b strings.Builder
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok || data == "[DONE]" {
				continue
			}
			var chunk schema.OpenAIResponse
			if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
				continue
			}
			if s, ok := chunk.Choices[0].Delta.Content.(string); ok {
				b.WriteString(s)
			}
		}
		Expect(rec.Body.String()).To(HaveSuffix("data: [DONE]\n\n"))
		return b.String()
	}

	Context("non-streaming", func() {
		It("marks an invented number", func() {
			stubBackend(backend.LLMResponse{Response: "There are 0 open invoices."})
			Expect(answerOf(run(resolve(withCheck, ""), false, "How many open invoices?"))).
				To(Equal("There are 0 open invoices.\n\n" + note))
		})

		It("leaves a number from the request unmarked", func() {
			stubBackend(backend.LLMResponse{Response: "Yes, all 9 are open."})
			Expect(answerOf(run(resolve(withCheck, ""), false, "Are the 9 invoices open?"))).
				To(Equal("Yes, all 9 are open."))
		})

		It("leaves a profile without the check unchanged", func() {
			stubBackend(backend.LLMResponse{Response: "There are 0 open invoices."})
			Expect(answerOf(run(resolve(withoutCheck, ""), false, "How many open invoices?"))).
				To(Equal("There are 0 open invoices."))
		})

		It("leaves the answer unmarked when an MCP tool ran in this request", func() {
			fx := newMCPAutoFixture()
			stubBackend(
				backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Id: "call-1", Name: "lookup", Arguments: "{}"}}}}},
				backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{Content: "There are 7 open invoices."}}},
			)
			Expect(answerOf(run(resolve(withCheck, fx.url), false, "How many open invoices?"))).
				To(Equal("There are 7 open invoices."))
			Expect(fx.requests.Load()).To(BeNumerically(">", 0))
		})

		It("marks an invented number when the MCP tool call failed", func() {
			fx := newMCPAutoFixture()
			// Arguments that are not JSON make the real executor fail the call.
			stubBackend(
				backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Id: "call-1", Name: "lookup", Arguments: "{not json"}}}}},
				backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{Content: "There are 7 open invoices."}}},
			)
			Expect(answerOf(run(resolve(withCheck, fx.url), false, "How many open invoices?"))).
				To(Equal("There are 7 open invoices.\n\n" + note))
		})

		It("marks the answer when MCP tools were offered but none ran", func() {
			fx := newMCPAutoFixture()
			stubBackend(backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{Content: "There are 0 open invoices."}}})
			Expect(answerOf(run(resolve(withCheck, fx.url), false, "How many open invoices?"))).
				To(Equal("There are 0 open invoices.\n\n" + note))
		})
	})

	Context("sources", func() {
		const withSources = "profile:\n  evidence_check:\n    sources: true\n    sources_label: Quellen\n"
		const toolResult = `{"results":[{"title":"A","url":"https://a.example/oil"},{"title":"B","url":"https://b.example/test."}],"see":"https://a.example/oil"}`

		toolThenAnswer := func(answer string) {
			stubBackend(
				backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Id: "call-1", Name: "lookup", Arguments: "{}"}}}}},
				backend.LLMResponse{Response: answer, ChatDeltas: []*pb.ChatDelta{{Content: answer}}},
			)
		}

		for _, stream := range []bool{false, true} {
			answer := func(rec *httptest.ResponseRecorder) string {
				if stream {
					return streamedAnswer(rec)
				}
				return answerOf(rec)
			}

			Context(map[bool]string{false: "non-streaming", true: "streaming"}[stream], func() {
				It("appends the URLs the tools returned when the answer names none", func() {
					fx := newMCPResultFixture(toolResult)
					toolThenAnswer("A good oil, according to two tests.")
					Expect(answer(run(resolve(withSources, fx.url), stream, "Is this a good olive oil?"))).
						To(HaveSuffix("A good oil, according to two tests.\n\nQuellen: https://a.example/oil, https://b.example/test"))
				})

				It("leaves an answer that names a URL unchanged", func() {
					fx := newMCPResultFixture(toolResult)
					toolThenAnswer("A good oil, see https://a.example/oil.")
					Expect(answer(run(resolve(withSources, fx.url), stream, "Is this a good olive oil?"))).
						To(HaveSuffix("A good oil, see https://a.example/oil."))
				})

				It("adds nothing when no tool result holds a URL", func() {
					fx := newMCPResultFixture("no links here")
					toolThenAnswer("A good oil.")
					Expect(answer(run(resolve(withSources, fx.url), stream, "Is this a good olive oil?"))).
						To(HaveSuffix("A good oil."))
				})

				It("adds nothing without the switch", func() {
					fx := newMCPResultFixture(toolResult)
					toolThenAnswer("A good oil.")
					Expect(answer(run(resolve(withoutCheck, fx.url), stream, "Is this a good olive oil?"))).
						To(HaveSuffix("A good oil."))
				})
			})
		}

		It("takes no URL from the request, only from tool results", func() {
			stubBackend(backend.LLMResponse{Response: "A good oil."})
			Expect(answerOf(run(resolve(withSources, ""), false, "Is https://shop.example/oil a good olive oil?"))).
				To(Equal("A good oil."))
		})

		It("uses the English label by default", func() {
			fx := newMCPResultFixture(toolResult)
			toolThenAnswer("A good oil.")
			Expect(answerOf(run(resolve("profile:\n  evidence_check:\n    sources: true\n", fx.url), false, "Is this a good olive oil?"))).
				To(Equal("A good oil.\n\nSources: https://a.example/oil, https://b.example/test"))
		})
	})

	Context("urls", func() {
		const withURLs = "profile:\n  evidence_check:\n    urls: true\n    urls_note: \"Nicht abgerufen: {urls}\"\n"
		const toolResult = `{"results":[{"title":"A","url":"https://a.example/oil"}]}`

		toolThenAnswer := func(answer string) {
			stubBackend(
				backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Id: "call-1", Name: "lookup", Arguments: "{}"}}}}},
				backend.LLMResponse{Response: answer, ChatDeltas: []*pb.ChatDelta{{Content: answer}}},
			)
		}

		for _, stream := range []bool{false, true} {
			answer := func(rec *httptest.ResponseRecorder) string {
				if stream {
					return streamedAnswer(rec)
				}
				return answerOf(rec)
			}

			Context(map[bool]string{false: "non-streaming", true: "streaming"}[stream], func() {
				It("marks a link no tool returned", func() {
					stubBackend(backend.LLMResponse{Response: "Per [Statista](https://www.statista.com/statistics/1/x), it grows."})
					Expect(answer(run(resolve(withURLs, ""), stream, "Does the market grow?"))).
						To(Equal("Per [Statista](https://www.statista.com/statistics/1/x), it grows.\n\nNicht abgerufen: https://www.statista.com/statistics/1/x"))
				})

				It("marks an invented link next to a retrieved one", func() {
					fx := newMCPResultFixture(toolResult)
					toolThenAnswer("See https://a.example/oil and https://gartner.example/report.")
					Expect(answer(run(resolve(withURLs, fx.url), stream, "Is this a good olive oil?"))).
						To(HaveSuffix("See https://a.example/oil and https://gartner.example/report.\n\nNicht abgerufen: https://gartner.example/report"))
				})

				It("leaves a link from a tool result unmarked", func() {
					fx := newMCPResultFixture(toolResult)
					toolThenAnswer("See https://a.example/oil/.")
					Expect(answer(run(resolve(withURLs, fx.url), stream, "Is this a good olive oil?"))).
						To(HaveSuffix("See https://a.example/oil/."))
				})

				It("leaves a link from the user message unmarked", func() {
					stubBackend(backend.LLMResponse{Response: "https://shop.example/oil sells a good oil."})
					Expect(answer(run(resolve(withURLs, ""), stream, "Is https://shop.example/oil a good olive oil?"))).
						To(Equal("https://shop.example/oil sells a good oil."))
				})

				It("changes nothing without urls: true", func() {
					stubBackend(backend.LLMResponse{Response: "See https://www.statista.com/statistics/1/x."})
					Expect(answer(run(resolve(withoutCheck, ""), stream, "Does the market grow?"))).
						To(Equal("See https://www.statista.com/statistics/1/x."))
				})
			})
		}

		It("marks a link when the MCP tool call failed", func() {
			fx := newMCPResultFixture(toolResult)
			stubBackend(
				backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Id: "call-1", Name: "lookup", Arguments: "{not json"}}}}},
				backend.LLMResponse{Response: "See https://a.example/oil.", ChatDeltas: []*pb.ChatDelta{{Content: "See https://a.example/oil."}}},
			)
			Expect(answerOf(run(resolve(withURLs, fx.url), false, "Is this a good olive oil?"))).
				To(Equal("See https://a.example/oil.\n\nNicht abgerufen: https://a.example/oil"))
		})

		It("uses the English note by default", func() {
			stubBackend(backend.LLMResponse{Response: "See https://statista.example/x."})
			Expect(answerOf(run(resolve("profile:\n  evidence_check:\n    urls: true\n", ""), false, "Market?"))).
				To(Equal("See https://statista.example/x.\n\n" + strings.ReplaceAll(config.DefaultURLsNote, config.URLsPlaceholder, "https://statista.example/x")))
		})

		It("keeps the number check unchanged when both are on", func() {
			stubBackend(backend.LLMResponse{Response: "There are 3 reports, see https://statista.example/x."})
			Expect(answerOf(run(resolve(withURLs+"    numbers: true\n    note: "+note+"\n", ""), false, "How many reports?"))).
				To(Equal("There are 3 reports, see https://statista.example/x.\n\nNicht abgerufen: https://statista.example/x\n\n" + note))
		})
	})

	Context("streaming", func() {
		It("sends the note as a last content delta", func() {
			stubBackend(backend.LLMResponse{Response: "There are 0 open invoices."})
			Expect(streamedAnswer(run(resolve(withCheck, ""), true, "How many open invoices?"))).
				To(Equal("There are 0 open invoices.\n\n" + note))
		})

		It("sends no note for a number from the request", func() {
			stubBackend(backend.LLMResponse{Response: "Yes, all 9 are open."})
			Expect(streamedAnswer(run(resolve(withCheck, ""), true, "Are the 9 invoices open?"))).
				To(Equal("Yes, all 9 are open."))
		})

		It("sends no note when an MCP tool ran in this request", func() {
			fx := newMCPAutoFixture()
			stubBackend(
				backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Id: "call-1", Name: "lookup", Arguments: "{}"}}}}},
				backend.LLMResponse{Response: "There are 7 open invoices.", ChatDeltas: []*pb.ChatDelta{{Content: "There are 7 open invoices."}}},
			)
			rec := run(resolve(withCheck, fx.url), true, "How many open invoices?")
			Expect(rec.Body.String()).To(ContainSubstring("mcp_tool_result"))
			Expect(streamedAnswer(rec)).To(ContainSubstring("There are 7 open invoices."))
			Expect(streamedAnswer(rec)).NotTo(ContainSubstring(note))
		})

		It("sends the note when the MCP tool call failed", func() {
			fx := newMCPAutoFixture()
			stubBackend(
				backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Id: "call-1", Name: "lookup", Arguments: "{not json"}}}}},
				backend.LLMResponse{Response: "There are 7 open invoices.", ChatDeltas: []*pb.ChatDelta{{Content: "There are 7 open invoices."}}},
			)
			rec := run(resolve(withCheck, fx.url), true, "How many open invoices?")
			Expect(rec.Body.String()).To(ContainSubstring("mcp_tool_result"))
			Expect(streamedAnswer(rec)).To(HaveSuffix("There are 7 open invoices.\n\n" + note))
		})

		It("sends no note for a profile without the check", func() {
			stubBackend(backend.LLMResponse{Response: "There are 0 open invoices."})
			Expect(streamedAnswer(run(resolve(withoutCheck, ""), true, "How many open invoices?"))).
				To(Equal("There are 0 open invoices."))
		})
	})
})

// newMCPResultFixture is a remote MCP server whose one tool, lookup, returns
// result as text.
func newMCPResultFixture(result string) *mcpAutoFixture {
	fx := &mcpAutoFixture{}
	srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v0.0.1"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:        "lookup",
		Description: "fixture tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: result}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	server := httptest.NewServer(handler)
	DeferCleanup(server.Close)
	fx.url = server.URL
	return fx
}
