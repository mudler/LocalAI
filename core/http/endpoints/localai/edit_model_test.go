package localai_test

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/gofiber/fiber/v2"
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

			// Define Fiber app.
			app := fiber.New()
			app.Put("/import-model", ImportModelEndpoint(modelConfigLoader, applicationConfig))

			requestBody := bytes.NewBufferString(`{"name": "foo", "backend": "foo", "model": "foo"}`)

			req := httptest.NewRequest("PUT", "/import-model", requestBody)
			resp, err := app.Test(req, 5000)
			Expect(err).ToNot(HaveOccurred())

			body, err := io.ReadAll(resp.Body)
			defer resp.Body.Close()
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("Model configuration created successfully"))
			Expect(resp.StatusCode).To(Equal(fiber.StatusOK))

			app.Get("/edit-model/:name", EditModelEndpoint(modelConfigLoader, applicationConfig))
			requestBody = bytes.NewBufferString(`{"name": "foo", "parameters": { "model": "foo"}}`)

			req = httptest.NewRequest("GET", "/edit-model/foo", requestBody)
			resp, _ = app.Test(req, 1)

			body, err = io.ReadAll(resp.Body)
			defer resp.Body.Close()
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(ContainSubstring(`"model":"foo"`))
			Expect(resp.StatusCode).To(Equal(fiber.StatusOK))
		})
	})
})
