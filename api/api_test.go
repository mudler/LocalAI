package api_test

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

	. "github.com/go-skynet/LocalAI/api"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/gofiber/fiber/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	openaigo "github.com/otiai10/openaigo"
	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

type modelApplyRequest struct {
	ID        string                 `json:"id"`
	URL       string                 `json:"url"`
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
	utils.GetURI(url, func(url string, i []byte) error {
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

//go:embed backend-assets/*
var backendAssets embed.FS

var _ = Describe("API test", func() {

	var app *fiber.App
	var modelLoader *model.ModelLoader
	var client *openai.Client
	var client2 *openaigo.Client
	var c context.Context
	var cancel context.CancelFunc
	var tmpdir string

	commonOpts := []options.AppOption{
		options.WithDebug(true),
		options.WithDisableMessage(true),
	}

	Context("API with ephemeral models", func() {
		BeforeEach(func() {
			var err error
			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())

			modelLoader = model.NewModelLoader(tmpdir)
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
			err = os.WriteFile(filepath.Join(tmpdir, "gallery_simple.yaml"), out, 0644)
			Expect(err).ToNot(HaveOccurred())

			galleries := []gallery.Gallery{
				{
					Name: "test",
					URL:  "file://" + filepath.Join(tmpdir, "gallery_simple.yaml"),
				},
			}

			app, err = App(
				append(commonOpts,
					options.WithContext(c),
					options.WithGalleries(galleries),
					options.WithModelLoader(modelLoader), options.WithBackendAssets(backendAssets), options.WithBackendAssetsOutput(tmpdir))...)
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
			app.Shutdown()
			os.RemoveAll(tmpdir)
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

				dat, err := os.ReadFile(filepath.Join(tmpdir, "bert2.yaml"))
				Expect(err).ToNot(HaveOccurred())

				_, err = os.ReadFile(filepath.Join(tmpdir, "foo.yaml"))
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

				dat, err := os.ReadFile(filepath.Join(tmpdir, "bert.yaml"))
				Expect(err).ToNot(HaveOccurred())

				content := map[string]interface{}{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["backend"]).To(Equal("llama"))
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

				dat, err := os.ReadFile(filepath.Join(tmpdir, "bert.yaml"))
				Expect(err).ToNot(HaveOccurred())

				content := map[string]interface{}{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["backend"]).To(Equal("bert-embeddings"))
			})

			It("runs openllama", Label("llama"), func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}
				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					URL:       "github:go-skynet/model-gallery/openllama_3b.yaml",
					Name:      "openllama_3b",
					Overrides: map[string]interface{}{"backend": "llama-stable", "mmap": true, "f16": true, "context_size": 128},
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
				Expect(res["location"]).To(Equal("San Francisco, California, United States"), fmt.Sprint(res))
				Expect(res["unit"]).To(Equal("celcius"), fmt.Sprint(res))
				Expect(string(resp2.Choices[0].FinishReason)).To(Equal("function_call"), fmt.Sprint(resp2.Choices[0].FinishReason))
			})

			It("runs openllama gguf", Label("llama-gguf"), func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}
				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					URL:       "github:go-skynet/model-gallery/openllama-3b-gguf.yaml",
					Name:      "openllama_3b_gguf",
					Overrides: map[string]interface{}{"backend": "llama", "mmap": true, "f16": true, "context_size": 128},
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					return response["processed"].(bool)
				}, "360s", "10s").Should(Equal(true))

				By("testing completion")
				resp, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "openllama_3b_gguf", Prompt: "Count up to five: one, two, three, four, "})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1))
				Expect(resp.Choices[0].Text).To(ContainSubstring("five"))

				By("testing functions")
				resp2, err := client.CreateChatCompletion(
					context.TODO(),
					openai.ChatCompletionRequest{
						Model: "openllama_3b_gguf",
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
				Expect(res["location"]).To(Equal("San Francisco, California"), fmt.Sprint(res))
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
				}, "360s", "10s").Should(Equal(true))

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

			modelLoader = model.NewModelLoader(tmpdir)
			c, cancel = context.WithCancel(context.Background())

			galleries := []gallery.Gallery{
				{
					Name: "model-gallery",
					URL:  "https://raw.githubusercontent.com/go-skynet/model-gallery/main/index.yaml",
				},
			}

			app, err = App(
				append(commonOpts,
					options.WithContext(c),
					options.WithAudioDir(tmpdir),
					options.WithImageDir(tmpdir),
					options.WithGalleries(galleries),
					options.WithModelLoader(modelLoader),
					options.WithBackendAssets(backendAssets),
					options.WithBackendAssetsOutput(tmpdir))...,
			)
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
			app.Shutdown()
			os.RemoveAll(tmpdir)
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
			Expect(err).ToNot(HaveOccurred(), string(dat))
			Expect(string(dat)).To(ContainSubstring("http://127.0.0.1:9090/"), string(dat))
			Expect(string(dat)).To(ContainSubstring(".png"), string(dat))

		})
	})

	Context("API query", func() {
		BeforeEach(func() {
			modelLoader = model.NewModelLoader(os.Getenv("MODELS_PATH"))
			c, cancel = context.WithCancel(context.Background())

			var err error
			app, err = App(
				append(commonOpts,
					options.WithExternalBackend("huggingface", os.Getenv("HUGGINGFACE_GRPC")),
					options.WithContext(c),
					options.WithModelLoader(modelLoader),
				)...)
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
			app.Shutdown()
		})
		It("returns the models list", func() {
			models, err := client.ListModels(context.TODO())
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models.Models)).To(Equal(6)) // If "config.yaml" should be included, this should be 8?
		})
		It("can generate completions", func() {
			resp, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "testmodel", Prompt: "abcdedfghikl"})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Text).ToNot(BeEmpty())
		})

		It("can generate chat completions ", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "testmodel", Messages: []openai.ChatCompletionMessage{openai.ChatCompletionMessage{Role: "user", Content: "abcdedfghikl"}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Message.Content).ToNot(BeEmpty())
		})

		It("can generate completions from model configs", func() {
			resp, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "gpt4all", Prompt: "abcdedfghikl"})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Text).ToNot(BeEmpty())
		})

		It("can generate chat completions from model configs", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "gpt4all-2", Messages: []openai.ChatCompletionMessage{openai.ChatCompletionMessage{Role: "user", Content: "abcdedfghikl"}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Message.Content).ToNot(BeEmpty())
		})

		It("returns errors", func() {
			backends := len(model.AutoLoadBackends) + 1 // +1 for huggingface
			_, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "foomodel", Prompt: "abcdedfghikl"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("error, status code: 500, message: could not load model - all backends returned error: %d errors occurred:", backends)))
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
			Expect(err).ToNot(HaveOccurred())
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
			It("calculate embeddings with huggingface", func() {
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
	})

	Context("Config file", func() {
		BeforeEach(func() {
			modelLoader = model.NewModelLoader(os.Getenv("MODELS_PATH"))
			c, cancel = context.WithCancel(context.Background())

			var err error
			app, err = App(
				append(commonOpts,
					options.WithContext(c),
					options.WithModelLoader(modelLoader),
					options.WithConfigFile(os.Getenv("CONFIG_FILE")))...,
			)
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
			app.Shutdown()
		})
		It("can generate chat completions from config file (list1)", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "list1", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "abcdedfghikl"}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Message.Content).ToNot(BeEmpty())
		})
		It("can generate chat completions from config file (list2)", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "list2", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "abcdedfghikl"}}})
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
