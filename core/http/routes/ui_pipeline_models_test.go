package routes_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pipeline models API", func() {
	var (
		app          *echo.Echo
		tempDir      string
		configLoader *config.ModelConfigLoader
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "pipeline-models-test-*")
		Expect(err).NotTo(HaveOccurred())

		configLoader = config.NewModelConfigLoader(tempDir)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tempDir)).To(Succeed())
	})

	writeConfig := func(name, body string) {
		path := filepath.Join(tempDir, name+".yaml")
		Expect(os.WriteFile(path, []byte(body), 0o644)).To(Succeed())
	}

	queryPipelineModels := func() []map[string]any {
		Expect(configLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

		app = echo.New()
		routes.RegisterUIRoutes(app, configLoader, nil, nil, func(next echo.HandlerFunc) echo.HandlerFunc { return next })

		req := httptest.NewRequest(http.MethodGet, "/api/pipeline-models", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		body, err := io.ReadAll(rec.Body)
		Expect(err).NotTo(HaveOccurred())

		var got []map[string]any
		Expect(json.Unmarshal(body, &got)).To(Succeed())
		return got
	}

	It("returns models with an explicit VAD/STT/LLM/TTS pipeline", func() {
		writeConfig("legacy-pipeline", `
name: legacy-pipeline
backend: llama-cpp
pipeline:
  vad: silero
  transcription: whisper
  llm: llama
  tts: piper
tts:
  voice: en-amy
`)
		// A model with a partial pipeline must not appear.
		writeConfig("half-pipeline", `
name: half-pipeline
backend: llama-cpp
pipeline:
  vad: silero
  transcription: whisper
`)

		models := queryPipelineModels()
		Expect(models).To(HaveLen(1))
		Expect(models[0]["name"]).To(Equal("legacy-pipeline"))
		Expect(models[0]["vad"]).To(Equal("silero"))
		Expect(models[0]["llm"]).To(Equal("llama"))
		Expect(models[0]["voice"]).To(Equal("en-amy"))
		// self_contained is omitempty — absent for legacy pipelines.
		_, hasFlag := models[0]["self_contained"]
		Expect(hasFlag).To(BeFalse())
	})

	It("surfaces self-contained any-to-any models tagged with realtime_audio", func() {
		writeConfig("lfm-realtime", `
name: lfm-realtime
backend: liquid-audio
known_usecases:
  - realtime_audio
  - chat
  - tts
  - transcript
tts:
  voice: us_female
`)

		models := queryPipelineModels()
		Expect(models).To(HaveLen(1))
		Expect(models[0]["name"]).To(Equal("lfm-realtime"))
		// All four pipeline slots are populated with the model's own name so
		// the Talk page UI has something to render.
		Expect(models[0]["vad"]).To(Equal("lfm-realtime"))
		Expect(models[0]["transcription"]).To(Equal("lfm-realtime"))
		Expect(models[0]["llm"]).To(Equal("lfm-realtime"))
		Expect(models[0]["tts"]).To(Equal("lfm-realtime"))
		Expect(models[0]["voice"]).To(Equal("us_female"))
		Expect(models[0]["self_contained"]).To(BeTrue())
	})

	It("includes both legacy and self-contained models in the same response", func() {
		writeConfig("legacy", `
name: legacy
backend: llama-cpp
pipeline:
  vad: silero
  transcription: whisper
  llm: llama
  tts: piper
`)
		writeConfig("realtime", `
name: realtime
backend: liquid-audio
known_usecases:
  - realtime_audio
`)

		models := queryPipelineModels()
		Expect(models).To(HaveLen(2))
		// Sorted by name → legacy, realtime.
		Expect(models[0]["name"]).To(Equal("legacy"))
		Expect(models[1]["name"]).To(Equal("realtime"))
		Expect(models[1]["self_contained"]).To(BeTrue())
	})

	It("excludes models that have neither a pipeline nor realtime_audio", func() {
		writeConfig("plain-chat", `
name: plain-chat
backend: llama-cpp
known_usecases:
  - chat
`)

		Expect(queryPipelineModels()).To(BeEmpty())
	})
})
