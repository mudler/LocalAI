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
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Config Metadata Endpoints", func() {
	var (
		app          *echo.Echo
		tempDir      string
		configLoader *config.ModelConfigLoader
		modelLoader  *model.ModelLoader
		appConfig    *config.ApplicationConfig
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "config-meta-test-*")
		Expect(err).NotTo(HaveOccurred())

		systemState, err := system.GetSystemState(
			system.WithModelPath(tempDir),
		)
		Expect(err).NotTo(HaveOccurred())

		appConfig = config.NewApplicationConfig(
			config.WithSystemState(systemState),
		)
		configLoader = config.NewModelConfigLoader(tempDir)
		modelLoader = model.NewModelLoader(systemState)

		app = echo.New()
		app.GET("/api/models/config-metadata", ConfigMetadataEndpoint())
		app.GET("/api/models/config-metadata/autocomplete/:provider", AutocompleteEndpoint(configLoader, modelLoader, appConfig))
		app.PATCH("/api/models/config-json/:name", PatchConfigEndpoint(configLoader, modelLoader, appConfig))
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Context("GET /api/models/config-metadata", func() {
		It("should return section index when no section param", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/models/config-metadata", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp).To(HaveKey("hint"))
			Expect(resp).To(HaveKey("sections"))

			sections, ok := resp["sections"].([]any)
			Expect(ok).To(BeTrue())
			Expect(sections).NotTo(BeEmpty())

			// Verify known section IDs are present
			ids := make([]string, len(sections))
			for i, s := range sections {
				sec := s.(map[string]any)
				Expect(sec).To(HaveKey("id"))
				Expect(sec).To(HaveKey("label"))
				Expect(sec).To(HaveKey("url"))
				ids[i] = sec["id"].(string)
			}
			Expect(ids).To(ContainElements("general", "parameters"))
		})

		It("should return all fields when section=all", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/models/config-metadata?section=all", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp).To(HaveKey("fields"))

			fields, ok := resp["fields"].([]any)
			Expect(ok).To(BeTrue())
			Expect(len(fields)).To(BeNumerically(">=", 80))
		})

		It("should filter by section", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/models/config-metadata?section=general", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var fields []map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &fields)).To(Succeed())
			Expect(fields).NotTo(BeEmpty())

			for _, f := range fields {
				Expect(f["section"]).To(Equal("general"))
			}
		})

		It("should return 404 for unknown section", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/models/config-metadata?section=nonexistent", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})
	})

	Context("GET /api/models/config-metadata/autocomplete/:provider", func() {
		It("should return values for backends provider", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/models/config-metadata/autocomplete/backends", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp).To(HaveKey("values"))
		})

		It("should return model names for models provider", func() {
			// Seed a model config
			seedConfig := `name: test-model
backend: llama-cpp
`
			Expect(os.WriteFile(filepath.Join(tempDir, "test-model.yaml"), []byte(seedConfig), 0644)).To(Succeed())
			Expect(configLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

			req := httptest.NewRequest(http.MethodGet, "/api/models/config-metadata/autocomplete/models", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())

			values, ok := resp["values"].([]any)
			Expect(ok).To(BeTrue())
			Expect(values).To(ContainElement("test-model"))
		})

		It("should return 404 for unknown provider", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/models/config-metadata/autocomplete/unknown", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})
	})

	Context("PATCH /api/models/config-json/:name", func() {
		It("should return 404 for nonexistent model", func() {
			body := bytes.NewBufferString(`{"backend": "bar"}`)
			req := httptest.NewRequest(http.MethodPatch, "/api/models/config-json/nonexistent", body)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})

		It("should return 400 for empty body", func() {
			// Seed a model config
			seedConfig := `name: test-model
backend: llama-cpp
`
			Expect(os.WriteFile(filepath.Join(tempDir, "test-model.yaml"), []byte(seedConfig), 0644)).To(Succeed())
			Expect(configLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

			req := httptest.NewRequest(http.MethodPatch, "/api/models/config-json/test-model", nil)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})

		It("should return 400 for invalid JSON", func() {
			seedConfig := `name: test-model
backend: llama-cpp
`
			Expect(os.WriteFile(filepath.Join(tempDir, "test-model.yaml"), []byte(seedConfig), 0644)).To(Succeed())
			Expect(configLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

			body := bytes.NewBufferString(`not json`)
			req := httptest.NewRequest(http.MethodPatch, "/api/models/config-json/test-model", body)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})

		It("should merge a field update and persist to disk", func() {
			seedConfig := `name: test-model
backend: llama-cpp
`
			configPath := filepath.Join(tempDir, "test-model.yaml")
			Expect(os.WriteFile(configPath, []byte(seedConfig), 0644)).To(Succeed())
			Expect(configLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

			body := bytes.NewBufferString(`{"backend": "vllm"}`)
			req := httptest.NewRequest(http.MethodPatch, "/api/models/config-json/test-model", body)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp["success"]).To(BeTrue())

			// Verify the reloaded config has the updated value
			updatedConfig, exists := configLoader.GetModelConfig("test-model")
			Expect(exists).To(BeTrue())
			Expect(updatedConfig.Backend).To(Equal("vllm"))

			// Verify the file on disk was updated
			data, err := os.ReadFile(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("vllm"))
		})

		It("should not persist runtime defaults (SetDefaults values) to disk", func() {
			// Create a minimal pipeline config - no sampling params
			seedConfig := `name: gpt-realtime
pipeline:
    vad: silero-vad
    transcription: whisper-base
    llm: llama3
    tts: piper
`
			configPath := filepath.Join(tempDir, "gpt-realtime.yaml")
			Expect(os.WriteFile(configPath, []byte(seedConfig), 0644)).To(Succeed())
			Expect(configLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

			// PATCH with a small change to the pipeline
			body := bytes.NewBufferString(`{"pipeline": {"tts": "vibevoice"}}`)
			req := httptest.NewRequest(http.MethodPatch, "/api/models/config-json/gpt-realtime", body)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			// Read the file from disk and verify no spurious defaults leaked
			data, err := os.ReadFile(configPath)
			Expect(err).NotTo(HaveOccurred())
			fileContent := string(data)

			// The patched value should be present
			Expect(fileContent).To(ContainSubstring("vibevoice"))

			// Runtime-only defaults from SetDefaults() should NOT be in the file
			Expect(fileContent).NotTo(ContainSubstring("top_p"))
			Expect(fileContent).NotTo(ContainSubstring("top_k"))
			Expect(fileContent).NotTo(ContainSubstring("temperature"))
			Expect(fileContent).NotTo(ContainSubstring("mirostat"))
			Expect(fileContent).NotTo(ContainSubstring("mmap"))
			Expect(fileContent).NotTo(ContainSubstring("mmlock"))
			Expect(fileContent).NotTo(ContainSubstring("threads"))
			Expect(fileContent).NotTo(ContainSubstring("low_vram"))
			Expect(fileContent).NotTo(ContainSubstring("embeddings"))
			Expect(fileContent).NotTo(ContainSubstring("f16"))

			// Original fields should still be present
			Expect(fileContent).To(ContainSubstring("gpt-realtime"))
			Expect(fileContent).To(ContainSubstring("silero-vad"))
			Expect(fileContent).To(ContainSubstring("whisper-base"))
			Expect(fileContent).To(ContainSubstring("llama3"))
		})
	})
})
