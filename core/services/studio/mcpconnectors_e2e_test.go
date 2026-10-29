package studio_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type e2eIn struct {
	Text string `json:"text"`
}

// e2eServer is a real MCP server over HTTP, in this process.
func e2eServer() (*httptest.Server, *atomic.Int32) {
	var reached atomic.Int32
	srv := mcp.NewServer(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "shout", Description: "upper-cases"}, func(_ context.Context, _ *mcp.CallToolRequest, in e2eIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.ToUpper(in.Text)}}}, nil, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		h.ServeHTTP(w, r)
	}))
	DeferCleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts, &reached
}

func realConnectors(allowPrivate bool) (*mcpconns.Service, mcpconns.Store) {
	ms, err := mcpconns.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	cs, _ := connectors.NewJSON("")
	svc, err := mcpconns.New(mcpconns.Options{
		Store: ms, Key: func(ctx context.Context) ([]byte, error) { return cs.EnsureKey(ctx, connectors.NewKey) },
		AllowPrivate: func() bool { return allowPrivate },
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(svc.Close)
	return svc, ms
}

var _ = Describe("mcpconnectors e2e", func() {
	It("a chat turn calls a tool of a user defined MCP server", func() {
		ts, reached := e2eServer()
		conns, _ := realConnectors(true) // the test server listens on loopback
		c, err := conns.Create(context.Background(), alice, mcpconns.Input{Name: "Shouter", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		svc, h := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "shout", Args: `{"text":"hello"}`}, {Say: "done"}}
		}, func(d *studio.Deps) { d.MCPConnectors = conns })
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		res := h.ResultsFor("shout")
		Expect(res).To(HaveLen(1), "result = %+v (server reached %d times)", res, reached.Load())
		Expect(res[0].Err).To(BeNil(), "result = %+v (server reached %d times)", res, reached.Load())
		Expect(res[0].Out).To(Equal("HELLO"), "result = %+v (server reached %d times)", res, reached.Load())
		// Renaming the connector does not break an agent that holds its id.
		_, err = conns.Update(context.Background(), alice, c.ID, mcpconns.Input{Name: "Renamed", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		tools, missing := conns.Resolve(context.Background(), alice, []string{c.ID}, false)
		Expect(tools).To(HaveLen(1), "after the rename: %v %v", tools, missing)
		Expect(missing).To(BeEmpty(), "after the rename: %v %v", tools, missing)
		Expect(tools[0].Connector).To(Equal("Renamed"), "after the rename: %v %v", tools, missing)
	})

	It("a private address gives the model a note and no tools", func() {
		ts, reached := e2eServer()
		conns, store := realConnectors(false) // the default: private addresses are refused
		_, err := conns.Create(context.Background(), alice, mcpconns.Input{Name: "Direct", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true})
		Expect(err).To(MatchError(mcpconns.ErrInvalid), "an enabled connector with a loopback address: %v", err)
		// A record that got in another way (saved while the switch was on, or
		// written by another frontend): the connection itself must refuse.
		rec := mcpconns.Connector{ID: "internal1", Owner: alice, Name: "Internal", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true}
		Expect(store.Put(context.Background(), rec)).To(Succeed())
		as, _ := agents.NewJSON("")
		svc, h := newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} },
			func(d *studio.Deps) { d.MCPConnectors, d.Agents = conns, as })
		_ = as.Put(context.Background(), agents.Agent{ID: "ag", Principal: alice, Name: "A", MCPServers: []string{"connector:internal1"}})
		chat, err := svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: "ag"})
		Expect(err).NotTo(HaveOccurred())
		sent(svc, alice, chat.ID, "go")
		idle(svc, alice, chat.ID)
		Expect(h.ToolNames(chat.ID)).NotTo(ContainElement("shout"), "the tool of a refused connector was offered")
		msg := strings.Join(h.Sent(chat.ID), "\n")
		Expect(msg).To(ContainSubstring("MCP connector Internal"), "no note for the model: %q", msg)
		Expect(msg).To(ContainSubstring("cannot be reached"), "no note for the model: %q", msg)
		Expect(reached.Load()).To(BeNumerically("==", 0), "the server on a private address was reached")
	})
})
