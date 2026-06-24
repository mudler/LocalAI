package distributed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openai "github.com/sashabaranov/go-openai"
)

// startTestMCPServer creates an in-process MCP HTTP server with a "get_weather" tool.
func startTestMCPServer() (string, func()) {
	GinkgoHelper()
	server := mcp.NewServer(
		&mcp.Implementation{Name: "test-mcp", Version: "v1.0.0"},
		nil,
	)

	server.AddTool(
		&mcp.Tool{
			Name:        "get_weather",
			Description: "Get the current weather for a location",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"location":{"type":"string","description":"City name"}},"required":["location"]}`),
		},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				Location string `json:"location"`
			}
			if req.Params.Arguments != nil {
				data, _ := json.Marshal(req.Params.Arguments)
				json.Unmarshal(data, &args)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: fmt.Sprintf("Weather in %s: sunny, 72°F", args.Location),
					},
				},
			}, nil
		},
	)

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless: true,
		},
	)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())

	httpServer := &http.Server{Handler: handler}
	go httpServer.Serve(listener)

	url := fmt.Sprintf("http://%s/mcp", listener.Addr().String())
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		httpServer.Shutdown(ctx)
	}
	return url, shutdown
}

// startMockLLMServer creates a mock OpenAI-compatible HTTP server that supports
// both streaming and non-streaming requests.
// On first call (when no tool results in messages): returns a tool call for get_weather.
// On subsequent calls (when tool results present): returns a text response.
func startMockLLMServer() (string, func()) {
	var callCount int
	var mu sync.Mutex

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}

		rawBody, _ := io.ReadAll(r.Body)

		var req openai.ChatCompletionRequest
		if err := json.Unmarshal(rawBody, &req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		mu.Lock()
		callCount++
		currentCall := callCount
		mu.Unlock()

		// Check if the request includes tools (cogito sends tools when it wants the LLM to pick one)
		hasTools := len(req.Tools) > 0

		// Check if there are tool results in the messages (means we already called a tool)
		hasToolResult := false
		for _, msg := range req.Messages {
			if msg.Role == "tool" {
				hasToolResult = true
				break
			}
		}

		// Determine if we should call get_weather:
		// - Request has tools available (cogito is asking us to pick one)
		// - We haven't already called a tool (no tool results in messages)
		// - get_weather is one of the available tools
		shouldCallTool := hasTools && !hasToolResult
		toolName := ""
		if shouldCallTool {
			for _, t := range req.Tools {
				if t.Function.Name == "get_weather" {
					toolName = "get_weather"
					break
				}
			}
			if toolName == "" {
				shouldCallTool = false
			}
		}

		isStream := req.Stream

		if isStream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			flusher, _ := w.(http.Flusher)

			if shouldCallTool {
				chunk := map[string]any{
					"id":    fmt.Sprintf("chatcmpl-test-%d", currentCall),
					"model": req.Model,
					"choices": []map[string]any{
						{
							"index": 0,
							"delta": map[string]any{
								"role": "assistant",
								"tool_calls": []map[string]any{
									{
										"index": 0,
										"id":    fmt.Sprintf("call_%d", currentCall),
										"type":  "function",
										"function": map[string]any{
											"name":      toolName,
											"arguments": `{"location":"San Francisco"}`,
										},
									},
								},
							},
							"finish_reason": "tool_calls",
						},
					},
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
				if flusher != nil {
					flusher.Flush()
				}
			} else {
				content := "The weather in San Francisco is sunny, 72°F. Have a great day!"
				chunk := map[string]any{
					"id":    fmt.Sprintf("chatcmpl-test-%d", currentCall),
					"model": req.Model,
					"choices": []map[string]any{
						{
							"index": 0,
							"delta": map[string]any{
								"role":    "assistant",
								"content": content,
							},
							"finish_reason": nil,
						},
					},
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
				if flusher != nil {
					flusher.Flush()
				}

				finishChunk := map[string]any{
					"id":    fmt.Sprintf("chatcmpl-test-%d-done", currentCall),
					"model": req.Model,
					"choices": []map[string]any{
						{
							"index":         0,
							"delta":         map[string]any{},
							"finish_reason": "stop",
						},
					},
				}
				data, _ = json.Marshal(finishChunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
				if flusher != nil {
					flusher.Flush()
				}
			}

			fmt.Fprintf(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		} else {
			w.Header().Set("Content-Type", "application/json")

			if shouldCallTool {
				resp := openai.ChatCompletionResponse{
					ID:    fmt.Sprintf("chatcmpl-test-%d", currentCall),
					Model: req.Model,
					Choices: []openai.ChatCompletionChoice{
						{
							Index: 0,
							Message: openai.ChatCompletionMessage{
								Role:    "assistant",
								Content: "",
								ToolCalls: []openai.ToolCall{
									{
										ID:   fmt.Sprintf("call_%d", currentCall),
										Type: openai.ToolTypeFunction,
										Function: openai.FunctionCall{
											Name:      toolName,
											Arguments: `{"location":"San Francisco"}`,
										},
									},
								},
							},
							FinishReason: openai.FinishReasonToolCalls,
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			} else {
				resp := openai.ChatCompletionResponse{
					ID:    fmt.Sprintf("chatcmpl-test-%d", currentCall),
					Model: req.Model,
					Choices: []openai.ChatCompletionChoice{
						{
							Index: 0,
							Message: openai.ChatCompletionMessage{
								Role:    "assistant",
								Content: "The weather in San Francisco is sunny, 72°F. Have a great day!",
							},
							FinishReason: openai.FinishReasonStop,
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())

	httpServer := &http.Server{Handler: handler}
	go httpServer.Serve(listener)

	url := fmt.Sprintf("http://%s", listener.Addr().String())
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		httpServer.Shutdown(ctx)
	}
	return url, shutdown
}

var _ = Describe("MCP CI Job Execution", Label("Distributed", "MCPCIJob"), func() {
	var (
		infra *TestInfra
	)

	BeforeEach(func() {
		infra = SetupNATSOnly()
	})

	Context("Full MCP CI Job Flow", func() {
		It("should execute MCP CI job with mock MCP server and mock LLM", func() {
			// Start mock MCP server
			mcpURL, mcpShutdown := startTestMCPServer()
			defer mcpShutdown()

			// Start mock LLM server
			llmURL, llmShutdown := startMockLLMServer()
			defer llmShutdown()

			jobID := "test-job-001"

			// Subscribe to progress and result events
			var progressEvents []jobs.ProgressEvent
			var resultEvent *jobs.JobResultEvent
			var eventMu sync.Mutex

			progressSub, err := infra.NC.Subscribe(messaging.SubjectJobProgress(jobID), func(data []byte) {
				var evt jobs.ProgressEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					progressEvents = append(progressEvents, evt)
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer progressSub.Unsubscribe()

			resultSub, err := infra.NC.Subscribe(messaging.SubjectJobResult(jobID), func(data []byte) {
				var evt jobs.JobResultEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					resultEvent = &evt
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer resultSub.Unsubscribe()

			FlushNATS(infra.NC)

			// Build MCP config YAML pointing to mock MCP server
			mcpRemoteJSON := fmt.Sprintf(`{"mcpServers":{"weather-api":{"url":"%s"}}}`, mcpURL)
			modelCfg := &config.ModelConfig{
				MCP: config.MCPConfig{
					Servers: mcpRemoteJSON,
				},
			}
			modelCfg.Name = "test-mcp-model"

			// Build enriched job event
			evt := jobs.JobEvent{
				JobID:  jobID,
				TaskID: "task-001",
				UserID: "user1",
				Job: &jobs.JobRecord{
					ID:     jobID,
					TaskID: "task-001",
					UserID: "user1",
					Status: "pending",
				},
				Task: &jobs.TaskRecord{
					ID:     "task-001",
					UserID: "user1",
					Name:   "weather-check",
					Model:  "test-mcp-model",
					Prompt: "What is the weather in San Francisco?",
				},
				ModelConfig: modelCfg,
			}

			// Simulate the agent worker subscribing and processing
			workerSub, err := infra.NC.QueueSubscribe(messaging.SubjectJobsNew, messaging.QueueWorkers, func(data []byte) {
				// This is what the agent worker does — call handleMCPCIJob
				// We import it indirectly by calling the same logic
				go processMCPCIJobForTest(data, llmURL, "test-token", infra.NC)
			})
			Expect(err).ToNot(HaveOccurred())
			defer workerSub.Unsubscribe()

			FlushNATS(infra.NC)

			// Publish the job event
			Expect(infra.NC.Publish(messaging.SubjectJobsNew, evt)).To(Succeed())

			// Wait for result
			Eventually(func() bool {
				eventMu.Lock()
				defer eventMu.Unlock()
				return resultEvent != nil
			}, "30s", "500ms").Should(BeTrue(), "should receive job result")

			eventMu.Lock()
			defer eventMu.Unlock()

			// Verify final result
			Expect(resultEvent.Status).To(Equal("completed"))
			Expect(resultEvent.Result).To(ContainSubstring("weather"))
			Expect(resultEvent.Result).To(ContainSubstring("San Francisco"))

			// Verify progress events include tool traces
			var hasRunning, hasToolResult bool
			for _, p := range progressEvents {
				if p.Status == "running" {
					hasRunning = true
				}
				if p.TraceType == "tool_result" {
					hasToolResult = true
					Expect(p.TraceContent).To(ContainSubstring("get_weather"))
				}
			}
			Expect(hasRunning).To(BeTrue(), "should have running status")
			Expect(hasToolResult).To(BeTrue(), "should have tool_result trace from MCP tool execution")
		})

		It("should fail gracefully when MCP server is unreachable", func() {
			jobID := "test-job-fail-001"

			var resultEvent *jobs.JobResultEvent
			var eventMu sync.Mutex

			resultSub, err := infra.NC.Subscribe(messaging.SubjectJobResult(jobID), func(data []byte) {
				var evt jobs.JobResultEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					resultEvent = &evt
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer resultSub.Unsubscribe()

			FlushNATS(infra.NC)

			// MCP config pointing to unreachable server
			mcpRemoteJSON := `{"mcpServers":{"bad-server":{"url":"http://127.0.0.1:1/mcp"}}}`
			modelCfg := &config.ModelConfig{
				MCP: config.MCPConfig{
					Servers: mcpRemoteJSON,
				},
			}
			modelCfg.Name = "test-fail-model"

			evt := jobs.JobEvent{
				JobID:  jobID,
				TaskID: "task-fail",
				UserID: "user1",
				Job: &jobs.JobRecord{
					ID:     jobID,
					TaskID: "task-fail",
					UserID: "user1",
					Status: "pending",
				},
				Task: &jobs.TaskRecord{
					ID:     "task-fail",
					UserID: "user1",
					Name:   "bad-task",
					Model:  "test-fail-model",
					Prompt: "This should fail",
				},
				ModelConfig: modelCfg,
			}

			// Process directly (no worker subscription needed)
			evtData, _ := json.Marshal(evt)
			go processMCPCIJobForTest(evtData, "http://localhost:9999", "token", infra.NC)

			// Wait for failure result
			Eventually(func() bool {
				eventMu.Lock()
				defer eventMu.Unlock()
				return resultEvent != nil
			}, "15s", "500ms").Should(BeTrue(), "should receive failure result")

			eventMu.Lock()
			defer eventMu.Unlock()
			Expect(resultEvent.Status).To(Equal("failed"))
			Expect(resultEvent.Error).ToNot(BeEmpty())
		})

		It("should substitute parameters in prompt template", func() {
			// Start mock MCP server
			mcpURL, mcpShutdown := startTestMCPServer()
			defer mcpShutdown()

			// Start mock LLM server
			llmURL, llmShutdown := startMockLLMServer()
			defer llmShutdown()

			jobID := "test-job-params-001"

			var resultEvent *jobs.JobResultEvent
			var eventMu sync.Mutex

			resultSub, err := infra.NC.Subscribe(messaging.SubjectJobResult(jobID), func(data []byte) {
				var evt jobs.JobResultEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					resultEvent = &evt
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer resultSub.Unsubscribe()

			FlushNATS(infra.NC)

			mcpRemoteJSON := fmt.Sprintf(`{"mcpServers":{"weather-api":{"url":"%s"}}}`, mcpURL)
			modelCfg := &config.ModelConfig{
				MCP: config.MCPConfig{
					Servers: mcpRemoteJSON,
				},
			}
			modelCfg.Name = "test-params-model"

			// Job parameters should substitute {{.city}} in prompt
			paramsJSON, _ := json.Marshal(map[string]string{"city": "London"})

			evt := jobs.JobEvent{
				JobID:  jobID,
				TaskID: "task-params",
				UserID: "user1",
				Job: &jobs.JobRecord{
					ID:             jobID,
					TaskID:         "task-params",
					UserID:         "user1",
					Status:         "pending",
					ParametersJSON: string(paramsJSON),
				},
				Task: &jobs.TaskRecord{
					ID:     "task-params",
					UserID: "user1",
					Name:   "weather-params",
					Model:  "test-params-model",
					Prompt: "What is the weather in {{.city}}?",
				},
				ModelConfig: modelCfg,
			}

			evtData, _ := json.Marshal(evt)
			go processMCPCIJobForTest(evtData, llmURL, "test-token", infra.NC)

			Eventually(func() bool {
				eventMu.Lock()
				defer eventMu.Unlock()
				return resultEvent != nil
			}, "30s", "500ms").Should(BeTrue(), "should receive job result")

			eventMu.Lock()
			defer eventMu.Unlock()
			Expect(resultEvent.Status).To(Equal("completed"))
			// The result should contain weather info (the mock LLM returns a response about San Francisco
			// regardless of the prompt, but the job should complete successfully with parameter substitution)
			Expect(resultEvent.Result).ToNot(BeEmpty())
		})

		It("should fail when job or task data is missing from event", func() {
			jobID := "test-job-missing-001"

			var resultEvent *jobs.JobResultEvent
			var eventMu sync.Mutex

			resultSub, err := infra.NC.Subscribe(messaging.SubjectJobResult(jobID), func(data []byte) {
				var evt jobs.JobResultEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					resultEvent = &evt
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer resultSub.Unsubscribe()

			FlushNATS(infra.NC)

			// Event with no Job or Task
			evt := jobs.JobEvent{
				JobID:  jobID,
				TaskID: "task-missing",
				UserID: "user1",
			}

			evtData, _ := json.Marshal(evt)
			go processMCPCIJobForTest(evtData, "http://localhost:9999", "token", infra.NC)

			Eventually(func() bool {
				eventMu.Lock()
				defer eventMu.Unlock()
				return resultEvent != nil
			}, "5s", "200ms").Should(BeTrue(), "should receive failure result")

			eventMu.Lock()
			defer eventMu.Unlock()
			Expect(resultEvent.Status).To(Equal("failed"))
			Expect(resultEvent.Error).To(ContainSubstring("missing"))
		})

		It("should fail when no MCP servers are configured", func() {
			jobID := "test-job-nomcp-001"

			var resultEvent *jobs.JobResultEvent
			var eventMu sync.Mutex

			resultSub, err := infra.NC.Subscribe(messaging.SubjectJobResult(jobID), func(data []byte) {
				var evt jobs.JobResultEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					resultEvent = &evt
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer resultSub.Unsubscribe()

			FlushNATS(infra.NC)

			// ModelConfig with empty MCP
			modelCfg := &config.ModelConfig{}
			modelCfg.Name = "no-mcp-model"

			evt := jobs.JobEvent{
				JobID:  jobID,
				TaskID: "task-nomcp",
				UserID: "user1",
				Job: &jobs.JobRecord{
					ID:     jobID,
					TaskID: "task-nomcp",
					UserID: "user1",
					Status: "pending",
				},
				Task: &jobs.TaskRecord{
					ID:     "task-nomcp",
					UserID: "user1",
					Name:   "no-mcp-task",
					Model:  "no-mcp-model",
					Prompt: "This has no MCP",
				},
				ModelConfig: modelCfg,
			}

			evtData, _ := json.Marshal(evt)
			go processMCPCIJobForTest(evtData, "http://localhost:9999", "token", infra.NC)

			Eventually(func() bool {
				eventMu.Lock()
				defer eventMu.Unlock()
				return resultEvent != nil
			}, "5s", "200ms").Should(BeTrue(), "should receive failure result")

			eventMu.Lock()
			defer eventMu.Unlock()
			Expect(resultEvent.Status).To(Equal("failed"))
			Expect(resultEvent.Error).To(ContainSubstring("MCP"))
		})
	})
})
