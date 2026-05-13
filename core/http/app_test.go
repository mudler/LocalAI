package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/xlog"
	openaigo "github.com/otiai10/openaigo"
	"github.com/sashabaranov/go-openai"
)

const apiKey = "joshua"
const bearerKey = "Bearer " + apiKey

const testPrompt = `### System:
You are an AI assistant that follows instruction extremely well. Help as much as you can.

### Instruction:

Say hello.

### Response:`

type modelApplyRequest struct {
	ID        string         `json:"id"`
	URL       string         `json:"url"`
	ConfigURL string         `json:"config_url"`
	Name      string         `json:"name"`
	Overrides map[string]any `json:"overrides"`
}

func getModelStatus(url string) (response map[string]any) {
	// Create the HTTP request
	req, err := http.NewRequest("GET", url, nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerKey)
	if err != nil {
		fmt.Println("Error creating request:", err)
		return
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error sending request:", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error reading response body:", err)
		return
	}

	// Unmarshal the response into a map[string]interface{}
	err = json.Unmarshal(body, &response)
	if err != nil {
		fmt.Println("Error unmarshaling JSON response:", err)
		return
	}
	return
}

func getModels(url string) ([]gallery.GalleryModel, error) {
	response := []gallery.GalleryModel{}
	uri := downloader.URI(url)
	// TODO: No tests currently seem to exercise file:// urls. Fix?
	err := uri.ReadWithAuthorizationAndCallback(context.TODO(), "", bearerKey, func(url string, i []byte) error {
		// Unmarshal YAML data into a struct
		return json.Unmarshal(i, &response)
	})
	return response, err
}

func postModelApplyRequest(url string, request modelApplyRequest) (response map[string]any) {

	//url := "http://localhost:AI/models/apply"

	// Create the request payload

	payload, err := json.Marshal(request)
	if err != nil {
		fmt.Println("Error marshaling JSON:", err)
		return
	}

	// Create the HTTP request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		fmt.Println("Error creating request:", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerKey)

	// Make the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error reading response body:", err)
		return
	}

	// Unmarshal the response into a map[string]interface{}
	err = json.Unmarshal(body, &response)
	if err != nil {
		fmt.Println("Error unmarshaling JSON response:", err)
		return
	}
	return
}

func postRequestJSON[B any](url string, bodyJson *B) error {
	payload, err := json.Marshal(bodyJson)
	if err != nil {
		return err
	}

	GinkgoWriter.Printf("POST %s: %s\n", url, string(payload))

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

func postRequestResponseJSON[B1 any, B2 any](url string, reqJson *B1, respJson *B2) error {
	payload, err := json.Marshal(reqJson)
	if err != nil {
		return err
	}

	GinkgoWriter.Printf("POST %s: %s\n", url, string(payload))

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	return json.Unmarshal(body, respJson)
}

func putRequestJSON[B any](url string, bodyJson *B) error {
	payload, err := json.Marshal(bodyJson)
	if err != nil {
		return err
	}

	GinkgoWriter.Printf("PUT %s: %s\n", url, string(payload))

	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

func postInvalidRequest(url string) (error, int) {

	req, err := http.NewRequest("POST", url, bytes.NewBufferString("invalid request"))
	if err != nil {
		return err, -1
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err, -1
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err, -1
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body)), resp.StatusCode
	}

	return nil, resp.StatusCode
}

func getRequest(url string, header http.Header) (error, int, []byte) {

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err, -1, nil
	}

	req.Header = header

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err, -1, nil
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err, -1, nil
	}

	return nil, resp.StatusCode, body
}

const bertEmbeddingsURL = `https://gist.githubusercontent.com/mudler/0a080b166b87640e8644b09c2aee6e3b/raw/f0e8c26bb72edc16d9fbafbfd6638072126ff225/bert-embeddings-gallery.yaml`

var _ = Describe("API test", func() {

	var app *echo.Echo
	var client *openai.Client
	var client2 *openaigo.Client
	var c context.Context
	var cancel context.CancelFunc
	var tmpdir string
	var modelDir string

	commonOpts := []config.AppOption{
		config.WithDebug(true),
	}

	Context("API with ephemeral models", func() {

		BeforeEach(func(sc SpecContext) {
			var err error
			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())

			// No real backends needed — these specs cover gallery API, auth,
			// routing, and file:// import. Use the suite-level empty backend dir.
			backendPath := backendDir

			modelDir = filepath.Join(tmpdir, "models")
			err = os.Mkdir(modelDir, 0750)
			Expect(err).ToNot(HaveOccurred())

			c, cancel = context.WithCancel(context.Background())

			g := []gallery.GalleryModel{
				{
					Metadata: gallery.Metadata{
						Name: "bert",
						URL:  bertEmbeddingsURL,
					},
					Overrides: map[string]any{"backend": "llama-cpp"},
				},
				{
					Metadata: gallery.Metadata{
						Name:            "bert2",
						URL:             bertEmbeddingsURL,
						AdditionalFiles: []gallery.File{{Filename: "foo.yaml", URI: bertEmbeddingsURL}},
					},
					Overrides: map[string]any{"foo": "bar"},
				},
			}
			out, err := yaml.Marshal(g)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(filepath.Join(modelDir, "gallery_simple.yaml"), out, 0600)
			Expect(err).ToNot(HaveOccurred())

			galleries := []config.Gallery{
				{
					Name: "test",
					URL:  "file://" + filepath.Join(modelDir, "gallery_simple.yaml"),
				},
			}

			systemState, err := system.GetSystemState(
				system.WithBackendPath(backendPath),
				system.WithModelPath(modelDir),
			)
			Expect(err).ToNot(HaveOccurred())

			application, err := application.New(
				append(commonOpts,
					config.WithContext(c),
					config.WithSystemState(systemState),
					config.WithGalleries(galleries),
					config.WithApiKeys([]string{apiKey}),
				)...)
			Expect(err).ToNot(HaveOccurred())

			app, err = API(application)
			Expect(err).ToNot(HaveOccurred())

			go func() {
				if err := app.Start("127.0.0.1:9090"); err != nil && err != http.ErrServerClosed {
					xlog.Error("server error", "error", err)
				}
			}()

			defaultConfig := openai.DefaultConfig(apiKey)
			defaultConfig.BaseURL = "http://127.0.0.1:9090/v1"

			client2 = openaigo.NewClient("")
			client2.BaseURL = defaultConfig.BaseURL

			// Wait for API to be ready
			client = openai.NewClientWithConfig(defaultConfig)
			Eventually(func() error {
				_, err := client.ListModels(context.TODO())
				return err
			}, "2m").ShouldNot(HaveOccurred())
		})

		AfterEach(func(sc SpecContext) {
			cancel()
			if app != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := app.Shutdown(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			err := os.RemoveAll(tmpdir)
			Expect(err).ToNot(HaveOccurred())
			_, err = os.ReadDir(tmpdir)
			Expect(err).To(HaveOccurred())
		})

		Context("Auth Tests", func() {
			It("Should fail if the api key is missing", func() {
				err, sc := postInvalidRequest("http://127.0.0.1:9090/models/available")
				Expect(err).ToNot(BeNil())
				Expect(sc).To(Equal(401))
			})
		})

		Context("URL routing Tests", func() {
			It("Should support reverse-proxy when unauthenticated", func() {

				err, sc, body := getRequest("http://127.0.0.1:9090/myprefix/", http.Header{
					"X-Forwarded-Proto":  {"https"},
					"X-Forwarded-Host":   {"example.org"},
					"X-Forwarded-Prefix": {"/myprefix/"},
				})
				Expect(err).To(BeNil(), "error")
				Expect(sc).To(Equal(200), "status code")
				// Non-API paths pass through to the React SPA (which handles login client-side)
				Expect(string(body)).To(ContainSubstring(`<base href="https://example.org/myprefix/" />`), "body")
				Expect(string(body)).To(ContainSubstring(`<div id="root">`), "should serve React SPA")
			})

			It("Should support reverse-proxy when authenticated", func() {

				err, sc, body := getRequest("http://127.0.0.1:9090/myprefix/", http.Header{
					"Authorization":      {bearerKey},
					"X-Forwarded-Proto":  {"https"},
					"X-Forwarded-Host":   {"example.org"},
					"X-Forwarded-Prefix": {"/myprefix/"},
				})
				Expect(err).To(BeNil(), "error")
				Expect(sc).To(Equal(200), "status code")
				Expect(string(body)).To(ContainSubstring(`<base href="https://example.org/myprefix/" />`), "body")
			})

			// Caddy's `handle_path` (and similar directives) strip the matched
			// prefix before forwarding upstream, so LocalAI receives the
			// already-stripped path together with X-Forwarded-Prefix. The base
			// href and asset URLs must still include the prefix so the browser
			// requests them through the proxy.
			It("Should support reverse-proxy when prefix is stripped by the proxy", func() {

				err, sc, body := getRequest("http://127.0.0.1:9090/app", http.Header{
					"X-Forwarded-Proto":  {"https"},
					"X-Forwarded-Host":   {"example.org"},
					"X-Forwarded-Prefix": {"/myprefix"},
				})
				Expect(err).To(BeNil(), "error")
				Expect(sc).To(Equal(200), "status code")
				Expect(string(body)).To(ContainSubstring(`<base href="https://example.org/myprefix/" />`), "body")
				Expect(string(body)).ToNot(ContainSubstring(`="/assets/`), "asset URLs must include the prefix")
				Expect(string(body)).ToNot(ContainSubstring(`="/favicon.svg"`), "favicon URL must include the prefix")
			})

			// X-Forwarded-Prefix is attacker controllable on misconfigured
			// proxy chains. A value like "//evil.com" would otherwise turn the
			// asset URL rewrite into a protocol-relative URL that loads JS
			// from a foreign origin. BasePathPrefix must reject these via
			// SafeForwardedPrefix and fall back to "/".
			It("Should ignore an unsafe X-Forwarded-Prefix and not poison asset URLs", func() {
				err, sc, body := getRequest("http://127.0.0.1:9090/app", http.Header{
					"X-Forwarded-Proto":  {"https"},
					"X-Forwarded-Host":   {"example.org"},
					"X-Forwarded-Prefix": {"//evil.com"},
				})
				Expect(err).To(BeNil(), "error")
				Expect(sc).To(Equal(200), "status code")
				Expect(string(body)).ToNot(ContainSubstring("evil.com"), "unsafe prefix must not leak into the response")
				Expect(string(body)).ToNot(ContainSubstring(`="//`), "asset URLs must not become protocol-relative")
			})
		})

		Context("Applying models", func() {

			It("applies models from a gallery", func() {
				models, err := getModels("http://127.0.0.1:9090/models/available")
				Expect(err).To(BeNil())
				Expect(len(models)).To(Equal(2), fmt.Sprint(models))
				Expect(models[0].Installed).To(BeFalse(), fmt.Sprint(models))
				Expect(models[1].Installed).To(BeFalse(), fmt.Sprint(models))

				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					ID: "test@bert2",
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)
				resp := map[string]any{}
				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					fmt.Println(response)
					resp = response
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))
				Expect(resp["message"]).ToNot(ContainSubstring("error"))

				dat, err := os.ReadFile(filepath.Join(modelDir, "bert2.yaml"))
				Expect(err).ToNot(HaveOccurred())

				_, err = os.ReadFile(filepath.Join(modelDir, "foo.yaml"))
				Expect(err).ToNot(HaveOccurred())

				content := map[string]any{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["usage"]).To(ContainSubstring("You can test this model with curl like this"))
				Expect(content["foo"]).To(Equal("bar"))

				models, err = getModels("http://127.0.0.1:9090/models/available")
				Expect(err).To(BeNil())
				Expect(len(models)).To(Equal(2), fmt.Sprint(models))
				Expect(models[0].Name).To(Or(Equal("bert"), Equal("bert2")))
				Expect(models[1].Name).To(Or(Equal("bert"), Equal("bert2")))
				for _, m := range models {
					if m.Name == "bert2" {
						Expect(m.Installed).To(BeTrue())
					} else {
						Expect(m.Installed).To(BeFalse())
					}
				}
			})
			It("overrides models", func() {

				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					URL:  bertEmbeddingsURL,
					Name: "bert",
					Overrides: map[string]any{
						"backend": "llama",
					},
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))

				dat, err := os.ReadFile(filepath.Join(modelDir, "bert.yaml"))
				Expect(err).ToNot(HaveOccurred())

				content := map[string]any{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["backend"]).To(Equal("llama"))
			})
			It("apply models without overrides", func() {
				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					URL:       bertEmbeddingsURL,
					Name:      "bert",
					Overrides: map[string]any{},
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))

				dat, err := os.ReadFile(filepath.Join(modelDir, "bert.yaml"))
				Expect(err).ToNot(HaveOccurred())

				content := map[string]any{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["usage"]).To(ContainSubstring("You can test this model with curl like this"))
			})

		})

		Context("Importing models from URI", func() {
			var testYamlFile string

			BeforeEach(func() {
				// Create a test YAML config file
				yamlContent := `name: test-import-model
backend: llama-cpp
description: Test model imported from file URI
parameters:
  model: path/to/model.gguf
  temperature: 0.7
`
				testYamlFile = filepath.Join(tmpdir, "test-import.yaml")
				err := os.WriteFile(testYamlFile, []byte(yamlContent), 0644)
				Expect(err).ToNot(HaveOccurred())
			})

			AfterEach(func() {
				err := os.Remove(testYamlFile)
				Expect(err).ToNot(HaveOccurred())
			})

			It("should import model from file:// URI pointing to local YAML config", func() {
				importReq := schema.ImportModelRequest{
					URI:         "file://" + testYamlFile,
					Preferences: json.RawMessage(`{}`),
				}

				var response schema.GalleryResponse
				err := postRequestResponseJSON("http://127.0.0.1:9090/models/import-uri", &importReq, &response)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.ID).ToNot(BeEmpty())

				uuid := response.ID
				resp := map[string]any{}
				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					resp = response
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))

				// Check that the model was imported successfully
				Expect(resp["message"]).ToNot(ContainSubstring("error"))
				Expect(resp["error"]).To(BeNil())

				// Verify the model config file was created
				dat, err := os.ReadFile(filepath.Join(modelDir, "test-import-model.yaml"))
				Expect(err).ToNot(HaveOccurred())

				content := map[string]any{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["name"]).To(Equal("test-import-model"))
				Expect(content["backend"]).To(Equal("llama-cpp"))
			})

			It("should return error when file:// URI points to non-existent file", func() {
				nonExistentFile := filepath.Join(tmpdir, "nonexistent.yaml")
				importReq := schema.ImportModelRequest{
					URI:         "file://" + nonExistentFile,
					Preferences: json.RawMessage(`{}`),
				}

				var response schema.GalleryResponse
				err := postRequestResponseJSON("http://127.0.0.1:9090/models/import-uri", &importReq, &response)
				// The endpoint should return an error immediately
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to discover model config"))
			})
		})

		Context("Importing models from URI can't point to absolute paths", func() {
			var testYamlFile string

			BeforeEach(func() {
				// Create a test YAML config file
				yamlContent := `name: test-import-model
backend: llama-cpp
description: Test model imported from file URI
parameters:
  model: /path/to/model.gguf
  temperature: 0.7
`
				testYamlFile = filepath.Join(tmpdir, "test-import.yaml")
				err := os.WriteFile(testYamlFile, []byte(yamlContent), 0644)
				Expect(err).ToNot(HaveOccurred())
			})

			AfterEach(func() {
				err := os.Remove(testYamlFile)
				Expect(err).ToNot(HaveOccurred())
			})

			It("should fail to import model from file:// URI pointing to local YAML config", func() {
				importReq := schema.ImportModelRequest{
					URI:         "file://" + testYamlFile,
					Preferences: json.RawMessage(`{}`),
				}

				var response schema.GalleryResponse
				err := postRequestResponseJSON("http://127.0.0.1:9090/models/import-uri", &importReq, &response)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.ID).ToNot(BeEmpty())

				uuid := response.ID
				resp := map[string]any{}
				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					resp = response
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))

				// Check that the model was imported successfully
				Expect(resp["message"]).To(ContainSubstring("error"))
				Expect(resp["error"]).ToNot(BeNil())
			})
		})
	})

	Context("API query", func() {
		BeforeEach(func() {
			if mockBackendPath == "" {
				Skip("mock-backend binary not built; run 'make build-mock-backend'")
			}
			c, cancel = context.WithCancel(context.Background())

			// Stand up an isolated model dir for this Context so the suite can
			// register a mock-model config (read by /v1/models, /system, and the
			// agent-jobs flow) without depending on real backend builds.
			var err error
			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			modelDir = filepath.Join(tmpdir, "models")
			Expect(os.Mkdir(modelDir, 0750)).To(Succeed())

			mockModelYAML := `name: mock-model
backend: mock-backend
parameters:
  model: mock-model.bin
`
			Expect(os.WriteFile(filepath.Join(modelDir, "mock-model.yaml"), []byte(mockModelYAML), 0644)).To(Succeed())

			systemState, err := system.GetSystemState(
				system.WithBackendPath(backendDir),
				system.WithModelPath(modelDir),
			)
			Expect(err).ToNot(HaveOccurred())

			application, err := application.New(
				append(commonOpts,
					config.WithContext(c),
					config.WithSystemState(systemState),
				)...)
			Expect(err).ToNot(HaveOccurred())
			application.ModelLoader().SetExternalBackend("mock-backend", mockBackendPath)
			app, err = API(application)
			Expect(err).ToNot(HaveOccurred())
			go func() {
				if err := app.Start("127.0.0.1:9090"); err != nil && err != http.ErrServerClosed {
					xlog.Error("server error", "error", err)
				}
			}()

			defaultConfig := openai.DefaultConfig("")
			defaultConfig.BaseURL = "http://127.0.0.1:9090/v1"

			client2 = openaigo.NewClient("")
			client2.BaseURL = defaultConfig.BaseURL

			// Wait for API to be ready
			client = openai.NewClientWithConfig(defaultConfig)
			Eventually(func() error {
				_, err := client.ListModels(context.TODO())
				return err
			}, "2m").ShouldNot(HaveOccurred())
		})
		AfterEach(func() {
			cancel()
			if app != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := app.Shutdown(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(os.RemoveAll(tmpdir)).To(Succeed())
		})
		It("returns the models list", func() {
			models, err := client.ListModels(context.TODO())
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models.Models)).To(BeNumerically(">=", 1))
		})

		It("returns errors", func() {
			_, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "foomodel", Prompt: testPrompt})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("error, status code: 404, status: 404 Not Found"))
		})

		It("shows the external backend on /system", func() {
			// /system reports the backends available to the application.
			// Mock-backend is registered via SetExternalBackend so it appears
			// alongside any built-in entries; verifying that string proves the
			// endpoint is wired up regardless of which real backends exist.
			resp, err := http.Get("http://127.0.0.1:9090/system")
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(200))
			dat, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(dat)).To(ContainSubstring("mock-backend"))
		})

		// Agent Jobs: HTTP API for task/job scheduling. The underlying AgentPool
		// service is exercised in core/services/agentpool/agent_jobs_test.go;
		// these specs cover the /api/agent/* HTTP plumbing on top.
		Context("Agent Jobs", func() {
			It("creates and manages tasks", func() {
				// Create a task
				taskBody := map[string]any{
					"name":        "Test Task",
					"description": "Test Description",
					"model":       "mock-model",
					"prompt":      "Hello {{.name}}",
					"enabled":     true,
				}

				var createResp map[string]any
				err := postRequestResponseJSON("http://127.0.0.1:9090/api/agent/tasks", &taskBody, &createResp)
				Expect(err).ToNot(HaveOccurred())
				Expect(createResp["id"]).ToNot(BeEmpty())
				taskID := createResp["id"].(string)

				// Get the task
				var task schema.Task
				resp, err := http.Get("http://127.0.0.1:9090/api/agent/tasks/" + taskID)
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
				body, _ := io.ReadAll(resp.Body)
				json.Unmarshal(body, &task)
				Expect(task.Name).To(Equal("Test Task"))

				// List tasks
				resp, err = http.Get("http://127.0.0.1:9090/api/agent/tasks")
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
				var tasks []schema.Task
				body, _ = io.ReadAll(resp.Body)
				json.Unmarshal(body, &tasks)
				Expect(len(tasks)).To(BeNumerically(">=", 1))

				// Update task
				taskBody["name"] = "Updated Task"
				err = putRequestJSON("http://127.0.0.1:9090/api/agent/tasks/"+taskID, &taskBody)
				Expect(err).ToNot(HaveOccurred())

				// Verify update
				resp, err = http.Get("http://127.0.0.1:9090/api/agent/tasks/" + taskID)
				Expect(err).ToNot(HaveOccurred())
				body, _ = io.ReadAll(resp.Body)
				json.Unmarshal(body, &task)
				Expect(task.Name).To(Equal("Updated Task"))

				// Delete task
				req, _ := http.NewRequest("DELETE", "http://127.0.0.1:9090/api/agent/tasks/"+taskID, nil)
				req.Header.Set("Authorization", bearerKey)
				resp, err = http.DefaultClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
			})

			It("executes and monitors jobs", func() {
				// Create a task first
				taskBody := map[string]any{
					"name":    "Job Test Task",
					"model":   "mock-model",
					"prompt":  "Say hello",
					"enabled": true,
				}

				var createResp map[string]any
				err := postRequestResponseJSON("http://127.0.0.1:9090/api/agent/tasks", &taskBody, &createResp)
				Expect(err).ToNot(HaveOccurred())
				taskID := createResp["id"].(string)

				// Execute a job
				jobBody := map[string]any{
					"task_id":    taskID,
					"parameters": map[string]string{},
				}

				var jobResp schema.JobExecutionResponse
				err = postRequestResponseJSON("http://127.0.0.1:9090/api/agent/jobs/execute", &jobBody, &jobResp)
				Expect(err).ToNot(HaveOccurred())
				Expect(jobResp.JobID).ToNot(BeEmpty())
				jobID := jobResp.JobID

				// Get job status
				var job schema.Job
				resp, err := http.Get("http://127.0.0.1:9090/api/agent/jobs/" + jobID)
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
				body, _ := io.ReadAll(resp.Body)
				json.Unmarshal(body, &job)
				Expect(job.ID).To(Equal(jobID))
				Expect(job.TaskID).To(Equal(taskID))

				// List jobs
				resp, err = http.Get("http://127.0.0.1:9090/api/agent/jobs")
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
				var jobs []schema.Job
				body, _ = io.ReadAll(resp.Body)
				json.Unmarshal(body, &jobs)
				Expect(len(jobs)).To(BeNumerically(">=", 1))

				// Cancel job (if still pending/running)
				if job.Status == schema.JobStatusPending || job.Status == schema.JobStatusRunning {
					req, _ := http.NewRequest("POST", "http://127.0.0.1:9090/api/agent/jobs/"+jobID+"/cancel", nil)
					req.Header.Set("Authorization", bearerKey)
					resp, err = http.DefaultClient.Do(req)
					Expect(err).ToNot(HaveOccurred())
					Expect(resp.StatusCode).To(Equal(200))
				}
			})

			It("executes task by name", func() {
				// Create a task with a specific name
				taskBody := map[string]any{
					"name":    "Named Task",
					"model":   "mock-model",
					"prompt":  "Hello",
					"enabled": true,
				}

				var createResp map[string]any
				err := postRequestResponseJSON("http://127.0.0.1:9090/api/agent/tasks", &taskBody, &createResp)
				Expect(err).ToNot(HaveOccurred())

				// Execute by name
				paramsBody := map[string]string{"param1": "value1"}
				var jobResp schema.JobExecutionResponse
				err = postRequestResponseJSON("http://127.0.0.1:9090/api/agent/tasks/Named Task/execute", &paramsBody, &jobResp)
				Expect(err).ToNot(HaveOccurred())
				Expect(jobResp.JobID).ToNot(BeEmpty())
			})
		})
	})

	// Config file Context: exercises the path where models are loaded from a
	// single multi-entry YAML (config_file option) rather than per-model YAMLs
	// in the model dir. The fixtures point at mock-backend so this is a
	// plumbing test for config-file loading and routing, not a real-inference
	// test.
	Context("Config file", func() {
		BeforeEach(func() {
			if mockBackendPath == "" {
				Skip("mock-backend binary not built; run 'make build-mock-backend'")
			}
			c, cancel = context.WithCancel(context.Background())

			var err error
			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			modelDir = filepath.Join(tmpdir, "models")
			Expect(os.Mkdir(modelDir, 0750)).To(Succeed())

			// Inline config file with two list entries that both resolve to mock-backend.
			// Mirrors the legacy testmodel.ggml shape so the test still proves that
			// config-file loading registers each entry as a routable model.
			configContent := `- name: list1
  parameters:
    model: mock-model.bin
  backend: mock-backend
  context_size: 200
- name: list2
  parameters:
    model: mock-model.bin
  backend: mock-backend
  context_size: 200
`
			configFile := filepath.Join(tmpdir, "config.yaml")
			Expect(os.WriteFile(configFile, []byte(configContent), 0644)).To(Succeed())

			systemState, err := system.GetSystemState(
				system.WithBackendPath(backendDir),
				system.WithModelPath(modelDir),
			)
			Expect(err).ToNot(HaveOccurred())

			application, err := application.New(
				append(commonOpts,
					config.WithContext(c),
					config.WithSystemState(systemState),
					config.WithConfigFile(configFile))...,
			)
			Expect(err).ToNot(HaveOccurred())
			application.ModelLoader().SetExternalBackend("mock-backend", mockBackendPath)
			app, err = API(application)
			Expect(err).ToNot(HaveOccurred())

			go func() {
				if err := app.Start("127.0.0.1:9090"); err != nil && err != http.ErrServerClosed {
					xlog.Error("server error", "error", err)
				}
			}()

			defaultConfig := openai.DefaultConfig("")
			defaultConfig.BaseURL = "http://127.0.0.1:9090/v1"
			client2 = openaigo.NewClient("")
			client2.BaseURL = defaultConfig.BaseURL
			// Wait for API to be ready
			client = openai.NewClientWithConfig(defaultConfig)
			Eventually(func() error {
				_, err := client.ListModels(context.TODO())
				return err
			}, "2m").ShouldNot(HaveOccurred())
		})
		AfterEach(func() {
			cancel()
			if app != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := app.Shutdown(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(os.RemoveAll(tmpdir)).To(Succeed())
		})
		It("can generate chat completions from config file (list1)", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "list1", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: testPrompt}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Message.Content).ToNot(BeEmpty())
		})
		It("can generate chat completions from config file (list2)", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "list2", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: testPrompt}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Message.Content).ToNot(BeEmpty())
		})
		It("can generate edit completions from config file", func() {
			request := openaigo.EditCreateRequestBody{
				Model:       "list2",
				Instruction: "foo",
				Input:       "bar",
			}
			resp, err := client2.CreateEdit(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Text).ToNot(BeEmpty())
		})

	})
})
