// SPDX-License-Identifier: MIT
package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	em "github.com/labstack/echo/v4/middleware"
	"github.com/mudler/LocalAI/core/config"
	mw "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Multimodal rerank request binding", func() {
	const image = `{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}`
	var (
		app   *echo.Echo
		typed *schema.RerankContent
		seen  *schema.JINARerankRequest
	)
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "rr.yaml"), []byte("name: rr\nbackend: test\nparameters:\n  model: rr\n"), 0600)).To(Succeed())
		ss := &system.SystemState{Model: system.Model{ModelsPath: dir}}
		cfg := config.NewApplicationConfig()
		cfg.SystemState = ss
		cfg.Context = context.Background()
		re := mw.NewRequestExtractor(config.NewModelConfigLoader(dir), model.NewModelLoader(ss), cfg)
		typed, seen = nil, nil
		app = echo.New()
		app.POST("/v1/rerank", func(c echo.Context) error {
			seen = c.Get(mw.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.JINARerankRequest)
			typed = seen.Typed
			return c.NoContent(200)
		}, re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.JINARerankRequest) }))
	})

	It("passes a legacy request through the default binder with no typed content", func() {
		Expect(postJSON(app, "/v1/rerank", `{"model":"rr","query":"q","documents":["a","b"],"top_n":1}`).Code).To(Equal(200))
		Expect(typed).To(BeNil())
		Expect(seen.Query).To(Equal("q"))
		Expect(seen.Documents).To(Equal([]string{"a", "b"}))
	})

	It("still answers 400 for a legacy body with a wrong type", func() {
		Expect(postJSON(app, "/v1/rerank", `{"model":"rr","query":5,"documents":["a"]}`).Code).To(Equal(400))
		Expect(postJSON(app, "/v1/rerank", `{"model":"rr","query":"q","documents":[1]}`).Code).To(Equal(400))
	})

	It("binds typed content and leaves the legacy fields empty", func() {
		Expect(postJSON(app, "/v1/rerank", `{"model":"rr","query":"q","documents":["a",{"content":[`+image+`]}]}`).Code).To(Equal(200))
		Expect(typed).NotTo(BeNil())
		Expect(typed.Documents).To(HaveLen(2))
		Expect(seen.Query).To(BeEmpty())
		Expect(seen.Documents).To(BeEmpty())
	})

	It("classifies typed content errors before binding", func() {
		for body, want := range map[string]int{
			`{"model":"rr","query":"q","documents":[{"content":[]}]}`:                                                     400,
			`{"model":"rr","query":"q","documents":[{"content":[` + image + `,{"type":"text","text":"late"}]}]}`:          400,
			`{"model":"rr","query":"q","documents":[{"content":[{"type":"input_audio","input_audio":{"data":"YQ=="}}]}]}`: 422,
			`{"model":"rr","query":"q","query":"p","documents":[{"content":[` + image + `]}]}`:                            400,
			`{"model":"rr","query":{"content":[` + image + `]},"documents":"a"}`:                                          400,
		} {
			Expect(postJSON(app, "/v1/rerank", body).Code).To(Equal(want), body)
		}
		Expect(typed).To(BeNil())
	})

	It("enforces the embedding body cap on rerank and preserves smaller global limits", func() {
		for _, delta := range []int{-1, 0, 1} {
			req := httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(strings.Repeat(" ", (48<<20)+delta)))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if delta > 0 {
				Expect(rec.Code).To(Equal(413))
			} else {
				Expect(rec.Code).To(Equal(400))
			}
		}
		app.Use(em.BodyLimit("1K"))
		Expect(postJSON(app, "/v1/rerank", strings.Repeat(" ", 1025)).Code).To(Equal(413))
	})
})
