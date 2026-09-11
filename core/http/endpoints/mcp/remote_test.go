// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
)

// recordingAgent records the context each verb was given, so a spec can read
// the budget the caller applied.
//
// The budget is invisible any other way. It travels as a context deadline, not
// as anything on the wire, so the only place it can be observed is inside the
// call; a spec that waited for it to expire would be a spec that sleeps for
// minutes, and one that read a constant would pass with the constant unused.
type recordingAgent struct {
	toolDeadline  time.Time
	toolHasDL     bool
	discoDeadline time.Time
	discoHasDL    bool

	toolReply  *mcpRemote.MCPToolResponse
	discoReply *mcpRemote.MCPDiscoveryResponse
	err        error
}

func (a *recordingAgent) ExecuteMCPTool(ctx context.Context, _ mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	a.toolDeadline, a.toolHasDL = ctx.Deadline()
	if a.err != nil {
		return nil, a.err
	}
	return a.toolReply, nil
}

func (a *recordingAgent) DiscoverMCPTools(ctx context.Context, _ mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	a.discoDeadline, a.discoHasDL = ctx.Deadline()
	if a.err != nil {
		return nil, a.err
	}
	return a.discoReply, nil
}

var (
	noRemote = config.MCPGenericConfig[config.MCPRemoteServers]{}
	noStdio  = config.MCPGenericConfig[config.MCPSTDIOServers]{}
)

var _ = Describe("Routing MCP to an agent worker", func() {
	ctx := context.Background()

	Describe("with no agent control client wired", func() {
		// A frontend in distributed mode with nothing to reach an agent worker
		// through must say so. The alternative shape, which this replaces, was
		// a nil-pointer dereference inside a chat request.
		It("names the missing client rather than dialling nothing", func() {
			_, err := ExecuteMCPToolCallRemote(ctx, nil, "m", noRemote, noStdio, "weather", "{}")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("agent control client"))
		})

		It("names it for discovery too", func() {
			_, err := DiscoverMCPToolsRemote(ctx, nil, "m", noRemote, noStdio)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("agent control client"))
		})
	})

	Describe("the budget it gives the call", func() {
		// Two verbs, two constants, two assertions. The rule is written at both
		// sites, so a single spec would leave whichever site it did not cover
		// free to lose its deadline: a tool call whose worker went quiet would
		// then hold the caller until the tunnel's keepalive noticed.
		//
		// Asserted as a WINDOW around the deadline rather than an equality,
		// because the deadline is stamped from a clock this spec does not hold.
		// The window is far tighter than the difference between the two
		// constants, so a call given the wrong one still fails here.
		const slack = 5 * time.Second

		It("bounds a tool call by the documented tool timeout", func() {
			agent := &recordingAgent{toolReply: &mcpRemote.MCPToolResponse{Result: "ok"}}
			start := time.Now()

			out, err := ExecuteMCPToolCallRemote(ctx, agent, "m", noRemote, noStdio, "weather", `{"city":"London"}`)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal("ok"))

			Expect(agent.toolHasDL).To(BeTrue(), "the tool call was given no deadline at all")
			Expect(agent.toolDeadline).To(BeTemporally("~", start.Add(config.DefaultMCPToolTimeout), slack))
		})

		It("bounds discovery by the documented discovery timeout", func() {
			agent := &recordingAgent{discoReply: &mcpRemote.MCPDiscoveryResponse{}}
			start := time.Now()

			_, err := DiscoverMCPToolsRemote(ctx, agent, "m", noRemote, noStdio)
			Expect(err).ToNot(HaveOccurred())

			Expect(agent.discoHasDL).To(BeTrue(), "discovery was given no deadline at all")
			Expect(agent.discoDeadline).To(BeTemporally("~", start.Add(config.DefaultMCPDiscoveryTimeout), slack))
		})

		It("does not extend a caller's shorter deadline", func() {
			// The caller's own budget wins. context.WithTimeout keeps the
			// earlier of the two, and a hand-rolled deadline would not.
			short, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			agent := &recordingAgent{toolReply: &mcpRemote.MCPToolResponse{Result: "ok"}}

			_, err := ExecuteMCPToolCallRemote(short, agent, "m", noRemote, noStdio, "weather", "{}")
			Expect(err).ToNot(HaveOccurred())
			Expect(agent.toolDeadline).To(BeTemporally("<", time.Now().Add(config.DefaultMCPToolTimeout)))
		})
	})

	It("passes the agent's failure through with its identity intact", func() {
		// The classification is the control client's, and re-deciding it here
		// would be the same rule in two places. What this pins is that
		// wrapping it for a human does not hide it from errors.Is.
		boom := errors.New("the fleet said no")
		agent := &recordingAgent{err: boom}

		_, err := ExecuteMCPToolCallRemote(ctx, agent, "m", noRemote, noStdio, "weather", "{}")
		Expect(err).To(MatchError(boom))
		_, err = DiscoverMCPToolsRemote(ctx, agent, "m", noRemote, noStdio)
		Expect(err).To(MatchError(boom))
	})

	It("refuses tool arguments that are not JSON before reaching for a worker", func() {
		agent := &recordingAgent{toolReply: &mcpRemote.MCPToolResponse{Result: "ok"}}

		_, err := ExecuteMCPToolCallRemote(ctx, agent, "m", noRemote, noStdio, "weather", "{not json")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid tool arguments JSON"))
		Expect(agent.toolHasDL).To(BeFalse(), "no worker should have been asked")
	})
})
