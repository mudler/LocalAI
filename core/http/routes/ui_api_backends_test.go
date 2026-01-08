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
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
		galleryService *services.GalleryService
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

		galleryService = services.NewGalleryService(appConfig, modelLoader)
		// Start the gallery service
		err = galleryService.Start(context.Background(), configLoader, systemState)
		Expect(err).NotTo(HaveOccurred())

		app = echo.New()

		// Register the API routes for backends
		opcache := services.NewOpCache(galleryService)
		routes.RegisterUIAPIRoutes(app, configLoader, modelLoader, appConfig, galleryService, opcache, nil)
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

			var response map[string]interface{}
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

			var response map[string]interface{}
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

			var response map[string]interface{}
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

			var response map[string]interface{}
			err := json.Unmarshal(rec.Body.Bytes(), &response)
			Expect(err).NotTo(HaveOccurred())
			Expect(response["queued"]).To(Equal(true))
			Expect(response["processed"]).To(Equal(false))
		})
	})
})

// Helper function to make POST request
func postRequest(url string, body interface{}) (*http.Response, error) {
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
func readResponseBody(resp *http.Response) (map[string]interface{}, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	return result, err
}

// Avoid unused import errors
var _ = gallery.GalleryModel{}
