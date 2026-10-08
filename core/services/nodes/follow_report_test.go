package nodes

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var _ = Describe("The workers as the switch of the carrier sees them", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		reg      *NodeRegistry
		presence *fakePresence
		workers  *SwitchWorkers
	)

	register := func(name, nodeType, address string) *BackendNode {
		GinkgoHelper()
		n := &BackendNode{Name: name, NodeType: nodeType, Address: address, TokenHash: "h"}
		Expect(reg.Register(ctx, n, true)).To(Succeed())
		return n
	}
	byName := func(list []cluster.WorkerInfo) map[string]cluster.WorkerInfo {
		out := map[string]cluster.WorkerInfo{}
		for _, w := range list {
			out[w.Name] = w
		}
		return out
	}

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		reg, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		presence = &fakePresence{by: map[string]cluster.Presence{}}
		workers = NewSwitchWorkers(reg, presence, cluster.DefaultReconnectGrace, 5*time.Minute)
	})

	It("lists the workers that are alive and leaves out the ones that are not", func() {
		register("live", NodeTypeBackend, "10.0.0.1:50051")
		stale := register("stale", NodeTypeBackend, "10.0.0.2:50051")
		offline := register("offline", NodeTypeBackend, "10.0.0.3:50051")
		Expect(db.Model(&BackendNode{}).Where("id = ?", stale.ID).Update("last_heartbeat", time.Now().Add(-time.Hour)).Error).To(Succeed())
		Expect(db.Model(&BackendNode{}).Where("id = ?", offline.ID).Update("status", StatusOffline).Error).To(Succeed())
		pending := &BackendNode{Name: "pending", NodeType: NodeTypeBackend, TokenHash: "h"}
		Expect(reg.Register(ctx, pending, false)).To(Succeed())

		list, err := workers.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(byName(list)).To(HaveLen(1))
		Expect(byName(list)).To(HaveKey("live"))
	})

	It("counts a worker that reports no capabilities as one that cannot follow, and says it is on NATS when it holds no tunnel", func() {
		register("old", NodeTypeBackend, "10.0.0.1:50051")
		list, err := workers.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		w := byName(list)["old"]
		Expect(w.Reports).To(BeFalse())
		Expect(w.Attached).To(Equal([]cluster.Carrier{cluster.CarrierNATS}))
	})

	It("says a worker that holds a tunnel is attached to the tunnel", func() {
		n := register("tun", NodeTypeBackend, "")
		presence.by[n.ID] = cluster.PresenceConnected
		list, err := workers.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(byName(list)["tun"].Attached).To(Equal([]cluster.Carrier{cluster.CarrierTunnel}))
	})

	It("says a worker that was attached to the tunnel a moment ago is still attached to it", func() {
		n := register("blink", NodeTypeBackend, "")
		presence.by[n.ID] = cluster.PresenceReconnecting
		list, err := workers.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(byName(list)["blink"].Attached).To(Equal([]cluster.Carrier{cluster.CarrierTunnel}))
	})

	It("takes what a worker reports over what can be inferred", func() {
		n := register("both", NodeTypeBackend, "10.0.0.1:50051")
		presence.by[n.ID] = cluster.PresenceConnected
		Expect(reg.SetCarrierReport(ctx, n.ID, CarrierReport{
			Attached: []cluster.Carrier{cluster.CarrierNATS, cluster.CarrierTunnel}, AttachedEpoch: 7,
			Follow: []cluster.Carrier{cluster.CarrierNATS, cluster.CarrierTunnel}, FollowError: "",
		})).To(Succeed())

		list, err := workers.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		w := byName(list)["both"]
		Expect(w.Reports).To(BeTrue())
		Expect(w.Attached).To(ConsistOf(cluster.CarrierNATS, cluster.CarrierTunnel))
		Expect(w.Follow).To(ConsistOf(cluster.CarrierNATS, cluster.CarrierTunnel))
	})

	It("carries the reason a worker gives for not following", func() {
		n := register("tunnel-only", NodeTypeBackend, "")
		Expect(reg.SetCarrierReport(ctx, n.ID, CarrierReport{
			Attached: []cluster.Carrier{cluster.CarrierTunnel}, Follow: []cluster.Carrier{cluster.CarrierTunnel},
			FollowError: "it has no address that the frontends can reach",
		})).To(Succeed())
		list, err := workers.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(byName(list)["tunnel-only"].FollowError).To(Equal("it has no address that the frontends can reach"))
	})

	It("answers where one worker is attached", func() {
		n := register("w", NodeTypeBackend, "10.0.0.1:50051")
		a, err := workers.AttachedCarriers(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(a).To(Equal([]cluster.Carrier{cluster.CarrierNATS}))

		_, err = workers.AttachedCarriers(ctx, "nobody")
		Expect(err).To(HaveOccurred())
	})

	It("says whether an agent worker is attached to a carrier", func() {
		register("backend", NodeTypeBackend, "10.0.0.1:50051")
		agent := register("agent", NodeTypeAgent, "")
		presence.by[agent.ID] = cluster.PresenceConnected

		onTunnel, err := workers.AgentsAttached(ctx, cluster.CarrierTunnel)
		Expect(err).ToNot(HaveOccurred())
		Expect(onTunnel).To(BeTrue())
		onNATS, err := workers.AgentsAttached(ctx, cluster.CarrierNATS)
		Expect(err).ToNot(HaveOccurred())
		Expect(onNATS).To(BeFalse(), "the backend worker is on NATS, and it is not an agent worker")
	})

	It("returns the error of the presence read and not a guess", func() {
		n := register("w", NodeTypeBackend, "")
		presence.err = errors.New("the database is down")
		_, err := workers.AttachedCarriers(ctx, n.ID)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("The direct client factory for a worker that holds a tunnel", func() {
	var (
		ctx     context.Context
		known   map[string]bool
		asked   int
		factory BackendClientFactory
	)

	BeforeEach(func() {
		ctx = context.Background()
		known = map[string]bool{"tunnel-only": false, "routable": true}
		asked = 0
		factory = NewGuardedDirectClientFactory("tok", func(nodeID string) (bool, error) {
			asked++
			has, ok := known[nodeID]
			if !ok {
				return false, errors.New("unknown node")
			}
			return has, nil
		})
	})

	health := func(nodeID, address string) (grpc.Backend, bool, error) {
		c := factory.NewClient(nodeID, address, false)
		hc, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		ok, err := c.HealthCheck(hc)
		return c, ok, err
	}

	It("does not dial the loopback address of a worker that registered no address, and reports a failure of the transport", func() {
		c, ok, err := health("tunnel-only", "127.0.0.1:1")
		Expect(ok).To(BeFalse())
		Expect(err).To(HaveOccurred())
		Expect(grpc.TransportFailureOf(c)).To(HaveOccurred(), "a caller that decides whether a backend is dead must not count this")
		Expect(classifyProbeOutcome(ok, err)).ToNot(Equal(ProbeAlive))
	})

	It("dials the loopback address of a worker that registered an address, as before", func() {
		c, ok, _ := health("routable", "127.0.0.1:1")
		Expect(ok).To(BeFalse())
		Expect(grpc.TransportFailureOf(c)).ToNot(HaveOccurred(), "a refused connection on a direct dial is the answer of the host about the backend")
	})

	It("does not ask the registry for an address that is not on loopback", func() {
		_, _, _ = health("tunnel-only", "10.255.255.1:1")
		Expect(asked).To(BeZero())
	})

	It("dials when the registry cannot say", func() {
		c, _, _ := health("unknown", "127.0.0.1:1")
		Expect(grpc.TransportFailureOf(c)).ToNot(HaveOccurred())
	})

	It("asks the registry once for a burst of clients of one worker", func() {
		for range 5 {
			factory.NewClient("tunnel-only", "127.0.0.1:1", false)
		}
		Expect(asked).To(Equal(1))
	})
})
