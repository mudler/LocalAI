// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// fakeModelServers stands for the MCP servers a model's configuration has.
type fakeModelServers struct{ asked []string }

func (f *fakeModelServers) Prompts(_ context.Context, _, _ string, servers []string) ([]studio.MCPPrompt, error) {
	f.asked = servers
	return []studio.MCPPrompt{{Server: "wiki", Name: "define", Description: "Define a term", Arguments: []studio.MCPPromptArg{{Name: "term", Required: true}}}}, nil
}

func (f *fakeModelServers) GetPrompt(_ context.Context, _, _, server, name string, args map[string]string) (string, error) {
	return "Define " + args["term"] + " (" + server + "/" + name + ")", nil
}

func (f *fakeModelServers) Resources(_ context.Context, _, _ string, _ []string) ([]studio.MCPResource, error) {
	return []studio.MCPResource{{Server: "wiki", URI: "wiki://home", Name: "Home"}}, nil
}

func (f *fakeModelServers) ReadResource(_ context.Context, _, _, server, uri string) (string, error) {
	return "contents of " + uri + " on " + server, nil
}

var _ = Describe("Chats MCP prompts and resources", func() {
	var (
		llm     *fakeLLM
		s       *studioServer
		connID  string
		content *fakeModelServers
	)
	BeforeEach(func() {
		llm, content = &fakeLLM{}, &fakeModelServers{}
		srv := mcp.NewServer(&mcp.Implementation{Name: "ctx", Version: "1"}, nil)
		srv.AddPrompt(&mcp.Prompt{Name: "review", Description: "Review code", Arguments: []*mcp.PromptArgument{{Name: "lang", Required: true}}},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "Please review this " + req.Params.Arguments["lang"] + " code."}}}}, nil
			})
		srv.AddResource(&mcp.Resource{URI: "doc://style", Name: "Style guide", MIMEType: "text/plain"},
			func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, Text: "Use tabs."}}}, nil
			})
		mcpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
		DeferCleanup(func() { mcpSrv.CloseClientConnections(); mcpSrv.Close() })
		cs, err := connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ms, err := mcpconns.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		s = newLLMStudioServerWith(llm, func(d *studio.Deps) {
			svc, err := mcpconns.New(mcpconns.Options{
				Store:        ms,
				Key:          func(ctx context.Context) ([]byte, error) { return cs.EnsureKey(ctx, connectors.NewKey) },
				Permitted:    func(context.Context, string) bool { return true },
				AllowPrivate: func() bool { return true },
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(svc.Close)
			d.MCPConnectors, d.MCPContent = svc, content
			d.MCPServers = func(context.Context, string, string) []studio.ModelMCPServer {
				return []studio.ModelMCPServer{{Name: "wiki", Auto: true}}
			}
		})
		rec := s.do("alice", http.MethodPost, "/api/chats/mcp-connectors", map[string]any{"name": "Style", "transport": "http", "url": mcpSrv.URL, "enabled": true})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		connID = decodeJSON(rec)["id"].(string)
	})

	It("lists the prompts and resources of the model's servers and the user's connectors", func() {
		id := s.create("alice")
		prompts := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/mcp/prompts", nil))["prompts"].([]any)
		servers := map[string]string{}
		for _, p := range prompts {
			servers[p.(map[string]any)["name"].(string)] = p.(map[string]any)["server"].(string)
		}
		Expect(servers).To(Equal(map[string]string{"define": "wiki", "review": "connector:" + connID}))
		Expect(content.asked).To(ConsistOf("wiki"))

		resources := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/mcp/resources", nil))["resources"].([]any)
		uris := []string{}
		for _, r := range resources {
			uris = append(uris, r.(map[string]any)["uri"].(string))
		}
		Expect(uris).To(ConsistOf("wiki://home", "doc://style"))
	})

	It("expands a prompt and reads a resource, for servers the chat can use only", func() {
		id := s.create("alice")
		ok := s.do("alice", http.MethodPost, "/api/chats/"+id+"/mcp/prompts", map[string]any{"server": "connector:" + connID, "name": "review", "arguments": map[string]string{"lang": "Go"}})
		Expect(ok.Code).To(Equal(http.StatusOK), ok.Body.String())
		Expect(decodeJSON(ok)["text"]).To(Equal("Please review this Go code."))
		read := s.do("alice", http.MethodPost, "/api/chats/"+id+"/mcp/resources/read", map[string]any{"server": "connector:" + connID, "uri": "doc://style"})
		Expect(decodeJSON(read)["text"]).To(Equal("Use tabs."))

		// A chat switch turns a server off for prompts as it does for tools.
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+id+"/mcp", map[string]any{"off": []string{"connector:" + connID}}).Code).To(Equal(http.StatusOK))
		off := s.do("alice", http.MethodPost, "/api/chats/"+id+"/mcp/prompts", map[string]any{"server": "connector:" + connID, "name": "review"})
		Expect(off.Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/mcp/prompts", map[string]any{"server": "other-server", "name": "x"}).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/mcp/prompts", map[string]any{"server": "wiki"}).Code).To(Equal(http.StatusBadRequest))

		// Another user's connector is not theirs to use, and another user's chat is not found.
		bobChat := s.create("bob")
		Expect(s.do("bob", http.MethodPost, "/api/chats/"+bobChat+"/mcp/prompts", map[string]any{"server": "connector:" + connID, "name": "review"}).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("bob", http.MethodGet, "/api/chats/"+id+"/mcp/prompts", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("adds prompts and resources to a message as context for the turn", func() {
		id := s.create("alice")
		rec := s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{
			"text": "go ahead",
			"context": []map[string]any{
				{"server": "connector:" + connID, "name": "review", "arguments": map[string]string{"lang": "Rust"}},
				{"server": "connector:" + connID, "uri": "doc://style"},
				{"server": "wiki", "uri": "wiki://home"},
			},
		})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		s.waitIdle("alice", id)
		text := strings.Join(userTexts(llm.last()), "\n")
		Expect(text).To(And(
			ContainSubstring("Please review this Rust code."), ContainSubstring("Use tabs."),
			ContainSubstring("contents of wiki://home on wiki"), HaveSuffix("go ahead")))

		// A context entry the chat cannot use stops the message before any turn.
		bad := s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "hi", "context": []map[string]any{{"server": "nope", "uri": "x://y"}}})
		Expect(bad.Code).To(Equal(http.StatusForbidden), bad.Body.String())
		Expect(roleTexts(s.transcript("alice", id), "user")).To(HaveLen(1))
	})
})
