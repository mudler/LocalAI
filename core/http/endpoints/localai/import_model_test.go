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

var _ = Describe("ImportModelEndpoint queued imports", func() {
	var (
		tempDir        string
		app            *echo.Echo
		galleryService *galleryop.GalleryService
	)

	BeforeEach(func() {
		tempDir = GinkgoT().TempDir()
		systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
		Expect(err).ToNot(HaveOccurred())
		applicationConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
		modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)
		ml := model.NewModelLoader(systemState)
		galleryService = galleryop.NewGalleryService(applicationConfig, ml)
		// The worker is deliberately NOT started: these specs assert the
		// admission contract (response shape, job visibility, op payload),
		// not the download itself.
		app = echo.New()
		app.POST("/models/import", ImportModelEndpoint(modelConfigLoader, galleryService, applicationConfig, nil))
	})

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/models/import", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	It("enqueues a config whose model is a URL and answers with a pollable job", func() {
		rec := post(`{"name": "remote", "backend": "llama-cpp", "parameters": {"model": "https://example.com/weights/foo.gguf"}}`)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).To(HaveKey("uuid"))
		Expect(resp["status"]).To(ContainSubstring("/models/jobs/" + resp["uuid"].(string)))

		// Queryable from the instant the ID is handed out, before any
		// worker picks the op up.
		Expect(galleryService.GetStatus(resp["uuid"].(string))).ToNot(BeNil())

		// The op writes the config once downloads finish; admission must
		// not have written it already.
		Expect(filepath.Join(tempDir, "remote.yaml")).ToNot(BeAnExistingFile())

		var op galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]
		Eventually(galleryService.ModelGalleryChannel).Should(Receive(&op))
		Expect(op.GalleryElementName).To(Equal("remote"))
		// Preload-matching name: the loader later finds the file under the
		// name the config resolves, so the config needs no rewriting.
		Expect(op.GalleryElement.Files).To(HaveLen(1))
		Expect(op.GalleryElement.Files[0].Filename).To(Equal("foo.gguf"))
		Expect(op.GalleryElement.Files[0].URI).To(Equal("https://example.com/weights/foo.gguf"))
		Expect(op.GalleryElement.ConfigFile).To(ContainSubstring("name: remote"))
	})

	It("carries download_files entries with their checksums into the op", func() {
		rec := post(`{"name": "assets", "backend": "llama-cpp", "parameters": {"model": "weights.gguf"}, "download_files": [{"filename": "weights.gguf", "sha256": "abc123", "uri": "https://example.com/weights.gguf"}]}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var op galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]
		Eventually(galleryService.ModelGalleryChannel).Should(Receive(&op))
		Expect(op.GalleryElement.Files).To(HaveLen(1))
		Expect(op.GalleryElement.Files[0].SHA256).To(Equal("abc123"))
	})

	It("keeps the synchronous path for configs without remote assets", func() {
		rec := post(`{"name": "local", "backend": "llama-cpp", "parameters": {"model": "already-here.gguf"}}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("created successfully"))
		Expect(filepath.Join(tempDir, "local.yaml")).To(BeAnExistingFile())
		// Nothing was enqueued for a config with no downloads.
		Consistently(galleryService.ModelGalleryChannel, "100ms").ShouldNot(Receive())
	})
})
