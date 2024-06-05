package http_test

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/go-skynet/LocalAI/core/config"
	. "github.com/go-skynet/LocalAI/core/http"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/startup"

	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	openaigo "github.com/otiai10/openaigo"
	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

const testPrompt = `### System:
You are an AI assistant that follows instruction extremely well. Help as much as you can.

### User:

Can you help rephrasing sentences?

### Response:`

type modelApplyRequest struct {
	ID        string                 `json:"id"`
	URL       string                 `json:"url"`
	ConfigURL string                 `json:"config_url"`
	Name      string                 `json:"name"`
	Overrides map[string]interface{} `json:"overrides"`
}

func getModelStatus(url string) (response map[string]interface{}) {
	// Create the HTTP request
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("Error creating request:", err)
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

func getModels(url string) (response []gallery.GalleryModel) {
	// TODO: No tests currently seem to exercise file:// urls. Fix?
	downloader.GetURI(url, "", func(url string, i []byte) error {
		// Unmarshal YAML data into a struct
		return json.Unmarshal(i, &response)
	})
	return
}

func postModelApplyRequest(url string, request modelApplyRequest) (response map[string]interface{}) {

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

//go:embed backend-assets/*
var backendAssets embed.FS

var _ = Describe("API test", func() {

	var app *fiber.App
	var client *openai.Client
	var client2 *openaigo.Client
	var c context.Context
	var cancel context.CancelFunc
	var tmpdir string
	var modelDir string
	var bcl *config.BackendConfigLoader
	var ml *model.ModelLoader
	var applicationConfig *config.ApplicationConfig

	commonOpts := []config.AppOption{
		config.WithDebug(true),
	}

	Context("API with ephemeral models", func() {

		BeforeEach(func(sc SpecContext) {
			var err error
			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())

			modelDir = filepath.Join(tmpdir, "models")
			err = os.Mkdir(modelDir, 0750)
			Expect(err).ToNot(HaveOccurred())
			backendAssetsDir := filepath.Join(tmpdir, "backend-assets")
			err = os.Mkdir(backendAssetsDir, 0750)
			Expect(err).ToNot(HaveOccurred())

			c, cancel = context.WithCancel(context.Background())

			g := []gallery.GalleryModel{
				{
					Name: "bert",
					URL:  "https://raw.githubusercontent.com/go-skynet/model-gallery/main/bert-embeddings.yaml",
				},
				{
					Name:            "bert2",
					URL:             "https://raw.githubusercontent.com/go-skynet/model-gallery/main/bert-embeddings.yaml",
					Overrides:       map[string]interface{}{"foo": "bar"},
					AdditionalFiles: []gallery.File{{Filename: "foo.yaml", URI: "https://raw.githubusercontent.com/go-skynet/model-gallery/main/bert-embeddings.yaml"}},
				},
			}
			out, err := yaml.Marshal(g)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(filepath.Join(modelDir, "gallery_simple.yaml"), out, 0600)
			Expect(err).ToNot(HaveOccurred())

			galleries := []gallery.Gallery{
				{
					Name: "test",
					URL:  "file://" + filepath.Join(modelDir, "gallery_simple.yaml"),
				},
			}

			bcl, ml, applicationConfig, err = startup.Startup(
				append(commonOpts,
					config.WithContext(c),
					config.WithGalleries(galleries),
					config.WithModelPath(modelDir),
					config.WithBackendAssets(backendAssets),
					config.WithBackendAssetsOutput(backendAssetsDir))...)
			Expect(err).ToNot(HaveOccurred())

			app, err = App(bcl, ml, applicationConfig)
			Expect(err).ToNot(HaveOccurred())

			go app.Listen("127.0.0.1:9090")

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

		AfterEach(func(sc SpecContext) {
			cancel()
			if app != nil {
				err := app.Shutdown()
				Expect(err).ToNot(HaveOccurred())
			}
			err := os.RemoveAll(tmpdir)
			Expect(err).ToNot(HaveOccurred())
			_, err = os.ReadDir(tmpdir)
			Expect(err).To(HaveOccurred())
		})

		Context("Applying models", func() {

			It("applies models from a gallery", func() {
				models := getModels("http://127.0.0.1:9090/models/available")
				Expect(len(models)).To(Equal(2), fmt.Sprint(models))
				Expect(models[0].Installed).To(BeFalse(), fmt.Sprint(models))
				Expect(models[1].Installed).To(BeFalse(), fmt.Sprint(models))

				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					ID: "test@bert2",
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)
				resp := map[string]interface{}{}
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

				content := map[string]interface{}{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["backend"]).To(Equal("bert-embeddings"))
				Expect(content["foo"]).To(Equal("bar"))

				models = getModels("http://127.0.0.1:9090/models/available")
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
					URL:  "https://raw.githubusercontent.com/go-skynet/model-gallery/main/bert-embeddings.yaml",
					Name: "bert",
					Overrides: map[string]interface{}{
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

				content := map[string]interface{}{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["backend"]).To(Equal("llama"))
			})
			It("apply models from config", func() {
				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					ConfigURL: "https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/models/hermes-2-pro-mistral.yaml",
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))

				Eventually(func() []string {
					models, _ := client.ListModels(context.TODO())
					modelList := []string{}
					for _, m := range models.Models {
						modelList = append(modelList, m.ID)
					}
					return modelList
				}, "360s", "10s").Should(ContainElements("hermes-2-pro-mistral"))
			})
			It("apply models without overrides", func() {
				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					URL:       "https://raw.githubusercontent.com/go-skynet/model-gallery/main/bert-embeddings.yaml",
					Name:      "bert",
					Overrides: map[string]interface{}{},
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))

				dat, err := os.ReadFile(filepath.Join(modelDir, "bert.yaml"))
				Expect(err).ToNot(HaveOccurred())

				content := map[string]interface{}{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["backend"]).To(Equal("bert-embeddings"))
			})

			It("runs openllama(llama-ggml backend)", Label("llama"), func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}
				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					URL:       "github:go-skynet/model-gallery/openllama_3b.yaml",
					Name:      "openllama_3b",
					Overrides: map[string]interface{}{"backend": "llama-ggml", "mmap": true, "f16": true, "context_size": 128},
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))

				By("testing completion")
				resp, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "openllama_3b", Prompt: "Count up to five: one, two, three, four, "})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1))
				Expect(resp.Choices[0].Text).To(ContainSubstring("five"))

				By("testing functions")
				resp2, err := client.CreateChatCompletion(
					context.TODO(),
					openai.ChatCompletionRequest{
						Model: "openllama_3b",
						Messages: []openai.ChatCompletionMessage{
							{
								Role:    "user",
								Content: "What is the weather like in San Francisco (celsius)?",
							},
						},
						Functions: []openai.FunctionDefinition{
							openai.FunctionDefinition{
								Name:        "get_current_weather",
								Description: "Get the current weather",
								Parameters: jsonschema.Definition{
									Type: jsonschema.Object,
									Properties: map[string]jsonschema.Definition{
										"location": {
											Type:        jsonschema.String,
											Description: "The city and state, e.g. San Francisco, CA",
										},
										"unit": {
											Type: jsonschema.String,
											Enum: []string{"celcius", "fahrenheit"},
										},
									},
									Required: []string{"location"},
								},
							},
						},
					})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp2.Choices)).To(Equal(1))
				Expect(resp2.Choices[0].Message.FunctionCall).ToNot(BeNil())
				Expect(resp2.Choices[0].Message.FunctionCall.Name).To(Equal("get_current_weather"), resp2.Choices[0].Message.FunctionCall.Name)

				var res map[string]string
				err = json.Unmarshal([]byte(resp2.Choices[0].Message.FunctionCall.Arguments), &res)
				Expect(err).ToNot(HaveOccurred())
				Expect(res["location"]).To(Equal("San Francisco"), fmt.Sprint(res))
				Expect(res["unit"]).To(Equal("celcius"), fmt.Sprint(res))
				Expect(string(resp2.Choices[0].FinishReason)).To(Equal("function_call"), fmt.Sprint(resp2.Choices[0].FinishReason))

			})

			It("runs openllama gguf(llama-cpp)", Label("llama-gguf"), func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}

				modelName := "hermes-2-pro-mistral"
				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					ConfigURL: "https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/models/hermes-2-pro-mistral.yaml",
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))

				By("testing chat")
				resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: modelName, Messages: []openai.ChatCompletionMessage{
					{
						Role:    "user",
						Content: "How much is 2+2?",
					},
				}})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1))
				Expect(resp.Choices[0].Message.Content).To(Or(ContainSubstring("4"), ContainSubstring("four")))

				By("testing functions")
				resp2, err := client.CreateChatCompletion(
					context.TODO(),
					openai.ChatCompletionRequest{
						Model: modelName,
						Messages: []openai.ChatCompletionMessage{
							{
								Role:    "user",
								Content: "What is the weather like in San Francisco (celsius)?",
							},
						},
						Functions: []openai.FunctionDefinition{
							openai.FunctionDefinition{
								Name:        "get_current_weather",
								Description: "Get the current weather",
								Parameters: jsonschema.Definition{
									Type: jsonschema.Object,
									Properties: map[string]jsonschema.Definition{
										"location": {
											Type:        jsonschema.String,
											Description: "The city and state, e.g. San Francisco, CA",
										},
										"unit": {
											Type: jsonschema.String,
											Enum: []string{"celcius", "fahrenheit"},
										},
									},
									Required: []string{"location"},
								},
							},
						},
					})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp2.Choices)).To(Equal(1))
				Expect(resp2.Choices[0].Message.FunctionCall).ToNot(BeNil())
				Expect(resp2.Choices[0].Message.FunctionCall.Name).To(Equal("get_current_weather"), resp2.Choices[0].Message.FunctionCall.Name)

				var res map[string]string
				err = json.Unmarshal([]byte(resp2.Choices[0].Message.FunctionCall.Arguments), &res)
				Expect(err).ToNot(HaveOccurred())
				Expect(res["location"]).To(ContainSubstring("San Francisco"), fmt.Sprint(res))
				Expect(res["unit"]).To(Equal("celcius"), fmt.Sprint(res))
				Expect(string(resp2.Choices[0].FinishReason)).To(Equal("function_call"), fmt.Sprint(resp2.Choices[0].FinishReason))
			})

			It("runs gpt4all", Label("gpt4all"), func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}

				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					URL:  "github:go-skynet/model-gallery/gpt4all-j.yaml",
					Name: "gpt4all-j",
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					return response["processed"].(bool)
				}, "960s", "10s").Should(Equal(true))

				resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "gpt4all-j", Messages: []openai.ChatCompletionMessage{openai.ChatCompletionMessage{Role: "user", Content: "How are you?"}}})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1))
				Expect(resp.Choices[0].Message.Content).To(ContainSubstring("well"))
			})

		})
	})

	Context("Model gallery", func() {
		BeforeEach(func() {
			var err error
			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			modelDir = filepath.Join(tmpdir, "models")
			backendAssetsDir := filepath.Join(tmpdir, "backend-assets")
			err = os.Mkdir(backendAssetsDir, 0750)
			Expect(err).ToNot(HaveOccurred())

			c, cancel = context.WithCancel(context.Background())

			galleries := []gallery.Gallery{
				{
					Name: "model-gallery",
					URL:  "https://raw.githubusercontent.com/go-skynet/model-gallery/main/index.yaml",
				},
			}

			bcl, ml, applicationConfig, err = startup.Startup(
				append(commonOpts,
					config.WithContext(c),
					config.WithAudioDir(tmpdir),
					config.WithImageDir(tmpdir),
					config.WithGalleries(galleries),
					config.WithModelPath(modelDir),
					config.WithBackendAssets(backendAssets),
					config.WithBackendAssetsOutput(tmpdir))...,
			)
			Expect(err).ToNot(HaveOccurred())
			app, err = App(bcl, ml, applicationConfig)
			Expect(err).ToNot(HaveOccurred())

			go app.Listen("127.0.0.1:9090")

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
				err := app.Shutdown()
				Expect(err).ToNot(HaveOccurred())
			}
			err := os.RemoveAll(tmpdir)
			Expect(err).ToNot(HaveOccurred())
			_, err = os.ReadDir(tmpdir)
			Expect(err).To(HaveOccurred())
		})
		It("installs and is capable to run tts", Label("tts"), func() {
			if runtime.GOOS != "linux" {
				Skip("test supported only on linux")
			}

			response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
				ID: "model-gallery@voice-en-us-kathleen-low",
			})

			Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

			uuid := response["uuid"].(string)

			Eventually(func() bool {
				response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
				fmt.Println(response)
				return response["processed"].(bool)
			}, "360s", "10s").Should(Equal(true))

			// An HTTP Post to the /tts endpoint should return a wav audio file
			resp, err := http.Post("http://127.0.0.1:9090/tts", "application/json", bytes.NewBuffer([]byte(`{"input": "Hello world", "model": "en-us-kathleen-low.onnx"}`)))
			Expect(err).ToNot(HaveOccurred(), fmt.Sprint(resp))
			dat, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred(), fmt.Sprint(resp))

			Expect(resp.StatusCode).To(Equal(200), fmt.Sprint(string(dat)))
			Expect(resp.Header.Get("Content-Type")).To(Equal("audio/x-wav"))
		})
		It("installs and is capable to generate images", Label("stablediffusion"), func() {
			if runtime.GOOS != "linux" {
				Skip("test supported only on linux")
			}

			response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
				ID: "model-gallery@stablediffusion",
				Overrides: map[string]interface{}{
					"parameters": map[string]interface{}{"model": "stablediffusion_assets"},
				},
			})

			Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

			uuid := response["uuid"].(string)

			Eventually(func() bool {
				response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
				fmt.Println(response)
				return response["processed"].(bool)
			}, "360s", "10s").Should(Equal(true))

			resp, err := http.Post(
				"http://127.0.0.1:9090/v1/images/generations",
				"application/json",
				bytes.NewBuffer([]byte(`{
					 			"prompt": "floating hair, portrait, ((loli)), ((one girl)), cute face, hidden hands, asymmetrical bangs, beautiful detailed eyes, eye shadow, hair ornament, ribbons, bowties, buttons, pleated skirt, (((masterpiece))), ((best quality)), colorful|((part of the head)), ((((mutated hands and fingers)))), deformed, blurry, bad anatomy, disfigured, poorly drawn face, mutation, mutated, extra limb, ugly, poorly drawn hands, missing limb, blurry, floating limbs, disconnected limbs, malformed hands, blur, out of focus, long neck, long body, Octane renderer, lowres, bad anatomy, bad hands, text",
								"mode": 2,  "seed":9000,
					 			"size": "256x256", "n":2}`)))
			// The response should contain an URL
			Expect(err).ToNot(HaveOccurred(), fmt.Sprint(resp))
			dat, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred(), "error reading /image/generations response")

			imgUrlResp := &schema.OpenAIResponse{}
			err = json.Unmarshal(dat, imgUrlResp)
			Expect(imgUrlResp.Data).ToNot(Or(BeNil(), BeZero()))
			imgUrl := imgUrlResp.Data[0].URL
			Expect(imgUrl).To(ContainSubstring("http://127.0.0.1:9090/"), imgUrl)
			Expect(imgUrl).To(ContainSubstring(".png"), imgUrl)

			imgResp, err := http.Get(imgUrl)
			Expect(err).To(BeNil())
			Expect(imgResp).ToNot(BeNil())
			Expect(imgResp.StatusCode).To(Equal(200))
			Expect(imgResp.ContentLength).To(BeNumerically(">", 0))
			imgData := make([]byte, 512)
			count, err := io.ReadFull(imgResp.Body, imgData)
			Expect(err).To(Or(BeNil(), MatchError(io.EOF)))
			Expect(count).To(BeNumerically(">", 0))
			Expect(count).To(BeNumerically("<=", 512))
			Expect(http.DetectContentType(imgData)).To(Equal("image/png"))
		})
	})

	Context("API query", func() {
		BeforeEach(func() {
			modelPath := os.Getenv("MODELS_PATH")
			c, cancel = context.WithCancel(context.Background())

			var err error

			bcl, ml, applicationConfig, err = startup.Startup(
				append(commonOpts,
					config.WithExternalBackend("huggingface", os.Getenv("HUGGINGFACE_GRPC")),
					config.WithContext(c),
					config.WithModelPath(modelPath),
				)...)
			Expect(err).ToNot(HaveOccurred())
			app, err = App(bcl, ml, applicationConfig)
			Expect(err).ToNot(HaveOccurred())
			go app.Listen("127.0.0.1:9090")

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
				err := app.Shutdown()
				Expect(err).ToNot(HaveOccurred())
			}
		})
		It("returns the models list", func() {
			models, err := client.ListModels(context.TODO())
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models.Models)).To(Equal(6)) // If "config.yaml" should be included, this should be 8?
		})
		It("can generate completions via ggml", func() {
			resp, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "testmodel.ggml", Prompt: testPrompt})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Text).ToNot(BeEmpty())
		})

		It("can generate chat completions via ggml", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "testmodel.ggml", Messages: []openai.ChatCompletionMessage{openai.ChatCompletionMessage{Role: "user", Content: testPrompt}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Message.Content).ToNot(BeEmpty())
		})

		It("can generate completions from model configs", func() {
			resp, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "gpt4all", Prompt: testPrompt})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Text).ToNot(BeEmpty())
		})

		It("can generate chat completions from model configs", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "gpt4all-2", Messages: []openai.ChatCompletionMessage{openai.ChatCompletionMessage{Role: "user", Content: testPrompt}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Message.Content).ToNot(BeEmpty())
		})

		It("returns errors", func() {
			_, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "foomodel", Prompt: testPrompt})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("error, status code: 500, message: could not load model - all backends returned error:"))
		})

		It("transcribes audio", func() {
			if runtime.GOOS != "linux" {
				Skip("test supported only on linux")
			}
			resp, err := client.CreateTranscription(
				context.Background(),
				openai.AudioRequest{
					Model:    openai.Whisper1,
					FilePath: filepath.Join(os.Getenv("TEST_DIR"), "audio.wav"),
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.Text).To(ContainSubstring("This is the Micro Machine Man presenting"))
		})

		It("calculate embeddings", func() {
			if runtime.GOOS != "linux" {
				Skip("test supported only on linux")
			}
			resp, err := client.CreateEmbeddings(
				context.Background(),
				openai.EmbeddingRequest{
					Model: openai.AdaEmbeddingV2,
					Input: []string{"sun", "cat"},
				},
			)
			Expect(err).ToNot(HaveOccurred(), err)
			Expect(len(resp.Data[0].Embedding)).To(BeNumerically("==", 384))
			Expect(len(resp.Data[1].Embedding)).To(BeNumerically("==", 384))

			sunEmbedding := resp.Data[0].Embedding
			resp2, err := client.CreateEmbeddings(
				context.Background(),
				openai.EmbeddingRequest{
					Model: openai.AdaEmbeddingV2,
					Input: []string{"sun"},
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp2.Data[0].Embedding).To(Equal(sunEmbedding))
		})

		Context("External gRPC calls", func() {
			It("calculate embeddings with sentencetransformers", func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}
				resp, err := client.CreateEmbeddings(
					context.Background(),
					openai.EmbeddingRequest{
						Model: openai.AdaCodeSearchCode,
						Input: []string{"sun", "cat"},
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Data[0].Embedding)).To(BeNumerically("==", 384))
				Expect(len(resp.Data[1].Embedding)).To(BeNumerically("==", 384))

				sunEmbedding := resp.Data[0].Embedding
				resp2, err := client.CreateEmbeddings(
					context.Background(),
					openai.EmbeddingRequest{
						Model: openai.AdaCodeSearchCode,
						Input: []string{"sun"},
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(resp2.Data[0].Embedding).To(Equal(sunEmbedding))
				Expect(resp2.Data[0].Embedding).ToNot(Equal(resp.Data[1].Embedding))
			})
		})

		Context("backends", func() {
			It("runs rwkv completion", func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}
				resp, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "rwkv_test", Prompt: "Count up to five: one, two, three, four,"})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices) > 0).To(BeTrue())
				Expect(resp.Choices[0].Text).To(ContainSubstring("five"))

				stream, err := client.CreateCompletionStream(context.TODO(), openai.CompletionRequest{
					Model: "rwkv_test", Prompt: "Count up to five: one, two, three, four,", Stream: true,
				})
				Expect(err).ToNot(HaveOccurred())
				defer stream.Close()

				tokens := 0
				text := ""
				for {
					response, err := stream.Recv()
					if errors.Is(err, io.EOF) {
						break
					}

					Expect(err).ToNot(HaveOccurred())
					text += response.Choices[0].Text
					tokens++
				}
				Expect(text).ToNot(BeEmpty())
				Expect(text).To(ContainSubstring("five"))
				Expect(tokens).ToNot(Or(Equal(1), Equal(0)))
			})
			It("runs rwkv chat completion", func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}
				resp, err := client.CreateChatCompletion(context.TODO(),
					openai.ChatCompletionRequest{Model: "rwkv_test", Messages: []openai.ChatCompletionMessage{{Content: "Can you count up to five?", Role: "user"}}})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices) > 0).To(BeTrue())
				Expect(resp.Choices[0].Message.Content).To(Or(ContainSubstring("Sure"), ContainSubstring("five")))

				stream, err := client.CreateChatCompletionStream(context.TODO(), openai.ChatCompletionRequest{Model: "rwkv_test", Messages: []openai.ChatCompletionMessage{{Content: "Can you count up to five?", Role: "user"}}})
				Expect(err).ToNot(HaveOccurred())
				defer stream.Close()

				tokens := 0
				text := ""
				for {
					response, err := stream.Recv()
					if errors.Is(err, io.EOF) {
						break
					}

					Expect(err).ToNot(HaveOccurred())
					text += response.Choices[0].Delta.Content
					tokens++
				}
				Expect(text).ToNot(BeEmpty())
				Expect(text).To(Or(ContainSubstring("Sure"), ContainSubstring("five")))

				Expect(tokens).ToNot(Or(Equal(1), Equal(0)))
			})
		})

		// See tests/integration/stores_test
		Context("Stores", Label("stores"), func() {

			It("sets, gets, finds and deletes entries", func() {
				ks := [][]float32{
					{0.1, 0.2, 0.3},
					{0.4, 0.5, 0.6},
					{0.7, 0.8, 0.9},
				}
				vs := []string{
					"test1",
					"test2",
					"test3",
				}
				setBody := schema.StoresSet{
					Keys:   ks,
					Values: vs,
				}

				url := "http://127.0.0.1:9090/stores/"
				err := postRequestJSON(url+"set", &setBody)
				Expect(err).ToNot(HaveOccurred())

				getBody := schema.StoresGet{
					Keys: ks,
				}
				var getRespBody schema.StoresGetResponse
				err = postRequestResponseJSON(url+"get", &getBody, &getRespBody)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(getRespBody.Keys)).To(Equal(len(ks)))

				for i, v := range getRespBody.Keys {
					if v[0] == 0.1 {
						Expect(getRespBody.Values[i]).To(Equal("test1"))
					} else if v[0] == 0.4 {
						Expect(getRespBody.Values[i]).To(Equal("test2"))
					} else {
						Expect(getRespBody.Values[i]).To(Equal("test3"))
					}
				}

				deleteBody := schema.StoresDelete{
					Keys: [][]float32{
						{0.1, 0.2, 0.3},
					},
				}
				err = postRequestJSON(url+"delete", &deleteBody)
				Expect(err).ToNot(HaveOccurred())

				findBody := schema.StoresFind{
					Key:  []float32{0.1, 0.3, 0.7},
					Topk: 10,
				}

				var findRespBody schema.StoresFindResponse
				err = postRequestResponseJSON(url+"find", &findBody, &findRespBody)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(findRespBody.Keys)).To(Equal(2))

				for i, v := range findRespBody.Keys {
					if v[0] == 0.4 {
						Expect(findRespBody.Values[i]).To(Equal("test2"))
					} else {
						Expect(findRespBody.Values[i]).To(Equal("test3"))
					}

					Expect(findRespBody.Similarities[i]).To(BeNumerically(">=", -1))
					Expect(findRespBody.Similarities[i]).To(BeNumerically("<=", 1))
				}
			})
		})
	})

	Context("Config file", func() {
		BeforeEach(func() {
			modelPath := os.Getenv("MODELS_PATH")
			c, cancel = context.WithCancel(context.Background())

			var err error
			bcl, ml, applicationConfig, err = startup.Startup(
				append(commonOpts,
					config.WithContext(c),
					config.WithModelPath(modelPath),
					config.WithConfigFile(os.Getenv("CONFIG_FILE")))...,
			)
			Expect(err).ToNot(HaveOccurred())
			app, err = App(bcl, ml, applicationConfig)
			Expect(err).ToNot(HaveOccurred())

			go app.Listen("127.0.0.1:9090")

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
				err := app.Shutdown()
				Expect(err).ToNot(HaveOccurred())
			}
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
