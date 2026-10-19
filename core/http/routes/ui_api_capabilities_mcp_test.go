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

// The chat UI derives MCP availability for non-admin users from this flag,
// because the full model config (config-json) is admin-only.
var _ = Describe("UI model capabilities MCP flag", func() {
	capabilitiesMCP := func(disableMCP bool) map[string]bool {
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
		cl := config.NewModelConfigLoader(dir)
		for name, extra := range map[string]string{
			"remote-mcp": "mcp:\n  remote: |\n    mcpServers:\n      weather:\n        url: http://weather.invalid/mcp\n",
			"stdio-mcp":  "mcp:\n  stdio: |\n    mcpServers:\n      files:\n        command: /bin/true\n",
			"plain":      "",
		} {
			path := filepath.Join(dir, name+".yaml")
			Expect(os.WriteFile(path, []byte("name: "+name+"\nbackend: llama-cpp\nknown_usecases: [chat]\n"+extra), 0600)).To(Succeed())
			Expect(cl.ReadModelConfig(path)).To(Succeed())
		}
		ac := config.NewApplicationConfig()
		ac.SystemState = st
		ac.DisableMCP = disableMCP
		ml := model.NewModelLoader(st)
		gs := galleryop.NewGalleryService(ac, ml)
		e := echo.New()
		routes.RegisterUIAPIRoutes(e, cl, ml, ac, gs, galleryop.NewOpCache(gs), &application.Application{}, func(next echo.HandlerFunc) echo.HandlerFunc { return next })

		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/capabilities", nil))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		var response struct {
			Data []struct {
				ID  string `json:"id"`
				MCP bool   `json:"mcp"`
			} `json:"data"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		got := map[string]bool{}
		for _, entry := range response.Data {
			got[entry.ID] = entry.MCP
		}
		return got
	}

	It("flags models whose config declares remote or stdio MCP servers", func() {
		Expect(capabilitiesMCP(false)).To(Equal(map[string]bool{
			"remote-mcp": true,
			"stdio-mcp":  true,
			"plain":      false,
		}))
	})

	It("flags no model when MCP is disabled on the instance", func() {
		Expect(capabilitiesMCP(true)).To(Equal(map[string]bool{
			"remote-mcp": false,
			"stdio-mcp":  false,
			"plain":      false,
		}))
	})
})
