package localai_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ImportModelURIEndpoint ambiguity handling", func() {

	var (
		tempDir string
		app     *echo.Echo
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "import-model-test")
		Expect(err).ToNot(HaveOccurred())

		systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
		Expect(err).ToNot(HaveOccurred())

		applicationConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
		modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)
		ml := model.NewModelLoader(systemState)
		galleryService := galleryop.NewGalleryService(applicationConfig, ml)

		app = echo.New()
		app.POST("/models/import-uri", ImportModelURIEndpoint(modelConfigLoader, applicationConfig, galleryService, nil))
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	It("returns HTTP 400 with a structured ambiguity body when the HF pipeline_tag matches a known modality but no importer matches", func() {
		// hexgrad/Kokoro-82M:
		//   - pipeline_tag: "text-to-speech" (whitelisted modality)
		//   - no tokenizer.json, no .gguf, no model_index.json, not mlx-community/
		// No importer matches, yet the modality is known → ErrAmbiguousImport.
		body := bytes.NewBufferString(`{"uri": "https://huggingface.co/hexgrad/Kokoro-82M", "preferences": {}}`)
		req := httptest.NewRequest("POST", "/models/import-uri", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusBadRequest))

		respBody, err := io.ReadAll(rec.Body)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]any
		Expect(json.Unmarshal(respBody, &parsed)).To(Succeed())
		Expect(parsed).To(HaveKey("error"))
		Expect(parsed["error"]).To(Equal("ambiguous import"))
		Expect(parsed).To(HaveKey("detail"))
		Expect(parsed).To(HaveKey("hint"))
	})
})
