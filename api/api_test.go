package api_test

import (
	"context"
	"os"

	. "github.com/go-skynet/LocalAI/api"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/sashabaranov/go-openai"
)

var _ = Describe("API test", func() {

	var app *fiber.App
	var modelLoader *model.ModelLoader
	var client *openai.Client
	Context("API query", func() {
		BeforeEach(func() {
			modelLoader = model.NewModelLoader(os.Getenv("MODELS_PATH"))
			app = App(modelLoader, 1, 512, false, false, true)
			go app.Listen("127.0.0.1:9090")

			defaultConfig := openai.DefaultConfig("")
			defaultConfig.BaseURL = "http://127.0.0.1:9090/v1"

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
			Expect(len(models.Models)).To(Equal(1))
			Expect(models.Models[0].ID).To(Equal("testmodel"))
		})
		It("can generate completions", func() {
			resp, err := client.CreateCompletion(context.TODO(), openai.CompletionRequest{Model: "testmodel", Prompt: "abcdedfghikl"})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Text).ToNot(BeEmpty())
		})
	})
})
