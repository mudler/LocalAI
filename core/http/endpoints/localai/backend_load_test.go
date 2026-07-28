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
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LoadModelEndpoint (/backend/load)", func() {
	var (
		app          *echo.Echo
		tempDir      string
		configLoader *config.ModelConfigLoader
		modelLoader  *model.ModelLoader
		appConfig    *config.ApplicationConfig
	)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/backend/load", bytes.NewBufferString(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	decode := func(rec *httptest.ResponseRecorder) schema.ModelLoadResponse {
		var resp schema.ModelLoadResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		return resp
	}

	writeConfig := func(name, contents string) {
		Expect(os.WriteFile(filepath.Join(tempDir, name+".yaml"), []byte(contents), 0o600)).To(Succeed())
	}

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "backend-load-test-*")
		Expect(err).NotTo(HaveOccurred())

		systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
		Expect(err).NotTo(HaveOccurred())

		appConfig = config.NewApplicationConfig(config.WithSystemState(systemState))
		configLoader = config.NewModelConfigLoader(tempDir)
		modelLoader = model.NewModelLoader(systemState) // no backends installed

		app = echo.New()
		app.POST("/backend/load", LoadModelEndpoint(configLoader, modelLoader, appConfig))
	})

	AfterEach(func() {
		_ = os.RemoveAll(tempDir)
	})

	It("rejects a request with no model name", func() {
		rec := post(`{}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(rec).Message).To(ContainSubstring("model is required"))
	})

	It("reports a load failure for a regular model with nothing loaded", func() {
		writeConfig("solo", "name: solo\n")

		rec := post(`{"model":"solo"}`)
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))

		resp := decode(rec)
		Expect(resp.Loaded).To(BeEmpty())
		Expect(resp.Message).To(ContainSubstring("failed to load model"))
	})

	It("expands a pipeline model and reports each sub-model that failed to load", func() {
		writeConfig("voicebot", "name: voicebot\npipeline:\n  vad: vad-m\n  transcription: stt-m\n  llm: llm-m\n  tts: tts-m\n")
		writeConfig("vad-m", "name: vad-m\n")
		writeConfig("stt-m", "name: stt-m\n")
		writeConfig("llm-m", "name: llm-m\n")
		writeConfig("tts-m", "name: tts-m\n")

		rec := post(`{"model":"voicebot"}`)
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))

		resp := decode(rec)
		Expect(resp.Message).To(ContainSubstring("failed to load model"))
		// The pipeline stub itself is never loaded; its sub-models are what the
		// endpoint tries, so the error names them rather than "voicebot".
		Expect(resp.Message).To(ContainSubstring("vad-m"))
		Expect(resp.Message).ToNot(ContainSubstring("voicebot"))
	})
})
