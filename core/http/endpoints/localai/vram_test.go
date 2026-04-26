package localai_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("VRAM Estimate Endpoint", func() {
	var (
		app          *echo.Echo
		tempDir      string
		configLoader *config.ModelConfigLoader
		appConfig    *config.ApplicationConfig
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "vram-test-*")
		Expect(err).NotTo(HaveOccurred())

		systemState, err := system.GetSystemState(
			system.WithModelPath(tempDir),
		)
		Expect(err).NotTo(HaveOccurred())

		appConfig = config.NewApplicationConfig(
			config.WithSystemState(systemState),
		)
		configLoader = config.NewModelConfigLoader(tempDir)

		app = echo.New()
		app.POST("/api/models/vram-estimate", VRAMEstimateEndpoint(configLoader, appConfig))
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	It("should return 400 for invalid request body", func() {
		body := bytes.NewBufferString(`not json`)
		req := httptest.NewRequest(http.MethodPost, "/api/models/vram-estimate", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("should return 400 when model name is missing", func() {
		body := bytes.NewBufferString(`{"context_size": 4096}`)
		req := httptest.NewRequest(http.MethodPost, "/api/models/vram-estimate", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusBadRequest))

		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["error"]).To(ContainSubstring("model name is required"))
	})

	It("should return 404 when model config does not exist", func() {
		body := bytes.NewBufferString(`{"model": "nonexistent"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/models/vram-estimate", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("should return no-weight-files message when model has no weight files", func() {
		seedConfig := "name: test-model\nbackend: llama-cpp\n"
		Expect(os.WriteFile(filepath.Join(tempDir, "test-model.yaml"), []byte(seedConfig), 0644)).To(Succeed())
		Expect(configLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

		body := bytes.NewBufferString(`{"model": "test-model"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/models/vram-estimate", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))

		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["message"]).To(ContainSubstring("no weight files"))
	})

	It("should return an estimate for a model with a weight file on disk", func() {
		// Create a dummy GGUF file (not valid GGUF, but the size resolver
		// will stat it and Estimate falls back to size-only estimation).
		dummyData := make([]byte, 1024*1024) // 1 MiB
		Expect(os.WriteFile(filepath.Join(tempDir, "model.gguf"), dummyData, 0644)).To(Succeed())

		seedConfig := "name: test-model\nbackend: llama-cpp\nparameters:\n  model: model.gguf\n"
		Expect(os.WriteFile(filepath.Join(tempDir, "test-model.yaml"), []byte(seedConfig), 0644)).To(Succeed())
		Expect(configLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

		body := bytes.NewBufferString(`{"model": "test-model", "context_size": 4096}`)
		req := httptest.NewRequest(http.MethodPost, "/api/models/vram-estimate", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))

		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		// The response should have non-zero size and vram estimates.
		// JSON numbers unmarshal as float64.
		sizeBytes, ok := resp["sizeBytes"].(float64)
		Expect(ok).To(BeTrue(), "sizeBytes should be a number, got: %v (response: %s)", resp["sizeBytes"], rec.Body.String())
		Expect(sizeBytes).To(BeNumerically(">", 0))
		vramBytes, ok := resp["vramBytes"].(float64)
		Expect(ok).To(BeTrue(), "vramBytes should be a number")
		Expect(vramBytes).To(BeNumerically(">", 0))
		Expect(resp["sizeDisplay"]).NotTo(BeEmpty())
		Expect(resp["vramDisplay"]).NotTo(BeEmpty())
	})
})
