// SPDX-License-Identifier: MIT
package routes

import (
	"context"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http/httptest"
)

var _ = Describe("Diagnostics route context reconstruction", func() {
	It("retains identity and cancellation through Ollama chat/generate and Anthropic middleware", func() {
		for _, kind := range []string{"chat", "generate", "anthropic"} {
			ac := config.NewApplicationConfig()
			ac.Context = context.Background()
			var events []diagnostics.Event
			recorder := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = recorder.Request(ctx)
			diagnostics.Mark(ctx, diagnostics.PhaseExtraction)
			c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil).WithContext(ctx), httptest.NewRecorder())
			c.Request().Header.Set("x-request-id", "sentinel-client-id")
			cfg := config.ModelConfig{Name: "model"}
			cfg.Model = "model"
			c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &cfg)
			var mw echo.MiddlewareFunc
			var getContext func() context.Context
			switch kind {
			case "chat":
				input := &schema.OllamaChatRequest{Model: "model"}
				c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
				getContext = func() context.Context { return input.Context }
				mw = setOllamaChatRequestContext(ac)
			case "generate":
				input := &schema.OllamaGenerateRequest{Model: "model"}
				c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
				getContext = func() context.Context { return input.Ctx }
				mw = setOllamaGenerateRequestContext(ac)
			default:
				input := &schema.AnthropicRequest{Model: "model", MaxTokens: 1}
				c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
				getContext = func() context.Context { return input.Context }
				mw = setAnthropicRequestContext(ac)
			}
			Expect(mw(func(echo.Context) error {
				out := getContext()
				Expect(diagnostics.Enabled(out)).To(BeTrue())
				Expect(out.Err()).NotTo(HaveOccurred())
				diagnostics.Mark(out, diagnostics.PhaseModelInit)
				cancel()
				Eventually(out.Done()).Should(BeClosed())
				return nil
			})(c)).To(Succeed())
			Expect(events).To(HaveLen(2))
			Expect(events[1].ID).To(Equal(events[0].ID))
			Expect(events[0].ID).NotTo(Equal("sentinel-client-id"))
		}
	})
})
