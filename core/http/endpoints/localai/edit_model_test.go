package localai_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// testRenderer is a simple renderer for tests that returns JSON
type testRenderer struct{}

func (t *testRenderer) Render(w io.Writer, name string, data any, c echo.Context) error {
	// For tests, just return the data as JSON
	return json.NewEncoder(w).Encode(data)
}

var _ = Describe("Edit Model test", func() {

	var tempDir string
	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "localai-test")
		Expect(err).ToNot(HaveOccurred())
	})
	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Context("Edit Model endpoint", func() {
		It("should edit a model", func() {
			systemState, err := system.GetSystemState(
				system.WithModelPath(filepath.Join(tempDir)),
			)
			Expect(err).ToNot(HaveOccurred())

			applicationConfig := config.NewApplicationConfig(
				config.WithSystemState(systemState),
			)
			//modelLoader := model.NewModelLoader(systemState, true)
			modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)

			// Define Echo app and register all routes upfront
			app := echo.New()
			// Set up a simple renderer for the test
			app.Renderer = &testRenderer{}
			app.POST("/import-model", ImportModelEndpoint(modelConfigLoader, applicationConfig))
			app.GET("/edit-model/:name", GetEditModelPage(modelConfigLoader, applicationConfig))

			requestBody := bytes.NewBufferString(`{"name": "foo", "backend": "foo", "model": "foo"}`)

			req := httptest.NewRequest("POST", "/import-model", requestBody)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			body, err := io.ReadAll(rec.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("Model configuration created successfully"))
			Expect(rec.Code).To(Equal(http.StatusOK))

			req = httptest.NewRequest("GET", "/edit-model/foo", nil)
			rec = httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			body, err = io.ReadAll(rec.Body)
			Expect(err).ToNot(HaveOccurred())
			// The response contains the model configuration with backend field
			Expect(string(body)).To(ContainSubstring(`"backend":"foo"`))
			Expect(string(body)).To(ContainSubstring(`"name":"foo"`))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("renames the config file on disk when the YAML name changes", func() {
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempDir),
			)
			Expect(err).ToNot(HaveOccurred())
			applicationConfig := config.NewApplicationConfig(
				config.WithSystemState(systemState),
			)
			modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)
			modelLoader := model.NewModelLoader(systemState)

			oldYAML := "name: oldname\nbackend: llama\nmodel: foo\n"
			oldPath := filepath.Join(tempDir, "oldname.yaml")
			Expect(os.WriteFile(oldPath, []byte(oldYAML), 0644)).To(Succeed())
			// Drop a gallery metadata file so we can check it is renamed too.
			galleryOldPath := filepath.Join(tempDir, gallery.GalleryFileName("oldname"))
			Expect(os.WriteFile(galleryOldPath, []byte("name: oldname\n"), 0644)).To(Succeed())

			Expect(modelConfigLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())
			_, exists := modelConfigLoader.GetModelConfig("oldname")
			Expect(exists).To(BeTrue())

			app := echo.New()
			app.POST("/models/edit/:name", EditModelEndpoint(modelConfigLoader, modelLoader, applicationConfig))

			newYAML := "name: newname\nbackend: llama\nmodel: foo\n"
			req := httptest.NewRequest("POST", "/models/edit/oldname", bytes.NewBufferString(newYAML))
			req.Header.Set("Content-Type", "application/x-yaml")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			body, err := io.ReadAll(rec.Body)
			Expect(err).ToNot(HaveOccurred(), string(body))
			Expect(rec.Code).To(Equal(http.StatusOK), string(body))

			// Old file is gone, new file exists.
			_, err = os.Stat(oldPath)
			Expect(os.IsNotExist(err)).To(BeTrue(), "old config file should be removed")
			newPath := filepath.Join(tempDir, "newname.yaml")
			_, err = os.Stat(newPath)
			Expect(err).ToNot(HaveOccurred(), "new config file should exist")

			// Gallery metadata followed the rename.
			_, err = os.Stat(galleryOldPath)
			Expect(os.IsNotExist(err)).To(BeTrue(), "old gallery metadata should be removed")
			_, err = os.Stat(filepath.Join(tempDir, gallery.GalleryFileName("newname")))
			Expect(err).ToNot(HaveOccurred(), "new gallery metadata should exist")

			// In-memory config loader holds exactly one entry, keyed by the new name.
			_, exists = modelConfigLoader.GetModelConfig("oldname")
			Expect(exists).To(BeFalse(), "old name must not remain in config loader")
			_, exists = modelConfigLoader.GetModelConfig("newname")
			Expect(exists).To(BeTrue(), "new name must be present in config loader")
			Expect(modelConfigLoader.GetAllModelsConfigs()).To(HaveLen(1))
		})

		It("rejects a rename when the new name already exists", func() {
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempDir),
			)
			Expect(err).ToNot(HaveOccurred())
			applicationConfig := config.NewApplicationConfig(
				config.WithSystemState(systemState),
			)
			modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)
			modelLoader := model.NewModelLoader(systemState)

			Expect(os.WriteFile(
				filepath.Join(tempDir, "oldname.yaml"),
				[]byte("name: oldname\nbackend: llama\nmodel: foo\n"),
				0644,
			)).To(Succeed())
			Expect(os.WriteFile(
				filepath.Join(tempDir, "newname.yaml"),
				[]byte("name: newname\nbackend: llama\nmodel: bar\n"),
				0644,
			)).To(Succeed())
			Expect(modelConfigLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

			app := echo.New()
			app.POST("/models/edit/:name", EditModelEndpoint(modelConfigLoader, modelLoader, applicationConfig))

			req := httptest.NewRequest(
				"POST",
				"/models/edit/oldname",
				bytes.NewBufferString("name: newname\nbackend: llama\nmodel: foo\n"),
			)
			req.Header.Set("Content-Type", "application/x-yaml")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusConflict))

			// Neither file should have been rewritten.
			oldBody, err := os.ReadFile(filepath.Join(tempDir, "oldname.yaml"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(oldBody)).To(ContainSubstring("name: oldname"))
			newBody, err := os.ReadFile(filepath.Join(tempDir, "newname.yaml"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(newBody)).To(ContainSubstring("model: bar"))
		})

		It("rejects a rename whose new name contains a path separator", func() {
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempDir),
			)
			Expect(err).ToNot(HaveOccurred())
			applicationConfig := config.NewApplicationConfig(
				config.WithSystemState(systemState),
			)
			modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)
			modelLoader := model.NewModelLoader(systemState)

			Expect(os.WriteFile(
				filepath.Join(tempDir, "oldname.yaml"),
				[]byte("name: oldname\nbackend: llama\nmodel: foo\n"),
				0644,
			)).To(Succeed())
			Expect(modelConfigLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

			app := echo.New()
			app.POST("/models/edit/:name", EditModelEndpoint(modelConfigLoader, modelLoader, applicationConfig))

			req := httptest.NewRequest(
				"POST",
				"/models/edit/oldname",
				bytes.NewBufferString("name: evil/name\nbackend: llama\nmodel: foo\n"),
			)
			req.Header.Set("Content-Type", "application/x-yaml")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			_, err = os.Stat(filepath.Join(tempDir, "oldname.yaml"))
			Expect(err).ToNot(HaveOccurred(), "original file must not be removed")
		})
	})
})
