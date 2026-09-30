package agentpool_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("standalone persistence contract", func() {
	var (
		llm *fakeLLM
		dir string
	)

	BeforeEach(func() {
		llm = newFakeLLM("ok")
		dir = GinkgoT().TempDir()
		DeferCleanup(llm.Close)
	})

	// The P2 migration imports this file, so its layout is an interface.
	It("writes pool.json keyed by userID:name with the agent config as value", func() {
		svc := startStandalone(dir, llm.URL())
		Expect(svc.CreateAgentForUser("alice", newAgentConfig("keeper"))).To(Succeed())
		awaitRunning(svc, "alice", "keeper")
		Expect(svc.CreateAgentForUser("", newAgentConfig("anon"))).To(Succeed())
		awaitRunning(svc, "", "anon")
		svc.Stop()

		raw, err := os.ReadFile(filepath.Join(dir, "pool.json"))
		Expect(err).ToNot(HaveOccurred())
		var pool map[string]map[string]any
		Expect(json.Unmarshal(raw, &pool)).To(Succeed())
		Expect(pool).To(HaveKey("alice:keeper"))
		Expect(pool).To(HaveKey("anon"))
		Expect(pool["alice:keeper"]["model"]).To(Equal("fake-model"))
		// The stored name repeats the key, prefix included: the P2 importer
		// strips the prefix, so it depends on this.
		Expect(pool["alice:keeper"]["name"]).To(Equal("alice:keeper"))
		Expect(pool["anon"]["name"]).To(Equal("anon"))
	})

	It("restores agents after a restart on the same state dir", func() {
		svc := startStandalone(dir, llm.URL())
		Expect(svc.CreateAgentForUser("alice", newAgentConfig("survivor"))).To(Succeed())
		awaitRunning(svc, "alice", "survivor")
		svc.Stop()

		again := startStandalone(dir, llm.URL())
		DeferCleanup(again.Stop)
		awaitRunning(again, "alice", "survivor")
		Expect(again.GetAgentConfigForUser("alice", "survivor")).ToNot(BeNil())
		Expect(again.ListAgentsForUser("alice")).To(HaveKey("survivor"))
	})

	// Pause is only an in-memory flag on the running agent: pool.json has no
	// status field and no per-agent file records pause, so a restart brings
	// the agent back active. Pinned as a known gap for the native-store migration to
	// close on purpose rather than by accident.
	It("does not keep a paused agent paused across a restart", func() {
		svc := startStandalone(dir, llm.URL())
		Expect(svc.CreateAgentForUser("alice", newAgentConfig("sleeper"))).To(Succeed())
		awaitRunning(svc, "alice", "sleeper")
		Expect(svc.PauseAgentForUser("alice", "sleeper")).To(Succeed())
		Expect(svc.ListAgentsForUser("alice")).To(HaveKeyWithValue("sleeper", false))
		svc.Stop()

		again := startStandalone(dir, llm.URL())
		DeferCleanup(again.Stop)
		awaitRunning(again, "alice", "sleeper")
		Expect(again.ListAgentsForUser("alice")).To(HaveKeyWithValue("sleeper", true))
	})

	// The /v1/responses interceptor decides "is this model an agent" with
	// GetAgent(name) using the raw pool key, with no user prefix.
	// Known gap, not a contract: any caller who sends model "alice:mine" runs
	// alice's agent (the interceptor has no user check), while alice's own
	// request for "mine" falls through; a later fix must not read as a break.
	It("resolves an agent by its raw key for the responses interceptor", func() {
		svc := startStandalone(dir, llm.URL())
		DeferCleanup(svc.Stop)
		Expect(svc.CreateAgentForUser("", newAgentConfig("global-agent"))).To(Succeed())
		awaitRunning(svc, "", "global-agent")
		Expect(svc.CreateAgentForUser("alice", newAgentConfig("mine"))).To(Succeed())
		awaitRunning(svc, "alice", "mine")

		Expect(svc.GetAgent("global-agent")).ToNot(BeNil())
		Expect(svc.GetAgent("alice:mine")).ToNot(BeNil())
		Expect(svc.GetAgent("mine")).To(BeNil(), "a user's agent is not visible without its prefix")
		Expect(svc.GetAgent("nope")).To(BeNil())
	})

	It("exposes the state dir it was started with", func() {
		svc := startStandalone(dir, llm.URL())
		DeferCleanup(svc.Stop)
		Expect(svc.StateDir()).To(Equal(dir))
	})
})
