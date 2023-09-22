package e2e_test

import (
	"context"
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	openaigo "github.com/otiai10/openaigo"
	"github.com/sashabaranov/go-openai"
)

var _ = Describe("E2E test", func() {
	var client *openai.Client
	var client2 *openaigo.Client

	Context("API with ephemeral models", func() {
		BeforeEach(func() {
			defaultConfig := openai.DefaultConfig("")
			defaultConfig.BaseURL = localAIURL

			client2 = openaigo.NewClient("")
			client2.BaseURL = defaultConfig.BaseURL

			// Wait for API to be ready
			client = openai.NewClientWithConfig(defaultConfig)
			Eventually(func() error {
				_, err := client.ListModels(context.TODO())
				return err
			}, "2m").ShouldNot(HaveOccurred())
		})

		// Check that the GPU was used
		AfterEach(func() {
			// Execute docker logs $$(docker ps -q --filter ancestor=localai-tests) as a command and check the output
			cmd := exec.Command("/bin/bash", "-xce", "docker logs $$(docker ps -q --filter ancestor=localai-tests)")
			out, err := cmd.CombinedOutput()
			Expect(err).ToNot(HaveOccurred())
			Expect(string(out)).To(ContainSubstring("found 1 CUDA devices"))
			Expect(string(out)).To(ContainSubstring("using CUDA for GPU acceleration"))
		})

		Context("Generates text", func() {
			It("streams chat tokens", func() {
				models, err := client.ListModels(context.TODO())
				Expect(err).ToNot(HaveOccurred())
				Expect(models.Models).ToNot(BeEmpty())

				model := models.Models[0].ID
				resp, err := client.CreateChatCompletion(context.TODO(), openai.ChatCompletionRequest{
					Model: model, Messages: []openai.ChatCompletionMessage{
						{
							Role:    "user",
							Content: "How much is 2+2?",
						},
					}})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1))
				Expect(resp.Choices[0].Message.Content).To(Or(ContainSubstring("4"), ContainSubstring("four")))

			})
		})
	})
})
