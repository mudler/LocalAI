package openai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests pin the streaming completion request validation added for issue
// #11021: a streaming request whose prompt does not resolve to exactly one
// string must be rejected with an HTTP 400 before any SSE headers are written,
// rather than panicking on PromptStrings[0] or returning a 500 on a
// half-opened event stream.
var _ = Describe("streaming completion prompt validation (issue #11021)", func() {
	DescribeTable("rejects malformed prompts with HTTP 400",
		func(prompts []string) {
			cfg := &config.ModelConfig{}
			cfg.PromptStrings = prompts

			err := validateStreamingPromptStrings(cfg)
			Expect(err).To(HaveOccurred())

			he, ok := err.(*echo.HTTPError)
			Expect(ok).To(BeTrue(), "expected an *echo.HTTPError, got %T", err)
			Expect(he.Code).To(Equal(http.StatusBadRequest))
		},
		Entry("omitted prompt (nil slice)", []string(nil)),
		Entry("empty array", []string{}),
		Entry("multiple prompt strings", []string{"a", "b", "c"}),
	)

	It("accepts exactly one prompt string", func() {
		cfg := &config.ModelConfig{}
		cfg.PromptStrings = []string{"hello"}
		Expect(validateStreamingPromptStrings(cfg)).To(Succeed())
	})
})
