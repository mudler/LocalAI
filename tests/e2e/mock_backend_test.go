package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openai/openai-go/v3"
)

var _ = Describe("Mock Backend E2E Tests", Label("MockBackend"), func() {
	Describe("Text Generation APIs", func() {
		Context("Predict (Chat Completions)", func() {
			It("should return mocked response", func() {
				resp, err := client.Chat.Completions.New(
					context.TODO(),
					openai.ChatCompletionNewParams{
						Model: "mock-model",
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

		Context("PredictStream (Streaming Chat Completions)", func() {
			It("should stream mocked tokens", func() {
				stream := client.Chat.Completions.NewStreaming(
					context.TODO(),
					openai.ChatCompletionNewParams{
						Model: "mock-model",
						Messages: []openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("Hello"),
						},
					},
				)
				hasContent := false
				for stream.Next() {
					response := stream.Current()
					if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
						hasContent = true
					}
				}
				Expect(stream.Err()).ToNot(HaveOccurred())
				Expect(hasContent).To(BeTrue())
			})
		})
	})

	Describe("Error Handling", func() {
		Context("Non-streaming errors", func() {
			It("should return error for request with error trigger", func() {
				_, err := client.Chat.Completions.New(
					context.TODO(),
					openai.ChatCompletionNewParams{
						Model: "mock-model",
						Messages: []openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("MOCK_ERROR"),
						},
					},
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("simulated failure"))
			})
		})

		Context("Streaming errors", func() {
			It("should return error for streaming request with immediate error trigger", func() {
				stream := client.Chat.Completions.NewStreaming(
					context.TODO(),
					openai.ChatCompletionNewParams{
						Model: "mock-model",
						Messages: []openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("MOCK_ERROR_IMMEDIATE"),
						},
					},
				)
				for stream.Next() {
					// drain
				}
				Expect(stream.Err()).To(HaveOccurred())
			})

			It("should return structured error for mid-stream failure", func() {
				body := `{"model":"mock-model","messages":[{"role":"user","content":"MOCK_ERROR_MIDSTREAM"}],"stream":true}`
				req, err := http.NewRequest("POST", apiURL+"/chat/completions", strings.NewReader(body))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				resp, err := http.DefaultClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				bodyStr := string(data)

				// Should contain a structured error event
				Expect(bodyStr).To(ContainSubstring(`"error"`))
				Expect(bodyStr).To(ContainSubstring(`"message"`))
				Expect(bodyStr).To(ContainSubstring("simulated mid-stream failure"))
				// Should also contain [DONE]
				Expect(bodyStr).To(ContainSubstring("[DONE]"))
			})
		})
	})

	Describe("Embeddings API", func() {
		It("should return mocked embeddings", func() {
			resp, err := client.Embeddings.New(
				context.TODO(),
				openai.EmbeddingNewParams{
					Model: "mock-model",
					Input: openai.EmbeddingNewParamsInputUnion{
						OfArrayOfStrings: []string{"test"},
					},
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Data)).To(Equal(1))
			Expect(len(resp.Data[0].Embedding)).To(Equal(768))
		})
	})

	Describe("TTS APIs", func() {
		Context("TTS", func() {
			It("should generate mocked audio", func() {
				body := `{"model":"mock-model","input":"Hello world","voice":"default"}`
				req, err := http.NewRequest("POST", apiURL+"/audio/speech", io.NopCloser(strings.NewReader(body)))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				httpClient := &http.Client{Timeout: 30 * time.Second}
				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))
				Expect(resp.Header.Get("Content-Type")).To(HavePrefix("audio/"), "TTS response should set an audio Content-Type")
				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(data)).To(BeNumerically(">", 0), "TTS response body should be non-empty")
			})
		})
	})

	Describe("Sound Generation API", func() {
		It("should generate mocked sound (simple mode)", func() {
			body := `{"model_id":"mock-model","text":"a soft Bengali love song for a quiet evening","instrumental":false,"vocal_language":"bn"}`
			req, err := http.NewRequest("POST", apiURL+"/sound-generation", io.NopCloser(strings.NewReader(body)))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("audio/"), "sound-generation response should set an audio Content-Type (pkg/audio normalization)")
			data, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(data)).To(BeNumerically(">", 0), "sound-generation response body should be non-empty")
		})

		It("should generate mocked sound (advanced mode)", func() {
			body := `{"model_id":"mock-model","text":"upbeat pop","caption":"A funky Japanese disco track","lyrics":"[Verse 1]\nTest lyrics","think":true,"bpm":120,"duration_seconds":225,"keyscale":"Ab major","language":"ja","timesignature":"4"}`
			req, err := http.NewRequest("POST", apiURL+"/sound-generation", io.NopCloser(strings.NewReader(body)))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("audio/"), "sound-generation response should set an audio Content-Type (pkg/audio normalization)")
			data, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(data)).To(BeNumerically(">", 0), "sound-generation response body should be non-empty")
		})
	})

	Describe("Image Generation API", func() {
		It("should generate mocked image", func() {
			req, err := http.NewRequest("POST", apiURL+"/images/generations", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			body := `{"model":"mock-model","prompt":"a cat"}`
			req.Body = http.NoBody
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(body)), nil
			}

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			if err == nil {
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(BeNumerically("<", 500))
			}
		})
	})

	Describe("Audio Transcription API", func() {
		It("should return mocked transcription", func() {
			req, err := http.NewRequest("POST", apiURL+"/audio/transcriptions", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "multipart/form-data")

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			if err == nil {
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(BeNumerically("<", 500))
			}
		})
	})

	Describe("Rerank API", func() {
		It("should return mocked reranking results", func() {
			req, err := http.NewRequest("POST", apiURL+"/rerank", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			body := `{"model":"mock-model","query":"test","documents":["doc1","doc2"]}`
			req.Body = http.NoBody
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(body)), nil
			}

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			if err == nil {
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(BeNumerically("<", 500))
			}
		})
	})

	Describe("Tokenization API", func() {
		It("should return mocked tokens", func() {
			req, err := http.NewRequest("POST", apiURL+"/tokenize", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			body := `{"model":"mock-model","text":"Hello world"}`
			req.Body = http.NoBody
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(body)), nil
			}

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			if err == nil {
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(BeNumerically("<", 500))
			}
		})
	})

	Describe("Autoparser ChatDelta Streaming", Label("Autoparser"), func() {
		// These tests verify that when the C++ autoparser handles tool calls
		// and content via ChatDeltas (with empty raw message), the streaming
		// endpoint does NOT unnecessarily retry. This is a regression test for
		// the bug where the retry logic only checked Go-side parsing, ignoring
		// ChatDelta results, causing up to 6 retries and concatenated output.

		Context("Streaming with tools and ChatDelta tool calls", func() {
			It("should return tool calls without unnecessary retries", func() {
				body := `{
					"model": "mock-model-autoparser",
					"messages": [{"role": "user", "content": "AUTOPARSER_TOOL_CALL"}],
					"tools": [{"type": "function", "function": {"name": "search_collections", "description": "Search documents", "parameters": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}}}],
					"stream": true
				}`
				req, err := http.NewRequest("POST", apiURL+"/chat/completions", strings.NewReader(body))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				httpClient := &http.Client{Timeout: 60 * time.Second}
				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				bodyStr := string(data)

				// Parse all SSE events
				lines := strings.Split(bodyStr, "\n")
				var toolCallChunks int
				var reasoningChunks int
				hasFinishReason := false

				for _, line := range lines {
					line = strings.TrimSpace(line)
					if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
						continue
					}
					jsonData := strings.TrimPrefix(line, "data: ")
					var chunk map[string]any
					if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
						continue
					}
					choices, ok := chunk["choices"].([]any)
					if !ok || len(choices) == 0 {
						continue
					}
					choice := choices[0].(map[string]any)
					delta, _ := choice["delta"].(map[string]any)
					if delta == nil {
						continue
					}
					if _, ok := delta["tool_calls"]; ok {
						toolCallChunks++
					}
					if _, ok := delta["reasoning"]; ok {
						reasoningChunks++
					}
					if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
						hasFinishReason = true
					}
				}

				// The key assertion: tool calls from ChatDeltas should be present
				Expect(toolCallChunks).To(BeNumerically(">", 0),
					"Expected tool_calls in streaming response from ChatDeltas, but got none. "+
						"This likely means the retry logic discarded ChatDelta tool calls.")

				// Should have a finish reason
				Expect(hasFinishReason).To(BeTrue(), "Expected a finish_reason in the streaming response")

				// Reasoning should be present (from ChatDelta reasoning)
				Expect(reasoningChunks).To(BeNumerically(">", 0),
					"Expected reasoning deltas from ChatDeltas")
			})
		})

		Context("Streaming with tools and ChatDelta content (no tool calls)", func() {
			It("should return content without retrying and without concatenation", func() {
				body := `{
					"model": "mock-model-autoparser",
					"messages": [{"role": "user", "content": "AUTOPARSER_CONTENT"}],
					"tools": [{"type": "function", "function": {"name": "search_collections", "description": "Search documents", "parameters": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}}}],
					"stream": true
				}`
				req, err := http.NewRequest("POST", apiURL+"/chat/completions", strings.NewReader(body))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				httpClient := &http.Client{Timeout: 60 * time.Second}
				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				bodyStr := string(data)

				// Parse all SSE events and collect content
				lines := strings.Split(bodyStr, "\n")
				var contentParts []string
				var reasoningParts []string

				for _, line := range lines {
					line = strings.TrimSpace(line)
					if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
						continue
					}
					jsonData := strings.TrimPrefix(line, "data: ")
					var chunk map[string]any
					if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
						continue
					}
					choices, ok := chunk["choices"].([]any)
					if !ok || len(choices) == 0 {
						continue
					}
					choice := choices[0].(map[string]any)
					delta, _ := choice["delta"].(map[string]any)
					if delta == nil {
						continue
					}
					if content, ok := delta["content"].(string); ok && content != "" {
						contentParts = append(contentParts, content)
					}
					if reasoning, ok := delta["reasoning"].(string); ok && reasoning != "" {
						reasoningParts = append(reasoningParts, reasoning)
					}
				}

				fullContent := strings.Join(contentParts, "")
				fullReasoning := strings.Join(reasoningParts, "")

				// Content should be present and match the expected answer
				Expect(fullContent).To(ContainSubstring("LocalAI"),
					"Expected content from ChatDeltas to contain 'LocalAI'. "+
						"The retry logic may have discarded ChatDelta content.")

				// Content should NOT be duplicated (no retry concatenation)
				occurrences := strings.Count(fullContent, "LocalAI is an open-source AI platform.")
				Expect(occurrences).To(Equal(1),
					"Expected content to appear exactly once, but found %d occurrences. "+
						"This indicates unnecessary retries are concatenating output.", occurrences)

				// Reasoning should be present
				Expect(fullReasoning).To(ContainSubstring("compose"),
					"Expected reasoning content from ChatDeltas")
			})
		})

		Context("Non-streaming with tools and ChatDelta tool calls", func() {
			It("should return tool calls from ChatDeltas", func() {
				body := `{
					"model": "mock-model-autoparser",
					"messages": [{"role": "user", "content": "AUTOPARSER_TOOL_CALL"}],
					"tools": [{"type": "function", "function": {"name": "search_collections", "description": "Search documents", "parameters": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}}}]
				}`
				req, err := http.NewRequest("POST", apiURL+"/chat/completions", strings.NewReader(body))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				httpClient := &http.Client{Timeout: 60 * time.Second}
				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())

				var result map[string]any
				Expect(json.Unmarshal(data, &result)).To(Succeed())

				choices, ok := result["choices"].([]any)
				Expect(ok).To(BeTrue())
				Expect(choices).To(HaveLen(1))

				choice := choices[0].(map[string]any)
				msg, _ := choice["message"].(map[string]any)
				Expect(msg).ToNot(BeNil())

				toolCalls, ok := msg["tool_calls"].([]any)
				Expect(ok).To(BeTrue(),
					"Expected tool_calls in non-streaming response from ChatDeltas, "+
						"but got: %s", string(data))
				Expect(toolCalls).To(HaveLen(1))

				tc := toolCalls[0].(map[string]any)
				fn, _ := tc["function"].(map[string]any)
				Expect(fn["name"]).To(Equal("search_collections"))
			})
		})

		// Regression test: thinking model (Gemma 4-style) with tools, where the
		// model responds with content only (no tool calls). The C++ autoparser
		// puts clean content in Message AND reasoning+content in ChatDeltas.
		// Bug: Go-side PrependThinkingTokenIfNeeded prepends <|channel>thought
		// to the clean content, causing it to be classified as unclosed reasoning,
		// leading to "Backend produced reasoning without actionable content, retrying".
		Context("Non-streaming thinking model with tools and ChatDelta content (no tool calls)", func() {
			It("should return content without retrying", func() {
				body := `{
					"model": "mock-model-thinking-autoparser",
					"messages": [{"role": "user", "content": "AUTOPARSER_THINKING_CONTENT"}],
					"tools": [{"type": "function", "function": {"name": "search_collections", "description": "Search documents", "parameters": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}}}]
				}`
				req, err := http.NewRequest("POST", apiURL+"/chat/completions", strings.NewReader(body))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				httpClient := &http.Client{Timeout: 60 * time.Second}
				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())

				var result map[string]any
				Expect(json.Unmarshal(data, &result)).To(Succeed())

				choices, ok := result["choices"].([]any)
				Expect(ok).To(BeTrue(), "Expected choices array, got: %s", string(data))
				Expect(choices).To(HaveLen(1))

				choice := choices[0].(map[string]any)
				msg, _ := choice["message"].(map[string]any)
				Expect(msg).ToNot(BeNil())

				content, _ := msg["content"].(string)
				Expect(content).ToNot(BeEmpty(),
					"Expected non-empty content in thinking model response with tools, "+
						"but got empty content. Full response: %s", string(data))
				Expect(content).To(ContainSubstring("helpful AI assistant"),
					"Expected content to contain the model's response text, got: %s", content)
			})
		})
	})

	// Tests for duplicate tool call emissions during streaming.
	// The Go-side incremental JSON parser was emitting the same tool call on
	// every streaming token, and the post-streaming default: case re-emitted
	// all tool calls again, producing massive duplication.
	Describe("Streaming Tool Call Deduplication", Label("ToolDedup"), func() {
		// Helper: parse SSE lines and count tool call name/arguments chunks
		parseToolCallChunks := func(data []byte) (nameChunks int, argChunks int) {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
					continue
				}
				var chunk map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
					continue
				}
				choices, _ := chunk["choices"].([]any)
				if len(choices) == 0 {
					continue
				}
				delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
				if delta == nil {
					continue
				}
				toolCalls, _ := delta["tool_calls"].([]any)
				for _, tc := range toolCalls {
					tcMap, _ := tc.(map[string]any)
					fn, _ := tcMap["function"].(map[string]any)
					if fn == nil {
						continue
					}
					if name, _ := fn["name"].(string); name != "" {
						nameChunks++
					}
					if args, _ := fn["arguments"].(string); args != "" {
						argChunks++
					}
				}
			}
			return
		}

		Context("Single tool call via Go-side JSON parser", func() {
			It("should emit exactly one tool call name without duplicates", func() {
				body := `{
					"model": "mock-model-autoparser",
					"messages": [{"role": "user", "content": "SINGLE_TOOL_CALL"}],
					"tools": [{"type": "function", "function": {"name": "get_weather", "description": "Get weather", "parameters": {"type": "object", "properties": {"location": {"type": "string"}}, "required": ["location"]}}}],
					"stream": true
				}`
				req, err := http.NewRequest("POST", apiURL+"/chat/completions", strings.NewReader(body))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				httpClient := &http.Client{Timeout: 60 * time.Second}
				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())

				nameChunks, argChunks := parseToolCallChunks(data)

				Expect(nameChunks).To(Equal(1),
					"Expected exactly 1 tool call name chunk, got %d. Full SSE:\n%s",
					nameChunks, string(data))
				Expect(argChunks).To(BeNumerically(">=", 1),
					"Expected at least 1 arguments chunk. Full SSE:\n%s", string(data))
			})
		})

		Context("ChatDelta tool calls (regression guard)", func() {
			It("should emit exactly one tool call name per tool", func() {
				body := `{
					"model": "mock-model-autoparser",
					"messages": [{"role": "user", "content": "AUTOPARSER_TOOL_CALL"}],
					"tools": [{"type": "function", "function": {"name": "search_collections", "description": "Search documents", "parameters": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}}}],
					"stream": true
				}`
				req, err := http.NewRequest("POST", apiURL+"/chat/completions", strings.NewReader(body))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				httpClient := &http.Client{Timeout: 60 * time.Second}
				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())

				nameChunks, _ := parseToolCallChunks(data)

				Expect(nameChunks).To(Equal(1),
					"Expected exactly 1 tool call name chunk from ChatDeltas, got %d. Full SSE:\n%s",
					nameChunks, string(data))
			})
		})

		Context("Multiple tool calls via Go-side JSON parser", func() {
			It("should emit exactly two tool call names without duplicates", func() {
				body := `{
					"model": "mock-model-autoparser",
					"messages": [{"role": "user", "content": "MULTI_TOOL_CALL"}],
					"tools": [{"type": "function", "function": {"name": "get_weather", "description": "Get weather", "parameters": {"type": "object", "properties": {"location": {"type": "string"}}, "required": ["location"]}}}],
					"stream": true
				}`
				req, err := http.NewRequest("POST", apiURL+"/chat/completions", strings.NewReader(body))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				httpClient := &http.Client{Timeout: 60 * time.Second}
				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				data, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())

				nameChunks, argChunks := parseToolCallChunks(data)

				Expect(nameChunks).To(Equal(2),
					"Expected exactly 2 tool call name chunks (one per tool), got %d. Full SSE:\n%s",
					nameChunks, string(data))
				Expect(argChunks).To(BeNumerically(">=", 2),
					"Expected at least 2 arguments chunks. Full SSE:\n%s", string(data))
			})
		})
	})
})
