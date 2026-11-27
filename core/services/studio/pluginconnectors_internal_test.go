package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

type shoutIn struct {
	Text string `json:"text"`
}

// pluginMCPServer is an MCP server over HTTP with one tool, named tool.
func pluginMCPServer(tool string) *httptest.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "plugin", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: tool, Description: "upper-cases"}, func(_ context.Context, _ *mcp.CallToolRequest, in shoutIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.ToUpper(in.Text)}}}, nil, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(h)
	DeferCleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts
}

var _ = Describe("the MCP servers of a plugin as connectors", func() {
	var (
		ctx   context.Context
		svc   *Service
		px    *pluginFixture
		conns *mcpconns.Service
		ms    mcpconns.Store
		id    string
		ts    *httptest.Server
	)
	model := func(url string) plugins.Model {
		return plugins.Model{Name: "fmt", Version: url, MCPServers: []plugins.MCPRef{{Name: "docs", Type: "http", URL: url}}}
	}
	toolsOf := func(conv string) []string {
		rec, err := svc.d.Store.GetChat(ctx, "alice", conv)
		Expect(err).NotTo(HaveOccurred())
		tools, _ := svc.connectorTools(ctx, userChatActor("alice", conv), rec, svc.scopeOf(ctx, rec), nil, set(rec.McpOff), nil)
		var names []string
		for _, t := range tools {
			names = append(names, t.Name)
		}
		return names
	}
	newChat := func(agentID string) string {
		c, err := svc.Create(ctx, "alice", CreateInput{Model: "m", AgentID: agentID})
		Expect(err).NotTo(HaveOccurred())
		return c.ID
	}

	BeforeEach(func() {
		ctx = context.Background()
		s, fs := newSkillService(&skillBroker{})
		svc = s
		px = newPluginFixture(svc, fs)
		var err error
		ms, err = mcpconns.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		key, _ := connectors.NewKey()
		conns, err = mcpconns.New(mcpconns.Options{Store: ms, Key: func(context.Context) ([]byte, error) { return key, nil },
			AllowPrivate: func() bool { return true }, ConnectTimeout: 5 * time.Second, CallTimeout: 5 * time.Second})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conns.Close)
		svc.d.MCPConnectors, svc.d.Connectors = conns, conns
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc.d.Agents = ag
		ts = pluginMCPServer("shout")
		id, _ = px.install("in-fmt", model(ts.URL), "", nil)
		px.enable("alice", id, true)
	})

	It("gives a plain chat the tools of an enabled plugin's server, made at the turn", func() {
		conv := newChat("")
		Expect(toolsOf(conv)).To(Equal([]string{"shout"}))
		list, err := conns.List(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].PluginInstallID).To(Equal(id))
		Expect(list[0].Name).To(Equal("fmt:docs"))

		panel, err := svc.Tools(ctx, "alice", conv)
		Expect(err).NotTo(HaveOccurred())
		Expect(panel.Connectors).To(HaveLen(1))
		Expect(panel.Connectors[0].PluginInstall).To(Equal(id))
	})

	It("gives none to a chat that switched the plugin off, or to a user who did not enable it", func() {
		conv := newChat("")
		Expect(svc.SetPluginsOff(ctx, "alice", conv, []string{id})).To(Succeed())
		Expect(toolsOf(conv)).To(BeEmpty())
		panel, err := svc.Tools(ctx, "alice", conv)
		Expect(err).NotTo(HaveOccurred())
		Expect(panel.Connectors).To(BeEmpty())

		px.enable("alice", id, false)
		Expect(toolsOf(newChat(""))).To(BeEmpty())
	})

	It("gives a chat with an agent the tools only when the agent grants the plugin", func() {
		none, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "None", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		Expect(toolsOf(newChat(none.ID))).To(BeEmpty())
		granted, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Granted", Model: "m", Plugins: []string{id}})
		Expect(err).NotTo(HaveOccurred())
		Expect(granted.MCPServers).To(BeEmpty())
		Expect(toolsOf(newChat(granted.ID))).To(Equal([]string{"shout"}), "no entry in the agent's MCP list is needed")
	})

	It("moves the connector to the new address after an update", func() {
		conv := newChat("")
		Expect(toolsOf(conv)).To(Equal([]string{"shout"}))
		other := pluginMCPServer("whisper")
		m, _ := json.Marshal(model(other.URL))
		digest := "sha256:" + strings.Repeat("cd", 32)
		Expect(px.st.PutPackage(ctx, store.Package{Digest: digest, Name: "fmt", Model: m})).To(Succeed())
		Expect(px.st.InstallTx(ctx, func(tx store.Tx) error {
			list, err := tx.Installs(store.InstallFilter{ID: id})
			if err != nil {
				return err
			}
			in := list[0]
			in.PreviousDigest, in.Digest = in.Digest, digest
			return tx.UpdateInstall(in)
		})).To(Succeed())
		Expect(toolsOf(conv)).To(Equal([]string{"whisper"}))
		list, _ := conns.List(ctx, "alice")
		Expect(list).To(HaveLen(1))
		Expect(list[0].URL).To(Equal(other.URL))
	})

	It("keeps the connectors of a plugin read only for the user", func() {
		conv := newChat("")
		Expect(toolsOf(conv)).NotTo(BeEmpty())
		list, _ := conns.List(ctx, "alice")
		_, err := conns.SetEnabled(ctx, "alice", list[0].ID, false)
		Expect(err).To(MatchError(mcpconns.ErrPluginOwned))
		Expect(conns.Delete(ctx, "alice", list[0].ID)).To(MatchError(mcpconns.ErrPluginOwned))
	})

	It("names the same guest root as the chats' file layer", func() {
		Expect(plugins.GuestRoot).To(Equal(session.GuestRoot + "/" + session.SkillLayers[3].At))
	})
})
