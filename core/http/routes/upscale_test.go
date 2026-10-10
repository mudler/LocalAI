// SPDX-License-Identifier: MIT
package routes_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Upscale route model selection", func() {
	It("uses upscale-only models on both aliases without admitting generation models", func() {
		root := GinkgoT().TempDir()
		app, err := application.New(config.WithDataPath(root), config.WithGeneratedContentDir(root), config.WithDisableLocalAIAssistant(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		upscale, image := config.FLAG_UPSCALE, config.FLAG_IMAGE
		configs := []config.ModelConfig{
			{Name: "esrgan", Backend: "stablediffusion-ggml", KnownUsecases: &upscale},
			{Name: "stable-diffusion-x4-upscaler", Backend: "diffusers", KnownUsecases: &upscale},
			{Name: "ordinary-image", Backend: "diffusers", KnownUsecases: &image},
			{Name: "incompatible", Backend: "whisper"},
		}
		for i := range configs {
			configs[i].SetDefaults()
			configs[i].Model = configs[i].Name
		}
		original := backend.ImageUpscaleFunc
		DeferCleanup(func() { backend.ImageUpscaleFunc = original })
		served := ""
		backend.ImageUpscaleFunc = func(_ context.Context, _, dst string, _ int, _ *model.ModelLoader, cfg config.ModelConfig, _ *config.ApplicationConfig) (func() error, error) {
			served = cfg.Name
			return func() error { return os.WriteFile(dst, []byte("PNG"), 0600) }, nil
		}
		e := echo.New()
		selected := ""
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				err := next(c)
				if cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig); ok && cfg != nil {
					selected = cfg.Name
				}
				return err
			}
		})
		routes.RegisterOpenAIRoutes(e, middleware.NewRequestExtractor(app.ModelConfigLoader(), app.ModelLoader(), app.ApplicationConfig()), app)
		for _, path := range []string{"/v1/images/upscale", "/images/upscale"} {
			for _, cfg := range configs {
				app.ModelConfigLoader().ReplaceModelConfigs([]config.ModelConfig{cfg})
				for _, explicit := range []bool{true, false} {
					served, selected = "", ""
					var body bytes.Buffer
					form := multipart.NewWriter(&body)
					if explicit {
						Expect(form.WriteField("model", cfg.Name)).To(Succeed())
					}
					Expect(form.WriteField("scale", "4")).To(Succeed())
					file, err := form.CreateFormFile("image", "image.png")
					Expect(err).NotTo(HaveOccurred())
					_, err = file.Write([]byte("IMAGE"))
					Expect(err).NotTo(HaveOccurred())
					Expect(form.Close()).To(Succeed())
					req := httptest.NewRequest(http.MethodPost, path, &body)
					req.Header.Set("Content-Type", form.FormDataContentType())
					rec := httptest.NewRecorder()
					e.ServeHTTP(rec, req)
					eligible := cfg.HasUsecases(config.FLAG_UPSCALE)
					if explicit && eligible {
						Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
						Expect(served).To(Equal(cfg.Name))
					} else {
						// The endpoint still requires a form model even when middleware selects a default.
						Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
						Expect(served).To(BeEmpty())
					}
					if !explicit {
						if eligible {
							Expect(selected).To(Equal(cfg.Name))
						} else {
							Expect(selected).NotTo(Equal(cfg.Name))
						}
					}
				}
			}
		}
	})
})
