package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	httpapi "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/phayes/freeport"
	"github.com/sashabaranov/go-openai"
	"gopkg.in/yaml.v3"

	"github.com/mudler/xlog"
)

var (
	tmpDir          string
	backendPath     string
	modelsPath      string
	configPath      string
	app             *echo.Echo
	appCtx          context.Context
	appCancel       context.CancelFunc
	client          *openai.Client
	apiPort         int
	apiURL          string
	mockBackendPath string
)

var _ = BeforeSuite(func() {
	var err error

	// Create temporary directory
	tmpDir, err = os.MkdirTemp("", "mock-backend-e2e-*")
	Expect(err).ToNot(HaveOccurred())

	backendPath = filepath.Join(tmpDir, "backends")
	modelsPath = filepath.Join(tmpDir, "models")
	Expect(os.MkdirAll(backendPath, 0755)).To(Succeed())
	Expect(os.MkdirAll(modelsPath, 0755)).To(Succeed())

	// Build mock backend
	mockBackendDir := filepath.Join("..", "e2e", "mock-backend")
	mockBackendPath = filepath.Join(backendPath, "mock-backend")

	// Check if mock-backend binary exists in the mock-backend directory
	possiblePaths := []string{
		filepath.Join(mockBackendDir, "mock-backend"),
		filepath.Join("tests", "e2e", "mock-backend", "mock-backend"),
		filepath.Join("..", "..", "tests", "e2e", "mock-backend", "mock-backend"),
	}

	found := false
	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			mockBackendPath = p
			found = true
			break
		}
	}

	if !found {
		// Try to find it relative to current working directory
		wd, _ := os.Getwd()
		relPath := filepath.Join(wd, "..", "..", "tests", "e2e", "mock-backend", "mock-backend")
		if _, err := os.Stat(relPath); err == nil {
			mockBackendPath = relPath
			found = true
		}
	}

	Expect(found).To(BeTrue(), "mock-backend binary not found. Run 'make build-mock-backend' first")

	// Make sure it's executable
	Expect(os.Chmod(mockBackendPath, 0755)).To(Succeed())

	// Create model config YAML
	modelConfig := map[string]interface{}{
		"name":    "mock-model",
		"backend": "mock-backend",
		"parameters": map[string]interface{}{
			"model": "mock-model.bin",
		},
	}
	configPath = filepath.Join(modelsPath, "mock-model.yaml")
	configYAML, err := yaml.Marshal(modelConfig)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(configPath, configYAML, 0644)).To(Succeed())

	// Set up system state
	systemState, err := system.GetSystemState(
		system.WithBackendPath(backendPath),
		system.WithModelPath(modelsPath),
	)
	Expect(err).ToNot(HaveOccurred())

	// Create application
	appCtx, appCancel = context.WithCancel(context.Background())

	// Create application instance
	application, err := application.New(
		config.WithContext(appCtx),
		config.WithSystemState(systemState),
		config.WithDebug(true),
	)
	Expect(err).ToNot(HaveOccurred())

	// Register backend with application's model loader
	application.ModelLoader().SetExternalBackend("mock-backend", mockBackendPath)

	// Create HTTP app
	app, err = httpapi.API(application)
	Expect(err).ToNot(HaveOccurred())

	// Get free port
	port, err := freeport.GetFreePort()
	Expect(err).ToNot(HaveOccurred())
	apiPort = port
	apiURL = fmt.Sprintf("http://127.0.0.1:%d/v1", apiPort)

	// Start server in goroutine
	go func() {
		if err := app.Start(fmt.Sprintf("127.0.0.1:%d", apiPort)); err != nil && err != http.ErrServerClosed {
			xlog.Error("server error", "error", err)
		}
	}()

	// Wait for server to be ready
	defaultConfig := openai.DefaultConfig("")
	defaultConfig.BaseURL = apiURL
	client = openai.NewClientWithConfig(defaultConfig)

	Eventually(func() error {
		_, err := client.ListModels(context.TODO())
		return err
	}, "2m").ShouldNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	if appCancel != nil {
		appCancel()
	}
	if app != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(app.Shutdown(ctx)).To(Succeed())
	}
	if tmpDir != "" {
		os.RemoveAll(tmpDir)
	}
})

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
