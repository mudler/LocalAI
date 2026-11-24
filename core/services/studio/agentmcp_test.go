package studio_test

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the MCP servers of a saved agent", func() {
	ctx := context.Background()
	rig := func() *mcpConnRig {
		return newMCPConnRig(nil, newFakeMCPConnectors())
	}

	It("saves a connector the user has defined and a server of the model", func() {
		r := rig()
		a, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "ok", Model: "m", MCPServers: []string{"connector:c1", "crm"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(a.MCPServers).To(ConsistOf("connector:c1", "crm"))
	})

	It("refuses a connector that is not defined, with a plain sentence", func() {
		_, err := rig().svc.SaveAgent(ctx, alice, agents.Agent{Name: "x", MCPServers: []string{"connector:nope"}})
		Expect(errors.Is(err, agents.ErrInvalid)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(`"connector:nope" is not one of your MCP connectors`))
		Expect(err.Error()).To(ContainSubstring("MCP connectors page"))
	})

	It("refuses the connector of another user", func() {
		_, err := rig().svc.SaveAgent(ctx, alice, agents.Agent{Name: "x", MCPServers: []string{"connector:b1"}})
		Expect(errors.Is(err, agents.ErrInvalid)).To(BeTrue())
	})

	It("refuses a name that is not a server of the fixed model, and tolerates it when no model is fixed", func() {
		r := rig()
		_, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "x", Model: "m", MCPServers: []string{"typo"}})
		Expect(errors.Is(err, agents.ErrInvalid)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(`"typo" is not an MCP server of the model "m"`))
		_, err = r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "free", MCPServers: []string{"typo"}})
		Expect(err).NotTo(HaveOccurred())
	})

	It("keeps a name the agent already holds, so an old agent still loads and saves", func() {
		r := rig()
		Expect(r.agents.Put(ctx, agents.Agent{ID: "old", Principal: alice, Name: "old", Model: "m", MCPServers: []string{"gone", "connector:deleted"}})).To(Succeed())
		saved, err := r.svc.SaveAgent(ctx, alice, agents.Agent{ID: "old", Name: "renamed", Model: "m", MCPServers: []string{"gone", "connector:deleted"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(saved.MCPServers).To(ConsistOf("gone", "connector:deleted"))
		// Adding one more unknown name to it is refused.
		_, err = r.svc.SaveAgent(ctx, alice, agents.Agent{ID: "old", Name: "renamed", Model: "m", MCPServers: []string{"gone", "another"}})
		Expect(errors.Is(err, agents.ErrInvalid)).To(BeTrue())
	})
})
