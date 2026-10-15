// SPDX-License-Identifier: MIT
package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type conversionRunner func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error)

func (f conversionRunner) Decide(c context.Context, r *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
	return f(c, r)
}

var _ = Describe("Routed typed Anthropic endpoint conversion", func() {
	for _, fallback := range []bool{false, true} {
		It("preserves original content after native selection or runtime fallback", func() {
			dir := GinkgoT().TempDir()
			for _, name := range []string{"chosen", "fallback"} {
				Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte("name: "+name+"\nbackend: mock-backend\n"), 0600)).To(Succeed())
			}
			cfg := &config.ModelConfig{Name: "route", Router: config.RouterConfig{Classifier: "decisions", ClassifierModel: "native", Fallback: "fallback", Policies: []config.RouterPolicy{{Label: "visual", Description: "image"}}, Candidates: []config.RouterCandidate{{Model: "chosen", Labels: []string{"visual"}}}}}
			var b bytes.Buffer
			Expect(png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))).To(Succeed())
			data := base64.StdEncoding.EncodeToString(b.Bytes())
			blocks := []schema.AnthropicContentBlock{{Type: "text", Text: "first"}, {Type: "image", Source: &schema.AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: data}}, {Type: "text", Text: "second"}, {Type: "image", Source: &schema.AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: data}}}
			req := &schema.AnthropicRequest{Model: "route", Messages: []schema.AnthropicMessage{{Role: "user", Content: blocks}}}
			before, err := json.Marshal(req.Messages)
			Expect(err).NotTo(HaveOccurred())
			app := &config.ApplicationConfig{Context: context.Background(), SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
			c := echo.New().NewContext(httptest.NewRequest("POST", "/v1/messages", nil), httptest.NewRecorder())
			c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
			c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, req)
			called := false
			deps := middleware.ClassifierDeps{ModelLookup: func(string) *config.ModelConfig {
				u := config.FLAG_DECISIONS
				return &config.ModelConfig{Backend: "llama-cpp", KnownUsecases: &u}
			}, Decisions: func(string) backend.DecisionRunner {
				return conversionRunner(func(_ context.Context, r *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
					called = true
					if fallback {
						return nil, errors.New("runtime failure")
					}
					answers := map[string]schema.SystemOneAnswer{}
					for k := range r.Questions {
						v := 1.0
						answers[k] = schema.SystemOneAnswer{Type: "noul", Noul: &v}
					}
					return &schema.SystemOneResponse{Answers: answers}, nil
				})
			}}
			var converted []schema.Message
			handler := middleware.RouteModel(config.NewModelConfigLoader(dir), app, nil, nil, middleware.AnthropicProbe, router.SourceAnthropic, deps)(func(c echo.Context) error {
				// Invoke the actual conversion used by the messages endpoint, not a
				// stub which only checks that middleware called next.
				converted = convertAnthropicToOpenAIMessages(c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.AnthropicRequest))
				return nil
			})
			Expect(handler(c)).To(Succeed())
			Expect(called).To(BeTrue())
			if fallback {
				Expect(req.Model).To(Equal("fallback"))
			} else {
				Expect(req.Model).To(Equal("chosen"))
			}
			Expect(converted).To(HaveLen(1))
			Expect(converted[0].StringContent).To(Equal("firstsecond"))
			Expect(converted[0].StringImages).To(Equal([]string{"data:image/png;base64," + data, "data:image/png;base64," + data}))
			after, err := json.Marshal(req.Messages)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
		})
	}
})
