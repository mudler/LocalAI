package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ListModelCapabilitiesEndpoint", func() {
	var (
		e       *echo.Echo
		tmpDir  string
		bcl     *config.ModelConfigLoader
		ml      *model.ModelLoader
		appConf *config.ApplicationConfig
	)

	BeforeEach(func() {
		var err error
		e = echo.New()
		tmpDir, err = os.MkdirTemp("", "models-caps-test-*")
		Expect(err).NotTo(HaveOccurred())

		st, err := system.GetSystemState(system.WithModelPath(tmpDir))
		Expect(err).NotTo(HaveOccurred())
		ml = model.NewModelLoader(st)
		bcl = config.NewModelConfigLoader(tmpDir)
		appConf = config.NewApplicationConfig()
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir)
	})

	writeConfig := func(name, yaml string) {
		path := filepath.Join(tmpDir, name+".yaml")
		Expect(os.WriteFile(path, []byte(yaml), 0o644)).To(Succeed())
		Expect(bcl.ReadModelConfig(path)).To(Succeed())
	}

	// call exercises the endpoint with auth disabled (no auth DB), which is the
	// standard deployment path. The per-user allowlist branch is shared verbatim
	// with ListModelsEndpoint (listVisibleModelNames) and covered there.
	call := func() schema.ModelCapabilitiesResponse {
		req := httptest.NewRequest(http.MethodGet, "/v1/models/capabilities", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		handler := ListModelCapabilitiesEndpoint(bcl, ml, appConf)
		Expect(handler(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))

		var resp schema.ModelCapabilitiesResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		return resp
	}

	entryFor := func(resp schema.ModelCapabilitiesResponse, id string) *schema.ModelCapabilities {
		for i := range resp.Data {
			if resp.Data[i].ID == id {
				return &resp.Data[i]
			}
		}
		return nil
	}

	It("returns the list envelope even with no models", func() {
		resp := call()
		Expect(resp.Object).To(Equal("list"))
	})

	It("enriches a vision chat model with capabilities and image input modality", func() {
		writeConfig("vlm", `
name: vlm
backend: llama-cpp
known_usecases:
  - FLAG_CHAT
  - FLAG_VISION
template:
  chat: "{{ .Input }}"
parameters:
  model: qwen2.5-vl-Q4_K_M.gguf
`)
		entry := entryFor(call(), "vlm")
		Expect(entry).NotTo(BeNil())
		Expect(entry.Object).To(Equal("model"))
		Expect(entry.Capabilities).To(ContainElements("chat", "vision"))
		Expect(entry.InputModalities).To(ContainElements("text", "image"))
		Expect(entry.OutputModalities).To(ContainElement("text"))
	})

	It("marks a parakeet model as an audio-in/text-out transcription model", func() {
		writeConfig("parakeet", `
name: parakeet
backend: parakeet-cpp
known_usecases:
  - FLAG_TRANSCRIPT
parameters:
  model: parakeet-tdt-0.6b
`)
		entry := entryFor(call(), "parakeet")
		Expect(entry).NotTo(BeNil())
		Expect(entry.Capabilities).To(ContainElement("transcript"))
		Expect(entry.InputModalities).To(Equal([]string{"audio"}))
		Expect(entry.OutputModalities).To(Equal([]string{"text"}))
		Expect(entry.Capabilities).NotTo(ContainElement("chat"))
	})
})
