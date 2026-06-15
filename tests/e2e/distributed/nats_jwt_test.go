package distributed_test

import (
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/natsauth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("NATS JWT Auth", Label("Distributed", "NatsJWT"), func() {
	var infra *JWTTestInfra

	BeforeEach(func() {
		infra = SetupJWTInfra()
	})

	It("connects with a minted backend worker JWT and publishes on allowed subjects", func() {
		// Backend workers may publish under nodes.<id>.files.> (see pkg/natsauth permissions).
		subject := nodeSubjectPrefix(infra.NodeID) + ".files.in"
		Expect(infra.NC.Publish(subject, map[string]string{"path": "/tmp/model"})).To(Succeed())
		Expect(infra.NC.Conn().FlushTimeout(2 * time.Second)).To(Succeed())
		Expect(infra.NC.Conn().IsConnected()).To(BeTrue())
	})

	It("allows backend subscribe on the node prefix", func() {
		wild := nodeSubjectPrefix(infra.NodeID) + ".>"
		sub, err := infra.NC.Subscribe(wild, func(_ []byte) {})
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = sub.Unsubscribe() }()
		Expect(infra.NC.Conn().FlushTimeout(2 * time.Second)).To(Succeed())
		Expect(infra.NC.Conn().IsConnected()).To(BeTrue())
	})

	It("rejects anonymous publish on the JWT-enabled server", func() {
		anon, err := messaging.New(infra.NatsURL)
		Expect(err).ToNot(HaveOccurred())
		defer anon.Close()

		err = anon.Publish("nodes.any.files.x", map[string]string{"x": "1"})
		Expect(err).ToNot(HaveOccurred())
		Expect(anon.Conn().FlushTimeout(2 * time.Second)).To(HaveOccurred())
	})

	It("denies backend publish to another node's subjects", func() {
		other := nodeSubjectPrefix("other-node-id") + ".files.stage"
		Expect(infra.NC.Publish(other, map[string]string{"stage": "nope"})).To(Succeed())
		Eventually(func() error {
			_ = infra.NC.Conn().FlushTimeout(500 * time.Millisecond)
			return infra.NC.Conn().LastError()
		}, "3s", "50ms").Should(HaveOccurred())
	})

	It("mints agent JWT without backend.install in claims", func() {
		cfg := natsauth.Config{AccountSeed: infra.AccountSeed}
		token, _, err := cfg.MintWorkerJWT("agent-node-1", "agent")
		Expect(err).ToNot(HaveOccurred())

		claims, err := natsauth.DecodeUserClaims(token)
		Expect(err).ToNot(HaveOccurred())
		Expect(claims.Permissions.Sub.Allow).To(ContainElement("agent.execute"))
		for _, subj := range claims.Permissions.Sub.Allow {
			Expect(subj).NotTo(ContainSubstring("backend.install"))
		}
	})

	// Regression guard for the silent permission gaps: decoding the JWT claims
	// (above) only proves the agent JWT is *restrictive*, not that it is
	// *sufficient*. Stand a real agent connection up against the enforcing
	// server and exercise every subscription core/cli/agent_worker.go actually
	// makes — a denied SUB now surfaces synchronously via confirmSubscription,
	// so a missing allow rule fails this test instead of silently dropping
	// backend.stop / MCP-CI deliveries at runtime.
	It("lets an agent-minted JWT establish all the subscriptions the agent worker uses", func() {
		const nodeID = "agent-node-subs"
		cfg := natsauth.Config{AccountSeed: infra.AccountSeed, WorkerJWTTTL: time.Hour}
		token, seed, err := cfg.MintWorkerJWT(nodeID, "agent")
		Expect(err).ToNot(HaveOccurred())

		nc, err := messaging.New(infra.NatsURL, messaging.WithUserJWT(token, seed))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(nc.Close)

		// Mirror core/cli/agent_worker.go exactly.
		_, err = nc.QueueSubscribeReply(messaging.SubjectMCPToolExecute, messaging.QueueAgentWorkers, func([]byte, func([]byte)) {})
		Expect(err).ToNot(HaveOccurred(), "agent JWT must allow %s", messaging.SubjectMCPToolExecute)

		_, err = nc.QueueSubscribeReply(messaging.SubjectMCPDiscovery, messaging.QueueAgentWorkers, func([]byte, func([]byte)) {})
		Expect(err).ToNot(HaveOccurred(), "agent JWT must allow %s", messaging.SubjectMCPDiscovery)

		_, err = nc.QueueSubscribe(messaging.SubjectMCPCIJobsNew, messaging.QueueWorkers, func([]byte) {})
		Expect(err).ToNot(HaveOccurred(), "agent JWT must allow %s (MCP CI jobs)", messaging.SubjectMCPCIJobsNew)

		_, err = nc.Subscribe(messaging.SubjectNodeBackendStop(nodeID), func([]byte) {})
		Expect(err).ToNot(HaveOccurred(), "agent JWT must allow %s (MCP session cleanup)", messaging.SubjectNodeBackendStop(nodeID))
	})
})
