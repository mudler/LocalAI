package api_test

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/go-skynet/LocalAI/api"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	openaigo "github.com/otiai10/openaigo"
	"github.com/sashabaranov/go-openai"
)

type modelApplyRequest struct {
	URL       string            `json:"url"`
	Name      string            `json:"name"`
	Overrides map[string]string `json:"overrides"`
}

func getModelStatus(url string) (response map[string]interface{}) {
	// Create the HTTP request
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("Error creating request:", err)
		return
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
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

	body, err := ioutil.ReadAll(resp.Body)
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

	Context("API with ephemeral models", func() {
		BeforeEach(func() {
			var err error
			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())

			modelLoader = model.NewModelLoader(tmpdir)
			c, cancel = context.WithCancel(context.Background())

			app, err = App(WithContext(c), WithModelLoader(modelLoader), WithBackendAssets(backendAssets), WithBackendAssetsOutput(tmpdir))
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
			It("overrides models", func() {
				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					URL:  "https://raw.githubusercontent.com/go-skynet/model-gallery/main/bert-embeddings.yaml",
					Name: "bert",
					Overrides: map[string]string{
						"backend": "llama",
					},
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					fmt.Println(response)
					return response["processed"].(bool)
				}, "360s").Should(Equal(true))

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
					Overrides: map[string]string{},
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					fmt.Println(response)
					return response["processed"].(bool)
				}, "360s").Should(Equal(true))

				dat, err := os.ReadFile(filepath.Join(tmpdir, "bert.yaml"))
				Expect(err).ToNot(HaveOccurred())

				content := map[string]interface{}{}
				err = yaml.Unmarshal(dat, &content)
				Expect(err).ToNot(HaveOccurred())
				Expect(content["backend"]).To(Equal("bert-embeddings"))
			})
			It("runs gpt4all", Label("gpt4all"), func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}

				response := postModelApplyRequest("http://127.0.0.1:9090/models/apply", modelApplyRequest{
					URL:       "github:go-skynet/model-gallery/gpt4all-j.yaml",
					Name:      "gpt4all-j",
					Overrides: map[string]string{},
				})

				Expect(response["uuid"]).ToNot(BeEmpty(), fmt.Sprint(response))

				uuid := response["uuid"].(string)

				Eventually(func() bool {
					response := getModelStatus("http://127.0.0.1:9090/models/jobs/" + uuid)
					fmt.Println(response)
					return response["processed"].(bool)
				}, "360s").Should(Equal(true))

				resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "gpt4all-j", Messages: []openai.ChatCompletionMessage{openai.ChatCompletionMessage{Role: "user", Content: "How are you?"}}})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1))
				Expect(resp.Choices[0].Message.Content).To(ContainSubstring("well"))
			})
		})
	})

	Context("API query", func() {
		BeforeEach(func() {
			modelLoader = model.NewModelLoader(os.Getenv("MODELS_PATH"))
			c, cancel = context.WithCancel(context.Background())

			var err error
			app, err = App(WithContext(c), WithModelLoader(modelLoader))
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
			Expect(len(models.Models)).To(Equal(10))
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
			_, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "foomodel", Prompt: "abcdedfghikl"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("error, status code: 500, message: could not load model - all backends returned error: 11 errors occurred:"))
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

		Context("backends", func() {
			It("runs rwkv", func() {
				if runtime.GOOS != "linux" {
					Skip("test supported only on linux")
				}
				resp, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "rwkv_test", Prompt: "Count up to five: one, two, three, four,"})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices) > 0).To(BeTrue())
				Expect(resp.Choices[0].Text).To(Equal(" five."))
			})
		})
	})

	Context("Config file", func() {
		BeforeEach(func() {
			modelLoader = model.NewModelLoader(os.Getenv("MODELS_PATH"))
			c, cancel = context.WithCancel(context.Background())

			var err error
			app, err = App(WithContext(c), WithModelLoader(modelLoader), WithConfigFile(os.Getenv("CONFIG_FILE")))
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
		It("can generate chat completions from config file", func() {
			models, err := client.ListModels(context.TODO())
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models.Models)).To(Equal(12))
		})
		It("can generate chat completions from config file", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "list1", Messages: []openai.ChatCompletionMessage{openai.ChatCompletionMessage{Role: "user", Content: "abcdedfghikl"}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Message.Content).ToNot(BeEmpty())
		})
		It("can generate chat completions from config file", func() {
			resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{Model: "list2", Messages: []openai.ChatCompletionMessage{openai.ChatCompletionMessage{Role: "user", Content: "abcdedfghikl"}}})
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
