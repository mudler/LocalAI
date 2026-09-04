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

	It("connects with a minted backend worker JWT and publishes on its one remaining allowed subject", func() {
		// A backend worker's whole grant is `_INBOX.>` now, on both sides.
		// Every verb a frontend gives it, file staging included, is an HTTP
		// route on its tunnel, and it no longer opens a bus connection at all;
		// the JWT is minted and unused. See pkg/natsauth.WorkerPermissions.
		Expect(infra.NC.Publish("_INBOX.probe", map[string]string{"path": "/tmp/model"})).To(Succeed())
		// ConfirmRoundTrip is the flush AND the server's verdict in one call.
		// Read separately they were two assertions that could drift apart; the
		// verdict is the one that matters, because a permission violation does
		// not close the connection.
		Expect(infra.NC.ConfirmRoundTrip(2 * time.Second)).To(Succeed())
		Expect(infra.NC.IsConnected()).To(BeTrue())
	})

	It("denies a backend worker the file-staging subjects it no longer serves", func() {
		// This spec used to assert the OPPOSITE, and kept passing after the
		// grant was deleted. A NATS permission violation does not close the
		// connection, so a spec that checks only FlushTimeout and IsConnected
		// cannot tell an allowed publish from a denied one; LastError is what
		// actually reads the server's verdict, which is why the sibling below
		// has always used it.
		subject := nodeSubjectPrefix(infra.NodeID) + ".files.stage"
		Expect(infra.NC.Publish(subject, map[string]string{"path": "/tmp/model"})).To(Succeed())
		Eventually(func() error {
			return infra.NC.ConfirmRoundTrip(500 * time.Millisecond)
		}, "3s", "50ms").Should(HaveOccurred())
	})

	It("denies backend subscribe on the node prefix it no longer listens to", func() {
		// The node subtree was granted while a backend worker still held a
		// connection with nothing under it subscribed. It does not hold one at
		// all now, so the grant went too; asserting the denial is what would
		// catch a subject quietly coming back to the bus.
		wild := nodeSubjectPrefix(infra.NodeID) + ".>"
		sub, err := infra.NC.Subscribe(wild, func(_ []byte) {})
		if err == nil {
			defer func() { _ = sub.Unsubscribe() }()
			Eventually(func() error {
				return infra.NC.ConfirmRoundTrip(500 * time.Millisecond)
			}, "3s", "50ms").Should(HaveOccurred())
		}
	})

	It("rejects anonymous publish on the JWT-enabled server", func() {
		anon, err := messaging.New(infra.NatsURL)
		Expect(err).ToNot(HaveOccurred())
		defer anon.Close()

		err = anon.Publish("nodes.any.files.x", map[string]string{"x": "1"})
		Expect(err).ToNot(HaveOccurred())
		Expect(anon.ConfirmRoundTrip(2 * time.Second)).To(HaveOccurred())
	})

	It("denies backend publish to another node's subjects", func() {
		other := nodeSubjectPrefix("other-node-id") + ".files.stage"
		Expect(infra.NC.Publish(other, map[string]string{"stage": "nope"})).To(Succeed())
		Eventually(func() error {
			return infra.NC.ConfirmRoundTrip(500 * time.Millisecond)
		}, "3s", "50ms").Should(HaveOccurred())
	})

	It("mints agent JWT without backend.install in claims", func() {
		cfg := natsauth.Config{AccountSeed: infra.AccountSeed}
		token, _, err := cfg.MintWorkerJWT("agent-node-1", "agent")
		Expect(err).ToNot(HaveOccurred())

		claims, err := natsauth.DecodeUserClaims(token)
		Expect(err).ToNot(HaveOccurred())
		// agent.execute has left this list: agent execution is a streaming
		// control verb on the tunnel now, driven by a claim a frontend replica
		// took off the job store. What must still be here is the cancel
		// broadcast, which cannot become an RPC.
		Expect(claims.Permissions.Sub.Allow).To(ContainElement("agent.*.cancel"))
		Expect(claims.Permissions.Sub.Allow).ToNot(ContainElement("agent.execute"))
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

		// Mirror core/cli/agent_worker.go exactly. MCP tool execution and
		// discovery are absent, so is the per-node backend.stop, and so now are
		// agent execution and MCP CI runs: none of them is a bus subject any
		// more. The frontend selects an agent worker itself and reaches it with
		// a control RPC over the tunnel that worker holds, and the work it
		// hands over is a row it claimed on the job store.
		//
		// What is left is the cancel broadcast, which cannot become an RPC: the
		// replica holding a run is not the one an API cancel lands on.
		_, err = nc.Subscribe(messaging.SubjectAgentCancelWildcard, func([]byte) {})
		Expect(err).ToNot(HaveOccurred(), "agent JWT must allow %s (cancellation)", messaging.SubjectAgentCancelWildcard)

		_, err = nc.Subscribe(messaging.SubjectJobProgressWildcard, func([]byte) {})
		Expect(err).ToNot(HaveOccurred(), "agent JWT must allow %s (progress bridging)", messaging.SubjectJobProgressWildcard)
	})

	// The narrowing, proved against the enforcing server rather than against
	// the allow list that feeds it. The subject is written out by hand because
	// its builder is deleted; that literal is what a worker from an older
	// release would still send, and this is what the server now answers it.
	//
	// It is a narrowing and not a lockout: the two subscriptions above are made
	// on a JWT minted the same way and both succeed, so the list this trims is
	// demonstrably not the empty one NATS would read as unrestricted.
	It("refuses an agent-minted JWT the retired per-node backend.stop subject", func() {
		const nodeID = "agent-node-stop"
		cfg := natsauth.Config{AccountSeed: infra.AccountSeed, WorkerJWTTTL: time.Hour}
		token, seed, err := cfg.MintWorkerJWT(nodeID, "agent")
		Expect(err).ToNot(HaveOccurred())

		nc, err := messaging.New(infra.NatsURL, messaging.WithUserJWT(token, seed))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(nc.Close)

		_, err = nc.Subscribe("nodes."+nodeID+".backend.stop", func([]byte) {})
		Expect(err).To(HaveOccurred(),
			"backend.stop is a control RPC on the worker's tunnel; the bus must not carry it")
	})

	// The same narrowing for the two queue subjects that became claim rows.
	// Written out by hand for the same reason: those literals are what a worker
	// from an older release would still subscribe to, and this is what the
	// server now answers it.
	DescribeTable("refuses an agent-minted JWT a retired queue subject",
		func(subject string) {
			cfg := natsauth.Config{AccountSeed: infra.AccountSeed, WorkerJWTTTL: time.Hour}
			token, seed, err := cfg.MintWorkerJWT("agent-node-queues", "agent")
			Expect(err).ToNot(HaveOccurred())

			nc, err := messaging.New(infra.NatsURL, messaging.WithUserJWT(token, seed))
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(nc.Close)

			_, err = nc.Subscribe(subject, func([]byte) {})
			Expect(err).To(HaveOccurred(),
				"%s became a claim on the job store; the bus must not carry it", subject)
		},
		Entry("agent execution", "agent.execute"),
		Entry("mcp ci jobs", "jobs.mcp-ci.new"),
	)
})
