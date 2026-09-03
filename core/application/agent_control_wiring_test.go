// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// recordingConnections captures the owner id the selection was made with. It is
// how this spec sees the one argument whose loss has no other symptom.
type recordingConnections struct{ owners []string }

func (r *recordingConnections) ConnectedAmong(_ context.Context, _ []string, owner string) ([]string, []string, error) {
	r.owners = append(r.owners, owner)
	return nil, nil, nil
}

// The wiring that connects MCP to the agent workers, guarded the way the
// absence wiring is and for the same reason: initDistributed opens a database
// and a bus, so no unit spec reaches the construction literal, and one of these
// arguments is silent when it is wrong.
var _ = Describe("building the frontend's agent control client", func() {
	var registry *nodes.NodeRegistry
	var ctx context.Context

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		var err error
		registry, err = nodes.NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
	})

	// Every refusal below is given a valid value for everything except the one
	// argument it is about, so no assertion can be satisfied by a guard that
	// fires for the wrong reason.
	It("makes the selection with THIS replica's instance id", func() {
		// The silent one. With an empty id every connection read reports
		// nothing as held here, so every MCP call relays through a peer even
		// for a worker whose tunnel this replica holds: correct answers, one
		// extra hop, and no log line anywhere.
		Expect(registry.Register(ctx, &nodes.BackendNode{
			Name: "agent-1", NodeType: nodes.NodeTypeAgent, Address: "a:50051",
		}, true)).To(Succeed())

		conns := &recordingConnections{}
		client, err := newAgentControl(
			config.DistributedConfig{InstanceID: "replica-7"}, registry, conns,
			nodes.NewControlClient(nil, "token"))
		Expect(err).ToNot(HaveOccurred())

		// Driven through a real call rather than read off a field: what has to
		// be true is that the id reaches the SELECTION, not that it was stored.
		_, _ = client.ExecuteMCPTool(ctx, mcpremote.MCPToolRequest{ModelName: "m"})
		Expect(conns.owners).To(ConsistOf("replica-7"))
	})

	It("refuses to build with no instance id", func() {
		_, err := newAgentControl(config.DistributedConfig{}, registry, &recordingConnections{},
			nodes.NewControlClient(nil, "token"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("instance id"))
	})

	It("refuses to build with nothing to read connections through", func() {
		_, err := newAgentControl(config.DistributedConfig{InstanceID: "replica-7"}, registry, nil,
			nodes.NewControlClient(nil, "token"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connected agent worker"))
	})

	It("refuses to build with no control transport", func() {
		_, err := newAgentControl(config.DistributedConfig{InstanceID: "replica-7"}, registry,
			&recordingConnections{}, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("control transport"))
	})
})
