package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openai/openai-go/v3"
)

// startMockMCPServer creates an in-process MCP HTTP server with a "get_weather" tool
// and returns its URL and a shutdown function.
func startMockMCPServer() (string, func()) {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "mock-mcp", Version: "v1.0.0"},
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

// mcpModelConfig generates a model config YAML that includes MCP remote server config.
func mcpModelConfig(mcpServerURL string) map[string]any {
	mcpRemote := fmt.Sprintf(`{"mcpServers":{"weather-api":{"url":"%s"}}}`, mcpServerURL)
	return map[string]any{
		"name":    "mock-model-mcp",
		"backend": "mock-backend",
		"parameters": map[string]any{
			"model": "mock-model-mcp.bin",
		},
		"mcp": map[string]any{
			"remote": mcpRemote,
		},
		"agent": map[string]any{
			// The mock backend returns a tool call on the first inference, then
			// a plain text response once tool results appear in the prompt.
			// max_iterations=1 is enough for one tool-call round-trip.
			"max_iterations": 1,
		},
	}
}

// httpPost sends a JSON POST request and returns the response.
func httpPost(url string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return (&http.Client{Timeout: 60 * time.Second}).Do(req)
}

// readBody reads and returns the response body as a string.
func readBody(resp *http.Response) string {
	data, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	return string(data)
}

var _ = Describe("MCP Tool Integration E2E Tests", Label("MCP"), func() {

	Describe("MCP Server Listing", func() {
		It("should list MCP servers and tools for a configured model", func() {
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/mcp/servers/mock-model-mcp", apiPort))
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))

			var result struct {
				Model   string `json:"model"`
				Servers []struct {
					Name  string   `json:"name"`
					Type  string   `json:"type"`
					Tools []string `json:"tools"`
				} `json:"servers"`
			}
			Expect(json.NewDecoder(resp.Body).Decode(&result)).To(Succeed())
			Expect(result.Model).To(Equal("mock-model-mcp"))
			Expect(result.Servers).To(HaveLen(1))
			Expect(result.Servers[0].Name).To(Equal("weather-api"))
			Expect(result.Servers[0].Tools).To(ContainElement("get_weather"))
		})

		It("should return empty servers for a model without MCP config", func() {
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/mcp/servers/mock-model", apiPort))
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))

			var result struct {
				Servers []any `json:"servers"`
			}
			Expect(json.NewDecoder(resp.Body).Decode(&result)).To(Succeed())
			Expect(result.Servers).To(BeEmpty())
		})
	})

	Describe("OpenAI Chat Completions with MCP", func() {
		Context("Non-streaming", func() {
			It("should inject and execute MCP tools when mcp_servers is set", func() {
				body := map[string]any{
					"model":    "mock-model-mcp",
					"messages": []map[string]string{{"role": "user", "content": "What is the weather in San Francisco?"}},
					"metadata": map[string]string{"mcp_servers": "weather-api"},
				}
				resp, err := httpPost(apiURL+"/chat/completions", body)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				respBody := readBody(resp)
				Expect(resp.StatusCode).To(Equal(200), "unexpected status, body: %s", respBody)

				var result struct {
					Choices []struct {
						Message struct {
							Content string `json:"content"`
						} `json:"message"`
					} `json:"choices"`
				}
				Expect(json.Unmarshal([]byte(respBody), &result)).To(Succeed())
				Expect(result.Choices).To(HaveLen(1))
				Expect(result.Choices[0].Message.Content).To(ContainSubstring("weather"))
			})

			It("should not inject MCP tools when mcp_servers is not set", func() {
				resp, err := client.Chat.Completions.New(
					context.TODO(),
					openai.ChatCompletionNewParams{
						Model: "mock-model-mcp",
						Messages: []openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("Hello"),
						},
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1))
				Expect(resp.Choices[0].Message.Content).To(ContainSubstring("mocked response"))
			})
		})

		Context("Streaming", func() {
			It("should work with MCP tools in streaming mode", func() {
				body := map[string]any{
					"model":    "mock-model-mcp",
					"messages": []map[string]string{{"role": "user", "content": "What is the weather?"}},
					"metadata": map[string]string{"mcp_servers": "weather-api"},
					"stream":   true,
				}
				resp, err := httpPost(apiURL+"/chat/completions", body)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))
				Expect(resp.Header.Get("Content-Type")).To(ContainSubstring("text/event-stream"))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(data)).To(ContainSubstring("data:"))
			})
		})
	})

	Describe("Anthropic Messages with MCP", func() {
		Context("Non-streaming", func() {
			It("should inject and execute MCP tools when mcp_servers is set", func() {
				body := map[string]any{
					"model":      "mock-model-mcp",
					"max_tokens": 1024,
					"messages":   []map[string]string{{"role": "user", "content": "What is the weather?"}},
					"metadata":   map[string]string{"mcp_servers": "weather-api"},
				}
				resp, err := httpPost(fmt.Sprintf("http://127.0.0.1:%d/v1/messages", apiPort), body)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				respBody := readBody(resp)
				Expect(resp.StatusCode).To(Equal(200), "unexpected status, body: %s", respBody)

				var result map[string]any
				Expect(json.Unmarshal([]byte(respBody), &result)).To(Succeed())
				content, ok := result["content"].([]any)
				Expect(ok).To(BeTrue())
				Expect(content).ToNot(BeEmpty())
				first, ok := content[0].(map[string]any)
				Expect(ok).To(BeTrue())
				Expect(first["text"]).To(ContainSubstring("weather"))
			})

			It("should return standard response without mcp_servers", func() {
				body := map[string]any{
					"model":      "mock-model-mcp",
					"max_tokens": 1024,
					"messages":   []map[string]string{{"role": "user", "content": "Hello"}},
				}
				resp, err := httpPost(fmt.Sprintf("http://127.0.0.1:%d/v1/messages", apiPort), body)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				var result map[string]any
				Expect(json.NewDecoder(resp.Body).Decode(&result)).To(Succeed())
				content, ok := result["content"].([]any)
				Expect(ok).To(BeTrue())
				Expect(content).ToNot(BeEmpty())
				first, ok := content[0].(map[string]any)
				Expect(ok).To(BeTrue())
				Expect(first["text"]).To(ContainSubstring("mocked response"))
			})
		})

		Context("Streaming", func() {
			It("should work with MCP tools in streaming mode", func() {
				body := map[string]any{
					"model":      "mock-model-mcp",
					"max_tokens": 1024,
					"messages":   []map[string]string{{"role": "user", "content": "What is the weather?"}},
					"metadata":   map[string]string{"mcp_servers": "weather-api"},
					"stream":     true,
				}
				resp, err := httpPost(fmt.Sprintf("http://127.0.0.1:%d/v1/messages", apiPort), body)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(data)).To(ContainSubstring("event:"))
			})
		})
	})

	Describe("Open Responses with MCP", func() {
		Context("Non-streaming", func() {
			It("should inject and execute MCP tools when mcp_servers is set", func() {
				body := map[string]any{
					"model":    "mock-model-mcp",
					"input":    "What is the weather in San Francisco?",
					"metadata": map[string]string{"mcp_servers": "weather-api"},
				}
				resp, err := httpPost(fmt.Sprintf("http://127.0.0.1:%d/v1/responses", apiPort), body)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				respBody := readBody(resp)
				Expect(resp.StatusCode).To(Equal(200), "unexpected status, body: %s", respBody)

				var result map[string]any
				Expect(json.Unmarshal([]byte(respBody), &result)).To(Succeed())
				// Open Responses wraps output in an "output" array
				output, ok := result["output"].([]any)
				Expect(ok).To(BeTrue(), "expected output array in response: %s", respBody)
				Expect(output).ToNot(BeEmpty())
			})

			It("should auto-activate MCP tools without mcp_servers (backward compat)", func() {
				// Open Responses auto-activates all MCP servers when no metadata
				// mcp_servers key is provided and no user tools are set.
				body := map[string]any{
					"model": "mock-model-mcp",
					"input": "Hello",
				}
				resp, err := httpPost(fmt.Sprintf("http://127.0.0.1:%d/v1/responses", apiPort), body)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				respBody := readBody(resp)
				Expect(resp.StatusCode).To(Equal(200), "unexpected status, body: %s", respBody)

				var result map[string]any
				Expect(json.Unmarshal([]byte(respBody), &result)).To(Succeed())
				output, ok := result["output"].([]any)
				Expect(ok).To(BeTrue(), "expected output array in response: %s", respBody)
				Expect(output).ToNot(BeEmpty())
			})
		})

		Context("Streaming", func() {
			It("should work with MCP tools in streaming mode", func() {
				body := map[string]any{
					"model":    "mock-model-mcp",
					"input":    "What is the weather?",
					"metadata": map[string]string{"mcp_servers": "weather-api"},
					"stream":   true,
				}
				resp, err := httpPost(fmt.Sprintf("http://127.0.0.1:%d/v1/responses", apiPort), body)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(data)).To(ContainSubstring("event:"))
			})
		})
	})

	Describe("Legacy /mcp endpoint", func() {
		It("should auto-enable all MCP servers and complete the tool loop", func() {
			body := map[string]any{
				"model":    "mock-model-mcp",
				"messages": []map[string]string{{"role": "user", "content": "What is the weather in San Francisco?"}},
			}
			resp, err := httpPost(fmt.Sprintf("http://127.0.0.1:%d/mcp/v1/chat/completions", apiPort), body)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			respBody := readBody(resp)
			Expect(resp.StatusCode).To(Equal(200), "unexpected status, body: %s", respBody)

			var result struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			Expect(json.Unmarshal([]byte(respBody), &result)).To(Succeed())
			Expect(result.Choices).To(HaveLen(1))
			Expect(result.Choices[0].Message.Content).To(ContainSubstring("weather"))
		})

		It("should respect metadata mcp_servers when provided", func() {
			body := map[string]any{
				"model":    "mock-model-mcp",
				"messages": []map[string]string{{"role": "user", "content": "Hello"}},
				"metadata": map[string]string{"mcp_servers": "non-existent-server"},
			}
			// Even through the /mcp endpoint, an explicit metadata selection
			// should be honoured — a non-existent server means no MCP tools.
			resp, err := httpPost(fmt.Sprintf("http://127.0.0.1:%d/mcp/v1/chat/completions", apiPort), body)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			respBody := readBody(resp)
			Expect(resp.StatusCode).To(Equal(200), "unexpected status, body: %s", respBody)

			var result struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			Expect(json.Unmarshal([]byte(respBody), &result)).To(Succeed())
			Expect(result.Choices).To(HaveLen(1))
			Expect(result.Choices[0].Message.Content).To(ContainSubstring("mocked response"))
		})

		It("should work in streaming mode", func() {
			body := map[string]any{
				"model":    "mock-model-mcp",
				"messages": []map[string]string{{"role": "user", "content": "What is the weather?"}},
				"stream":   true,
			}
			resp, err := httpPost(fmt.Sprintf("http://127.0.0.1:%d/mcp/v1/chat/completions", apiPort), body)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))
			Expect(resp.Header.Get("Content-Type")).To(ContainSubstring("text/event-stream"))

			data, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("data:"))
		})
	})

	Describe("MCP with invalid server name", func() {
		It("should work without MCP tools when specifying non-existent server", func() {
			body := map[string]any{
				"model":    "mock-model-mcp",
				"messages": []map[string]string{{"role": "user", "content": "Hello"}},
				"metadata": map[string]string{"mcp_servers": "non-existent-server"},
			}
			resp, err := httpPost(apiURL+"/chat/completions", body)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))

			var result struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			Expect(json.NewDecoder(resp.Body).Decode(&result)).To(Succeed())
			Expect(result.Choices).To(HaveLen(1))
			Expect(result.Choices[0].Message.Content).To(ContainSubstring("mocked response"))
		})
	})
})
