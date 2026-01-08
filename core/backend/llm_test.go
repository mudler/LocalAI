package backend_test

import (
	. "github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LLM tests", func() {
	Context("Finetune LLM output", func() {
		var (
			testConfig config.ModelConfig
			input      string
			prediction string
			result     string
		)

		BeforeEach(func() {
			testConfig = config.ModelConfig{
				PredictionOptions: schema.PredictionOptions{
					Echo: false,
				},
				LLMConfig: config.LLMConfig{
					Cutstrings:   []string{`<.*?>`},                  // Example regex for removing XML tags
					ExtractRegex: []string{`<result>(.*?)</result>`}, // Example regex to extract from tags
					TrimSpace:    []string{" ", "\n"},
					TrimSuffix:   []string{".", "!"},
				},
			}
		})

		Context("when echo is enabled", func() {
			BeforeEach(func() {
				testConfig.Echo = true
				input = "Hello"
				prediction = "World"
			})

			It("should prepend input to prediction", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("HelloWorld"))
			})
		})

		Context("when echo is disabled", func() {
			BeforeEach(func() {
				testConfig.Echo = false
				input = "Hello"
				prediction = "World"
			})

			It("should not modify the prediction with input", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("World"))
			})
		})

		Context("when cutstrings regex is applied", func() {
			BeforeEach(func() {
				input = ""
				prediction = "<div>Hello</div> World"
			})

			It("should remove substrings matching cutstrings regex", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("Hello World"))
			})
		})

		Context("when extract regex is applied", func() {
			BeforeEach(func() {
				input = ""
				prediction = "<response><result>42</result></response>"
			})

			It("should extract substrings matching the extract regex", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("42"))
			})
		})

		Context("when trimming spaces", func() {
			BeforeEach(func() {
				input = ""
				prediction = "   Hello World   "
			})

			It("should trim spaces from the prediction", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("Hello World"))
			})
		})

		Context("when trimming suffixes", func() {
			BeforeEach(func() {
				input = ""
				prediction = "Hello World."
			})

			It("should trim suffixes from the prediction", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("Hello World"))
			})
		})
	})
})
