package carrier_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/agentworker"
	"github.com/mudler/LocalAI/core/services/cluster"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
)

const agentToken = "own-tunnel-credential-of-the-agent"

var _ = Describe("Agent workers on the tunnel carrier, end to end", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		dsn      string
		clusterR *cluster.Registry
		nodeReg  *nodes.NodeRegistry
		a, b     *replica
		agentID  string

		cfg agentworker.Config
	)

	// startAgent connects the agent worker to a frontend.
	startAgent := func(frontend string) *agentworker.Runtime {
		GinkgoHelper()
		rt, err := agentworker.Start(ctx, agentworker.Options{
			FrontendURL:  frontend,
			NodeID:       agentID,
			TunnelToken:  func() string { return agentToken },
			ControlToken: registrationToken,
			Handler:      agentworker.Handler(cfg),
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(rt.Close)
		return rt
	}

	BeforeEach(func() {
		ctx = context.Background()
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		var err error
		nodeReg, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		agent := &nodes.BackendNode{Name: "agent-1", NodeType: nodes.NodeTypeAgent, TokenHash: hashOf(registrationToken)}
		Expect(nodeReg.Register(ctx, agent, true)).To(Succeed())
		agentID = agent.ID
		Expect(nodeReg.SetTunnelTokenHash(ctx, agentID, hashOf(agentToken))).To(Succeed())

		a, b = startReplica(ctx, "replica-a", db, dsn, clusterR, nodeReg), startReplica(ctx, "replica-b", db, dsn, clusterR, nodeReg)

		cfg = agentworker.Config{
			MCPTool: agentworker.Unary(func(_ context.Context, req mcpremote.MCPToolRequest) mcpremote.MCPToolResponse {
				if req.ToolName == "fails" {
					return mcpremote.MCPToolResponse{Error: "the tool failed"}
				}
				return mcpremote.MCPToolResponse{Result: "ran " + req.ToolName}
			}),
		}
	})

	holds := func(r *replica) func() bool {
		return func() bool { return r.tunnels.Holds(agentID) }
	}

	Describe("the MCP requests of the frontend", func() {
		It("reach the agent worker from the replica that holds its tunnel and from the one that does not", func() {
			startAgent(a.url)
			Eventually(holds(a), "15s", "50ms").Should(BeTrue())
			for _, r := range []*replica{a, b} {
				reply, err := r.agents.ExecuteMCPTool(ctx, mcpremote.MCPToolRequest{ModelName: "m", ToolName: "search"})
				Expect(err).ToNot(HaveOccurred(), r.id)
				Expect(reply.Result).To(Equal("ran search"), r.id)

				failed, err := r.agents.ExecuteMCPTool(ctx, mcpremote.MCPToolRequest{ModelName: "m", ToolName: "fails"})
				Expect(err).ToNot(HaveOccurred(), "a tool that failed is an answer: "+r.id)
				Expect(failed.Error).To(Equal("the tool failed"), r.id)
			}
		})

		It("find no route when no agent worker is connected, which the frontend reads as no agent worker", func() {
			for _, r := range []*replica{a, b} {
				_, err := r.agents.ExecuteMCPTool(ctx, mcpremote.MCPToolRequest{ModelName: "m", ToolName: "search"})
				Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeTrue(), "%s: %v", r.id, err)
				Expect(errors.Is(err, cluster.ErrNoConnection)).To(BeFalse(), "absence must not reach the caller")
			}
		})
	})
})
