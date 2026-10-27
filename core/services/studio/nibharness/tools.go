package nibharness

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// toolServer serves the session's tools over an in-memory MCP transport.
type toolServer struct {
	srv    *mcp.Server
	t      mcp.Transport
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newToolServer returns the server, the client side transport for nib, and
// the tool names for nib's BuiltinTools list.
func newToolServer(tools []session.Tool) (*toolServer, mcp.Transport, []string, error) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "session-tools", Version: "v1"}, nil)
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		if tl.Name == "" || tl.Run == nil {
			return nil, nil, nil, fmt.Errorf("nib: tool %q is not complete", tl.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(tl.Schema, &schema); err != nil {
			return nil, nil, nil, fmt.Errorf("nib: tool %q: bad schema: %w", tl.Name, err)
		}
		run := tl.Run
		srv.AddTool(&mcp.Tool{Name: tl.Name, Description: tl.Description, InputSchema: schema},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				out, err := run(ctx, req.Params.Arguments)
				if err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil
			})
		names = append(names, tl.Name)
	}
	srvT, cliT := mcp.NewInMemoryTransports()
	return &toolServer{srv: srv, t: srvT}, cliT, names, nil
}

func (s *toolServer) start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.srv.Run(ctx, s.t)
	}()
}

func (s *toolServer) stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}
