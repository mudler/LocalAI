package studio_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

type e2eIn struct {
	Text string `json:"text"`
}

// e2eServer is a real MCP server over HTTP, in this process.
func e2eServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
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
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts, &reached
}

func realConnectors(t *testing.T, allowPrivate bool) (*mcpconns.Service, mcpconns.Store) {
	t.Helper()
	ms, err := mcpconns.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := connectors.NewJSON("")
	svc, err := mcpconns.New(mcpconns.Options{
		Store: ms, Key: func(ctx context.Context) ([]byte, error) { return cs.EnsureKey(ctx, connectors.NewKey) },
		AllowPrivate: func() bool { return allowPrivate },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return svc, ms
}

func TestAChatTurnCallsAToolOfAUserDefinedMCPServer(t *testing.T) {
	ts, reached := e2eServer(t)
	conns, _ := realConnectors(t, true) // the test server listens on loopback
	c, err := conns.Create(context.Background(), alice, mcpconns.Input{Name: "Shouter", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	svc, h := newToolService(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "shout", Args: `{"text":"hello"}`}, {Say: "done"}}
	}, func(d *studio.Deps) { d.MCPConnectors = conns })
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	res := h.ResultsFor("shout")
	if len(res) != 1 || res[0].Err != nil || res[0].Out != "HELLO" {
		t.Fatalf("result = %+v (server reached %d times)", res, reached.Load())
	}
	// Renaming the connector does not break an agent that holds its id.
	if _, err := conns.Update(context.Background(), alice, c.ID, mcpconns.Input{Name: "Renamed", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	tools, missing := conns.Resolve(context.Background(), alice, []string{c.ID}, false)
	if len(tools) != 1 || len(missing) != 0 || tools[0].Connector != "Renamed" {
		t.Fatalf("after the rename: %v %v", tools, missing)
	}
}

func TestAPrivateAddressGivesTheModelANoteAndNoTools(t *testing.T) {
	ts, reached := e2eServer(t)
	conns, store := realConnectors(t, false) // the default: private addresses are refused
	if _, err := conns.Create(context.Background(), alice, mcpconns.Input{Name: "Direct", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true}); !errors.Is(err, mcpconns.ErrInvalid) {
		t.Fatalf("an enabled connector with a loopback address: %v", err)
	}
	// A record that got in another way (saved while the switch was on, or
	// written by another frontend): the connection itself must refuse.
	rec := mcpconns.Connector{ID: "internal1", Owner: alice, Name: "Internal", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true}
	if err := store.Put(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	as, _ := agents.NewJSON("")
	svc, h := newToolService(t, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} },
		func(d *studio.Deps) { d.MCPConnectors, d.Agents = conns, as })
	_ = as.Put(context.Background(), agents.Agent{ID: "ag", Principal: alice, Name: "A", MCPServers: []string{"connector:internal1"}})
	chat, err := svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: "ag"})
	if err != nil {
		t.Fatal(err)
	}
	sent(t, svc, alice, chat.ID, "go")
	idle(t, svc, alice, chat.ID)
	if slices.Contains(h.ToolNames(chat.ID), "shout") {
		t.Fatal("the tool of a refused connector was offered")
	}
	if msg := strings.Join(h.Sent(chat.ID), "\n"); !strings.Contains(msg, "MCP connector Internal") || !strings.Contains(msg, "cannot be reached") {
		t.Fatalf("no note for the model: %q", msg)
	}
	if reached.Load() != 0 {
		t.Fatal("the server on a private address was reached")
	}
}
