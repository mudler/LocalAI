package middleware_test

import (
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

// A chat request with an image, sent to an alias whose target is text-only,
// must reach the alias's vision_alias model; the same request without an
// image must stay on the target.
var _ = Describe("vision_alias in request resolution", func() {
	var app *echo.Echo

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		write := func(name, body string) {
			Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0644)).To(Succeed())
		}
		write("text-llm", "name: text-llm\nbackend: llama-cpp\nparameters:\n  model: text-llm.gguf\n")
		write("vision-llm", "name: vision-llm\nbackend: llama-cpp\nmmproj: vision-mmproj.gguf\nparameters:\n  model: vision-llm.gguf\n")
		write("assistant", "name: assistant\nalias: text-llm\nvision_alias: vision-llm\nprofile:\n  system_prompt: everyday\n")

		ss := &system.SystemState{Model: system.Model{ModelsPath: dir}}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss
		mcl := config.NewModelConfigLoader(dir)
		Expect(mcl.LoadModelConfigsFromPath(dir)).To(Succeed())
		re := NewRequestExtractor(mcl, model.NewModelLoader(ss), appConfig)

		app = echo.New()
		app.POST("/v1/chat/completions", func(c echo.Context) error {
			cfg := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
			return c.JSON(http.StatusOK, map[string]string{"served": cfg.Name, "prompt": cfg.SystemPrompt})
		}, re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
	})

	chat := func(content string) *httptest.ResponseRecorder {
		body := `{"model":"assistant","messages":[{"role":"user","content":` + content + `}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	It("serves an image_url part on the vision_alias model with the alias profile", func() {
		rec := chat(`[{"type":"text","text":"Is this good olive oil?"},` +
			`{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(`"served":"vision-llm"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"prompt":"everyday"`))
	})

	It("keeps text-only content, plain or in parts, on the alias target", func() {
		for _, content := range []string{`"Hello"`, `[{"type":"text","text":"Hello"}]`} {
			rec := chat(content)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring(`"served":"text-llm"`), content)
		}
	})
})
