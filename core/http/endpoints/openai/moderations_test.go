package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Moderations endpoint", func() {
	It("classifies each text input and returns the OpenAI response shape", func() {
		inputs := []string{}
		generate := func(_ context.Context, input string, cfg *config.ModelConfig) (string, backend.TokenUsage, error) {
			inputs = append(inputs, input)
			Expect(cfg.Grammar).To(ContainSubstring("harassment"))
			return `{
				"categories":{"harassment":true,"harassment/threatening":false,"hate":false,"hate/threatening":false,"illicit":false,"illicit/violent":false,"self-harm":false,"self-harm/intent":false,"self-harm/instructions":false,"sexual":false,"sexual/minors":false,"violence":false,"violence/graphic":false},
				"category_scores":{"harassment":0.9,"harassment/threatening":0.1,"hate":0,"hate/threatening":0,"illicit":0,"illicit/violent":0,"self-harm":0,"self-harm/intent":0,"self-harm/instructions":0,"sexual":0,"sexual/minors":0,"violence":0,"violence/graphic":0}
			}`, backend.TokenUsage{Prompt: 12, Completion: 8}, nil
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/v1/moderations", strings.NewReader(`{"model":"guard","input":["first","second"]}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.ModerationRequest{
			BasicModelRequest: schema.BasicModelRequest{Model: "guard"},
			Input:             schema.ModerationInput{"first", "second"},
		})
		modelConfig := &config.ModelConfig{Name: "guard"}
		modelConfig.Model = "guard.gguf"
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, modelConfig)

		Expect(moderationEndpoint(generate)(ctx)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(inputs).To(Equal([]string{"first", "second"}))

		var response schema.ModerationResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response.ID).To(HavePrefix("modr-"))
		Expect(response.Model).To(Equal("guard"))
		Expect(response.Results).To(HaveLen(2))
		Expect(response.Results[0].Flagged).To(BeTrue())
		Expect(response.Results[0].Categories["harassment"]).To(BeTrue())
		Expect(response.Results[0].CategoryAppliedInputTypes["harassment"]).To(Equal([]string{"text"}))
	})

	It("rejects an empty input list", func() {
		e := echo.New()
		ctx := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/moderations", nil), httptest.NewRecorder())
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.ModerationRequest{
			BasicModelRequest: schema.BasicModelRequest{Model: "guard"},
		})
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Name: "guard"})

		err := moderationEndpoint(nil)(ctx)
		Expect(err).To(MatchError(ContainSubstring("input must contain at least one text string")))
		Expect(err.(*echo.HTTPError).Code).To(Equal(http.StatusBadRequest))
	})

	It("surfaces malformed classifier output without returning a partial result", func() {
		generate := func(context.Context, string, *config.ModelConfig) (string, backend.TokenUsage, error) {
			return "not-json", backend.TokenUsage{}, nil
		}
		e := echo.New()
		ctx := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/moderations", nil), httptest.NewRecorder())
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.ModerationRequest{
			BasicModelRequest: schema.BasicModelRequest{Model: "guard"},
			Input:             schema.ModerationInput{"text"},
		})
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Name: "guard"})

		err := moderationEndpoint(generate)(ctx)
		Expect(err).To(MatchError(ContainSubstring("invalid moderation result")))
		Expect(err.(*echo.HTTPError).Code).To(Equal(http.StatusInternalServerError))
	})
})

var _ = Describe("Moderation input", func() {
	DescribeTable("accepts OpenAI text input forms",
		func(body string, expected schema.ModerationInput) {
			var req schema.ModerationRequest
			Expect(json.Unmarshal([]byte(body), &req)).To(Succeed())
			Expect(req.Input).To(Equal(expected))
		},
		Entry("single text", `{"input":"hello"}`, schema.ModerationInput{"hello"}),
		Entry("text array", `{"input":["hello","world"]}`, schema.ModerationInput{"hello", "world"}),
	)

	It("rejects multimodal input in the text-only MVP", func() {
		var req schema.ModerationRequest
		err := json.Unmarshal([]byte(`{"input":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}`), &req)
		Expect(err).To(MatchError(ContainSubstring("text string or array of text strings")))
	})
})
