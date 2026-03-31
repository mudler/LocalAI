package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newRequestApp creates a minimal Echo app with SetModelAndConfig middleware.
func newRequestApp(re *RequestExtractor) *echo.Echo {
	e := echo.New()
	e.POST("/v1/chat/completions",
		func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		},
		re.SetModelAndConfig(func() schema.LocalAIRequest {
			return new(schema.OpenAIRequest)
		}),
	)
	return e
}

func postJSON(e *echo.Echo, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

var _ = Describe("SetModelAndConfig middleware", func() {
	var (
		app      *echo.Echo
		modelDir string
	)

	BeforeEach(func() {
		var err error
		modelDir, err = os.MkdirTemp("", "localai-test-models-*")
		Expect(err).ToNot(HaveOccurred())

		ss := &system.SystemState{
			Model: system.Model{ModelsPath: modelDir},
		}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss

		mcl := config.NewModelConfigLoader(modelDir)
		ml := model.NewModelLoader(ss)

		re := NewRequestExtractor(mcl, ml, appConfig)
		app = newRequestApp(re)
	})

	AfterEach(func() {
		os.RemoveAll(modelDir)
	})

	Context("when the model does not exist", func() {
		It("returns 404 with a helpful error message", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"model":"nonexistent-model","messages":[{"role":"user","content":"hi"}]}`)

			Expect(rec.Code).To(Equal(http.StatusNotFound))

			var resp schema.ErrorResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Error).ToNot(BeNil())
			Expect(resp.Error.Message).To(ContainSubstring("nonexistent-model"))
			Expect(resp.Error.Message).To(ContainSubstring("not found"))
			Expect(resp.Error.Type).To(Equal("invalid_request_error"))
		})
	})

	Context("when the model exists as a config file", func() {
		BeforeEach(func() {
			cfgContent := []byte("name: test-model\nbackend: llama-cpp\n")
			err := os.WriteFile(filepath.Join(modelDir, "test-model.yaml"), cfgContent, 0644)
			Expect(err).ToNot(HaveOccurred())
		})

		It("passes through to the handler", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("when the model exists as a pre-loaded config", func() {
		var mcl *config.ModelConfigLoader

		BeforeEach(func() {
			// Simulate a model installed via gallery: config is loaded in memory
			// (not just a YAML file on disk). Recreate the app with the pre-loaded config.
			ss := &system.SystemState{
				Model: system.Model{ModelsPath: modelDir},
			}
			appConfig := config.NewApplicationConfig()
			appConfig.SystemState = ss

			mcl = config.NewModelConfigLoader(modelDir)
			// Pre-load a config as if installed via gallery
			cfgContent := []byte("name: gallery-model\nbackend: llama-cpp\nmodel: gallery-model\n")
			err := os.WriteFile(filepath.Join(modelDir, "gallery-model.yaml"), cfgContent, 0644)
			Expect(err).ToNot(HaveOccurred())
			Expect(mcl.ReadModelConfig(filepath.Join(modelDir, "gallery-model.yaml"))).To(Succeed())

			ml := model.NewModelLoader(ss)
			re := NewRequestExtractor(mcl, ml, appConfig)
			app = newRequestApp(re)
		})

		It("passes through to the handler", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"model":"gallery-model","messages":[{"role":"user","content":"hi"}]}`)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("when the model name contains a slash (HuggingFace ID)", func() {
		It("skips the existence check and passes through", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"model":"stabilityai/stable-diffusion-xl-base-1.0","messages":[{"role":"user","content":"hi"}]}`)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("when no model is specified", func() {
		It("passes through without checking", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"messages":[{"role":"user","content":"hi"}]}`)

			// No model name → middleware doesn't reject, handler runs
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})
})
