package nodes

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/system"
)

// gatedNATS answers backend install requests only after the test opens the
// gate of the target node. It records how many requests are in flight, in
// total and per node.
type gatedNATS struct {
	*scriptedMessagingClient
	mu         sync.Mutex
	gates      map[string]chan struct{}
	arrived    []string
	inFlight   int
	maxInFlt   int
	perNode    map[string]int
	nodeClash  bool
	allOpen    bool
	requestsBy map[string]int
	nodeIDs    []string
}

func newGatedNATS() *gatedNATS {
	return &gatedNATS{
		scriptedMessagingClient: newScriptedMessagingClient(),
		gates:                   map[string]chan struct{}{},
		perNode:                 map[string]int{},
		requestsBy:              map[string]int{},
	}
}

func (g *gatedNATS) gate(nodeID string) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.gates[nodeID]
	if !ok {
		c = make(chan struct{})
		g.gates[nodeID] = c
	}
	return c
}

func (g *gatedNATS) open(nodeID string) { close(g.gate(nodeID)) }

func (g *gatedNATS) arrivals() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.arrived...)
}

func (g *gatedNATS) Request(subject string, _ []byte, _ time.Duration) ([]byte, error) {
	var nodeID string
	g.mu.Lock()
	for _, id := range g.nodeIDs {
		if subject == messaging.SubjectNodeBackendInstall(id) {
			nodeID = id
		}
	}
	g.mu.Unlock()
	gate := g.gate(nodeID)
	g.mu.Lock()
	g.arrived = append(g.arrived, nodeID)
	g.requestsBy[nodeID]++
	g.inFlight++
	g.perNode[nodeID]++
	if g.perNode[nodeID] > 1 {
		g.nodeClash = true
	}
	if g.inFlight > g.maxInFlt {
		g.maxInFlt = g.inFlight
	}
	g.mu.Unlock()

	<-gate

	g.mu.Lock()
	g.inFlight--
	g.perNode[nodeID]--
	g.mu.Unlock()
	return json.Marshal(workerctl.BackendInstallReply{Success: true})
}

// Backend operations to several nodes at once. These specs use SQLite, so they
// need no container.
var _ = Describe("DistributedBackendManager fan-out", func() {
	var (
		registry *NodeRegistry
		nats     *gatedNATS
		mgr      *DistributedBackendManager
		ctx      context.Context
		limit    int
		nodes    []*BackendNode
	)

	register := func(name, address string) *BackendNode {
		n := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: address}
		Expect(registry.Register(ctx, n, true)).To(Succeed())
		got, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		nats.mu.Lock()
		nats.nodeIDs = append(nats.nodeIDs, got.ID)
		nats.mu.Unlock()
		return got
	}

	BeforeEach(func() {
		ctx = context.Background()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "nodes.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).ToNot(HaveOccurred())
		sqlDB.SetMaxOpenConns(1)
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		nats = newGatedNATS()
		limit = 1
		mgr = &DistributedBackendManager{
			local:         stubLocalBackendManager{},
			adapter:       NewRemoteUnloaderAdapter(nil, nats, 3*time.Minute, 15*time.Minute),
			registry:      registry,
			systemState:   &system.SystemState{},
			maxConcurrent: func() int { return limit },
		}
		nodes = []*BackendNode{register("node-a", "10.0.0.1:1"), register("node-b", "10.0.0.2:1"), register("node-c", "10.0.0.3:1")}
	})

	install := func(name string) chan BackendOpResult {
		done := make(chan BackendOpResult, 1)
		go func() {
			defer GinkgoRecover()
			res, err := mgr.enqueueAndDrainBackendOp(ctx, "", OpBackendInstall, name, nil, nil, func(node BackendNode) error {
				reply, err := mgr.adapter.InstallBackend(node.ID, name, "", "", "", "", "", 0, "", nil)
				if err != nil {
					return err
				}
				if !reply.Success {
					return context.Canceled
				}
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			done <- res
		}()
		return done
	}

	It("visits the nodes one after the other with the default limit of 1", func() {
		done := install("llama-cpp")
		Eventually(nats.arrivals, "5s", "10ms").Should(HaveLen(1))
		Consistently(nats.arrivals, "300ms", "10ms").Should(HaveLen(1))

		for i := range nodes {
			Eventually(nats.arrivals, "5s", "10ms").Should(HaveLen(i + 1))
			nats.open(nats.arrivals()[i])
		}
		var res BackendOpResult
		Eventually(done, "5s").Should(Receive(&res))
		Expect(res.Nodes).To(HaveLen(3))
		Expect(nats.maxInFlt).To(Equal(1))
	})

	It("sends to the nodes in parallel up to the limit, and keeps the node order of the result", func() {
		limit = 2
		done := install("llama-cpp")
		Eventually(nats.arrivals, "5s", "10ms").Should(HaveLen(2))
		Consistently(nats.arrivals, "300ms", "10ms").Should(HaveLen(2))

		// A slow node does not hold the others: finish the second one first.
		first, second := nats.arrivals()[0], nats.arrivals()[1]
		nats.open(second)
		Eventually(nats.arrivals, "5s", "10ms").Should(HaveLen(3))
		nats.open(nats.arrivals()[2])
		Consistently(done, "200ms").ShouldNot(Receive())
		nats.open(first)

		var res BackendOpResult
		Eventually(done, "5s").Should(Receive(&res))
		Expect(res.Nodes).To(HaveLen(3))
		for i, n := range res.Nodes {
			Expect(n.Status).To(Equal(galleryop.NodeStatusSuccess))
			Expect(n.NodeID).To(Equal(nodes[i].ID), "results keep the order of the registry")
		}
		Expect(nats.maxInFlt).To(Equal(2))
	})

	It("never runs two operations on one backend and node at once", func() {
		limit = 3
		first := install("llama-cpp")
		Eventually(nats.arrivals, "5s", "10ms").Should(HaveLen(3))
		second := install("llama-cpp")
		Consistently(nats.arrivals, "300ms", "10ms").Should(HaveLen(3), "the second operation waits for the first on every node")

		for _, n := range nodes {
			nats.open(n.ID)
		}
		Eventually(first, "5s").Should(Receive())
		Eventually(second, "5s").Should(Receive())
		Expect(nats.nodeClash).To(BeFalse())
		for _, n := range nodes {
			Expect(nats.requestsBy[n.ID]).To(Equal(2))
		}
	})

	It("lets the reconciler skip a (node, backend) that a live operation holds", func() {
		node := nodes[0]
		Expect(registry.UpsertPendingBackendOp(ctx, node.ID, "llama-cpp", OpBackendInstall, nil)).To(Succeed())
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Adapter: mgr.adapter})

		unlock, ok := registry.backendOpLocks.tryLock(node.ID, "llama-cpp")
		Expect(ok).To(BeTrue())
		rc.drainPendingBackendOps(ctx)
		Expect(nats.arrivals()).To(BeEmpty(), "the live operation owns the node and backend")

		unlock()
		nats.open(node.ID)
		rc.drainPendingBackendOps(ctx)
		Expect(nats.arrivals()).To(Equal([]string{node.ID}))
		rows, err := registry.ListPendingBackendOps(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(rows).To(BeEmpty())
	})

	It("reads the limit from the application config when built from one", func() {
		cfg := config.NewApplicationConfig(config.WithMaxConcurrentBackendOperations(5))
		m := NewDistributedBackendManager(cfg, nil, nil, registry, nil)
		Expect(m.fanOutLimit()).To(Equal(5))
		Expect(NewDistributedBackendManager(config.NewApplicationConfig(), nil, nil, registry, nil).fanOutLimit()).To(Equal(1))
	})
})
