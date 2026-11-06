package nibharness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/nib/chat"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// cogitoAgentTools are the names of the agent tools of the tool loop library
// under nib. It reserves them: an MCP tool of one of these names is not offered
// to the model, even when nib's own agent tool is off. A host tool of such a
// name is therefore served as "studio_<name>" (see wireName), hidden from the
// model by the alias layer, and called by its own name. The other built-ins of
// nib need no alias: a tool of the host replaces a built-in (chat.HostTransport).
var cogitoAgentTools = map[string]bool{
	"spawn_agent": true, "check_agent": true, "get_agent_result": true, "send_agent_message": true,
}

// wireNamePrefix marks a host tool whose name the tool loop reserves.
const wireNamePrefix = "studio_"

// wireName is the name of a host tool as nib sees it.
func wireName(name string) string {
	if cogitoAgentTools[name] {
		return wireNamePrefix + name
	}
	return name
}

// hostName is the inverse of wireName, for the events of a finished call.
func hostName(wire string) string {
	if orig := wire[min(len(wireNamePrefix), len(wire)):]; strings.HasPrefix(wire, wireNamePrefix) && cogitoAgentTools[orig] {
		return orig
	}
	return wire
}

// toolServer serves the session's tools over an in-memory MCP transport.
type toolServer struct {
	srv    *mcp.Server
	t      mcp.Transport
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newToolServer returns the server, the client side transport for nib, and
// nothing else: the tools pass through chat.HostTransport.
func newToolServer(tools []session.Tool) (*toolServer, mcp.Transport, error) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "session-tools", Version: "v1"}, nil)
	for _, tl := range tools {
		if tl.Name == "" || tl.Run == nil {
			return nil, nil, fmt.Errorf("nib: tool %q is not complete", tl.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(tl.Schema, &schema); err != nil {
			return nil, nil, fmt.Errorf("nib: tool %q: bad schema: %w", tl.Name, err)
		}
		run := tl.Run
		srv.AddTool(&mcp.Tool{Name: wireName(tl.Name), Description: tl.Description, InputSchema: schema},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				out, err := run(ctx, req.Params.Arguments)
				if err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil
			})
	}
	srvT, cliT := mcp.NewInMemoryTransports()
	return &toolServer{srv: srv, t: srvT}, chat.HostTransport(cliT), nil
}

func (s *toolServer) start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Go(func() {
		_ = s.srv.Run(ctx, s.t)
	})
}

func (s *toolServer) stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}
