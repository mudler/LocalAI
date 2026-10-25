package mcp

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/config"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/pkg/functions"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type recordingAgentControl struct {
	toolReply      mcpRemote.MCPToolResponse
	discoveryReply mcpRemote.MCPDiscoveryResponse
	discoveries    int
	toolDeadline   time.Time
	discDeadline   time.Time
}

func (r *recordingAgentControl) ExecuteMCPTool(ctx context.Context, _ mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	r.toolDeadline, _ = ctx.Deadline()
	reply := r.toolReply
	return &reply, nil
}

func (r *recordingAgentControl) DiscoverMCPTools(ctx context.Context, _ mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	r.discoveries++
	r.discDeadline, _ = ctx.Deadline()
	reply := r.discoveryReply
	return &reply, nil
}

// The distributed mode switch is interface nil-ness: a nil AgentControl must
// keep MCP sessions local, exactly as a nil messaging client did before.
var _ = Describe("MCP routing through AgentControl", func() {
	var (
		remote config.MCPGenericConfig[config.MCPRemoteServers]
		stdio  config.MCPGenericConfig[config.MCPSTDIOServers]
	)

	It("keeps sessions local when no agent control is wired", func() {
		exec := NewToolExecutor(context.Background(), nil, "agent-control-nil", remote, stdio, nil)
		Expect(exec).To(BeAssignableToTypeOf(&LocalToolExecutor{}))
	})

	It("routes to agent workers when agent control is wired", func() {
		ac := &recordingAgentControl{discoveryReply: mcpRemote.MCPDiscoveryResponse{
			Tools: []mcpRemote.MCPToolDef{{ToolName: "weather", Function: functions.Function{Name: "weather"}}},
		}}
		exec := NewToolExecutor(context.Background(), ac, "agent-control-set", remote, stdio, nil)
		Expect(exec).To(BeAssignableToTypeOf(&DistributedToolExecutor{}))
		Expect(ac.discoveries).To(Equal(1))
		Expect(exec.IsTool("weather")).To(BeTrue())
	})

	It("keeps the worker's tool error text and bounds the call by the tool budget", func() {
		ac := &recordingAgentControl{toolReply: mcpRemote.MCPToolResponse{Error: "tool 'x' not found"}}
		start := time.Now()
		_, err := ExecuteMCPToolCallRemote(context.Background(), ac, "m", remote, stdio, "x", "{}")
		Expect(err).To(MatchError("remote MCP tool error: tool 'x' not found"))
		Expect(ac.toolDeadline).ToNot(BeZero())
		Expect(ac.toolDeadline.Sub(start)).To(BeNumerically("~", config.DefaultMCPToolTimeout, time.Second))
	})

	It("keeps the worker's discovery error text and bounds the call by the discovery budget", func() {
		ac := &recordingAgentControl{discoveryReply: mcpRemote.MCPDiscoveryResponse{Error: "no MCP servers"}}
		start := time.Now()
		_, err := DiscoverMCPToolsRemote(context.Background(), ac, "m", remote, stdio)
		Expect(err).To(MatchError("remote MCP discovery error: no MCP servers"))
		Expect(ac.discDeadline).ToNot(BeZero())
		Expect(ac.discDeadline.Sub(start)).To(BeNumerically("~", config.DefaultMCPDiscoveryTimeout, time.Second))
	})
})
