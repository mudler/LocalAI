package localai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MCPServersEndpoint with profile aliases", func() {
	var (
		tempDir string
		fixture *httptest.Server
		app     *echo.Echo
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "localai-mcp-profile-test")
		Expect(err).ToNot(HaveOccurred())

		srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v0.0.1"}, nil)
		srv.AddTool(&mcp.Tool{
			Name:        "list_collections",
			Description: "fixture tool",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
		fixture = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))

		write := func(name, body string) {
			Expect(os.WriteFile(filepath.Join(tempDir, name+".yaml"), []byte(body), 0644)).To(Succeed())
		}
		write("real", "name: real\nbackend: llama-cpp\nmodel: foo\n")
		write("plain-alias", "name: plain-alias\nalias: real\n")
		write("intern", fmt.Sprintf(`name: intern
alias: real
profile:
  mcp:
    auto: true
    remote: |
      {"mcpServers": {"intern-set": {"url": %q}}}
`, fixture.URL))

		loader := config.NewModelConfigLoader(tempDir)
		Expect(loader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

		app = echo.New()
		app.GET("/v1/mcp/servers/:model", MCPServersEndpoint(loader, &config.ApplicationConfig{}, nil))
	})

	AfterEach(func() {
		// The endpoint caches the session under the profile's name and keeps its
		// stream open; close it first or the fixture waits for it on shutdown.
		mcpTools.CloseMCPSessions("intern")
		fixture.Close()
		_ = os.RemoveAll(tempDir)
	})

	type serverInfo struct {
		Name  string   `json:"name"`
		Tools []string `json:"tools"`
		Error string   `json:"error"`
	}
	servers := func(model string) []serverInfo {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/mcp/servers/"+model, nil))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var body struct {
			Model   string       `json:"model"`
			Servers []serverInfo `json:"servers"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Model).To(Equal(model))
		return body.Servers
	}

	It("lists the servers and tools a profile alias brings, as chat would use them", func() {
		got := servers("intern")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Name).To(Equal("intern-set"))
		Expect(got[0].Error).To(BeEmpty())
		Expect(got[0].Tools).To(ConsistOf("list_collections"))
	})

	It("does not attach the profile's servers to the target or to a plain alias", func() {
		Expect(servers("real")).To(BeEmpty())
		Expect(servers("plain-alias")).To(BeEmpty())
	})
})
