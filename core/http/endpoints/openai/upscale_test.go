package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Image upscaling", func() {
	var (
		appConfig *config.ApplicationConfig
		tmpDir    string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "upscale")
		Expect(err).ToNot(HaveOccurred())
		appConfig = config.NewApplicationConfig(config.WithGeneratedContentDir(tmpDir))
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	It("stores the result in the directory served by /generated-images", func() {
		original := backend.ImageUpscaleFunc
		backend.ImageUpscaleFunc = func(_ context.Context, _, dst string, scale int, _ *model.ModelLoader, _ config.ModelConfig, _ *config.ApplicationConfig) (func() error, error) {
			Expect(scale).To(Equal(4))
			return func() error {
				return os.WriteFile(dst, []byte("PNGDATA"), 0o644)
			}, nil
		}
		DeferCleanup(func() { backend.ImageUpscaleFunc = original })

		req, _ := makeMultipartRequest(
			map[string]string{"model": "stable-diffusion-x4-upscaler", "scale": "4"},
			map[string][]byte{"image": []byte("IMAGEDATA")},
		)
		rec := httptest.NewRecorder()
		ctx := echo.New().NewContext(req, rec)
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Backend: "diffusers"})

		Expect(UpscaleEndpoint(nil, nil, appConfig)(ctx)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))

		var response schema.OpenAIResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Data).To(HaveLen(1))
		Expect(response.Data[0].URL).To(ContainSubstring("/generated-images/upscale_"))

		filename := filepath.Base(response.Data[0].URL)
		contents, err := os.ReadFile(filepath.Join(tmpDir, "images", filename))
		Expect(err).ToNot(HaveOccurred())
		Expect(contents).To(Equal([]byte("PNGDATA")))
	})

	It("rejects unsupported scale factors", func() {
		req, _ := makeMultipartRequest(
			map[string]string{"model": "stable-diffusion-x4-upscaler", "scale": "3"},
			map[string][]byte{"image": []byte("IMAGEDATA")},
		)
		rec := httptest.NewRecorder()
		ctx := echo.New().NewContext(req, rec)
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Backend: "diffusers"})

		err := UpscaleEndpoint(nil, nil, appConfig)(ctx)
		var httpErr *echo.HTTPError
		Expect(err).To(MatchError(ContainSubstring("scale must be 2 or 4")))
		Expect(err).To(BeAssignableToTypeOf(httpErr))
		httpErr = err.(*echo.HTTPError)
		Expect(httpErr.Code).To(Equal(http.StatusBadRequest))
		Expect(httpErr.Message).To(Equal("scale must be 2 or 4"))
		Expect(bytes.TrimSpace(rec.Body.Bytes())).To(BeEmpty())
	})
})
