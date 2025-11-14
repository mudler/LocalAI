package localai_test

import (
	"bytes"
	"io"
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

			// Define Echo app.
			app := echo.New()
			app.POST("/import-model", ImportModelEndpoint(modelConfigLoader, applicationConfig))

			requestBody := bytes.NewBufferString(`{"name": "foo", "backend": "foo", "model": "foo"}`)

			req := httptest.NewRequest("POST", "/import-model", requestBody)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			body, err := io.ReadAll(rec.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("Model configuration created successfully"))
			Expect(rec.Code).To(Equal(http.StatusOK))

			app.GET("/edit-model/:name", GetEditModelPage(modelConfigLoader, applicationConfig))
			requestBody = bytes.NewBufferString(`{"name": "foo", "parameters": { "model": "foo"}}`)

			req = httptest.NewRequest("GET", "/edit-model/foo", requestBody)
			rec = httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			body, err = io.ReadAll(rec.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(ContainSubstring(`"model":"foo"`))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})
})
