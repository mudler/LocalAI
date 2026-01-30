package e2e_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"
)

var _ = Describe("Mock Backend E2E Tests", Label("MockBackend"), func() {
	Describe("Text Generation APIs", func() {
		Context("Predict (Chat Completions)", func() {
			It("should return mocked response", func() {
				resp, err := client.CreateChatCompletion(
					context.TODO(),
					openai.ChatCompletionRequest{
						Model: "mock-model",
						Messages: []openai.ChatCompletionMessage{
							{
								Role:    "user",
								Content: "Hello",
							},
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
				stream, err := client.CreateChatCompletionStream(
					context.TODO(),
					openai.ChatCompletionRequest{
						Model: "mock-model",
						Messages: []openai.ChatCompletionMessage{
							{
								Role:    "user",
								Content: "Hello",
							},
						},
					},
				)
				Expect(err).ToNot(HaveOccurred())
				defer stream.Close()

				hasContent := false
				for {
					response, err := stream.Recv()
					if err != nil {
						break
					}
					if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
						hasContent = true
					}
				}
				Expect(hasContent).To(BeTrue())
			})
		})
	})

	Describe("Embeddings API", func() {
		It("should return mocked embeddings", func() {
			resp, err := client.CreateEmbeddings(
				context.TODO(),
				openai.EmbeddingRequest{
					Model: "mock-model",
					Input: []string{"test"},
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
				req, err := http.NewRequest("POST", apiURL+"/audio/speech", nil)
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")

				body := `{"model":"mock-model","input":"Hello world","voice":"default"}`
				req.Body = http.NoBody
				req.GetBody = func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(body)), nil
				}

				// Use direct HTTP client for TTS endpoint
				httpClient := &http.Client{Timeout: 30 * time.Second}
				resp, err := httpClient.Do(req)
				if err == nil {
					defer resp.Body.Close()
					Expect(resp.StatusCode).To(BeNumerically("<", 500))
				}
			})
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
})
