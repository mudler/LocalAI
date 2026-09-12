package ollama_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/ollama"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOllamaEndpoints(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ollama Endpoints Suite")
}

var _ = Describe("Ollama endpoint handlers", func() {
	var e *echo.Echo

	BeforeEach(func() {
		e = echo.New()
	})

	Describe("HeartbeatEndpoint", func() {
		It("returns 'Ollama is running' on GET /", func() {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ollama.HeartbeatEndpoint()
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(Equal("Ollama is running"))
		})

		It("returns 200 on HEAD /", func() {
			req := httptest.NewRequest(http.MethodHead, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ollama.HeartbeatEndpoint()
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Describe("VersionEndpoint", func() {
		It("returns a JSON object with version field", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ollama.VersionEndpoint()
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(ContainSubstring(`"version"`))
			Expect(rec.Body.String()).To(MatchRegexp(`\d+\.\d+\.\d+`))
		})
	})

	Describe("ShowModelEndpoint", func() {
		var (
			tmpDir string
			bcl    *config.ModelConfigLoader
		)

		BeforeEach(func() {
			var err error
			tmpDir, err = os.MkdirTemp("", "ollama-show-test-*")
			Expect(err).ToNot(HaveOccurred())
			bcl = config.NewModelConfigLoader(tmpDir)
		})

		AfterEach(func() {
			_ = os.RemoveAll(tmpDir)
		})

		writeConfig := func(name, yaml string) {
			path := filepath.Join(tmpDir, name+".yaml")
			Expect(os.WriteFile(path, []byte(yaml), 0o644)).To(Succeed())
			Expect(bcl.ReadModelConfig(path)).To(Succeed())
		}

		callShow := func(name string) *schema.OllamaShowResponse {
			req := httptest.NewRequest(http.MethodPost, "/api/show",
				strings.NewReader(`{"name":"`+name+`"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ollama.ShowModelEndpoint(bcl)
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp schema.OllamaShowResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			return &resp
		}

		It("returns capabilities=['embedding'] for embedding-only models", func() {
			writeConfig("embed", `
name: embed
backend: llama-cpp
embeddings: true
parameters:
  model: Qwen3-4B-Embedding-Q4_K_M.gguf
`)
			resp := callShow("embed")
			Expect(resp.Capabilities).To(ConsistOf("embedding"))
		})

		It("returns capabilities=['completion'] for plain chat models", func() {
			writeConfig("chat", `
name: chat
backend: llama-cpp
template:
  chat: "{{ .Input }}"
parameters:
  model: Llama-3-8B-Q4_K_M.gguf
`)
			resp := callShow("chat")
			Expect(resp.Capabilities).To(ContainElement("completion"))
			Expect(resp.Capabilities).ToNot(ContainElement("embedding"))
		})

		It("populates details.parameter_size and details.quantization_level from the GGUF filename", func() {
			writeConfig("qwen", `
name: qwen
backend: llama-cpp
template:
  chat: "{{ .Input }}"
parameters:
  model: Qwen3-4B-Instruct-Q4_K_M.gguf
`)
			resp := callShow("qwen")
			Expect(resp.Details.ParameterSize).To(Equal("4B"))
			Expect(resp.Details.QuantizationLevel).To(Equal("Q4_K_M"))
			Expect(resp.Details.Format).To(Equal("gguf"))
			Expect(resp.Details.Families).ToNot(BeEmpty())
		})

		It("looks up the model when the Ollama :latest tag is included", func() {
			writeConfig("chat", `
name: chat
backend: llama-cpp
template:
  chat: "{{ .Input }}"
parameters:
  model: Llama-3-8B-Q4_K_M.gguf
`)
			resp := callShow("chat:latest")
			Expect(resp.Details.Format).To(Equal("gguf"))
			Expect(resp.Capabilities).To(ContainElement("completion"))
		})
	})

	Describe("ListModelsEndpoint", func() {
		var (
			tmpDir string
			bcl    *config.ModelConfigLoader
			ml     *model.ModelLoader
		)

		BeforeEach(func() {
			var err error
			tmpDir, err = os.MkdirTemp("", "ollama-tags-test-*")
			Expect(err).ToNot(HaveOccurred())

			systemState, err := system.GetSystemState(system.WithModelPath(tmpDir))
			Expect(err).ToNot(HaveOccurred())
			ml = model.NewModelLoader(systemState)
			bcl = config.NewModelConfigLoader(tmpDir)
		})

		AfterEach(func() {
			_ = os.RemoveAll(tmpDir)
		})

		writeConfig := func(name, yaml string) {
			path := filepath.Join(tmpDir, name+".yaml")
			Expect(os.WriteFile(path, []byte(yaml), 0o644)).To(Succeed())
			Expect(bcl.ReadModelConfig(path)).To(Succeed())
		}

		callTags := func() (schema.OllamaListResponse, []byte) {
			req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ollama.ListModelsEndpoint(bcl, ml)
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp schema.OllamaListResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			return resp, rec.Body.Bytes()
		}

		It("reports on-disk size from ModelFileName+ModelPath and omits size when unknown", func() {
			weight := []byte("fake-gguf-weights-0123456789")
			Expect(os.WriteFile(filepath.Join(tmpDir, "Llama-3-8B-Q4_K_M.gguf"), weight, 0o644)).To(Succeed())
			writeConfig("chat", `
name: chat
backend: llama-cpp
template:
  chat: "{{ .Input }}"
parameters:
  model: Llama-3-8B-Q4_K_M.gguf
`)
			writeConfig("missing-weights", `
name: missing-weights
backend: llama-cpp
template:
  chat: "{{ .Input }}"
parameters:
  model: does-not-exist.gguf
`)

			resp, raw := callTags()
			Expect(resp.Models).To(HaveLen(2))

			byName := map[string]schema.OllamaModelEntry{}
			for _, m := range resp.Models {
				byName[m.Name] = m
			}

			chat := byName["chat:latest"]
			Expect(chat.Size).ToNot(BeNil())
			Expect(*chat.Size).To(Equal(int64(len(weight))))
			Expect(chat.Capabilities).To(ContainElement("completion"))
			Expect(chat.Details.QuantizationLevel).To(Equal("Q4_K_M"))

			missing := byName["missing-weights:latest"]
			Expect(missing.Size).To(BeNil())
			Expect(string(raw)).ToNot(ContainSubstring(`"size":0`))
		})
	})

	Describe("ListRunningEndpoint", func() {
		var (
			tmpDir string
			bcl    *config.ModelConfigLoader
			ml     *model.ModelLoader
		)

		BeforeEach(func() {
			var err error
			tmpDir, err = os.MkdirTemp("", "ollama-ps-test-*")
			Expect(err).ToNot(HaveOccurred())

			systemState, err := system.GetSystemState(system.WithModelPath(tmpDir))
			Expect(err).ToNot(HaveOccurred())
			ml = model.NewModelLoader(systemState)
			bcl = config.NewModelConfigLoader(tmpDir)
		})

		AfterEach(func() {
			_ = os.RemoveAll(tmpDir)
		})

		It("reports on-disk size for loaded models and omits size_vram when unknown", func() {
			weight := []byte("loaded-model-weights-abcdef")
			Expect(os.WriteFile(filepath.Join(tmpDir, "granite-Q4_K_M.gguf"), weight, 0o644)).To(Succeed())

			cfgPath := filepath.Join(tmpDir, "granite.yaml")
			Expect(os.WriteFile(cfgPath, []byte(`
name: granite
backend: llama-cpp
template:
  chat: "{{ .Input }}"
parameters:
  model: granite-Q4_K_M.gguf
`), 0o644)).To(Succeed())
			Expect(bcl.ReadModelConfig(cfgPath)).To(Succeed())

			store := model.NewInMemoryModelStore()
			store.Set("granite", model.NewModel("granite", "addr", nil))
			ml.SetModelStore(store)

			req := httptest.NewRequest(http.MethodGet, "/api/ps", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ollama.ListRunningEndpoint(bcl, ml)
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))

			raw := rec.Body.String()
			Expect(raw).ToNot(ContainSubstring(`"size":0`))
			Expect(raw).ToNot(ContainSubstring(`"size_vram"`))

			var resp schema.OllamaPsResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Models).To(HaveLen(1))
			Expect(resp.Models[0].Name).To(Equal("granite:latest"))
			Expect(resp.Models[0].Size).ToNot(BeNil())
			Expect(*resp.Models[0].Size).To(Equal(int64(len(weight))))
			Expect(resp.Models[0].SizeVRAM).To(BeNil())
			Expect(resp.Models[0].Details.QuantizationLevel).To(Equal("Q4_K_M"))
		})
	})
})
