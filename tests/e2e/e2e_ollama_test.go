package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/ollama/ollama/api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Ollama API E2E test", Label("Ollama"), func() {
	var client *api.Client

	Context("API with Ollama client", func() {
		BeforeEach(func() {
			u, err := url.Parse(ollamaBaseURL)
			Expect(err).ToNot(HaveOccurred())
			client = api.NewClient(u, http.DefaultClient)
		})

		Context("Model management", func() {
			It("lists available models via /api/tags", func() {
				resp, err := client.List(context.TODO())
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.Models).ToNot(BeEmpty())

				// Find mock-model and validate its fields
				var found *api.ListModelResponse
				for i, m := range resp.Models {
					if m.Name == "mock-model:latest" {
						found = &resp.Models[i]
						break
					}
				}
				Expect(found).ToNot(BeNil(), "mock-model:latest should be in the list")
				Expect(found.Model).To(Equal("mock-model:latest"))
				Expect(found.Digest).ToNot(BeEmpty())
				Expect(found.ModifiedAt).ToNot(BeZero())
			})

			It("shows model details via /api/show", func() {
				resp, err := client.Show(context.TODO(), &api.ShowRequest{
					Name: "mock-model",
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.Modelfile).To(ContainSubstring("FROM"))
				Expect(resp.Details.Format).To(Equal("gguf"))
			})

			It("returns 404 for unknown model in /api/show", func() {
				_, err := client.Show(context.TODO(), &api.ShowRequest{
					Name: "nonexistent-model",
				})
				Expect(err).To(HaveOccurred())
			})

			It("returns version via /api/version", func() {
				version, err := client.Version(context.TODO())
				Expect(err).ToNot(HaveOccurred())
				Expect(version).ToNot(BeEmpty())
				// Should be a semver-like string
				Expect(version).To(MatchRegexp(`^\d+\.\d+\.\d+`))
			})

			It("responds to HEAD /api/version", func() {
				req, err := http.NewRequest("HEAD", fmt.Sprintf("%s/api/version", ollamaBaseURL), nil)
				Expect(err).ToNot(HaveOccurred())
				resp, err := http.DefaultClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))
			})

			It("responds to HEAD /api/tags", func() {
				req, err := http.NewRequest("HEAD", fmt.Sprintf("%s/api/tags", ollamaBaseURL), nil)
				Expect(err).ToNot(HaveOccurred())
				resp, err := http.DefaultClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))
			})

			// Heartbeat (HEAD /) requires the OllamaAPIRootEndpoint CLI flag
			// which is not enabled in the default test setup.

			It("lists running models via /api/ps after a model has been loaded", func() {
				// First, trigger a chat to ensure the model is loaded
				stream := false
				err := client.Chat(context.TODO(), &api.ChatRequest{
					Model:    "mock-model",
					Messages: []api.Message{{Role: "user", Content: "ping"}},
					Stream:   &stream,
				}, func(resp api.ChatResponse) error { return nil })
				Expect(err).ToNot(HaveOccurred())

				// Now check ps
				resp, err := client.ListRunning(context.TODO())
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.Models).ToNot(BeEmpty(), "at least one model should be loaded after chat")

				var found bool
				for _, m := range resp.Models {
					if m.Name == "mock-model:latest" {
						found = true
						Expect(m.Digest).ToNot(BeEmpty())
						break
					}
				}
				Expect(found).To(BeTrue(), "mock-model should appear in running models")
			})
		})

		Context("Chat endpoint", func() {
			It("generates a non-streaming chat response with valid fields", func() {
				stream := false
				var finalResp api.ChatResponse

				err := client.Chat(context.TODO(), &api.ChatRequest{
					Model: "mock-model",
					Messages: []api.Message{
						{Role: "user", Content: "How much is 2+2?"},
					},
					Stream: &stream,
				}, func(resp api.ChatResponse) error {
					finalResp = resp
					return nil
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(finalResp.Done).To(BeTrue())
				Expect(finalResp.DoneReason).To(Equal("stop"))
				Expect(finalResp.Message.Role).To(Equal("assistant"))
				Expect(finalResp.Message.Content).ToNot(BeEmpty())
				Expect(finalResp.Model).To(Equal("mock-model"))
				Expect(finalResp.CreatedAt).ToNot(BeZero())
				Expect(finalResp.TotalDuration).To(BeNumerically(">", 0))
			})

			It("streams tokens incrementally", func() {
				stream := true
				var chunks []api.ChatResponse

				err := client.Chat(context.TODO(), &api.ChatRequest{
					Model: "mock-model",
					Messages: []api.Message{
						{Role: "user", Content: "Say hello"},
					},
					Stream: &stream,
				}, func(resp api.ChatResponse) error {
					chunks = append(chunks, resp)
					return nil
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(chunks)).To(BeNumerically(">=", 2), "should have at least one content chunk + done chunk")

				// Last chunk must be the done signal
				lastChunk := chunks[len(chunks)-1]
				Expect(lastChunk.Done).To(BeTrue())
				Expect(lastChunk.DoneReason).To(Equal("stop"))
				Expect(lastChunk.TotalDuration).To(BeNumerically(">", 0))

				// Non-final chunks should carry content
				hasContent := false
				for _, c := range chunks[:len(chunks)-1] {
					if c.Message.Content != "" {
						hasContent = true
						break
					}
				}
				Expect(hasContent).To(BeTrue(), "intermediate streaming chunks should carry token content")
			})

			It("handles multi-turn conversation with system prompt", func() {
				stream := false
				var finalResp api.ChatResponse

				err := client.Chat(context.TODO(), &api.ChatRequest{
					Model: "mock-model",
					Messages: []api.Message{
						{Role: "system", Content: "You are a helpful assistant."},
						{Role: "user", Content: "What is Go?"},
						{Role: "assistant", Content: "Go is a programming language."},
						{Role: "user", Content: "Who created it?"},
					},
					Stream: &stream,
				}, func(resp api.ChatResponse) error {
					finalResp = resp
					return nil
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(finalResp.Done).To(BeTrue())
				Expect(finalResp.Message.Content).ToNot(BeEmpty())
			})
		})

		Context("Generate endpoint", func() {
			It("generates a non-streaming response with valid fields", func() {
				stream := false
				var finalResp api.GenerateResponse

				err := client.Generate(context.TODO(), &api.GenerateRequest{
					Model:  "mock-model",
					Prompt: "Once upon a time",
					Stream: &stream,
				}, func(resp api.GenerateResponse) error {
					finalResp = resp
					return nil
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(finalResp.Done).To(BeTrue())
				Expect(finalResp.DoneReason).To(Equal("stop"))
				Expect(finalResp.Response).ToNot(BeEmpty())
				Expect(finalResp.Model).To(Equal("mock-model"))
				Expect(finalResp.CreatedAt).ToNot(BeZero())
				Expect(finalResp.TotalDuration).To(BeNumerically(">", 0))
			})

			It("streams tokens incrementally", func() {
				stream := true
				var chunks []api.GenerateResponse

				err := client.Generate(context.TODO(), &api.GenerateRequest{
					Model:  "mock-model",
					Prompt: "Tell me a story",
					Stream: &stream,
				}, func(resp api.GenerateResponse) error {
					chunks = append(chunks, resp)
					return nil
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(chunks)).To(BeNumerically(">=", 2))

				lastChunk := chunks[len(chunks)-1]
				Expect(lastChunk.Done).To(BeTrue())
				Expect(lastChunk.DoneReason).To(Equal("stop"))

				// Check that intermediate chunks have response text
				hasContent := false
				for _, c := range chunks[:len(chunks)-1] {
					if c.Response != "" {
						hasContent = true
						break
					}
				}
				Expect(hasContent).To(BeTrue(), "intermediate streaming chunks should carry token content")
			})

			It("returns load response for empty prompt", func() {
				stream := false
				var finalResp api.GenerateResponse

				err := client.Generate(context.TODO(), &api.GenerateRequest{
					Model:  "mock-model",
					Prompt: "",
					Stream: &stream,
				}, func(resp api.GenerateResponse) error {
					finalResp = resp
					return nil
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(finalResp.Done).To(BeTrue())
				Expect(finalResp.DoneReason).To(Equal("load"))
			})

			It("supports system prompt in generate", func() {
				stream := false
				var finalResp api.GenerateResponse

				err := client.Generate(context.TODO(), &api.GenerateRequest{
					Model:  "mock-model",
					Prompt: "Hello",
					System: "You are a pirate.",
					Stream: &stream,
				}, func(resp api.GenerateResponse) error {
					finalResp = resp
					return nil
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(finalResp.Done).To(BeTrue())
				Expect(finalResp.Response).ToNot(BeEmpty())
			})
		})

		Context("Embed endpoint", func() {
			It("generates embeddings for a single input via /api/embed", func() {
				resp, err := client.Embed(context.TODO(), &api.EmbedRequest{
					Model: "mock-model",
					Input: "Hello, world!",
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.Embeddings).To(HaveLen(1))
				Expect(len(resp.Embeddings[0])).To(BeNumerically(">", 0), "embedding vector should have dimensions")
				Expect(resp.Model).To(Equal("mock-model"))
			})

			It("generates embeddings via the legacy /api/embeddings alias", func() {
				// The ollama client uses /api/embed, so test the legacy endpoint with raw HTTP
				body := map[string]any{
					"model": "mock-model",
					"input": "test input",
				}
				bodyJSON, err := json.Marshal(body)
				Expect(err).ToNot(HaveOccurred())

				resp, err := http.Post(
					fmt.Sprintf("%s/api/embeddings", ollamaBaseURL),
					"application/json",
					bytes.NewReader(bodyJSON),
				)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(200))

				var result map[string]any
				respBody, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				Expect(json.Unmarshal(respBody, &result)).To(Succeed())
				Expect(result).To(HaveKey("embeddings"))
			})
		})

		Context("Error handling", func() {
			It("returns error for chat with unknown model", func() {
				stream := false
				err := client.Chat(context.TODO(), &api.ChatRequest{
					Model:    "nonexistent-model-xyz",
					Messages: []api.Message{{Role: "user", Content: "hi"}},
					Stream:   &stream,
				}, func(resp api.ChatResponse) error { return nil })
				Expect(err).To(HaveOccurred())
			})

			It("returns error for generate with unknown model", func() {
				stream := false
				err := client.Generate(context.TODO(), &api.GenerateRequest{
					Model:  "nonexistent-model-xyz",
					Prompt: "hi",
					Stream: &stream,
				}, func(resp api.GenerateResponse) error { return nil })
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
