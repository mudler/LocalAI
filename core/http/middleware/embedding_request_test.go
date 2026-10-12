// SPDX-License-Identifier: MIT
package middleware_test

import (
	"context"
	"io"
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

var _ = Describe("Multimodal contracts", func() {
	var app *echo.Echo
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		ss := &system.SystemState{Model: system.Model{ModelsPath: dir}}
		cfg := config.NewApplicationConfig()
		cfg.SystemState = ss
		cfg.Context = context.Background()
		re := mw.NewRequestExtractor(config.NewModelConfigLoader(dir), model.NewModelLoader(ss), cfg)
		app = echo.New()
		for _, path := range []string{"/embeddings", "/v1/embeddings", "/v1/engines/:model/embeddings", "/v1/chat/completions"} {
			app.POST(path, func(c echo.Context) error { return c.NoContent(200) }, re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
		}
	})
	for _, path := range []string{"/embeddings", "/v1/embeddings", "/v1/engines/test/embeddings"} {
		It("fails closed for valid structured input on "+path, func() {
			Expect(postJSON(app, path, `{"model":"missing","input":{"content":[{"type":"text","text":"hello"}]}}`).Code).To(Equal(501))
		})
		for _, unknown := range []bool{false, true} {
			It("enforces prebind body boundary on "+path+" unknown="+map[bool]string{false: "false", true: "true"}[unknown], func() {
				for _, delta := range []int{-1, 0, 1} {
					// Whitespace proves overflow is caught before the JSON binder can return 400.
					body := strings.NewReader(strings.Repeat(" ", (48<<20)+delta))
					req := httptest.NewRequest(http.MethodPost, path, body)
					req.Header.Set("Content-Type", "application/json")
					if unknown {
						req.ContentLength = -1
						req.TransferEncoding = []string{"chunked"}
					}
					rec := httptest.NewRecorder()
					app.ServeHTTP(rec, req)
					if delta > 0 {
						Expect(rec.Code).To(Equal(413))
					} else {
						Expect(rec.Code).To(Equal(400))
					}
				}
			})
		}
	}
	It("preserves smaller global limits", func() {
		app.Use(em.BodyLimit("1K"))
		Expect(postJSON(app, "/embeddings", strings.Repeat(" ", 1025)).Code).To(Equal(413))
	})
	for _, body := range []string{
		`{"input":{"content":[]}}`,
		`{"input":{"content":[{"type":"text","text":"a"}]},"messages":[]}`,
		`{"input":{"content":[{"type":"text","text":"a"}]},"input":"hidden"}`,
		`{"input":"hidden","input":{"content":[{"type":"text","text":"a"}]}}`,
		`{"input":{"content":[{"type":"text","text":"a"}]},"model":"a","model":"b"}`,
		`{"input":{"content":[{"type":"text","text":"a"}]},"unexpected":true}`,
	} {
		It("rejects raw structured conflicts before bind "+body, func() { Expect(postJSON(app, "/embeddings", body).Code).To(Equal(400)) })
	}
	It("does not cap other endpoints", func() {
		req := httptest.NewRequest("POST", "/v1/chat/completions", io.MultiReader(strings.NewReader(strings.Repeat(" ", 48<<20)), strings.NewReader(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).NotTo(Equal(413))
	})
})

var _ = Describe("Legacy embedding parsing", func() {
	It("preserves small legacy input messages options and permissive envelope fields", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "test.yaml"), []byte("name: test\nbackend: test\nparameters:\n  model: test\n"), 0600)).To(Succeed())
		ss := &system.SystemState{Model: system.Model{ModelsPath: dir}}
		cfg := config.NewApplicationConfig()
		cfg.SystemState = ss
		re := mw.NewRequestExtractor(config.NewModelConfigLoader(dir), model.NewModelLoader(ss), cfg)
		app := echo.New()
		app.POST("/embeddings", func(c echo.Context) error {
			request := c.Get(mw.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
			Expect(request.EmbeddingInput).To(BeNil())
			return c.JSON(200, request)
		}, re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
		for _, body := range []string{
			`{"model":"test","input":"text","dimensions":4,"encoding_format":"base64","extra":1}`,
			`{"model":"test","input":["a","b"]}`, `{"model":"test","input":[1,2]}`, `{"model":"test","input":[[1,2],[3]]}`,
			`{"model":"test","messages":[{"role":"user","content":"text"}]}`,
			`{"model":"old","model":"test","input":"first","input":"last"}`,
		} {
			Expect(postJSON(app, "/embeddings", body).Code).To(Equal(200))
		}
	})
})
