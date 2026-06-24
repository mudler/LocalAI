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
		// nari-labs/Dia-1.6B:
		//   - pipeline_tag: "text-to-speech" (whitelisted modality)
		//   - no tokenizer.json, no .gguf, no model_index.json, not mlx-community/
		//   - owner/repo-name match none of the Batch-2 TTS importers
		// No importer matches, yet the modality is known → ErrAmbiguousImport.
		// (Previously referenced hexgrad/Kokoro-82M; Batch 2 added a dedicated
		// kokoro importer that now matches that repo, so the ambiguity fixture
		// moved to nari-labs/Dia-1.6B which remains unclaimed.)
		body := bytes.NewBufferString(`{"uri": "https://huggingface.co/nari-labs/Dia-1.6B", "preferences": {}}`)
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

	It("exposes the HF modality on the structured ambiguity body", func() {
		body := bytes.NewBufferString(`{"uri": "https://huggingface.co/nari-labs/Dia-1.6B", "preferences": {}}`)
		req := httptest.NewRequest("POST", "/models/import-uri", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		respBody, err := io.ReadAll(rec.Body)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]any
		Expect(json.Unmarshal(respBody, &parsed)).To(Succeed())
		Expect(parsed).To(HaveKey("modality"))
		Expect(parsed["modality"]).To(Equal("tts"))
	})

	It("returns TTS candidate backends on the ambiguity body", func() {
		body := bytes.NewBufferString(`{"uri": "https://huggingface.co/nari-labs/Dia-1.6B", "preferences": {}}`)
		req := httptest.NewRequest("POST", "/models/import-uri", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		respBody, err := io.ReadAll(rec.Body)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]any
		Expect(json.Unmarshal(respBody, &parsed)).To(Succeed())
		Expect(parsed).To(HaveKey("candidates"))

		candidatesRaw, ok := parsed["candidates"].([]any)
		Expect(ok).To(BeTrue(), "candidates should be a JSON array")
		candidates := make([]string, 0, len(candidatesRaw))
		for _, c := range candidatesRaw {
			s, ok := c.(string)
			Expect(ok).To(BeTrue())
			candidates = append(candidates, s)
		}
		// TTS importers must appear; text-LLM backends must not.
		Expect(candidates).To(ContainElements("piper", "bark", "kokoro"))
		Expect(candidates).ToNot(ContainElement("llama-cpp"))
		Expect(candidates).ToNot(ContainElement("vllm"))
	})
})
