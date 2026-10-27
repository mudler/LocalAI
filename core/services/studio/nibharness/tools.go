package nibharness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// nibBuiltin are the names of nib's own tools. nib turns a built-in on when its
// name is in the allowlist, and the allowlist also has to name the host tools so
// that they pass nib's filter. A host tool with a built-in's name would
// therefore switch the built-in on too: the model would get two tools of one
// name, and nib's would answer the call. Such a host tool is offered to the
// model as "studio_<name>" instead (see wireName), and called by its own name.
var nibBuiltin = map[string]bool{
	"spawn_agent": true, "check_agent": true, "get_agent_result": true, "send_agent_message": true,
	"ask_user": true, "agent_logs": true, "schedule_wakeup": true,
	"cron": true, "cron_list": true, "cron_delete": true, "cron_pause": true, "cron_resume": true, "cron_trigger": true,
	"read_image": true, "transcribe_audio": true, "read_video": true,
	"memory": true, "index": true, "repo_map": true, "tree": true, "lsp": true, "todo_write": true,
}

// wireNamePrefix marks a host tool whose name nib uses for a built-in.
const wireNamePrefix = "studio_"

// wireName is the name of a host tool as nib and the model see it.
func wireName(name string) string {
	if nibBuiltin[name] {
		return wireNamePrefix + name
	}
	return name
}

// hostName is the inverse of wireName, for the events of a finished call.
func hostName(wire string) string {
	if orig := wire[min(len(wireNamePrefix), len(wire)):]; strings.HasPrefix(wire, wireNamePrefix) && nibBuiltin[orig] {
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
		srv.AddTool(&mcp.Tool{Name: wireName(tl.Name), Description: tl.Description, InputSchema: schema},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				out, err := run(ctx, req.Params.Arguments)
				if err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil
			})
		names = append(names, wireName(tl.Name))
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
