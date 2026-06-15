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
	})

	Describe("ListModelsEndpoint", func() {
		It("includes capabilities and details for each listed model in /api/tags", func() {
			Skip("covered by per-entry tests; integration smoke test")
		})
	})
})
