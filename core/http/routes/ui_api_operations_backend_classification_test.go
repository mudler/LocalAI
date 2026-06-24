package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/system"
)

// /api/operations classifies an op as backend-vs-model by looking the name up
// in the backend gallery. That lookup must not be capability-filtered: on a
// distributed controller the GPU lives on a worker, so a GPU-only backend
// install was misfiled as a model operation in the UI panel.
var _ = Describe("/api/operations backend classification", func() {
	noopMw := func(next echo.HandlerFunc) echo.HandlerFunc { return next }

	It("classifies a GPU-only backend op as a backend on a GPU-less host", func() {
		tmpDir, err := os.MkdirTemp("", "ops-classification-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(tmpDir)).To(Succeed())
		})

		galleryPath := filepath.Join(tmpDir, "gallery.yaml")
		data, err := yaml.Marshal([]gallery.GalleryBackend{
			{
				Metadata: gallery.Metadata{Name: "longcat-video"},
				CapabilitiesMap: map[string]string{
					"nvidia":             "longcat-video-nvidia",
					"nvidia-cuda-13":     "longcat-video-cuda-13",
					"nvidia-l4t-cuda-13": "longcat-video-l4t-cuda-13",
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(galleryPath, data, 0o644)).To(Succeed())

		appCfg := &config.ApplicationConfig{
			BackendGalleries: []config.Gallery{{Name: "test-gallery", URL: "file://" + galleryPath}},
			SystemState: system.NewCapabilityState("default",
				system.WithBackendPath(tmpDir), system.WithBackendSystemPath(tmpDir)),
		}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)

		jobID := "job-longcat-1"
		// Set (not SetBackend) so the handler has to fall back to the gallery
		// lookup — exactly the path that was misclassifying.
		opcache.Set("longcat-video", jobID)

		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, galleryService, opcache, &application.Application{}, noopMw)

		req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope struct {
			Operations []map[string]any `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())

		var found map[string]any
		for _, op := range envelope.Operations {
			if op["jobID"] == jobID {
				found = op
				break
			}
		}
		Expect(found).ToNot(BeNil(), "operation should appear in /api/operations")
		Expect(found["isBackend"]).To(Equal(true))
	})
})
