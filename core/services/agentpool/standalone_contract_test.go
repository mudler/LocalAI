package agentpool_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/agentpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("standalone agent service contract", func() {
	var (
		llm *fakeLLM
		dir string
	)

	BeforeEach(func() {
		llm = newFakeLLM("hello from the fake model")
		// DeferCleanup runs after AfterEach and in LIFO order, so registering
		// here lets a pool started later stop before its LLM goes away.
		DeferCleanup(llm.Close)
		dir = GinkgoT().TempDir()
	})

	It("boots against a fake LLM and lists no agents", func() {
		svc := startStandalone(dir, llm.URL())
		DeferCleanup(svc.Stop)

		Expect(svc.ListAgentsForUser("")).To(BeEmpty())
	})

	Context("agent CRUD", func() {
		var svc *agentpool.AgentPoolService
		BeforeEach(func() {
			svc = startStandalone(dir, llm.URL())
			DeferCleanup(svc.Stop)
		})

		It("creates, reads back, updates and deletes an agent", func() {
			Expect(svc.CreateAgentForUser("alice", newAgentConfig("helper"))).To(Succeed())

			got := svc.GetAgentConfigForUser("alice", "helper")
			Expect(got).ToNot(BeNil())
			// The pool stores the key ("alice:helper"); the API must show the bare name.
			Expect(got.Name).To(Equal("helper"))
			Expect(got.Model).To(Equal("fake-model"))
			Expect(svc.ListAgentsForUser("alice")).To(HaveKeyWithValue("helper", true))

			updated := newAgentConfig("helper")
			updated.Description = "changed"
			Expect(svc.UpdateAgentForUser("alice", "helper", updated)).To(Succeed())
			Expect(svc.GetAgentConfigForUser("alice", "helper").Description).To(Equal("changed"))

			Expect(svc.DeleteAgentForUser("alice", "helper")).To(Succeed())
			Expect(svc.GetAgentConfigForUser("alice", "helper")).To(BeNil())
			Expect(svc.ListAgentsForUser("alice")).ToNot(HaveKey("helper"))
		})

		It("reports an update of a missing agent as ErrAgentNotFound", func() {
			err := svc.UpdateAgentForUser("alice", "ghost", newAgentConfig("ghost"))
			Expect(err).To(MatchError(agentpool.ErrAgentNotFound))
		})

		It("keeps two users' agents with the same name apart", func() {
			a := newAgentConfig("shared-name")
			a.Description = "alice's"
			b := newAgentConfig("shared-name")
			b.Description = "bob's"
			Expect(svc.CreateAgentForUser("alice", a)).To(Succeed())
			Expect(svc.CreateAgentForUser("bob", b)).To(Succeed())

			Expect(svc.GetAgentConfigForUser("alice", "shared-name").Description).To(Equal("alice's"))
			Expect(svc.GetAgentConfigForUser("bob", "shared-name").Description).To(Equal("bob's"))

			Expect(svc.DeleteAgentForUser("alice", "shared-name")).To(Succeed())
			Expect(svc.GetAgentConfigForUser("alice", "shared-name")).To(BeNil())
			Expect(svc.GetAgentConfigForUser("bob", "shared-name")).ToNot(BeNil())

			grouped := svc.ListAllAgentsGrouped()
			Expect(grouped).To(HaveKey("bob"))
			Expect(grouped).ToNot(HaveKey("alice"))
		})

		It("round-trips a config through export and import without a user", func() {
			cfg := newAgentConfig("portable")
			cfg.Description = "carry me"
			Expect(svc.CreateAgentForUser("", cfg)).To(Succeed())

			data, err := svc.ExportAgentForUser("", "portable")
			Expect(err).ToNot(HaveOccurred())

			Expect(svc.DeleteAgentForUser("", "portable")).To(Succeed())
			Expect(svc.ImportAgentForUser("", data)).To(Succeed())

			got := svc.GetAgentConfigForUser("", "portable")
			Expect(got).ToNot(BeNil())
			Expect(got.Description).To(Equal("carry me"))
		})

		// Known defect pinned on purpose: export returns the stored config, whose
		// name is the pool key, and import refuses ":" in names. A rewrite that
		// fixes this must flip this spec rather than silently change behavior.
		It("exports a user's agent under its pool key, which import then rejects", func() {
			cfg := newAgentConfig("portable")
			cfg.Description = "carry me"
			Expect(svc.CreateAgentForUser("alice", cfg)).To(Succeed())

			data, err := svc.ExportAgentForUser("alice", "portable")
			Expect(err).ToNot(HaveOccurred())
			var out map[string]any
			Expect(json.Unmarshal(data, &out)).To(Succeed())
			Expect(out["name"]).To(Equal("alice:portable"))

			Expect(svc.DeleteAgentForUser("alice", "portable")).To(Succeed())
			Expect(svc.ImportAgentForUser("alice", data)).To(MatchError(ContainSubstring("invalid characters")))
			Expect(svc.GetAgentConfigForUser("alice", "portable")).To(BeNil())
		})

		// P5 strips connectors and actions and the P2 migration reads old configs,
		// so record what a config that carries them looks like today.
		It("accepts and returns a config that carries connectors and actions", func() {
			raw := []byte(`{
				"name": "legacy",
				"model": "fake-model",
				"description": "old style",
				"connectors": [{"type": "irc", "config": "{}"}],
				"actions": [{"name": "search", "config": "{}"}]
			}`)
			Expect(svc.ImportAgentForUser("alice", raw)).To(Succeed())

			data, err := svc.ExportAgentForUser("alice", "legacy")
			Expect(err).ToNot(HaveOccurred())
			var out map[string]any
			Expect(json.Unmarshal(data, &out)).To(Succeed())
			Expect(out["connectors"]).To(HaveLen(1))
			Expect(out["actions"]).To(HaveLen(1))
		})
	})
})
