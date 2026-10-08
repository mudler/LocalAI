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
		ctx     context.Context
		db      *gorm.DB
		reg     *NodeRegistry
		workers *SwitchWorkers
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
		workers = NewSwitchWorkers(reg, 5*time.Minute)
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

	It("reads the age of a heartbeat on the clock of the database and not on the clock of this replica", func() {
		n := register("agent-1", NodeTypeAgent, "10.0.0.9:50051")
		beat := time.Now()
		Expect(db.Model(&BackendNode{}).Where("id = ?", n.ID).Update("last_heartbeat", beat).Error).To(Succeed())

		// This replica's clock says the heartbeat is fresh. The clock of the database
		// is ten minutes ahead, which makes it stale.
		skewed := NewSwitchWorkers(reg, 5*time.Minute)
		skewed.now = func(context.Context) time.Time { return beat.Add(10 * time.Minute) }
		list, err := skewed.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(list).To(BeEmpty())
		on, err := skewed.AgentsAttached(ctx, cluster.CarrierNATS)
		Expect(err).ToNot(HaveOccurred())
		Expect(on).To(BeFalse())

		// And the other way: a clock that is ahead here does not hide a live worker.
		real := NewSwitchWorkers(reg, 5*time.Minute)
		list, err = real.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(list).To(HaveLen(1))
	})

	It("counts a worker that reports no capabilities as one that cannot follow, and as one that is on NATS", func() {
		register("old", NodeTypeBackend, "10.0.0.1:50051")
		list, err := workers.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		w := byName(list)["old"]
		Expect(w.Reports).To(BeFalse())
		Expect(w.Attached).To(Equal([]cluster.Carrier{cluster.CarrierNATS}), "a worker that predates carrier switching has no other carrier")
	})

	It("takes the carriers a worker reports, and nothing else", func() {
		n := register("both", NodeTypeBackend, "10.0.0.1:50051")
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

	It("says that a worker which reports a tunnel is on the tunnel, and one which reports NATS is on NATS", func() {
		a := register("tun", NodeTypeBackend, "")
		b := register("nats", NodeTypeBackend, "10.0.0.1:50051")
		Expect(reg.SetCarrierReport(ctx, a.ID, CarrierReport{Attached: []cluster.Carrier{cluster.CarrierTunnel}, Follow: []cluster.Carrier{cluster.CarrierTunnel}})).To(Succeed())
		Expect(reg.SetCarrierReport(ctx, b.ID, CarrierReport{Attached: []cluster.Carrier{cluster.CarrierNATS}, Follow: []cluster.Carrier{cluster.CarrierNATS}})).To(Succeed())
		list, err := workers.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(byName(list)["tun"].Attached).To(Equal([]cluster.Carrier{cluster.CarrierTunnel}))
		Expect(byName(list)["nats"].Attached).To(Equal([]cluster.Carrier{cluster.CarrierNATS}))
	})

	It("does not guess a carrier for a worker that reports it is attached to none", func() {
		n := register("between", NodeTypeBackend, "")
		Expect(reg.SetCarrierReport(ctx, n.ID, CarrierReport{Attached: nil, Follow: []cluster.Carrier{cluster.CarrierTunnel}})).To(Succeed())
		list, err := workers.Workers(ctx)
		Expect(err).ToNot(HaveOccurred())
		w := byName(list)["between"]
		Expect(w.Reports).To(BeTrue())
		Expect(w.Attached).To(BeEmpty())
		a, err := workers.AttachedCarriers(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(a).To(BeEmpty())
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
		Expect(reg.SetCarrierReport(ctx, agent.ID, CarrierReport{Attached: []cluster.Carrier{cluster.CarrierTunnel}, Follow: []cluster.Carrier{cluster.CarrierNATS, cluster.CarrierTunnel}})).To(Succeed())

		onTunnel, err := workers.AgentsAttached(ctx, cluster.CarrierTunnel)
		Expect(err).ToNot(HaveOccurred())
		Expect(onTunnel).To(BeTrue())
		onNATS, err := workers.AgentsAttached(ctx, cluster.CarrierNATS)
		Expect(err).ToNot(HaveOccurred())
		Expect(onNATS).To(BeFalse(), "the backend worker is on NATS, and it is not an agent worker")
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
