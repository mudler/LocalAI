package distributed_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ggrpc "google.golang.org/grpc"

	pgdriver "gorm.io/driver/postgres"
	gormDB "gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// prefixStubBackend implements grpc.Backend with a canned-success HealthCheck
// and LoadModel so SmartRouter.probeHealth passes and any cold load returns
// success — no real inference happens. Mirrors the stubBackend pattern used by
// the SmartRouter unit tests in core/services/nodes/router_test.go, reproduced
// here because that fake lives in the internal (unexported) nodes package.
type prefixStubBackend struct {
	grpcPkg.Backend // embed so unused methods satisfy the interface; they panic only if called

	healthResult bool
}

func (f *prefixStubBackend) HealthCheck(_ context.Context) (bool, error) {
	return f.healthResult, nil
}

func (f *prefixStubBackend) LoadModel(_ context.Context, _ *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

func (f *prefixStubBackend) IsBusy() bool { return false }

// prefixStubClientFactory hands the same fake backend to every NewClient call,
// so the SmartRouter never opens a real gRPC connection during routing.
type prefixStubClientFactory struct {
	client *prefixStubBackend
}

func (f *prefixStubClientFactory) NewClient(_ string, _ bool) grpcPkg.Backend {
	return f.client
}

var _ = Describe("Prefix-cache aware routing", Label("Distributed"), func() {
	const model = "model"

	var (
		infra    *TestInfra
		db       *gormDB.DB
		registry *nodes.NodeRegistry
		router   *nodes.SmartRouter
		idx      *prefixcache.Index

		nodeXID string
		nodeYID string

		chainA         = []uint64{1, 2, 3, 4, 5} // conversation A
		chainShared    = []uint64{1, 2, 3, 9, 9} // shares leading prefix [1,2,3] with A
		chainUnrelated = []uint64{7, 8, 9}       // no shared prefix with A
	)

	// routeAndSettle drives one request through the router for the given prefix
	// chain and immediately settles the in-flight reservation the way a real
	// inference completion would (Release closes the client; the DecrementInFlight
	// emulates the OnFirstComplete callback that fires after the first inference).
	// Settling keeps both nodes balanced at in_flight=0 so the prefix-cache load
	// guard never falsely forces a request off its warm node between steps.
	routeAndSettle := func(chain []uint64) string {
		GinkgoHelper()
		ctx := distributedhdr.WithPrefixChain(context.Background(), chain)
		result, err := router.Route(ctx, model, model, "llama-cpp",
			&pb.ModelOptions{ModelFile: model}, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())
		Expect(result.Node).ToNot(BeNil())
		nodeID := result.Node.ID
		result.Release()
		Expect(registry.DecrementInFlight(context.Background(), nodeID, model, 0)).To(Succeed())
		return nodeID
	}

	BeforeEach(func() {
		infra = SetupInfra("localai_prefix_cache_routing_test")

		var err error
		db, err = gormDB.Open(pgdriver.Open(infra.PGURL), &gormDB.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		// The prefix-cache index is the real radix-tree provider. Keep a handle so
		// the specs can assert Decide() directly in addition to observing Route().
		idx = prefixcache.NewIndex(prefixcache.DefaultConfig())

		// Wire the registry chokepoint hook ourselves. In production distributed.go
		// wires this; a bare SmartRouter test must register it so removal-path
		// invalidation is exercised end to end. A negative replica index means
		// "all replicas of the node" (InvalidateNode); otherwise drop the exact
		// replica.
		registry.SetReplicaRemovedHook(func(modelName, nodeID string, replica int) {
			if replica < 0 {
				idx.InvalidateNode(modelName, nodeID)
			} else {
				idx.Invalidate(modelName, prefixcache.ReplicaKey{NodeID: nodeID, Replica: replica})
			}
		})

		// Register TWO healthy nodes and mark the model loaded on both (replica 0).
		nodeX := &nodes.BackendNode{Name: "node-x", Address: "127.0.0.1:50051"}
		nodeY := &nodes.BackendNode{Name: "node-y", Address: "127.0.0.1:50052"}
		Expect(registry.Register(context.Background(), nodeX, true)).To(Succeed())
		Expect(registry.Register(context.Background(), nodeY, true)).To(Succeed())
		nodeXID = nodeX.ID
		nodeYID = nodeY.ID
		Expect(registry.SetNodeModel(context.Background(), nodeXID, model, 0, "loaded", "", 0)).To(Succeed())
		Expect(registry.SetNodeModel(context.Background(), nodeYID, model, 0, "loaded", "", 0)).To(Succeed())

		factory := &prefixStubClientFactory{client: &prefixStubBackend{healthResult: true}}
		router = nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{
			ClientFactory:  factory,
			PrefixProvider: idx,
			PrefixConfig:   prefixcache.DefaultConfig(),
			DB:             db,
		})
	})

	It("locks affinity, honors shared prefixes, isolates unrelated chains, and re-homes on failover", func() {
		now := time.Now()
		// Both nodes host replica 0 of the model.
		keys := []prefixcache.ReplicaKey{{NodeID: nodeXID, Replica: 0}, {NodeID: nodeYID, Replica: 0}}

		// --- Step 1: cold miss + observe -------------------------------------
		// chainA's prefix has never been seen, so there is no hot match yet; the
		// request cold-places on some loaded node X and the assignment is recorded.
		Expect(idx.Decide(model, chainA, keys, now).HasHot).To(BeFalse(),
			"step 1: chainA must be a cold miss (no prior affinity)")
		placedNode := routeAndSettle(chainA)
		Expect(placedNode).To(Or(Equal(nodeXID), Equal(nodeYID)))
		// From here on, "X" is whichever node served chainA first.
		nodeX := placedNode
		var nodeY string
		if nodeX == nodeXID {
			nodeY = nodeYID
		} else {
			nodeY = nodeXID
		}
		hotX := prefixcache.ReplicaKey{NodeID: nodeX, Replica: 0}
		Expect(idx.Decide(model, chainA, keys, time.Now()).Hot).To(Equal(hotX),
			"step 1: chainA must now be recorded against the replica that served it")

		// --- Step 2: hot-match affinity --------------------------------------
		// The SAME chain routes back to X.
		Expect(routeAndSettle(chainA)).To(Equal(nodeX),
			"step 2: a repeat of chainA must return to its warm node X")

		// --- Step 3: shared-prefix match (the regression we fixed) -----------
		// A DIFFERENT chain that shares the leading prefix [1,2,3] with X's chain
		// but diverges at the tail still matches the shared head and routes to X.
		// Before the radix-tree fix this fell through to a cold placement.
		Expect(idx.Decide(model, chainShared, keys, time.Now()).Hot).To(Equal(hotX),
			"step 3: chainShared must hot-match X on the shared prefix")
		Expect(routeAndSettle(chainShared)).To(Equal(nodeX),
			"step 3: chainShared must route to X via the shared-prefix match")

		// --- Step 4: negative control ----------------------------------------
		// A completely unrelated chain shares no prefix with X's chain, so it must
		// NOT hot-match X's affinity. (Cold placement may still pick X or Y by
		// load/cacheWeight, but it must not be a false hot match.) Asserting the
		// provider decision directly is the robust check.
		Expect(idx.Decide(model, chainUnrelated, keys, time.Now()).HasHot).To(BeFalse(),
			"step 4: chainUnrelated must be a cold miss, not a false hot match on X")

		// --- Step 5: failover + invalidation ---------------------------------
		// Remove node X's replica of the model. This fires the registry chokepoint
		// hook, which invalidates the prefix-cache entry for X. A request for X's
		// chain must then fail over to the surviving node Y, and the prefix entry
		// must no longer pin to X (it re-homes to Y on the next observe).
		Expect(registry.RemoveAllNodeModelReplicas(context.Background(), nodeX, model)).To(Succeed())

		yKeys := []prefixcache.ReplicaKey{{NodeID: nodeY, Replica: 0}}
		// The chokepoint hook dropped X from the index immediately.
		Expect(idx.Decide(model, chainA, yKeys, time.Now()).Hot).ToNot(Equal(hotX),
			"step 5: after X's replica is removed, chainA must no longer pin to X")

		// Route(chainA): only Y still hosts the model, so it fails over to Y.
		Expect(routeAndSettle(chainA)).To(Equal(nodeY),
			"step 5: chainA must fail over to the surviving node Y")

		// And the entry has re-homed: chainA now hot-matches Y, never X.
		reHomed := idx.Decide(model, chainA, yKeys, time.Now())
		hotY := prefixcache.ReplicaKey{NodeID: nodeY, Replica: 0}
		Expect(reHomed.Hot).ToNot(Equal(hotX),
			"step 5: chainA must not re-home to the removed node X")
		Expect(reHomed.Hot).To(Equal(hotY),
			"step 5: chainA must re-home to the surviving node Y")
	})

	It("tracks affinity per replica when ONE node hosts TWO replicas of the model", func() {
		// This is the bug the replica-granular change fixes: two replicas of the
		// same model on the SAME node are distinct KV caches. A prefix observed
		// on replica (node,0) must NOT be reported as hot on the sibling replica
		// (node,1) of the same node.
		const multiNodeModel = "multi-replica-model"
		multiNode := &nodes.BackendNode{Name: "node-multi", Address: "127.0.0.1:50060", MaxReplicasPerModel: 2}
		Expect(registry.Register(context.Background(), multiNode, true)).To(Succeed())
		Expect(registry.SetNodeModel(context.Background(), multiNode.ID, multiNodeModel, 0, "loaded", "addr0", 0)).To(Succeed())
		Expect(registry.SetNodeModel(context.Background(), multiNode.ID, multiNodeModel, 1, "loaded", "addr1", 0)).To(Succeed())

		chain := []uint64{42, 43, 44}
		key0 := prefixcache.ReplicaKey{NodeID: multiNode.ID, Replica: 0}
		key1 := prefixcache.ReplicaKey{NodeID: multiNode.ID, Replica: 1}

		// Observe the chain on replica 0 only.
		idx.Observe(multiNodeModel, chain, key0, time.Now())

		d := idx.Decide(multiNodeModel, chain, []prefixcache.ReplicaKey{key0, key1}, time.Now())
		Expect(d.HasHot).To(BeTrue())
		Expect(d.Hot).To(Equal(key0),
			"the prefix was served by replica 0; the SAME-node sibling replica 1 must NOT be chosen")
	})
})
