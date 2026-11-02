package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type capabilityEntry struct {
	ID           string   `json:"id"`
	Capabilities []string `json:"capabilities"`
	Scale        *int     `json:"upscaleScale"`
	Backend      string   `json:"backend"`
	Disabled     bool     `json:"disabled"`
	Pinned       bool     `json:"pinned"`
	MCP          bool     `json:"mcp"`
}

// capabilitiesOf writes one model config per entry (name -> YAML after the
// name line), registers the UI API routes and returns the answer of
// GET /api/models/capabilities.
func capabilitiesOf(models map[string]string) []capabilityEntry {
	dir := GinkgoT().TempDir()
	st, err := system.GetSystemState(system.WithModelPath(dir))
	Expect(err).ToNot(HaveOccurred())
	cl := config.NewModelConfigLoader(dir)
	for name, body := range models {
		path := filepath.Join(dir, name+".yaml")
		Expect(os.WriteFile(path, []byte("name: "+name+"\n"+body), 0600)).To(Succeed())
		Expect(cl.ReadModelConfig(path)).To(Succeed())
	}
	ac := config.NewApplicationConfig()
	ac.SystemState = st
	ml := model.NewModelLoader(st)
	gs := galleryop.NewGalleryService(ac, ml)
	app := echo.New()
	routes.RegisterUIAPIRoutes(app, cl, ml, ac, gs, galleryop.NewOpCache(gs), &application.Application{}, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/capabilities", nil))
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	var response struct {
		Data []capabilityEntry `json:"data"`
	}
	Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
	return response.Data
}

var _ = Describe("UI model capabilities", func() {
	It("exposes only a valid upscale scale", func() {
		const upscale = "backend: stablediffusion-ggml\nknown_usecases: [upscale]\n"
		data := capabilitiesOf(map[string]string{
			"upscaler":    upscale + "options: [upscale_scale:4]\n",
			"absent":      upscale,
			"malformed":   upscale + "options: [upscale_scale:bad]\n",
			"nonpositive": upscale + "options: [upscale_scale:0]\n",
			"negative":    upscale + "options: [upscale_scale:-1]\n",
			"overflow":    upscale + "options: [upscale_scale:2147483648]\n",
			"duplicate":   upscale + "options: [upscale_scale:4, upscale_scale:4]\n",
		})
		Expect(data).To(HaveLen(7))
		for _, entry := range data {
			Expect(entry.Capabilities).To(Equal([]string{"FLAG_UPSCALE"}), entry.ID)
			if entry.ID == "upscaler" {
				Expect(entry.Scale).ToNot(BeNil())
				Expect(*entry.Scale).To(Equal(4))
			} else {
				Expect(entry.Scale).To(BeNil(), "invalid scale exposed for %s", entry.ID)
			}
		}
	})

	It("gives a valid alias the capabilities of its target and nothing else", func() {
		data := capabilitiesOf(map[string]string{
			"target":         "backend: llama-cpp\nknown_usecases: [chat, vision]\npinned: true\n",
			"alias":          "alias: target\nknown_usecases: [embeddings]\nprofile:\n  system_prompt: Answer briefly.\n  client_tools: drop\n",
			"disabled-alias": "alias: target\ndisabled: true\n",
			"broken":         "alias: missing\nknown_usecases: [chat]\n",
			"chain":          "alias: alias\nknown_usecases: [chat]\n",
		})
		Expect(data).To(HaveLen(5))
		for _, entry := range data {
			switch entry.ID {
			case "target", "alias", "disabled-alias":
				Expect(entry.Capabilities).To(ConsistOf("FLAG_CHAT", "FLAG_VISION"), "%s must inherit chat/vision", entry.ID)
			case "broken", "chain":
				Expect(entry.Capabilities).To(BeEmpty(), "invalid alias %s gained capabilities", entry.ID)
			default:
				Fail("unexpected ID: " + entry.ID)
			}
			if entry.ID != "target" {
				Expect(entry.Backend).To(BeEmpty(), "alias %s inherited management metadata", entry.ID)
				Expect(entry.Pinned).To(BeFalse(), entry.ID)
				Expect(entry.MCP).To(BeFalse(), entry.ID)
			}
			Expect(entry.Disabled).To(Equal(entry.ID == "disabled-alias"), entry.ID)
		}
	})
})
