package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	httpmiddleware "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	compressionservice "github.com/mudler/LocalAI/core/services/compression"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type transformerFunc func(context.Context, config.CompressionConfig, int, string, []schema.Message) ([]schema.Message, *compressionservice.Metadata, error)

func (f transformerFunc) Transform(ctx context.Context, policy config.CompressionConfig, contextSize int, model string, messages []schema.Message) ([]schema.Message, *compressionservice.Metadata, error) {
	return f(ctx, policy, contextSize, model, messages)
}

var _ = Describe("Context compression middleware", func() {
	It("transforms requests and stamps metadata", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		ctxSize := 32
		c.Set(httpmiddleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Name: "primary", LLMConfig: config.LLMConfig{ContextSize: &ctxSize}, Compression: config.CompressionConfig{Enabled: true}})
		c.Set(httpmiddleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.OpenAIRequest{Messages: []schema.Message{{Role: "user", Content: "old"}}})
		wantMeta := &compressionservice.Metadata{OriginalTokens: 40, CompressedTokens: 10}
		middleware := httpmiddleware.ContextCompression(transformerFunc(func(_ context.Context, _ config.CompressionConfig, gotSize int, model string, _ []schema.Message) ([]schema.Message, *compressionservice.Metadata, error) {
			Expect(gotSize).To(Equal(32))
			Expect(model).To(Equal("primary"))
			return []schema.Message{{Role: "system", Content: "summary"}}, wantMeta, nil
		}))
		handler := middleware(func(c echo.Context) error {
			input := c.Get(httpmiddleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
			Expect(input.Messages).To(HaveLen(1))
			Expect(input.Messages[0].Role).To(Equal("system"))
			Expect(httpmiddleware.CompressionMetadata(c)).NotTo(BeNil())
			Expect(httpmiddleware.CompressionMetadata(c).OriginalTokens).To(Equal(40))
			return c.NoContent(http.StatusNoContent)
		})

		Expect(handler(c)).To(Succeed())
	})

	It("maps overflow to HTTP 413", func() {
		e := echo.New()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), httptest.NewRecorder())
		ctxSize := 1
		c.Set(httpmiddleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Name: "primary", LLMConfig: config.LLMConfig{ContextSize: &ctxSize}, Compression: config.CompressionConfig{Enabled: true}})
		c.Set(httpmiddleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.OpenAIRequest{Messages: []schema.Message{{Role: "user"}, {Role: "user"}}})
		realService := compressionservice.New(wordCountAdapter{}, compressionservice.SummarizerFunc(func(context.Context, string, []schema.Message, int) (string, int, error) {
			return "still too large", 3, nil
		}))

		err := httpmiddleware.ContextCompression(realService)(func(echo.Context) error { return nil })(c)
		httpErr, ok := err.(*echo.HTTPError)
		Expect(ok).To(BeTrue())
		Expect(httpErr.Code).To(Equal(http.StatusRequestEntityTooLarge))
	})

	It("resolves the automatic context-size sentinel", func() {
		e := echo.New()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), httptest.NewRecorder())
		autoContext := -1
		c.Set(httpmiddleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Name: "primary", LLMConfig: config.LLMConfig{ContextSize: &autoContext}, Compression: config.CompressionConfig{Enabled: true}})
		c.Set(httpmiddleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.OpenAIRequest{Messages: []schema.Message{{Role: "user", Content: "hello"}}})

		err := httpmiddleware.CompressChatRequest(c, transformerFunc(func(_ context.Context, _ config.CompressionConfig, gotSize int, _ string, messages []schema.Message) ([]schema.Message, *compressionservice.Metadata, error) {
			Expect(gotSize).To(Equal(config.DefaultContextSize))
			return messages, nil, nil
		}))
		Expect(err).NotTo(HaveOccurred())
	})
})

type wordCountAdapter struct{}

func (wordCountAdapter) CountMessages(messages []schema.Message) (int, error) {
	return len(messages) * 10, nil
}
