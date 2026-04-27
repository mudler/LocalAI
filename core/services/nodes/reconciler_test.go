package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Fake ModelScheduler
// ---------------------------------------------------------------------------

type fakeScheduler struct {
	scheduleNode  *BackendNode
	scheduleErr   error
	scheduleCalls []scheduleCall
}

type scheduleCall struct {
	modelName    string
	candidateIDs []string
}

func (f *fakeScheduler) ScheduleAndLoadModel(_ context.Context, modelName string, candidateNodeIDs []string) (*BackendNode, error) {
	f.scheduleCalls = append(f.scheduleCalls, scheduleCall{modelName, candidateNodeIDs})
	return f.scheduleNode, f.scheduleErr
}

var _ = Describe("ReplicaReconciler", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	// Helper to register a healthy node with enough replica capacity for
	// most tests. Pre-PR4 the reconciler ignored capacity, so existing
	// fixtures didn't bother setting MaxReplicasPerModel — bumping the
	// default here keeps the test intent ("scale up enough") working under
	// the new capacity-aware logic. Tests that specifically exercise the
	// circuit breaker should register nodes with a tighter cap.
	registerNode := func(name, address string) *BackendNode {
		node := &BackendNode{
			Name:                name,
			NodeType:            NodeTypeBackend,
			Address:             address,
			MaxReplicasPerModel: 4,
		}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
		return node
	}

	// Helper to set up a scheduling config.
	setSchedulingConfig := func(modelName string, minReplicas, maxReplicas int, nodeSelector string) {
		cfg := &ModelSchedulingConfig{
			ModelName:    modelName,
			MinReplicas:  minReplicas,
			MaxReplicas:  maxReplicas,
			NodeSelector: nodeSelector,
		}
		Expect(registry.SetModelScheduling(context.Background(), cfg)).To(Succeed())
	}

	Context("model below min_replicas", func() {
		It("scales up to min_replicas", func() {
			node := registerNode("node-1", "10.0.0.1:50051")
			setSchedulingConfig("model-a", 2, 4, "")

			scheduler := &fakeScheduler{
				scheduleNode: node,
			}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			// No replicas loaded — should schedule 2
			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(HaveLen(2))
			Expect(scheduler.scheduleCalls[0].modelName).To(Equal("model-a"))
			Expect(scheduler.scheduleCalls[1].modelName).To(Equal("model-a"))
		})
	})

	Context("all replicas busy and below max_replicas", func() {
		It("scales up by 1", func() {
			node := registerNode("node-busy", "10.0.0.2:50051")
			setSchedulingConfig("model-b", 1, 4, "")

			// Load 2 replicas, both busy (in_flight > 0)
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-b", 0, "loaded", "addr1", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node.ID, "model-b", 0)).To(Succeed())

			node2 := registerNode("node-busy-2", "10.0.0.3:50051")
			Expect(registry.SetNodeModel(context.Background(), node2.ID, "model-b", 0, "loaded", "addr2", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node2.ID, "model-b", 0)).To(Succeed())

			scheduler := &fakeScheduler{
				scheduleNode: node,
			}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(HaveLen(1))
			Expect(scheduler.scheduleCalls[0].modelName).To(Equal("model-b"))
		})
	})

	Context("all replicas busy and at max_replicas", func() {
		It("does not scale up", func() {
			node := registerNode("node-max", "10.0.0.4:50051")
			setSchedulingConfig("model-c", 1, 2, "")

			// Load 2 replicas (at max), both busy
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-c", 0, "loaded", "addr1", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node.ID, "model-c", 0)).To(Succeed())

			node2 := registerNode("node-max-2", "10.0.0.5:50051")
			Expect(registry.SetNodeModel(context.Background(), node2.ID, "model-c", 0, "loaded", "addr2", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node2.ID, "model-c", 0)).To(Succeed())

			scheduler := &fakeScheduler{
				scheduleNode: node,
			}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(BeEmpty())
		})
	})

	Context("idle replicas above min_replicas", func() {
		It("scales down after idle delay", func() {
			node1 := registerNode("node-idle-1", "10.0.0.6:50051")
			node2 := registerNode("node-idle-2", "10.0.0.7:50051")
			node3 := registerNode("node-idle-3", "10.0.0.8:50051")
			setSchedulingConfig("model-d", 1, 4, "")

			// Load 3 replicas, all idle with last_used in the past
			pastTime := time.Now().Add(-10 * time.Minute)
			for _, n := range []*BackendNode{node1, node2, node3} {
				Expect(registry.SetNodeModel(context.Background(), n.ID, "model-d", 0, "loaded", "", 0)).To(Succeed())
				// Set last_used to past time to trigger scale-down
				db.Model(&NodeModel{}).Where("node_id = ? AND model_name = ?", n.ID, "model-d").
					Update("last_used", pastTime)
			}

			unloader := &fakeUnloader{}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:       registry,
				Unloader:       unloader,
				DB:             db,
				ScaleDownDelay: 1 * time.Minute, // short delay for test
			})

			reconciler.reconcile(context.Background())

			// Should scale down 2 replicas (3 - floor of 1)
			Expect(unloader.unloadCalls).To(HaveLen(2))
		})
	})

	Context("idle replicas at min_replicas", func() {
		It("does not scale down", func() {
			node1 := registerNode("node-keep-1", "10.0.0.9:50051")
			node2 := registerNode("node-keep-2", "10.0.0.10:50051")
			setSchedulingConfig("model-e", 2, 4, "")

			// Load exactly 2 replicas (at min), both idle with past last_used
			pastTime := time.Now().Add(-10 * time.Minute)
			for _, n := range []*BackendNode{node1, node2} {
				Expect(registry.SetNodeModel(context.Background(), n.ID, "model-e", 0, "loaded", "", 0)).To(Succeed())
				db.Model(&NodeModel{}).Where("node_id = ? AND model_name = ?", n.ID, "model-e").
					Update("last_used", pastTime)
			}

			unloader := &fakeUnloader{}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:       registry,
				Unloader:       unloader,
				DB:             db,
				ScaleDownDelay: 1 * time.Minute,
			})

			reconciler.reconcile(context.Background())

			Expect(unloader.unloadCalls).To(BeEmpty())
		})
	})

	Context("model with node_selector", func() {
		It("passes candidate node IDs to scheduler", func() {
			node1 := registerNode("gpu-node", "10.0.0.11:50051")
			node2 := registerNode("cpu-node", "10.0.0.12:50051")

			// Add labels — only node1 matches the selector
			Expect(registry.SetNodeLabel(context.Background(), node1.ID, "gpu.vendor", "nvidia")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), node2.ID, "gpu.vendor", "none")).To(Succeed())

			setSchedulingConfig("model-f", 1, 2, `{"gpu.vendor":"nvidia"}`)

			scheduler := &fakeScheduler{
				scheduleNode: node1,
			}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			// No replicas loaded — should schedule 1 with candidate node IDs
			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(HaveLen(1))
			Expect(scheduler.scheduleCalls[0].modelName).To(Equal("model-f"))
			Expect(scheduler.scheduleCalls[0].candidateIDs).To(ContainElement(node1.ID))
			Expect(scheduler.scheduleCalls[0].candidateIDs).ToNot(ContainElement(node2.ID))
		})
	})

	Describe("Capacity gating + circuit breaker (PR4)", func() {
		// Helper: register a node with an explicit per-model replica cap.
		// Tests in this Describe block want to exercise both "fits" and
		// "doesn't fit" capacity scenarios precisely.
		registerCappedNode := func(name, address string, cap int) *BackendNode {
			node := &BackendNode{
				Name:                name,
				NodeType:            NodeTypeBackend,
				Address:             address,
				MaxReplicasPerModel: cap,
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			return node
		}

		It("caps scale-up at cluster capacity instead of looping forever", func() {
			// 1 node × 1 slot = capacity 1, but MinReplicas=2.
			// Pre-PR4 this looped: every 30s "scaling up to meet minimum"
			// because the registry never grew to 2. Post-PR4 the reconciler
			// does the math up front and only schedules 1 (the achievable
			// target), then flags unsatisfiable on the next ticks.
			node := registerCappedNode("cap-1-slot", "10.0.0.40:50051", 1)
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{
				ModelName:   "tight-model",
				MinReplicas: 2,
			})).To(Succeed())

			scheduler := &fakeScheduler{scheduleNode: node}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(HaveLen(1),
				"only 1 schedule call: capacity is 1, not the requested 2 — must not loop")
		})

		It("flags unsatisfiable after threshold consecutive ticks at capacity 0", func() {
			// 1 node × 1 slot, already loaded. Capacity=0, but MinReplicas=2.
			// Each tick increments UnsatisfiableTicks; once we cross the
			// threshold the cooldown timestamp is set and further ticks
			// short-circuit (the scheduler is no longer called).
			node := registerCappedNode("cb-node", "10.0.0.41:50051", 1)
			Expect(registry.SetNodeModel(context.Background(), node.ID, "cb-model", 0, "loaded", "addr1", 0)).To(Succeed())
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{
				ModelName:   "cb-model",
				MinReplicas: 2,
			})).To(Succeed())

			scheduler := &fakeScheduler{scheduleNode: node}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			// Drive enough ticks to cross the threshold, plus a couple more
			// to confirm the cooldown holds.
			for i := 0; i < unsatisfiableTickThreshold+2; i++ {
				reconciler.reconcile(context.Background())
			}

			cfg, err := registry.GetModelScheduling(context.Background(), "cb-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.UnsatisfiableUntil).ToNot(BeNil(),
				"reconciler must flag the config after threshold ticks of capacity exhaustion")
			Expect(cfg.UnsatisfiableUntil.After(time.Now())).To(BeTrue(),
				"cooldown must point to the future")
			// Capacity 0 + cooldown active means the scheduler shouldn't have
			// been invoked at all — capacity was 0 from the first tick.
			Expect(scheduler.scheduleCalls).To(BeEmpty(),
				"capacity was always 0 — no schedule attempts should have been made")
		})

		It("clears unsatisfiable on a successful scale-up", func() {
			// Pre-flag the config (simulate a prior unsatisfiable run), then
			// register enough capacity and tick — the reconciler must clear
			// the flag and proceed.
			node := registerCappedNode("clear-node", "10.0.0.42:50051", 4)
			until := time.Now().Add(-1 * time.Second) // already-expired cooldown
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{
				ModelName:          "clear-model",
				MinReplicas:        1,
				UnsatisfiableTicks: 5,
				UnsatisfiableUntil: &until,
			})).To(Succeed())

			scheduler := &fakeScheduler{scheduleNode: node}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(HaveLen(1),
				"expired cooldown should not block scheduling")

			cfg, err := registry.GetModelScheduling(context.Background(), "clear-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.UnsatisfiableUntil).To(BeNil(), "successful scale-up must clear the cooldown")
			Expect(cfg.UnsatisfiableTicks).To(Equal(0), "successful scale-up must reset the counter")
		})

		It("recovers when a new node joins (ClearAllUnsatisfiable on Register)", func() {
			// One full node, then config flagged unsatisfiable. Adding a
			// second node simulates the user's recovery question: capacity
			// returns, cooldown clears, the next tick schedules.
			node1 := registerCappedNode("rec-node-1", "10.0.0.43:50051", 1)
			Expect(registry.SetNodeModel(context.Background(), node1.ID, "rec-model", 0, "loaded", "addr1", 0)).To(Succeed())

			until := time.Now().Add(unsatisfiableCooldown)
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{
				ModelName:          "rec-model",
				MinReplicas:        2,
				UnsatisfiableTicks: unsatisfiableTickThreshold,
				UnsatisfiableUntil: &until,
			})).To(Succeed())

			// New node registers — this is the recovery event.
			registerCappedNode("rec-node-2", "10.0.0.44:50051", 1)

			cfg, err := registry.GetModelScheduling(context.Background(), "rec-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.UnsatisfiableUntil).To(BeNil(),
				"Register must clear unsatisfiable flags so the reconciler retries")
			Expect(cfg.UnsatisfiableTicks).To(Equal(0))
		})

		It("recovers when node labels change (ClearAllUnsatisfiable on label ops)", func() {
			node := registerCappedNode("lbl-node", "10.0.0.45:50051", 1)
			until := time.Now().Add(unsatisfiableCooldown)
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{
				ModelName:          "lbl-model",
				MinReplicas:        2,
				UnsatisfiableTicks: unsatisfiableTickThreshold,
				UnsatisfiableUntil: &until,
			})).To(Succeed())

			// Adding a label could change which models the node matches via
			// a NodeSelector, so capacity for some config may have just
			// changed. ClearAllUnsatisfiable lets the reconciler re-check.
			Expect(registry.SetNodeLabel(context.Background(), node.ID, "tier", "fast")).To(Succeed())

			cfg, err := registry.GetModelScheduling(context.Background(), "lbl-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.UnsatisfiableUntil).To(BeNil())
		})
	})

	Describe("ClusterCapacityForModel", func() {
		It("sums (max_replicas_per_model - replicas[node, model]) over candidates", func() {
			// Three nodes with caps 4, 2, 1. Loaded counts: 1, 0, 1 → free
			// slots: 3, 2, 0 → total capacity 5.
			a := &BackendNode{Name: "cap-a", NodeType: NodeTypeBackend, Address: "10.0.0.50:50051", MaxReplicasPerModel: 4}
			b := &BackendNode{Name: "cap-b", NodeType: NodeTypeBackend, Address: "10.0.0.51:50051", MaxReplicasPerModel: 2}
			c := &BackendNode{Name: "cap-c", NodeType: NodeTypeBackend, Address: "10.0.0.52:50051", MaxReplicasPerModel: 1}
			Expect(registry.Register(context.Background(), a, true)).To(Succeed())
			Expect(registry.Register(context.Background(), b, true)).To(Succeed())
			Expect(registry.Register(context.Background(), c, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), a.ID, "cap-model", 0, "loaded", "x", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), c.ID, "cap-model", 0, "loaded", "y", 0)).To(Succeed())

			cap, err := registry.ClusterCapacityForModel(context.Background(), "cap-model", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(cap).To(Equal(5))

			// Restricting to {b, c}: b free=2, c free=0 → capacity 2.
			cap, err = registry.ClusterCapacityForModel(context.Background(), "cap-model", []string{b.ID, c.ID})
			Expect(err).ToNot(HaveOccurred())
			Expect(cap).To(Equal(2))
		})
	})
})

// fakeProber lets tests control whether a model's gRPC address "responds".
type fakeProber struct {
	alive map[string]bool
	calls int
}

func (f *fakeProber) IsAlive(_ context.Context, address string) bool {
	f.calls++
	if f.alive == nil {
		return false
	}
	return f.alive[address]
}

var _ = Describe("ReplicaReconciler — state reconciliation", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("probeLoadedModels", func() {
		It("removes loaded models whose gRPC address is unreachable", func() {
			node := &BackendNode{Name: "n1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			// Two loaded models — one stale (will probe), one fresh (skipped).
			stale := &NodeModel{
				ID:        "stale-1",
				NodeID:    node.ID,
				ModelName: "stale-model",
				Address:   "10.0.0.1:12345",
				State:     "loaded",
				UpdatedAt: time.Now().Add(-5 * time.Minute),
			}
			fresh := &NodeModel{
				ID:        "fresh-1",
				NodeID:    node.ID,
				ModelName: "fresh-model",
				Address:   "10.0.0.1:54321",
				State:     "loaded",
				UpdatedAt: time.Now(), // within probeStaleAfter
			}
			Expect(db.Create(stale).Error).To(Succeed())
			Expect(db.Create(fresh).Error).To(Succeed())

			prober := &fakeProber{alive: map[string]bool{"10.0.0.1:12345": false}}
			rc := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:        registry,
				DB:              db,
				Prober:          prober,
				ProbeStaleAfter: 2 * time.Minute,
			})

			rc.probeLoadedModels(context.Background())

			// Stale was unreachable — row removed.
			var after []NodeModel
			Expect(db.Find(&after).Error).To(Succeed())
			Expect(after).To(HaveLen(1))
			Expect(after[0].ModelName).To(Equal("fresh-model"))
			// Prober was only called once (the fresh row was filtered out).
			Expect(prober.calls).To(Equal(1))
		})

		It("keeps reachable models and bumps their updated_at", func() {
			node := &BackendNode{Name: "n1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			stale := &NodeModel{
				ID:        "stale-2",
				NodeID:    node.ID,
				ModelName: "alive-model",
				Address:   "10.0.0.1:12345",
				State:     "loaded",
				UpdatedAt: time.Now().Add(-5 * time.Minute),
			}
			Expect(db.Create(stale).Error).To(Succeed())

			prober := &fakeProber{alive: map[string]bool{"10.0.0.1:12345": true}}
			rc := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:        registry,
				DB:              db,
				Prober:          prober,
				ProbeStaleAfter: 2 * time.Minute,
			})

			rc.probeLoadedModels(context.Background())

			var after NodeModel
			Expect(db.First(&after, "id = ?", "stale-2").Error).To(Succeed())
			Expect(after.UpdatedAt).To(BeTemporally("~", time.Now(), time.Second))
		})
	})

	Describe("UpsertPendingBackendOp + RecordPendingBackendOpFailure", func() {
		It("upserts on the composite key rather than duplicating rows", func() {
			node := &BackendNode{Name: "n1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.UpsertPendingBackendOp(context.Background(), node.ID, "foo", OpBackendDelete, nil)).To(Succeed())
			// Second call for the same (node, backend, op) should not create a
			// new row — that's how re-issuing a delete works.
			Expect(registry.UpsertPendingBackendOp(context.Background(), node.ID, "foo", OpBackendDelete, nil)).To(Succeed())

			var rows []PendingBackendOp
			Expect(db.Find(&rows).Error).To(Succeed())
			Expect(rows).To(HaveLen(1))
		})

		It("increments attempts and moves next_retry_at out on failure", func() {
			node := &BackendNode{Name: "n1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.UpsertPendingBackendOp(context.Background(), node.ID, "foo", OpBackendDelete, nil)).To(Succeed())

			var row PendingBackendOp
			Expect(db.First(&row).Error).To(Succeed())
			before := row.NextRetryAt

			Expect(registry.RecordPendingBackendOpFailure(context.Background(), row.ID, "boom")).To(Succeed())
			Expect(db.First(&row, row.ID).Error).To(Succeed())
			Expect(row.Attempts).To(Equal(1))
			Expect(row.LastError).To(Equal("boom"))
			Expect(row.NextRetryAt).To(BeTemporally(">", before))
		})
	})

	Describe("NewNodeRegistry malformed-row pruning", func() {
		It("drops queue rows for agent nodes and non-existent nodes on startup", func() {
			agent := &BackendNode{Name: "agent-1", NodeType: NodeTypeAgent, Address: "x"}
			Expect(registry.Register(context.Background(), agent, true)).To(Succeed())
			backend := &BackendNode{Name: "backend-1", NodeType: NodeTypeBackend, Address: "y"}
			Expect(registry.Register(context.Background(), backend, true)).To(Succeed())

			// Three rows: one for a valid backend node (should survive),
			// one for an agent node (pruned), one for an empty backend name
			// on the valid node (pruned).
			Expect(registry.UpsertPendingBackendOp(context.Background(), backend.ID, "foo", OpBackendInstall, nil)).To(Succeed())
			Expect(registry.UpsertPendingBackendOp(context.Background(), agent.ID, "foo", OpBackendInstall, nil)).To(Succeed())
			Expect(registry.UpsertPendingBackendOp(context.Background(), backend.ID, "", OpBackendInstall, nil)).To(Succeed())

			// Re-instantiating the registry runs the cleanup migration.
			_, err := NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())

			var rows []PendingBackendOp
			Expect(db.Find(&rows).Error).To(Succeed())
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].NodeID).To(Equal(backend.ID))
			Expect(rows[0].Backend).To(Equal("foo"))
		})
	})
})
