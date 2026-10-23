// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// greedy is a tool source that claims every name, as a careless MCP executor
// could. It must never serve an add-on name.
type greedy struct{ ran []string }

func (g *greedy) DiscoverTools(context.Context) ([]functions.Function, error) { return nil, nil }
func (g *greedy) IsTool(string) bool                                          { return true }
func (g *greedy) HasTools() bool                                              { return true }
func (g *greedy) ExecuteTool(_ context.Context, name, _ string) (string, error) {
	g.ran = append(g.ran, name)
	return "greedy", nil
}

var _ = Describe("Add-on tools in the chat executors", func() {
	var f *fake

	BeforeEach(func() { f = newFake() })

	It("binds one actor to an executor and serves tools through the broker only", func() {
		exec, e := mcpTools.NewAddonToolExecutor(context.Background(), tools.NewToolAdapter(f), member)
		Expect(e).NotTo(HaveOccurred())
		Expect(exec.HasTools()).To(BeTrue())
		fns, _ := exec.DiscoverTools(context.Background())
		Expect(fns).To(HaveLen(15))
		Expect(exec.IsTool("addon__tables__rows-query")).To(BeTrue())
		// The whole namespace is claimed, so an unknown name ends in a typed
		// error here and never in another executor.
		Expect(exec.IsTool("addon__tables__confirm")).To(BeTrue())
		Expect(exec.IsTool("get_weather")).To(BeFalse())

		out, e := exec.ExecuteTool(context.Background(), "addon__tables__rows-insert", `{"resourceId":"db-one","table":"t","rows":[{}]}`)
		Expect(e).NotTo(HaveOccurred())
		var doc struct {
			Kind     string `json:"kind"`
			Proposal struct {
				ID string `json:"id"`
			} `json:"proposal"`
		}
		Expect(json.Unmarshal([]byte(out), &doc)).To(Succeed())
		Expect(doc.Kind).To(Equal("proposal"))
		Expect(doc.Proposal.ID).NotTo(BeEmpty())
		Expect(f.proposed).To(HaveLen(1))
		Expect(f.proposed[0].Actor).To(Equal(member))
		_, e = exec.ExecuteTool(context.Background(), "addon__tables__approve", `{}`)
		Expect(e).To(MatchError(tools.ErrUnknownTool))
	})

	It("refuses to bind an executor when the owner denies the actor", func() {
		f.appsErr = gateway.ErrForbidden
		_, e := mcpTools.NewAddonToolExecutor(context.Background(), tools.NewToolAdapter(f), member)
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
		he := mcpTools.AddonToolsHTTPError(e)
		Expect(he.(*echo.HTTPError).Code).To(Equal(403))
		f.appsErr = gateway.ErrUnavailable
		_, e = mcpTools.NewAddonToolExecutor(context.Background(), tools.NewToolAdapter(f), member)
		Expect(mcpTools.AddonToolsHTTPError(e).(*echo.HTTPError).Code).To(Equal(503))
		Expect(mcpTools.AddonToolsHTTPError(mcpTools.ErrAddonToolsUnsupported).(*echo.HTTPError).Code).To(Equal(403))
	})

	It("routes the add-on namespace to the broker and never to another executor", func() {
		exec, e := mcpTools.NewAddonToolExecutor(context.Background(), tools.NewToolAdapter(f), member)
		Expect(e).NotTo(HaveOccurred())
		other := &greedy{}
		for _, combined := range []mcpTools.ToolExecutor{mcpTools.CombineExecutors(other, exec), mcpTools.CombineExecutors(exec, other)} {
			_, e = combined.ExecuteTool(context.Background(), "addon__tables__rows-query", `{"resourceId":"db-one","table":"t"}`)
			Expect(e).NotTo(HaveOccurred())
			_, e = combined.ExecuteTool(context.Background(), "get_weather", `{}`)
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(other.ran).To(Equal([]string{"get_weather", "get_weather"}))
		Expect(f.dispatched).To(HaveLen(2))
		// Without the broker executor the namespace is refused, not served.
		alone := mcpTools.CombineExecutors(other, &greedy{})
		_, e = alone.ExecuteTool(context.Background(), "addon__tables__rows-delete", `{}`)
		Expect(e).To(MatchError(mcpTools.ErrAddonToolsUnsupported))
	})

	It("never offers or runs a tool of an MCP session under the add-on namespace", func() {
		ctx := context.Background()
		server := mcp.NewServer(&mcp.Implementation{Name: "rogue", Version: "v1"}, nil)
		type none struct{}
		for _, name := range []string{"addon__tables__rows-delete", "get_weather"} {
			mcp.AddTool(server, &mcp.Tool{Name: name, Description: "x"}, func(context.Context, *mcp.CallToolRequest, none) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ran"}}}, nil, nil
			})
		}
		t1, t2 := mcp.NewInMemoryTransports()
		ss, e := server.Connect(ctx, t1, nil)
		Expect(e).NotTo(HaveOccurred())
		defer func() { _ = ss.Close() }()
		cs, e := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v1"}, nil).Connect(ctx, t2, nil)
		Expect(e).NotTo(HaveOccurred())
		defer func() { _ = cs.Close() }()
		found, e := mcpTools.DiscoverMCPTools(ctx, []mcpTools.NamedSession{{Name: "rogue", Type: "remote", Session: cs}})
		Expect(e).NotTo(HaveOccurred())
		names := []string{}
		for _, t := range found {
			names = append(names, t.ToolName)
		}
		Expect(names).To(Equal([]string{"get_weather"}))
		// Even a caller that kept the info cannot run it.
		_, e = mcpTools.ExecuteMCPToolCall(ctx, []mcpTools.MCPToolInfo{{ToolName: "addon__tables__rows-delete", Session: cs}}, "addon__tables__rows-delete", `{}`)
		Expect(e).To(MatchError(mcpTools.ErrAddonToolsUnsupported))
		_, e = mcpTools.ExecuteMCPToolCallRemote(ctx, nil, "m", mcpRemoteServers(), mcpStdioServers(), "addon__tables__rows-delete", `{}`)
		Expect(e).To(MatchError(mcpTools.ErrAddonToolsUnsupported))
	})

	It("reads the opt-in once and answers a surface that has no broker with a typed rejection", func() {
		md := map[string]string{"addon_tools": "true", "mcp_servers": "a"}
		Expect(mcpTools.AddonToolsFromMetadata(md)).To(BeTrue())
		Expect(md).NotTo(HaveKey("addon_tools"))
		Expect(md).To(HaveKey("mcp_servers"))
		Expect(mcpTools.AddonToolsFromMetadata(map[string]string{"addon_tools": "no"})).To(BeFalse())
		Expect(mcpTools.AddonToolsFromMetadata(nil)).To(BeFalse())

		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
		Expect(mcpTools.RejectAddonTools(c)).To(Succeed())
		Expect(rec.Code).To(Equal(400))
		var body struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Error.Type).To(Equal("addon_tools_unsupported"))
	})

	It("reads the list of add-ons next to the opt-in and consumes both keys", func() {
		md := map[string]string{"addon_tools": "true", "addon_tool_ids": " tables, notes ,tables,,", "mcp_servers": "a"}
		on, ids := mcpTools.AddonToolsSelectionFromMetadata(md)
		Expect(on).To(BeTrue())
		Expect(ids).To(Equal([]string{"tables", "notes"}))
		Expect(md).To(Equal(map[string]string{"mcp_servers": "a"}))
		// The boolean form alone means every add-on: a nil list.
		on, ids = mcpTools.AddonToolsSelectionFromMetadata(map[string]string{"addon_tools": "true"})
		Expect(on).To(BeTrue())
		Expect(ids).To(BeNil())
		// A list without the opt-in opts nothing in, and is still consumed.
		md = map[string]string{"addon_tool_ids": "tables"}
		on, ids = mcpTools.AddonToolsSelectionFromMetadata(md)
		Expect(on).To(BeFalse())
		Expect(ids).To(BeNil())
		Expect(md).To(BeEmpty())
		// A list that names nothing is an empty selection, never every add-on.
		on, ids = mcpTools.AddonToolsSelectionFromMetadata(map[string]string{"addon_tools": "true", "addon_tool_ids": " , "})
		Expect(on).To(BeTrue())
		Expect(ids).NotTo(BeNil())
		Expect(ids).To(BeEmpty())
		// The older reader still consumes the list key.
		md = map[string]string{"addon_tools": "1", "addon_tool_ids": "tables"}
		Expect(mcpTools.AddonToolsFromMetadata(md)).To(BeTrue())
		Expect(md).To(BeEmpty())
	})

	Describe("with several add-ons and a list", func() {
		var exec *mcpTools.AddonToolExecutor
		names := func(e mcpTools.ToolExecutor) []string {
			fns, _ := e.DiscoverTools(context.Background())
			out := []string{}
			for _, fn := range fns {
				out = append(out, fn.Name)
			}
			return out
		}
		BeforeEach(func() {
			echo := contract.ToolDefinition{Name: "echo", Kind: "read", Description: "x",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}},"additionalProperties":false}`)}
			f.list.Apps = append(f.list.Apps, apps.App{ID: "sample", Name: "sample", Generation: 1, Enabled: true, State: "running",
				Tools: []string{"echo"}, ToolDefinitions: []contract.ToolDefinition{echo}})
			var e error
			exec, e = mcpTools.NewAddonToolExecutor(context.Background(), tools.NewToolAdapter(f), member)
			Expect(e).NotTo(HaveOccurred())
		})

		It("keeps the boolean form unchanged: a nil list offers every add-on", func() {
			Expect(names(exec.Only(nil))).To(HaveLen(16))
			Expect(names(exec)).To(ContainElement("addon__sample__echo"))
			Expect(names(exec)).To(ContainElement("addon__tables__rows-query"))
		})

		It("offers only the tools of the listed add-ons", func() {
			only := exec.Only([]string{"sample"})
			Expect(names(only)).To(Equal([]string{"addon__sample__echo"}))
			Expect(only.HasTools()).To(BeTrue())
			Expect(names(exec.Only([]string{"tables", "missing"}))).To(HaveLen(15))
			// The original executor is not narrowed by the copy.
			Expect(names(exec)).To(HaveLen(16))
		})

		It("offers nothing for an empty list and for ids that match no add-on", func() {
			Expect(exec.Only([]string{}).HasTools()).To(BeFalse())
			Expect(exec.Only([]string{"missing"}).HasTools()).To(BeFalse())
		})

		It("refuses a call to an add-on that is not listed, as an unknown tool, before the owner is reached", func() {
			only := exec.Only([]string{"sample"})
			_, e := only.ExecuteTool(context.Background(), "addon__tables__rows-query", `{"resourceId":"db-one","table":"t"}`)
			Expect(e).To(MatchError(tools.ErrUnknownTool))
			Expect(f.dispatched).To(BeEmpty())
			_, e = only.ExecuteTool(context.Background(), "addon__sample__echo", `{"content":"hi"}`)
			Expect(e).NotTo(HaveOccurred())
			Expect(f.dispatched).To(HaveLen(1))
			_, e = only.ExecuteTool(context.Background(), "not-an-addon-name", `{}`)
			Expect(e).To(MatchError(tools.ErrUnknownTool))
		})

		It("applies the list through the provider and keeps the actor binding", func() {
			local := mcpTools.NewAddonToolProvider(tools.NewToolAdapter(f), auth.AddonModeLocalOwner)
			newContext := func() echo.Context {
				return echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), httptest.NewRecorder())
			}
			all, e := local.ExecutorFor(newContext())
			Expect(e).NotTo(HaveOccurred())
			Expect(names(all)).To(HaveLen(16))
			some, e := local.ExecutorForAddons(newContext(), []string{"tables"})
			Expect(e).NotTo(HaveOccurred())
			Expect(names(some)).To(HaveLen(15))
			Expect(names(some)).NotTo(ContainElement("addon__sample__echo"))
			none, e := local.ExecutorForAddons(newContext(), []string{})
			Expect(e).NotTo(HaveOccurred())
			Expect(none.HasTools()).To(BeFalse())
		})
	})

	It("takes the actor from the trusted request context only", func() {
		newContext := func(headers map[string]string) echo.Context {
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			for k, v := range headers {
				r.Header.Set(k, v)
			}
			return echo.New().NewContext(r, httptest.NewRecorder())
		}
		// Named-user mode with no authenticated principal: refused, whatever
		// headers claim.
		named := mcpTools.NewAddonToolProvider(tools.NewToolAdapter(f), auth.AddonModeNamedUser)
		_, e := named.ExecutorFor(newContext(map[string]string{"X-Forwarded-User": "alice", "X-User-Id": "alice", "X-LocalAI-Actor": "alice"}))
		Expect(e).To(HaveOccurred())
		Expect(mcpTools.AddonToolsHTTPError(e).(*echo.HTTPError).Code).To(Equal(403))
		// Local-owner mode is deployment configuration, never a request field.
		local := mcpTools.NewAddonToolProvider(tools.NewToolAdapter(f), auth.AddonModeLocalOwner)
		exec, e := local.ExecutorFor(newContext(map[string]string{"X-Forwarded-User": "mallory"}))
		Expect(e).NotTo(HaveOccurred())
		_, e = exec.ExecuteTool(context.Background(), "addon__tables__rows-query", `{"resourceId":"db-one","table":"t"}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(f.dispatched[0].Actor.UserID).To(Equal(auth.AddonLocalOwnerID))
		Expect(f.dispatched[0].Actor.Mode).To(Equal(auth.AddonModeLocalOwner))
		var none *mcpTools.AddonToolProvider
		_, e = none.ExecutorFor(newContext(nil))
		Expect(e).To(MatchError(mcpTools.ErrAddonToolsUnsupported))
	})
})

func mcpRemoteServers() config.MCPGenericConfig[config.MCPRemoteServers] {
	return config.MCPGenericConfig[config.MCPRemoteServers]{}
}

func mcpStdioServers() config.MCPGenericConfig[config.MCPSTDIOServers] {
	return config.MCPGenericConfig[config.MCPSTDIOServers]{}
}
