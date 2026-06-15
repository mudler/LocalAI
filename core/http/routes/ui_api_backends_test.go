package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

func TestRoutes(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Routes Suite")
}

var _ = Describe("Backend API Routes", func() {
	var (
		app            *echo.Echo
		tempDir        string
		appConfig      *config.ApplicationConfig
		galleryService *galleryop.GalleryService
		modelLoader    *model.ModelLoader
		systemState    *system.SystemState
		configLoader   *config.ModelConfigLoader
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "backend-routes-test-*")
		Expect(err).NotTo(HaveOccurred())

		systemState, err = system.GetSystemState(
			system.WithBackendPath(filepath.Join(tempDir, "backends")),
		)
		Expect(err).NotTo(HaveOccurred())
		systemState.Model.ModelsPath = filepath.Join(tempDir, "models")

		// Create directories
		err = os.MkdirAll(systemState.Backend.BackendsPath, 0750)
		Expect(err).NotTo(HaveOccurred())
		err = os.MkdirAll(systemState.Model.ModelsPath, 0750)
		Expect(err).NotTo(HaveOccurred())

		modelLoader = model.NewModelLoader(systemState)
		configLoader = config.NewModelConfigLoader(tempDir)

		appConfig = config.NewApplicationConfig(
			config.WithContext(context.Background()),
		)
		appConfig.SystemState = systemState
		appConfig.BackendGalleries = []config.Gallery{}

		galleryService = galleryop.NewGalleryService(appConfig, modelLoader)
		// Start the gallery service
		err = galleryService.Start(context.Background(), configLoader, systemState)
		Expect(err).NotTo(HaveOccurred())

		app = echo.New()

		// Register the API routes for backends
		opcache := galleryop.NewOpCache(galleryService)
		// Use a no-op admin middleware for tests
		noopMw := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		routes.RegisterUIAPIRoutes(app, configLoader, modelLoader, appConfig, galleryService, opcache, nil, noopMw)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("POST /api/backends/install-external", func() {
		It("should return error when URI is missing", func() {
			reqBody := map[string]string{
				"name": "test-backend",
			}
			jsonBody, err := json.Marshal(reqBody)
			Expect(err).NotTo(HaveOccurred())

			req := httptest.NewRequest(http.MethodPost, "/api/backends/install-external", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusBadRequest))

			var response map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			Expect(err).NotTo(HaveOccurred())
			Expect(response["error"]).To(Equal("uri is required"))
		})

		It("should accept valid request and return job ID", func() {
			reqBody := map[string]string{
				"uri":   "oci://quay.io/example/backend:latest",
				"name":  "test-backend",
				"alias": "test-alias",
			}
			jsonBody, err := json.Marshal(reqBody)
			Expect(err).NotTo(HaveOccurred())

			req := httptest.NewRequest(http.MethodPost, "/api/backends/install-external", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var response map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			Expect(err).NotTo(HaveOccurred())
			Expect(response["jobID"]).NotTo(BeEmpty())
			Expect(response["message"]).To(Equal("External backend installation started"))
		})

		It("should accept request with only URI", func() {
			reqBody := map[string]string{
				"uri": "/path/to/local/backend",
			}
			jsonBody, err := json.Marshal(reqBody)
			Expect(err).NotTo(HaveOccurred())

			req := httptest.NewRequest(http.MethodPost, "/api/backends/install-external", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var response map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			Expect(err).NotTo(HaveOccurred())
			Expect(response["jobID"]).NotTo(BeEmpty())
		})

		It("should return error for invalid JSON body", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/backends/install-external", bytes.NewBufferString("invalid json"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("GET /api/backends/job/:uid", func() {
		It("should return queued status for unknown job", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/backends/job/unknown-job-id", nil)
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var response map[string]any
			err := json.Unmarshal(rec.Body.Bytes(), &response)
			Expect(err).NotTo(HaveOccurred())
			Expect(response["queued"]).To(Equal(true))
			Expect(response["processed"]).To(Equal(false))
		})
	})

	Describe("Backend upgrade API", func() {
		var (
			galleryFile        string
			upgradeApp         *echo.Echo
			upgradeGallerySvc  *galleryop.GalleryService
		)

		BeforeEach(func() {
			// Place gallery file inside backends dir so it passes trusted root checks
			galleryFile = filepath.Join(systemState.Backend.BackendsPath, "test-gallery.yaml")

			// Create a fake "v1" backend on disk (simulates a previously installed backend)
			backendDir := filepath.Join(systemState.Backend.BackendsPath, "test-upgrade-backend")
			err := os.MkdirAll(backendDir, 0750)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(backendDir, "run.sh"), []byte("#!/bin/sh\necho v1"), 0755)
			Expect(err).NotTo(HaveOccurred())

			// Write metadata.json for the installed backend (v1)
			metadata := map[string]string{
				"name":         "test-upgrade-backend",
				"version":      "1.0.0",
				"installed_at": "2024-01-01T00:00:00Z",
			}
			metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(backendDir, "metadata.json"), metadataBytes, 0644)
			Expect(err).NotTo(HaveOccurred())

			// Create a "v2" source directory (the upgrade target)
			// Must be inside backends path to pass trusted root checks
			v2SrcDir := filepath.Join(systemState.Backend.BackendsPath, "v2-backend-src")
			err = os.MkdirAll(v2SrcDir, 0750)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(v2SrcDir, "run.sh"), []byte("#!/bin/sh\necho v2"), 0755)
			Expect(err).NotTo(HaveOccurred())

			// Write gallery YAML pointing to v2
			galleryData := []map[string]any{
				{
					"name":    "test-upgrade-backend",
					"uri":     v2SrcDir,
					"version": "2.0.0",
				},
			}
			yamlBytes, err := yaml.Marshal(galleryData)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(galleryFile, yamlBytes, 0644)
			Expect(err).NotTo(HaveOccurred())

			// Configure the gallery in appConfig BEFORE creating the gallery service
			// so the backend manager captures the correct galleries
			appConfig.BackendGalleries = []config.Gallery{
				{Name: "test", URL: "file://" + galleryFile},
			}

			// Create a fresh gallery service with the upgrade gallery configured
			upgradeGallerySvc = galleryop.NewGalleryService(appConfig, modelLoader)
			err = upgradeGallerySvc.Start(context.Background(), configLoader, systemState)
			Expect(err).NotTo(HaveOccurred())

			// Register routes with the upgrade-aware gallery service
			upgradeApp = echo.New()
			opcache := galleryop.NewOpCache(upgradeGallerySvc)
			noopMw := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
			routes.RegisterUIAPIRoutes(upgradeApp, configLoader, modelLoader, appConfig, upgradeGallerySvc, opcache, nil, noopMw)
		})

		Describe("GET /api/backends/upgrades", func() {
			It("should return available upgrades", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/backends/upgrades", nil)
				rec := httptest.NewRecorder()

				upgradeApp.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))

				var response map[string]any
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				Expect(err).NotTo(HaveOccurred())
				// Response is empty (upgrade checker not running in test),
				// but the endpoint should not error
			})
		})

		Describe("POST /api/backends/upgrade/:name", func() {
			It("should accept upgrade request and return job ID", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/backends/upgrade/test-upgrade-backend", nil)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()

				upgradeApp.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))

				var response map[string]any
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				Expect(err).NotTo(HaveOccurred())
				Expect(response["uuid"]).NotTo(BeEmpty())
				Expect(response["statusUrl"]).NotTo(BeEmpty())
			})

			It("should upgrade the backend and update metadata", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/backends/upgrade/test-upgrade-backend", nil)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()

				upgradeApp.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))

				var response map[string]any
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				Expect(err).NotTo(HaveOccurred())
				jobID := response["uuid"].(string)

				// Wait for the upgrade job to complete
				Eventually(func() bool {
					jobReq := httptest.NewRequest(http.MethodGet, "/api/backends/job/"+jobID, nil)
					jobRec := httptest.NewRecorder()
					upgradeApp.ServeHTTP(jobRec, jobReq)

					var jobResp map[string]any
					json.Unmarshal(jobRec.Body.Bytes(), &jobResp)

					processed, _ := jobResp["processed"].(bool)
					return processed
				}, "10s", "200ms").Should(BeTrue())

				// Verify the backend was upgraded: run.sh should now contain "v2"
				runContent, err := os.ReadFile(filepath.Join(
					systemState.Backend.BackendsPath, "test-upgrade-backend", "run.sh"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(runContent)).To(ContainSubstring("v2"))

				// Verify metadata was updated with new version
				metadataContent, err := os.ReadFile(filepath.Join(
					systemState.Backend.BackendsPath, "test-upgrade-backend", "metadata.json"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(metadataContent)).To(ContainSubstring(`"version": "2.0.0"`))
			})
		})

		Describe("POST /api/backends/upgrades/check", func() {
			It("should trigger an upgrade check and return 200", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/backends/upgrades/check", nil)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()

				upgradeApp.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))
			})
		})
	})
})

// Helper function to make POST request
func postRequest(url string, body any) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	return client.Do(req)
}

// Helper function to read response body
func readResponseBody(resp *http.Response) (map[string]any, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	err = json.Unmarshal(body, &result)
	return result, err
}

// Avoid unused import errors
var _ = gallery.GalleryModel{}
