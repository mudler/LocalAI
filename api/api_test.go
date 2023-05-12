package api_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/go-skynet/LocalAI/api"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	openaigo "github.com/otiai10/openaigo"
	"github.com/sashabaranov/go-openai"
)

var _ = Describe("API test", func() {

	var app *fiber.App
	var modelLoader *model.ModelLoader
	var client *openai.Client
	var client2 *openaigo.Client
	Context("API query", func() {
		BeforeEach(func() {
			modelLoader = model.NewModelLoader(os.Getenv("MODELS_PATH"))
			app = App("", modelLoader, 15, 1, 512, false, true, true)
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
			app.Shutdown()
		})
		It("returns the models list", func() {
			models, err := client.ListModels(context.TODO())
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models.Models)).To(Equal(7))
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
			Expect(err.Error()).To(ContainSubstring("error, status code: 500, message: could not load model - all backends returned error: 12 errors occurred:"))
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
	})

	Context("Config file", func() {
		BeforeEach(func() {
			modelLoader = model.NewModelLoader(os.Getenv("MODELS_PATH"))
			app = App(os.Getenv("CONFIG_FILE"), modelLoader, 5, 1, 512, false, true, true)
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
			app.Shutdown()
		})
		It("can generate chat completions from config file", func() {

			models, err := client.ListModels(context.TODO())
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models.Models)).To(Equal(9))
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
