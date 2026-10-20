package localai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/onsi/gomega"
)

// A request for add-on tools gets the typed rejection from the legacy MCP chat
// route whether or not the model has MCP servers configured. Before the check
// came first, a model without servers answered an untyped 500.
func TestMCPEndpointRejectsAddonToolsBeforeLookingAtServers(t *testing.T) {
	g := gomega.NewWithT(t)
	handler := MCPEndpoint(nil, nil, nil, &config.ApplicationConfig{}, nil, nil)
	for name, model := range map[string]*config.ModelConfig{
		"no servers": {},
		"servers":    {MCP: config.MCPConfig{Servers: "mcpServers: {}"}},
	} {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/v1/mcp/chat/completions", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "m"}},
			Metadata:          map[string]string{"addon_tools": "true"},
		})
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, model)
		g.Expect(handler(c)).To(gomega.Succeed(), name)
		g.Expect(rec.Code).To(gomega.Equal(http.StatusBadRequest), name)
		g.Expect(rec.Body.String()).To(gomega.ContainSubstring("addon_tools_unsupported"), name)
	}
}
