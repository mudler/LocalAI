package studio

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("sub-agents of an agent with several folders", func() {
	It("fits only when every folder of the sub-agent is inside a folder of the parent", func() {
		ctx := context.Background()
		s, ws, _ := capacityService()
		home := session.HomePath("alice")
		for _, d := range []string{"/Projects", "/Projects/Acme", "/Notes", "/Secret"} {
			_, err := ws.Mkdir(ctx, s.d.Files.Actor("alice"), home+d)
			Expect(err).NotTo(HaveOccurred())
		}
		save := func(name string, rows ...AgentFolderWrite) agents.Agent {
			GinkgoHelper()
			a, err := s.SaveAgentWithFolders(ctx, "alice", agents.Agent{Name: name, Model: "test"}, &rows)
			Expect(err).NotTo(HaveOccurred())
			return a
		}
		parent := save("Parent", AgentFolderWrite{Path: "/Projects", Mode: "write"}, AgentFolderWrite{Path: "/Notes", Mode: "read"})
		inside := save("Inside", AgentFolderWrite{Path: "/Projects/Acme", Mode: "write"}, AgentFolderWrite{Path: "/Notes", Mode: "read"})
		outside := save("Outside", AgentFolderWrite{Path: "/Projects/Acme", Mode: "write"}, AgentFolderWrite{Path: "/Secret", Mode: "read"})
		rec, err := s.Create(ctx, "alice", CreateInput{AgentID: parent.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(s.agentFitsChat(ctx, rec, inside)).To(Succeed())
		Expect(s.agentFitsChat(ctx, rec, outside)).To(MatchError(ErrAgentOutsideFolder))
	})
})
