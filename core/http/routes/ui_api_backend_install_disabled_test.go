package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("UI API with backend installation disabled", func() {
	var (
		app       *echo.Echo
		tempDir   string
		appConfig *config.ApplicationConfig
	)
	galleryRevision := 0

	post := func(path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			Expect(json.NewEncoder(&buf).Encode(body)).To(Succeed())
		}
		req := httptest.NewRequest(http.MethodPost, path, &buf)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	decode := func(rec *httptest.ResponseRecorder) map[string]any {
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		return body
	}

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "backend-install-disabled-routes-*")
		Expect(err).NotTo(HaveOccurred())

		systemState, err := system.GetSystemState(
			system.WithBackendPath(filepath.Join(tempDir, "backends")),
			system.WithModelPath(filepath.Join(tempDir, "models")),
			system.WithDisableBackendInstall(true),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.MkdirAll(systemState.Backend.BackendsPath, 0o750)).To(Succeed())
		Expect(os.MkdirAll(systemState.Model.ModelsPath, 0o750)).To(Succeed())

		// A file:// model gallery inside the models path (the only root a file
		// URL is trusted from): one entry needing a backend nobody has, one
		// declaring none. Unique name per spec — the listing is cached.
		entries := []gallery.GalleryModel{
			{Metadata: gallery.Metadata{Name: "needs-vllm", Backend: "vllm"}},
			{Metadata: gallery.Metadata{Name: "no-backend"}},
		}
		out, err := yaml.Marshal(entries)
		Expect(err).NotTo(HaveOccurred())
		galleryName := fmt.Sprintf("disabled-%d", galleryRevision)
		galleryRevision++
		galleryPath := filepath.Join(systemState.Model.ModelsPath, galleryName+".yaml")
		Expect(os.WriteFile(galleryPath, out, 0o600)).To(Succeed())

		modelLoader := model.NewModelLoader(systemState)
		configLoader := config.NewModelConfigLoader(tempDir)
		appConfig = config.NewApplicationConfig(config.WithContext(context.Background()))
		appConfig.SystemState = systemState
		appConfig.DisableBackendInstall = true
		appConfig.Galleries = []config.Gallery{{Name: galleryName, URL: "file://" + galleryPath}}
		appConfig.BackendGalleries = []config.Gallery{}

		galleryService := galleryop.NewGalleryService(appConfig, modelLoader)
		// Not started: nothing here should reach the queue, and a 200 for the
		// pass-through case is asserted on the response alone.
		app = echo.New()
		noopMw := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		routes.RegisterUIAPIRoutes(app, configLoader, modelLoader, appConfig, galleryService, galleryop.NewOpCache(galleryService), nil, noopMw)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	It("refuses backend installs and upgrades with 403", func() {
		for _, path := range []string{
			"/api/backends/install/llama-cpp",
			"/api/backends/install-external",
			"/api/backends/upgrade/llama-cpp",
		} {
			rec := post(path, map[string]string{"uri": "quay.io/example/backend:latest", "name": "x"})
			Expect(rec.Code).To(Equal(http.StatusForbidden), path)
			Expect(decode(rec)["code"]).To(Equal("backend_install_disabled"), path)
		}
	})

	It("refuses a model whose backend is missing with 409 and names the backend", func() {
		rec := post("/api/models/install/"+appConfig.Galleries[0].Name+"@needs-vllm", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		body := decode(rec)
		Expect(body["code"]).To(Equal("backend_not_installed"))
		Expect(body["backend"]).To(Equal("vllm"))
	})

	It("lets a model that names no backend through", func() {
		rec := post("/api/models/install/"+appConfig.Galleries[0].Name+"@no-backend", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})

// A distributed frontend keeps no backends of its own; what counts is what the
// workers have. clusterBackendManager stands in for the NATS-backed manager
// and answers availability from a fixed set.
type clusterBackendManager struct {
	available map[string]bool
}

func (clusterBackendManager) InstallBackend(_ context.Context, _ *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
	return nil
}
func (clusterBackendManager) DeleteBackend(_ string) error { return nil }
func (clusterBackendManager) ListBackends() (gallery.SystemBackends, error) {
	return gallery.SystemBackends{}, nil
}
func (clusterBackendManager) UpgradeBackend(_ context.Context, _ *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
	return nil
}
func (clusterBackendManager) CheckUpgrades(_ context.Context) (map[string]gallery.UpgradeInfo, error) {
	return nil, nil
}
func (clusterBackendManager) IsDistributed() bool { return true }
func (m clusterBackendManager) BackendAvailable(name string) (bool, error) {
	return m.available[name], nil
}

var _ = Describe("Model installs on a distributed frontend with backend installation disabled", func() {
	var (
		app         *echo.Echo
		tempDir     string
		galleryName string
	)
	galleryRevision := 0

	setup := func(workersHave map[string]bool) {
		var err error
		tempDir, err = os.MkdirTemp("", "backend-install-disabled-cluster-*")
		Expect(err).NotTo(HaveOccurred())

		// Empty backends directory: the frontend installs nothing locally.
		systemState, err := system.GetSystemState(
			system.WithBackendPath(filepath.Join(tempDir, "backends")),
			system.WithModelPath(filepath.Join(tempDir, "models")),
			system.WithDisableBackendInstall(true),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.MkdirAll(systemState.Backend.BackendsPath, 0o750)).To(Succeed())
		Expect(os.MkdirAll(systemState.Model.ModelsPath, 0o750)).To(Succeed())

		entries := []gallery.GalleryModel{{Metadata: gallery.Metadata{Name: "needs-vllm", Backend: "vllm"}}}
		out, err := yaml.Marshal(entries)
		Expect(err).NotTo(HaveOccurred())
		galleryName = fmt.Sprintf("cluster-%d", galleryRevision)
		galleryRevision++
		galleryPath := filepath.Join(systemState.Model.ModelsPath, galleryName+".yaml")
		Expect(os.WriteFile(galleryPath, out, 0o600)).To(Succeed())

		modelLoader := model.NewModelLoader(systemState)
		configLoader := config.NewModelConfigLoader(tempDir)
		appConfig := config.NewApplicationConfig(config.WithContext(context.Background()))
		appConfig.SystemState = systemState
		appConfig.DisableBackendInstall = true
		appConfig.Galleries = []config.Gallery{{Name: galleryName, URL: "file://" + galleryPath}}
		appConfig.BackendGalleries = []config.Gallery{}

		galleryService := galleryop.NewGalleryService(appConfig, modelLoader)
		galleryService.SetBackendManager(clusterBackendManager{available: workersHave})
		app = echo.New()
		noopMw := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		routes.RegisterUIAPIRoutes(app, configLoader, modelLoader, appConfig, galleryService, galleryop.NewOpCache(galleryService), nil, noopMw)
	}

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	post := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	It("accepts a model whose backend a worker already has", func() {
		setup(map[string]bool{"vllm": true})
		rec := post("/api/models/install/" + galleryName + "@needs-vllm")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	})

	It("refuses a model whose backend no worker has", func() {
		setup(map[string]bool{})
		rec := post("/api/models/install/" + galleryName + "@needs-vllm")
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body["code"]).To(Equal("backend_not_installed"))
		Expect(body["backend"]).To(Equal("vllm"))
	})
})
