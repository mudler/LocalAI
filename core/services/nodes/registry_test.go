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

var _ = Describe("NodeRegistry", func() {
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

	// Helper to build a minimal BackendNode.
	makeNode := func(name, address string, vram uint64) *BackendNode {
		return &BackendNode{
			Name:          name,
			NodeType:      NodeTypeBackend,
			Address:       address,
			TotalVRAM:     vram,
			AvailableVRAM: vram,
		}
	}

	Describe("Register", func() {
		It("sets StatusPending when autoApprove is false", func() {
			node := makeNode("worker-1", "10.0.0.1:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, false)).To(Succeed())
			Expect(node.Status).To(Equal(StatusPending))

			fetched, err := registry.GetByName(context.Background(), "worker-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusPending))
		})

		It("sets StatusHealthy when autoApprove is true", func() {
			node := makeNode("worker-2", "10.0.0.2:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))
		})
	})

	Describe("Re-registration", func() {
		It("keeps a pending node pending on re-register with autoApprove=false", func() {
			node := makeNode("re-pending", "10.0.0.3:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, false)).To(Succeed())
			Expect(node.Status).To(Equal(StatusPending))

			// Re-register same name, still no auto-approve
			node2 := makeNode("re-pending", "10.0.0.3:50052", 4_000_000_000)
			Expect(registry.Register(context.Background(), node2, false)).To(Succeed())
			Expect(node2.Status).To(Equal(StatusPending))

			// ID is preserved from original registration
			Expect(node2.ID).To(Equal(node.ID))
		})

		It("restores a previously approved node to healthy on re-register with autoApprove=false", func() {
			node := makeNode("re-approved", "10.0.0.4:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))

			// Simulate the node becoming unhealthy
			Expect(registry.MarkUnhealthy(context.Background(), node.ID)).To(Succeed())
			fetched, err := registry.GetByName(context.Background(), "re-approved")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusUnhealthy))

			// Re-register with autoApprove=false — should restore to healthy
			// because the node was previously approved (status != pending)
			node2 := makeNode("re-approved", "10.0.0.4:50052", 8_000_000_000)
			Expect(registry.Register(context.Background(), node2, false)).To(Succeed())
			Expect(node2.Status).To(Equal(StatusHealthy))
		})
	})

	Describe("ApproveNode", func() {
		It("transitions a pending node to healthy", func() {
			node := makeNode("approve-me", "10.0.0.5:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, false)).To(Succeed())
			Expect(node.Status).To(Equal(StatusPending))

			Expect(registry.ApproveNode(context.Background(), node.ID)).To(Succeed())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})

		It("returns error for non-existent node ID", func() {
			err := registry.ApproveNode(context.Background(), "non-existent-id")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found or not in pending status"))
		})

		It("returns error for an already-healthy node", func() {
			node := makeNode("already-healthy", "10.0.0.6:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			err := registry.ApproveNode(context.Background(), node.ID)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found or not in pending status"))
		})
	})

	Describe("MarkOffline", func() {
		It("sets status to offline and clears model records", func() {
			node := makeNode("offline-test", "10.0.0.7:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Load a model on the node
			Expect(registry.SetNodeModel(context.Background(), node.ID, "llama-7b", 0, "loaded", "10.0.0.7:50052", 0)).To(Succeed())
			models, err := registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(1))

			// Mark offline
			Expect(registry.MarkOffline(context.Background(), node.ID)).To(Succeed())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusOffline))

			// Model records should be cleared
			models, err = registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})

		It("returns error for non-existent node", func() {
			err := registry.MarkOffline(context.Background(), "does-not-exist")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Describe("SetNodeModel ID stability", func() {
		It("preserves the ID when called twice for the same node+model", func() {
			node := makeNode("stable-id-node", "10.0.0.99:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), node.ID, "my-model", 0, "loaded", "10.0.0.99:50052", 0)).To(Succeed())
			nm1, err := registry.GetNodeModel(context.Background(), node.ID, "my-model", 0)
			Expect(err).ToNot(HaveOccurred())

			// Call again with different state/address
			Expect(registry.SetNodeModel(context.Background(), node.ID, "my-model", 0, "loaded", "10.0.0.99:50053", 0)).To(Succeed())
			nm2, err := registry.GetNodeModel(context.Background(), node.ID, "my-model", 0)
			Expect(err).ToNot(HaveOccurred())

			Expect(nm2.ID).To(Equal(nm1.ID), "ID should remain stable across SetNodeModel calls")
			Expect(nm2.Address).To(Equal("10.0.0.99:50053"), "Address should be updated")
		})
	})

	Describe("FindNodeWithVRAM", func() {
		It("selects the node with sufficient VRAM", func() {
			small := makeNode("small-gpu", "10.0.0.10:50051", 4_000_000_000)
			big := makeNode("big-gpu", "10.0.0.11:50051", 16_000_000_000)
			Expect(registry.Register(context.Background(), small, true)).To(Succeed())
			Expect(registry.Register(context.Background(), big, true)).To(Succeed())

			// Request 8 GB — only big-gpu qualifies
			found, err := registry.FindNodeWithVRAM(context.Background(), 8_000_000_000)
			Expect(err).ToNot(HaveOccurred())
			Expect(found.Name).To(Equal("big-gpu"))
		})

		It("returns error when no node has enough VRAM", func() {
			small := makeNode("tiny-gpu", "10.0.0.12:50051", 2_000_000_000)
			Expect(registry.Register(context.Background(), small, true)).To(Succeed())

			_, err := registry.FindNodeWithVRAM(context.Background(), 32_000_000_000)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("FindIdleNode", func() {
		It("returns the node with no loaded models", func() {
			busy := makeNode("busy-node", "10.0.0.20:50051", 8_000_000_000)
			idle := makeNode("idle-node", "10.0.0.21:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), busy, true)).To(Succeed())
			Expect(registry.Register(context.Background(), idle, true)).To(Succeed())

			// Load a model on the busy node
			Expect(registry.SetNodeModel(context.Background(), busy.ID, "model-a", 0, "loaded", "", 0)).To(Succeed())

			found, err := registry.FindIdleNode(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(found.Name).To(Equal("idle-node"))
		})

		It("returns error when all nodes have models loaded", func() {
			n := makeNode("all-busy", "10.0.0.22:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n.ID, "model-x", 0, "loaded", "", 0)).To(Succeed())

			_, err := registry.FindIdleNode(context.Background())
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("FindLeastLoadedNode", func() {
		It("returns the node with fewer in-flight requests", func() {
			heavy := makeNode("heavy-node", "10.0.0.30:50051", 8_000_000_000)
			light := makeNode("light-node", "10.0.0.31:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), heavy, true)).To(Succeed())
			Expect(registry.Register(context.Background(), light, true)).To(Succeed())

			// Set up models with different in-flight counts
			Expect(registry.SetNodeModel(context.Background(), heavy.ID, "model-a", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), heavy.ID, "model-a", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), heavy.ID, "model-a", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), heavy.ID, "model-a", 0)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), light.ID, "model-b", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), light.ID, "model-b", 0)).To(Succeed())

			found, err := registry.FindLeastLoadedNode(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(found.Name).To(Equal("light-node"))
		})
	})

	Describe("FindAndLockNodeWithModel", func() {
		It("returns the correct node and increments in-flight", func() {
			node := makeNode("lock-node", "10.0.0.40:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "my-model", 0, "loaded", "10.0.0.40:50052", 0)).To(Succeed())

			foundNode, foundNM, err := registry.FindAndLockNodeWithModel(context.Background(), "my-model", nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundNode.ID).To(Equal(node.ID))
			Expect(foundNM.ModelName).To(Equal("my-model"))

			// Verify in-flight was incremented
			nm, err := registry.GetNodeModel(context.Background(), node.ID, "my-model", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(1))
		})

		It("returns error when model is not loaded anywhere", func() {
			_, _, err := registry.FindAndLockNodeWithModel(context.Background(), "nonexistent-model", nil, nil)
			Expect(err).To(HaveOccurred())
		})

		It("selects the node with fewer in-flight when multiple exist", func() {
			n1 := makeNode("lock-heavy", "10.0.0.41:50051", 8_000_000_000)
			n2 := makeNode("lock-light", "10.0.0.42:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), n1.ID, "shared-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n2.ID, "shared-model", 0, "loaded", "", 0)).To(Succeed())

			// Add in-flight to n1
			Expect(registry.IncrementInFlight(context.Background(), n1.ID, "shared-model", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), n1.ID, "shared-model", 0)).To(Succeed())

			foundNode, _, err := registry.FindAndLockNodeWithModel(context.Background(), "shared-model", nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundNode.Name).To(Equal("lock-light"))
		})

		It("filters by candidateNodeIDs even when an excluded node has lower in_flight", func() {
			// Reproduces the selector-mismatch loop: a model loaded on a node
			// the selector now excludes (excluded) and on a node it includes
			// (included). Without the filter the excluded node wins on
			// in_flight ASC; with the filter the included node is returned
			// directly so Route() can serve from its existing replica.
			excluded := makeNode("excluded-node", "10.0.0.43:50051", 8_000_000_000)
			included := makeNode("included-node", "10.0.0.44:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), excluded, true)).To(Succeed())
			Expect(registry.Register(context.Background(), included, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), excluded.ID, "filtered-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), included.ID, "filtered-model", 0, "loaded", "", 0)).To(Succeed())

			// Make `included` strictly busier than `excluded` so the unfiltered
			// query would prefer the excluded one — proving the filter is
			// what's steering the result, not the in_flight ordering.
			Expect(registry.IncrementInFlight(context.Background(), included.ID, "filtered-model", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), included.ID, "filtered-model", 0)).To(Succeed())

			foundNode, foundNM, err := registry.FindAndLockNodeWithModel(context.Background(), "filtered-model", []string{included.ID}, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundNode.ID).To(Equal(included.ID))
			Expect(foundNM.NodeID).To(Equal(included.ID))
		})

		It("round-robins between replicas when in_flight ties (last_used tiebreaker)", func() {
			// Three replicas of the same model on three nodes, all with in_flight=0.
			// Without the last_used tiebreaker, the node with the largest available_vram
			// would win every pick and one node would take ~all the load. With it,
			// each successful pick refreshes last_used so the next pick rotates to
			// the oldest-used replica.
			fat := makeNode("rr-fat", "10.0.0.50:50051", 24_000_000_000)
			mid := makeNode("rr-mid", "10.0.0.51:50051", 16_000_000_000)
			small := makeNode("rr-small", "10.0.0.52:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), fat, true)).To(Succeed())
			Expect(registry.Register(context.Background(), mid, true)).To(Succeed())
			Expect(registry.Register(context.Background(), small, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), fat.ID, "rr-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), mid.ID, "rr-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), small.ID, "rr-model", 0, "loaded", "", 0)).To(Succeed())

			// Decrement back to 0 after each pick so the next call sees a tie.
			// (FindAndLockNodeWithModel atomically increments to lock the row.)
			picks := make([]string, 0, 9)
			for i := 0; i < 9; i++ {
				n, nm, err := registry.FindAndLockNodeWithModel(context.Background(), "rr-model", nil, nil)
				Expect(err).ToNot(HaveOccurred())
				picks = append(picks, n.Name)
				Expect(registry.DecrementInFlight(context.Background(), n.ID, "rr-model", nm.ReplicaIndex)).To(Succeed())
			}

			// Each replica should have been picked at least twice across 9 ties —
			// proves we're rotating, not pinning to the largest-VRAM node.
			counts := map[string]int{}
			for _, p := range picks {
				counts[p]++
			}
			Expect(counts["rr-fat"]).To(BeNumerically(">=", 2), "fat node was picked %d times across 9 ties: %v", counts["rr-fat"], picks)
			Expect(counts["rr-mid"]).To(BeNumerically(">=", 2), "mid node was picked %d times across 9 ties: %v", counts["rr-mid"], picks)
			Expect(counts["rr-small"]).To(BeNumerically(">=", 2), "small node was picked %d times across 9 ties: %v", counts["rr-small"], picks)
		})

		It("returns not-found when the model is loaded only on excluded nodes", func() {
			loadedExcluded := makeNode("excl-only-node", "10.0.0.45:50051", 8_000_000_000)
			emptyIncluded := makeNode("empty-included-node", "10.0.0.46:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), loadedExcluded, true)).To(Succeed())
			Expect(registry.Register(context.Background(), emptyIncluded, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), loadedExcluded.ID, "no-match-model", 0, "loaded", "", 0)).To(Succeed())

			// Filter restricts to a node that does not have the model — the
			// query must return an error so Route() falls through to schedule
			// a fresh load on a matching node instead of reusing the excluded
			// replica.
			_, _, err := registry.FindAndLockNodeWithModel(context.Background(), "no-match-model", []string{emptyIncluded.ID}, nil)
			Expect(err).To(HaveOccurred())
		})

		It("agrees with PickBestReplica on a seeded dataset (policy mirror)", func() {
			// Guard against drift between the SQL ORDER BY in
			// FindAndLockNodeWithModel and the canonical Go implementation in
			// PickBestReplica. The two layers will eventually diverge in
			// caller (DB-backed atomic pick vs in-memory snapshot pick for the
			// per-frontend rotating cache), but the policy itself must stay
			// the single source of truth. If this test fails, update *both*
			// sides — never just one.
			//
			// Scenario exercises all three tiers:
			//   - "loser-busy" has the most VRAM but in_flight=2 — loses tier 1.
			//   - "loser-recent" ties at in_flight=0 but its last_used is the
			//     newest of the in_flight=0 group — loses tier 2.
			//   - "winner-mid" and "winner-fat" both tie at in_flight=0 and
			//     share the oldest last_used — tier 3 decides: fattest wins.
			loserBusy := makeNode("mirror-loser-busy", "10.0.0.70:50051", 32_000_000_000)
			loserRecent := makeNode("mirror-loser-recent", "10.0.0.71:50051", 8_000_000_000)
			winnerMid := makeNode("mirror-winner-mid", "10.0.0.72:50051", 16_000_000_000)
			winnerFat := makeNode("mirror-winner-fat", "10.0.0.73:50051", 24_000_000_000)
			for _, n := range []*BackendNode{loserBusy, loserRecent, winnerMid, winnerFat} {
				Expect(registry.Register(context.Background(), n, true)).To(Succeed())
				Expect(registry.SetNodeModel(context.Background(), n.ID, "mirror-model", 0, "loaded", "", 0)).To(Succeed())
			}

			// Force in_flight=2 on the "busy" node so tier 1 disqualifies it.
			Expect(registry.IncrementInFlight(context.Background(), loserBusy.ID, "mirror-model", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), loserBusy.ID, "mirror-model", 0)).To(Succeed())

			// Slam last_used to known values so the test is deterministic
			// regardless of clock resolution between the helpers above.
			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			set := func(id string, t time.Time) {
				Expect(db.Model(&NodeModel{}).
					Where("node_id = ? AND model_name = ?", id, "mirror-model").
					Update("last_used", t).Error).To(Succeed())
			}
			set(loserBusy.ID, base) // newest doesn't matter — already disqualified by tier 1
			set(loserRecent.ID, base.Add(time.Hour))
			set(winnerMid.ID, base)
			set(winnerFat.ID, base)

			// Pull the same dataset both pickers will operate on. The Go
			// picker is a faithful representation of the policy; the SQL is
			// the production path.
			var rows []NodeModel
			Expect(db.Where("model_name = ? AND state = ?", "mirror-model", "loaded").
				Find(&rows).Error).To(Succeed())
			candidates := make([]ReplicaCandidate, 0, len(rows))
			for _, nm := range rows {
				var bn BackendNode
				Expect(db.First(&bn, "id = ? AND status = ?", nm.NodeID, StatusHealthy).Error).To(Succeed())
				candidates = append(candidates, ReplicaCandidate{
					NodeID:        nm.NodeID,
					Address:       bn.Address,
					ReplicaIndex:  nm.ReplicaIndex,
					InFlight:      nm.InFlight,
					LastUsed:      nm.LastUsed,
					AvailableVRAM: bn.AvailableVRAM,
				})
			}
			goPick := PickBestReplica(candidates)
			Expect(goPick).ToNot(BeNil())

			sqlNode, _, err := registry.FindAndLockNodeWithModel(context.Background(), "mirror-model", nil, nil)
			Expect(err).ToNot(HaveOccurred())

			Expect(sqlNode.ID).To(Equal(goPick.NodeID),
				"SQL ORDER BY picked %s; PickBestReplica picked %s — policy has drifted",
				sqlNode.ID, goPick.NodeID)
			// Sanity check: the policy says winner-fat wins on tier 3.
			Expect(goPick.NodeID).To(Equal(winnerFat.ID))
		})
	})

	Describe("FindAndLockNodeWithModel preference", func() {
		var nodeA, nodeB *BackendNode

		BeforeEach(func() {
			nodeA = makeNode("pref-a", "10.0.0.70:50051", 8_000_000_000)
			nodeB = makeNode("pref-b", "10.0.0.71:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), nodeA, true)).To(Succeed())
			Expect(registry.Register(context.Background(), nodeB, true)).To(Succeed())
			// Both loaded+healthy for model "pref-model", in_flight 0.
			Expect(registry.SetNodeModel(context.Background(), nodeA.ID, "pref-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), nodeB.ID, "pref-model", 0, "loaded", "", 0)).To(Succeed())
		})

		It("locks the preferred node when eligible", func() {
			node, nm, err := registry.FindAndLockNodeWithModel(context.Background(), "pref-model", nil, &RoutePreference{PreferredNodeID: nodeB.ID})
			Expect(err).ToNot(HaveOccurred())
			Expect(node.ID).To(Equal(nodeB.ID))
			Expect(nm.NodeID).To(Equal(nodeB.ID))

			// in_flight is incremented atomically via gorm.Expr, so verify the
			// persisted value through a re-fetch (the returned struct mirrors
			// the pre-increment read, like the default-pick path).
			persisted, err := registry.GetNodeModel(context.Background(), nodeB.ID, "pref-model", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(persisted.InFlight).To(Equal(1))
		})

		It("falls back to default order when preferred not loaded", func() {
			node, _, err := registry.FindAndLockNodeWithModel(context.Background(), "pref-model", nil, &RoutePreference{PreferredNodeID: "ZZZ"})
			Expect(err).ToNot(HaveOccurred())
			Expect(node.ID).To(BeElementOf(nodeA.ID, nodeB.ID))
		})

		It("nil preference behaves like before", func() {
			node, _, err := registry.FindAndLockNodeWithModel(context.Background(), "pref-model", nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(node).ToNot(BeNil())
		})

		It("locks the EXACT preferred replica when the node hosts two replicas", func() {
			// A single node hosts replica 0 and replica 1 of a model, both
			// loaded+healthy. The preference must lock the SPECIFIC replica
			// requested, not the least-loaded replica on the node.
			node := makeNode("pref-multi", "10.0.0.72:50051", 16_000_000_000)
			node.MaxReplicasPerModel = 2
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "multi-model", 0, "loaded", "addr0", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "multi-model", 1, "loaded", "addr1", 0)).To(Succeed())

			// pref={node, 1} must lock replica 1 specifically.
			gotNode, nm1, err := registry.FindAndLockNodeWithModel(context.Background(), "multi-model", nil,
				&RoutePreference{PreferredNodeID: node.ID, PreferredReplica: 1})
			Expect(err).ToNot(HaveOccurred())
			Expect(gotNode.ID).To(Equal(node.ID))
			Expect(nm1.ReplicaIndex).To(Equal(1))

			// pref={node, 0} must lock replica 0 specifically.
			_, nm0, err := registry.FindAndLockNodeWithModel(context.Background(), "multi-model", nil,
				&RoutePreference{PreferredNodeID: node.ID, PreferredReplica: 0})
			Expect(err).ToNot(HaveOccurred())
			Expect(nm0.ReplicaIndex).To(Equal(0))
		})
	})

	Describe("LoadedReplicaStats", func() {
		var n1, n2, n3 *BackendNode

		BeforeEach(func() {
			n1 = makeNode("stats-1", "10.0.0.80:50051", 8_000_000_000)
			n2 = makeNode("stats-2", "10.0.0.81:50051", 8_000_000_000)
			n3 = makeNode("stats-3", "10.0.0.82:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n3, true)).To(Succeed())
			// n1 loaded+busy, n2 loaded+idle, n3 has a different model only.
			Expect(registry.SetNodeModel(context.Background(), n1.ID, "stats-model", 0, "loaded", "10.0.0.80:6000", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n2.ID, "stats-model", 0, "loaded", "10.0.0.81:6000", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n3.ID, "other-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), n1.ID, "stats-model", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), n1.ID, "stats-model", 0)).To(Succeed())
		})

		It("returns loaded healthy replicas with in-flight counts", func() {
			stats, err := registry.LoadedReplicaStats(context.Background(), "stats-model", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(stats).To(HaveLen(2))
			byNode := map[string]ReplicaCandidate{}
			for _, s := range stats {
				byNode[s.NodeID] = s
			}
			Expect(byNode).To(HaveKey(n1.ID))
			Expect(byNode).To(HaveKey(n2.ID))
			Expect(byNode[n1.ID].InFlight).To(Equal(2))
			Expect(byNode[n2.ID].InFlight).To(Equal(0))
		})

		It("filters to the candidate node set when provided", func() {
			stats, err := registry.LoadedReplicaStats(context.Background(), "stats-model", []string{n2.ID})
			Expect(err).ToNot(HaveOccurred())
			Expect(stats).To(HaveLen(1))
			Expect(stats[0].NodeID).To(Equal(n2.ID))
		})

		It("excludes unhealthy nodes", func() {
			Expect(registry.MarkUnhealthy(context.Background(), n1.ID)).To(Succeed())
			stats, err := registry.LoadedReplicaStats(context.Background(), "stats-model", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(stats).To(HaveLen(1))
			Expect(stats[0].NodeID).To(Equal(n2.ID))
		})

		It("returns empty for a model with no loaded replicas", func() {
			stats, err := registry.LoadedReplicaStats(context.Background(), "no-such-model", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(stats).To(BeEmpty())
		})
	})

	Describe("MarkHealthy and MarkUnhealthy round-trip", func() {
		It("transitions healthy -> unhealthy -> healthy", func() {
			node := makeNode("roundtrip-node", "10.0.0.60:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))

			// Mark unhealthy
			Expect(registry.MarkUnhealthy(context.Background(), node.ID)).To(Succeed())
			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusUnhealthy))

			// Mark healthy again
			Expect(registry.MarkHealthy(context.Background(), node.ID)).To(Succeed())
			fetched, err = registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})

		It("returns error for non-existent node", func() {
			err := registry.MarkHealthy(context.Background(), "does-not-exist")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Describe("NodeLabel CRUD", func() {
		It("sets and retrieves labels for a node", func() {
			node := makeNode("label-node", "10.0.0.70:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), node.ID, "env", "prod")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), node.ID, "region", "us-east")).To(Succeed())

			labels, err := registry.GetNodeLabels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(HaveLen(2))

			labelMap := make(map[string]string)
			for _, l := range labels {
				labelMap[l.Key] = l.Value
			}
			Expect(labelMap["env"]).To(Equal("prod"))
			Expect(labelMap["region"]).To(Equal("us-east"))
		})

		It("overwrites existing label with same key", func() {
			node := makeNode("label-overwrite", "10.0.0.71:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), node.ID, "env", "dev")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), node.ID, "env", "prod")).To(Succeed())

			labels, err := registry.GetNodeLabels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(HaveLen(1))
			Expect(labels[0].Value).To(Equal("prod"))
		})

		It("removes a single label by key", func() {
			node := makeNode("label-remove", "10.0.0.72:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), node.ID, "env", "prod")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), node.ID, "region", "us-east")).To(Succeed())

			Expect(registry.RemoveNodeLabel(context.Background(), node.ID, "env")).To(Succeed())

			labels, err := registry.GetNodeLabels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(HaveLen(1))
			Expect(labels[0].Key).To(Equal("region"))
		})

		It("SetNodeLabels replaces all labels", func() {
			node := makeNode("label-replace", "10.0.0.73:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), node.ID, "old-key", "old-val")).To(Succeed())

			newLabels := map[string]string{"new-a": "val-a", "new-b": "val-b"}
			Expect(registry.SetNodeLabels(context.Background(), node.ID, newLabels)).To(Succeed())

			labels, err := registry.GetNodeLabels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(HaveLen(2))

			labelMap := make(map[string]string)
			for _, l := range labels {
				labelMap[l.Key] = l.Value
			}
			Expect(labelMap).To(Equal(newLabels))
		})
	})

	Describe("FindNodesBySelector", func() {
		It("returns nodes matching all labels in selector", func() {
			n1 := makeNode("sel-match", "10.0.0.80:50051", 8_000_000_000)
			n2 := makeNode("sel-nomatch", "10.0.0.81:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), n1.ID, "env", "prod")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n1.ID, "region", "us-east")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n2.ID, "env", "dev")).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{"env": "prod", "region": "us-east"})
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(HaveLen(1))
			Expect(nodes[0].Name).To(Equal("sel-match"))
		})

		It("returns empty when no nodes match", func() {
			n := makeNode("sel-empty", "10.0.0.82:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n, true)).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "env", "dev")).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{"env": "prod"})
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(BeEmpty())
		})

		It("ignores unhealthy nodes", func() {
			n := makeNode("sel-unhealthy", "10.0.0.83:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n, true)).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "env", "prod")).To(Succeed())
			Expect(registry.MarkUnhealthy(context.Background(), n.ID)).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{"env": "prod"})
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(BeEmpty())
		})

		It("matches nodes with more labels than selector requires", func() {
			n := makeNode("sel-superset", "10.0.0.84:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n, true)).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "env", "prod")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "region", "us-east")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "tier", "gpu")).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{"env": "prod"})
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(HaveLen(1))
			Expect(nodes[0].Name).To(Equal("sel-superset"))
		})

		It("returns all healthy nodes for empty selector", func() {
			n1 := makeNode("sel-all-1", "10.0.0.85:50051", 8_000_000_000)
			n2 := makeNode("sel-all-2", "10.0.0.86:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nodes)).To(BeNumerically(">=", 2))
		})
	})

	Describe("ModelSchedulingConfig CRUD", func() {
		It("creates and retrieves a scheduling config", func() {
			config := &ModelSchedulingConfig{
				ModelName:    "llama-7b",
				NodeSelector: `{"gpu.vendor":"nvidia"}`,
				MinReplicas:  1,
				MaxReplicas:  3,
			}
			Expect(registry.SetModelScheduling(context.Background(), config)).To(Succeed())
			Expect(config.ID).ToNot(BeEmpty())

			fetched, err := registry.GetModelScheduling(context.Background(), "llama-7b")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched).ToNot(BeNil())
			Expect(fetched.ModelName).To(Equal("llama-7b"))
			Expect(fetched.NodeSelector).To(Equal(`{"gpu.vendor":"nvidia"}`))
			Expect(fetched.MinReplicas).To(Equal(1))
			Expect(fetched.MaxReplicas).To(Equal(3))
		})

		It("updates existing config via SetModelScheduling", func() {
			config := &ModelSchedulingConfig{
				ModelName:   "update-model",
				MinReplicas: 1,
				MaxReplicas: 2,
			}
			Expect(registry.SetModelScheduling(context.Background(), config)).To(Succeed())

			config2 := &ModelSchedulingConfig{
				ModelName:   "update-model",
				MinReplicas: 2,
				MaxReplicas: 5,
			}
			Expect(registry.SetModelScheduling(context.Background(), config2)).To(Succeed())

			fetched, err := registry.GetModelScheduling(context.Background(), "update-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.MinReplicas).To(Equal(2))
			Expect(fetched.MaxReplicas).To(Equal(5))
		})

		It("persists and updates route policy and thresholds", func() {
			err := registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{
				ModelName: "prefix-cache-model", RoutePolicy: "prefix_cache",
				BalanceAbsThreshold: 3, BalanceRelThreshold: 2.0, MinPrefixMatch: 0.4,
			})
			Expect(err).ToNot(HaveOccurred())

			got, err := registry.GetModelScheduling(context.Background(), "prefix-cache-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.RoutePolicy).To(Equal("prefix_cache"))
			Expect(got.BalanceAbsThreshold).To(Equal(3))
			Expect(got.BalanceRelThreshold).To(BeNumerically("==", 2.0))
			Expect(got.MinPrefixMatch).To(BeNumerically("==", 0.4))

			// Update must not be dropped on conflict.
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{
				ModelName: "prefix-cache-model", RoutePolicy: "round_robin",
			})).ToNot(HaveOccurred())

			got, err = registry.GetModelScheduling(context.Background(), "prefix-cache-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.RoutePolicy).To(Equal("round_robin"))
		})

		It("lists all configs", func() {
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "list-a", MinReplicas: 1})).To(Succeed())
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "list-b", MaxReplicas: 2})).To(Succeed())

			configs, err := registry.ListModelSchedulings(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(len(configs)).To(BeNumerically(">=", 2))
		})

		It("lists only auto-scaling configs", func() {
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "auto-a", MinReplicas: 2})).To(Succeed())
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "auto-b", MaxReplicas: 3})).To(Succeed())
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "no-auto", NodeSelector: `{"env":"prod"}`})).To(Succeed())

			configs, err := registry.ListAutoScalingConfigs(context.Background())
			Expect(err).ToNot(HaveOccurred())

			names := make([]string, len(configs))
			for i, c := range configs {
				names[i] = c.ModelName
			}
			Expect(names).To(ContainElement("auto-a"))
			Expect(names).To(ContainElement("auto-b"))
			Expect(names).ToNot(ContainElement("no-auto"))
		})

		It("deletes a config", func() {
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "delete-me", MinReplicas: 1})).To(Succeed())

			Expect(registry.DeleteModelScheduling(context.Background(), "delete-me")).To(Succeed())

			fetched, err := registry.GetModelScheduling(context.Background(), "delete-me")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched).To(BeNil())
		})

		It("returns nil for non-existent model", func() {
			fetched, err := registry.GetModelScheduling(context.Background(), "does-not-exist")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched).To(BeNil())
		})
	})

	Describe("CountLoadedReplicas", func() {
		It("returns correct count of loaded replicas", func() {
			n1 := makeNode("replica-node-1", "10.0.0.90:50051", 8_000_000_000)
			n2 := makeNode("replica-node-2", "10.0.0.91:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), n1.ID, "counted-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n2.ID, "counted-model", 0, "loaded", "", 0)).To(Succeed())

			count, err := registry.CountLoadedReplicas(context.Background(), "counted-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(int64(2)))
		})

		It("excludes non-loaded states", func() {
			n1 := makeNode("replica-loaded", "10.0.0.92:50051", 8_000_000_000)
			n2 := makeNode("replica-loading", "10.0.0.93:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), n1.ID, "state-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n2.ID, "state-model", 0, "loading", "", 0)).To(Succeed())

			count, err := registry.CountLoadedReplicas(context.Background(), "state-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(int64(1)))
		})
	})

	Describe("DecrementInFlight", func() {
		It("does not go below zero", func() {
			node := makeNode("dec-node", "10.0.0.50:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "dec-model", 0, "loaded", "", 0)).To(Succeed())

			// in_flight starts at 0 — decrement should be a no-op
			Expect(registry.DecrementInFlight(context.Background(), node.ID, "dec-model", 0)).To(Succeed())

			nm, err := registry.GetNodeModel(context.Background(), node.ID, "dec-model", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(0))
		})

		It("decrements correctly from a positive value", func() {
			node := makeNode("dec-node-2", "10.0.0.51:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "dec-model-2", 0, "loaded", "", 0)).To(Succeed())

			Expect(registry.IncrementInFlight(context.Background(), node.ID, "dec-model-2", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node.ID, "dec-model-2", 0)).To(Succeed())

			nm, err := registry.GetNodeModel(context.Background(), node.ID, "dec-model-2", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(2))

			Expect(registry.DecrementInFlight(context.Background(), node.ID, "dec-model-2", 0)).To(Succeed())

			nm, err = registry.GetNodeModel(context.Background(), node.ID, "dec-model-2", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(1))
		})
	})

	Describe("Schema defaults", func() {
		// These tests pin the GORM defaults that the multi-replica refactor
		// relies on. If a future migration changes a default, the
		// reconciler/router will silently misbehave (e.g. capacity 0 instead
		// of 1) — these assertions catch that at the migration boundary.
		It("BackendNode.MaxReplicasPerModel defaults to 1", func() {
			node := makeNode("schema-default-mrpm", "10.0.0.200:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.MaxReplicasPerModel).To(Equal(1),
				"old workers don't send the field; default must preserve single-replica behavior")
		})

		It("BackendNode.ReservedVRAM defaults to 0", func() {
			node := makeNode("schema-default-reserved", "10.0.0.201:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.ReservedVRAM).To(Equal(uint64(0)))
		})

		It("NodeModel.ReplicaIndex defaults to 0", func() {
			node := makeNode("schema-default-replica", "10.0.0.202:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "default-replica-model", 0, "loaded", "127.0.0.1:50100", 0)).To(Succeed())

			nm, err := registry.GetNodeModel(context.Background(), node.ID, "default-replica-model", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(nm).ToNot(BeNil())
			Expect(nm.ReplicaIndex).To(Equal(0))
		})

		It("ModelSchedulingConfig.UnsatisfiableUntil is nullable and defaults to nil", func() {
			cfg := &ModelSchedulingConfig{
				ModelName:   "schema-default-unsat",
				MinReplicas: 1,
			}
			Expect(registry.SetModelScheduling(context.Background(), cfg)).To(Succeed())

			fetched, err := registry.GetModelScheduling(context.Background(), "schema-default-unsat")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched).ToNot(BeNil())
			Expect(fetched.UnsatisfiableUntil).To(BeNil())
			Expect(fetched.UnsatisfiableTicks).To(Equal(0))
		})
	})

	Describe("Multi-replica registry", func() {
		// PR2 tests: SetNodeModel with distinct replica indexes creates distinct
		// rows; per-row mutations (Remove, Increment, Decrement, Touch) target
		// only their indexed row so siblings are not orphaned.
		It("SetNodeModel(replicaIndex=0) then SetNodeModel(replicaIndex=1) creates two distinct rows", func() {
			node := makeNode("multi-1", "10.0.0.210:50051", 16_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), node.ID, "multi-model", 0, "loaded", "127.0.0.1:50100", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "multi-model", 1, "loaded", "127.0.0.1:50101", 0)).To(Succeed())

			models, err := registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(2))

			byIdx := map[int]NodeModel{}
			for _, m := range models {
				byIdx[m.ReplicaIndex] = m
			}
			Expect(byIdx[0].Address).To(Equal("127.0.0.1:50100"))
			Expect(byIdx[1].Address).To(Equal("127.0.0.1:50101"))
			Expect(byIdx[0].ID).ToNot(Equal(byIdx[1].ID))
		})

		It("RemoveNodeModel(replicaIndex=0) leaves replica 1 intact", func() {
			node := makeNode("multi-2", "10.0.0.211:50051", 16_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "kept-model", 0, "loaded", "127.0.0.1:50110", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "kept-model", 1, "loaded", "127.0.0.1:50111", 0)).To(Succeed())

			Expect(registry.RemoveNodeModel(context.Background(), node.ID, "kept-model", 0)).To(Succeed())

			// Sibling replica must still exist — this was the latent bug pre-PR2:
			// the WHERE clause matched both rows and orphaned the healthy sibling.
			survivor, err := registry.GetNodeModel(context.Background(), node.ID, "kept-model", 1)
			Expect(err).ToNot(HaveOccurred())
			Expect(survivor).ToNot(BeNil())
			Expect(survivor.Address).To(Equal("127.0.0.1:50111"))

			// Replica 0 is gone
			_, err = registry.GetNodeModel(context.Background(), node.ID, "kept-model", 0)
			Expect(err).To(HaveOccurred())
		})

		It("RemoveAllNodeModelReplicas deletes every replica of the model on the node", func() {
			node := makeNode("multi-3", "10.0.0.212:50051", 16_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "purge-model", 0, "loaded", "a", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "purge-model", 1, "loaded", "b", 0)).To(Succeed())

			Expect(registry.RemoveAllNodeModelReplicas(context.Background(), node.ID, "purge-model")).To(Succeed())

			models, err := registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})

		It("IncrementInFlight only updates the targeted replica row", func() {
			node := makeNode("multi-4", "10.0.0.213:50051", 16_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "infl-model", 0, "loaded", "a", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "infl-model", 1, "loaded", "b", 0)).To(Succeed())

			Expect(registry.IncrementInFlight(context.Background(), node.ID, "infl-model", 1)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node.ID, "infl-model", 1)).To(Succeed())

			r0, err := registry.GetNodeModel(context.Background(), node.ID, "infl-model", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(r0.InFlight).To(Equal(0), "replica 0 must not have been incremented")

			r1, err := registry.GetNodeModel(context.Background(), node.ID, "infl-model", 1)
			Expect(err).ToNot(HaveOccurred())
			Expect(r1.InFlight).To(Equal(2))
		})

		It("CountReplicasOnNode returns the per-(node, model) row count", func() {
			node := makeNode("multi-5", "10.0.0.214:50051", 16_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "count-model", 0, "loaded", "a", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "count-model", 1, "loaded", "b", 0)).To(Succeed())

			n, err := registry.CountReplicasOnNode(context.Background(), node.ID, "count-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(2))
		})

		It("NextFreeReplicaIndex returns the lowest unused index < maxSlots", func() {
			node := makeNode("multi-6", "10.0.0.215:50051", 16_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Slot 0 free initially
			idx, err := registry.NextFreeReplicaIndex(context.Background(), node.ID, "slot-model", 4)
			Expect(err).ToNot(HaveOccurred())
			Expect(idx).To(Equal(0))

			// Occupy 0 and 2 — next free is 1 (lowest gap)
			Expect(registry.SetNodeModel(context.Background(), node.ID, "slot-model", 0, "loaded", "a", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "slot-model", 2, "loaded", "c", 0)).To(Succeed())
			idx, err = registry.NextFreeReplicaIndex(context.Background(), node.ID, "slot-model", 4)
			Expect(err).ToNot(HaveOccurred())
			Expect(idx).To(Equal(1), "must allocate the lowest free index for compactness")

			// Fill all 4 — must return ErrNoFreeSlot
			Expect(registry.SetNodeModel(context.Background(), node.ID, "slot-model", 1, "loaded", "b", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "slot-model", 3, "loaded", "d", 0)).To(Succeed())
			_, err = registry.NextFreeReplicaIndex(context.Background(), node.ID, "slot-model", 4)
			Expect(err).To(MatchError(ErrNoFreeSlot))

			// maxSlots=0 always returns ErrNoFreeSlot
			_, err = registry.NextFreeReplicaIndex(context.Background(), node.ID, "no-slots-model", 0)
			Expect(err).To(MatchError(ErrNoFreeSlot))
		})
	})

	Describe("SetReplicaRemovedHook", func() {
		type removed struct {
			model, node string
			replica     int
		}

		It("fires once with the specific replica after RemoveNodeModel", func() {
			node := makeNode("hook-remove-one", "10.0.0.230:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "hook-model", 1, "loaded", "a", 0)).To(Succeed())

			var fired []removed
			registry.SetReplicaRemovedHook(func(modelName, nodeID string, replicaIndex int) {
				fired = append(fired, removed{model: modelName, node: nodeID, replica: replicaIndex})
			})

			// RemoveNodeModel(replica 1) must fire with the SPECIFIC replica index.
			Expect(registry.RemoveNodeModel(context.Background(), node.ID, "hook-model", 1)).To(Succeed())
			Expect(fired).To(HaveLen(1))
			Expect(fired[0]).To(Equal(removed{model: "hook-model", node: node.ID, replica: 1}))
		})

		It("fires once with replica<0 after RemoveAllNodeModelReplicas", func() {
			node := makeNode("hook-remove-all", "10.0.0.231:50051", 16_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "hook-all-model", 0, "loaded", "a", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "hook-all-model", 1, "loaded", "b", 0)).To(Succeed())

			var fired []removed
			registry.SetReplicaRemovedHook(func(modelName, nodeID string, replicaIndex int) {
				fired = append(fired, removed{model: modelName, node: nodeID, replica: replicaIndex})
			})

			// One call covers all replicas of that model on the node: a negative
			// replica index signals "all replicas", and the consumer's
			// InvalidateNode drops every entry for the (model, node) pair.
			Expect(registry.RemoveAllNodeModelReplicas(context.Background(), node.ID, "hook-all-model")).To(Succeed())
			Expect(fired).To(HaveLen(1))
			Expect(fired[0].model).To(Equal("hook-all-model"))
			Expect(fired[0].node).To(Equal(node.ID))
			Expect(fired[0].replica).To(BeNumerically("<", 0))
		})

		It("does not panic when no hook is set", func() {
			node := makeNode("hook-unset", "10.0.0.232:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "no-hook-model", 0, "loaded", "a", 0)).To(Succeed())

			Expect(func() {
				Expect(registry.RemoveNodeModel(context.Background(), node.ID, "no-hook-model", 0)).To(Succeed())
				Expect(registry.RemoveAllNodeModelReplicas(context.Background(), node.ID, "no-hook-model")).To(Succeed())
			}).ToNot(Panic())
		})

		// firedModelSet collects the distinct model names the hook saw for the
		// given node. The bulk node-scoped deletes below remove every replica of
		// every model on the node in one statement, so the chokepoint must fire
		// the hook once per distinct model name (the consumer's Invalidate
		// drops all entries for that (model, node) pair).
		seedTwoModels := func(node *BackendNode) {
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-a", 0, "loaded", "a0", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-a", 1, "loaded", "a1", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-b", 0, "loaded", "b0", 0)).To(Succeed())
		}

		It("fires once per distinct model after MarkOffline", func() {
			node := makeNode("hook-offline", "10.0.0.240:50051", 8_000_000_000)
			seedTwoModels(node)

			fired := map[removed]int{}
			registry.SetReplicaRemovedHook(func(modelName, nodeID string, replicaIndex int) {
				// Bulk node-scoped deletes signal "all replicas" with replica<0.
				Expect(replicaIndex).To(BeNumerically("<", 0))
				fired[removed{model: modelName, node: nodeID}]++
			})

			Expect(registry.MarkOffline(context.Background(), node.ID)).To(Succeed())
			Expect(fired).To(HaveLen(2))
			Expect(fired[removed{model: "model-a", node: node.ID}]).To(Equal(1))
			Expect(fired[removed{model: "model-b", node: node.ID}]).To(Equal(1))
		})

		It("fires once per distinct model after MarkDraining", func() {
			node := makeNode("hook-draining", "10.0.0.241:50051", 8_000_000_000)
			seedTwoModels(node)

			fired := map[removed]int{}
			registry.SetReplicaRemovedHook(func(modelName, nodeID string, replicaIndex int) {
				// Bulk node-scoped deletes signal "all replicas" with replica<0.
				Expect(replicaIndex).To(BeNumerically("<", 0))
				fired[removed{model: modelName, node: nodeID}]++
			})

			Expect(registry.MarkDraining(context.Background(), node.ID)).To(Succeed())
			Expect(fired).To(HaveLen(2))
			Expect(fired[removed{model: "model-a", node: node.ID}]).To(Equal(1))
			Expect(fired[removed{model: "model-b", node: node.ID}]).To(Equal(1))
		})

		It("fires once per distinct model after Deregister", func() {
			node := makeNode("hook-deregister", "10.0.0.242:50051", 8_000_000_000)
			seedTwoModels(node)

			fired := map[removed]int{}
			registry.SetReplicaRemovedHook(func(modelName, nodeID string, replicaIndex int) {
				// Bulk node-scoped deletes signal "all replicas" with replica<0.
				Expect(replicaIndex).To(BeNumerically("<", 0))
				fired[removed{model: modelName, node: nodeID}]++
			})

			Expect(registry.Deregister(context.Background(), node.ID)).To(Succeed())
			Expect(fired).To(HaveLen(2))
			Expect(fired[removed{model: "model-a", node: node.ID}]).To(Equal(1))
			Expect(fired[removed{model: "model-b", node: node.ID}]).To(Equal(1))
		})

		It("fires once per distinct model when re-registration clears stale rows", func() {
			node := makeNode("hook-reregister", "10.0.0.243:50051", 8_000_000_000)
			seedTwoModels(node)

			fired := map[removed]int{}
			registry.SetReplicaRemovedHook(func(modelName, nodeID string, replicaIndex int) {
				// Bulk node-scoped deletes signal "all replicas" with replica<0.
				Expect(replicaIndex).To(BeNumerically("<", 0))
				fired[removed{model: modelName, node: nodeID}]++
			})

			// Re-register the same node (same name): the re-register path
			// clears the stale model rows, which must fire the hook.
			again := makeNode("hook-reregister", "10.0.0.243:50052", 8_000_000_000)
			Expect(registry.Register(context.Background(), again, true)).To(Succeed())
			Expect(fired).To(HaveLen(2))
			Expect(fired[removed{model: "model-a", node: node.ID}]).To(Equal(1))
			Expect(fired[removed{model: "model-b", node: node.ID}]).To(Equal(1))
		})

		// Atomicity: the bulk node-scoped delete in MarkOffline/MarkDraining/
		// re-register now captures the model names and deletes the rows inside a
		// single transaction. A true SetNodeModel-between-capture-and-delete race
		// can't be forced deterministically here, but we can assert the
		// post-condition the transaction guarantees: the set of fired hooks
		// equals exactly the set of node_models rows the operation removed, with
		// nothing left behind. If the capture and delete ever saw inconsistent
		// snapshots, either a surviving row (delete missed it) or a missing hook
		// (capture missed it) would break one of these assertions.
		It("MarkOffline fires hooks for exactly the rows it deletes (consistent snapshot)", func() {
			node := makeNode("hook-atomic-offline", "10.0.0.244:50051", 8_000_000_000)
			seedTwoModels(node)

			// Capture what the transaction should remove, straight from the DB,
			// before running the operation.
			before, err := registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			expectedModels := map[string]struct{}{}
			for _, nm := range before {
				expectedModels[nm.ModelName] = struct{}{}
			}
			Expect(expectedModels).To(HaveLen(2), "seed should create two distinct models")

			fired := map[string]struct{}{}
			registry.SetReplicaRemovedHook(func(modelName, nodeID string, replicaIndex int) {
				Expect(nodeID).To(Equal(node.ID))
				Expect(replicaIndex).To(BeNumerically("<", 0))
				fired[modelName] = struct{}{}
			})

			Expect(registry.MarkOffline(context.Background(), node.ID)).To(Succeed())

			// Hooks fired for exactly the distinct models that existed.
			Expect(fired).To(Equal(expectedModels),
				"hooks must fire for exactly the set of models the transaction deleted")

			// And the delete actually emptied the node_models rows for the node:
			// no row survives that did not get a hook.
			after, err := registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(after).To(BeEmpty(), "no node_models row should survive the bulk delete")
		})
	})

	Describe("ApplyAutoLabels", func() {
		It("mirrors MaxReplicasPerModel as the node.replica-slots label", func() {
			node := makeNode("auto-label-replicas", "10.0.0.220:50051", 16_000_000_000)
			node.MaxReplicasPerModel = 4
			node.GPUVendor = "nvidia"
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			registry.ApplyAutoLabels(context.Background(), node.ID, node)

			labels, err := registry.GetNodeLabels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			byKey := map[string]string{}
			for _, l := range labels {
				byKey[l.Key] = l.Value
			}
			Expect(byKey).To(HaveKeyWithValue("node.replica-slots", "4"),
				"selectors targeting fat nodes need this auto-label")
			Expect(byKey).To(HaveKeyWithValue("gpu.vendor", "nvidia"))
		})

		It("defaults node.replica-slots to 1 when MaxReplicasPerModel is unset", func() {
			node := makeNode("auto-label-default", "10.0.0.221:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Fetch back; default should be 1 (PR1 schema test)
			fetched, _ := registry.Get(context.Background(), node.ID)
			Expect(fetched.MaxReplicasPerModel).To(Equal(1))

			registry.ApplyAutoLabels(context.Background(), node.ID, fetched)

			labels, _ := registry.GetNodeLabels(context.Background(), node.ID)
			byKey := map[string]string{}
			for _, l := range labels {
				byKey[l.Key] = l.Value
			}
			Expect(byKey).To(HaveKeyWithValue("node.replica-slots", "1"))
		})
	})

	Describe("VRAM soft-reservation (PR5)", func() {
		// These tests pin the soft-reservation contract: ReserveVRAM is the
		// admission gate that prevents two concurrent scheduling decisions
		// from over-committing the same node within one heartbeat window.
		It("ReserveVRAM atomically deducts from effectively-free VRAM", func() {
			node := makeNode("reserve-1", "10.0.0.230:50051", 10_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.ReserveVRAM(context.Background(), node.ID, 3_000_000_000)).To(Succeed())

			fetched, _ := registry.Get(context.Background(), node.ID)
			Expect(fetched.ReservedVRAM).To(Equal(uint64(3_000_000_000)))
		})

		It("ReserveVRAM rejects when effectively-free VRAM is insufficient", func() {
			node := makeNode("reserve-2", "10.0.0.231:50051", 5_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// First reservation fits.
			Expect(registry.ReserveVRAM(context.Background(), node.ID, 4_000_000_000)).To(Succeed())
			// Second is too big — only 1 GB effectively free.
			err := registry.ReserveVRAM(context.Background(), node.ID, 2_000_000_000)
			Expect(err).To(MatchError(ErrInsufficientVRAM))

			fetched, _ := registry.Get(context.Background(), node.ID)
			Expect(fetched.ReservedVRAM).To(Equal(uint64(4_000_000_000)),
				"failed reservation must not bump the column")
		})

		It("ReserveVRAM with bytes=0 is a no-op", func() {
			node := makeNode("reserve-3", "10.0.0.232:50051", 1_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.ReserveVRAM(context.Background(), node.ID, 0)).To(Succeed())

			fetched, _ := registry.Get(context.Background(), node.ID)
			Expect(fetched.ReservedVRAM).To(Equal(uint64(0)))
		})

		It("ReleaseVRAM returns reserved bytes to the pool", func() {
			node := makeNode("release-1", "10.0.0.233:50051", 10_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.ReserveVRAM(context.Background(), node.ID, 4_000_000_000)).To(Succeed())

			Expect(registry.ReleaseVRAM(context.Background(), node.ID, 1_000_000_000)).To(Succeed())

			fetched, _ := registry.Get(context.Background(), node.ID)
			Expect(fetched.ReservedVRAM).To(Equal(uint64(3_000_000_000)))
		})

		It("ReleaseVRAM cannot underflow past zero", func() {
			node := makeNode("release-underflow", "10.0.0.234:50051", 1_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// No reservation; release is a guarded no-op rather than wrapping
			// uint64 to a huge positive number.
			Expect(registry.ReleaseVRAM(context.Background(), node.ID, 5_000_000_000)).To(Succeed())

			fetched, _ := registry.Get(context.Background(), node.ID)
			Expect(fetched.ReservedVRAM).To(Equal(uint64(0)))
		})

		It("Heartbeat with available_vram resets reserved_vram to 0", func() {
			node := makeNode("heartbeat-reset", "10.0.0.235:50051", 10_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.ReserveVRAM(context.Background(), node.ID, 5_000_000_000)).To(Succeed())

			fresh := uint64(8_000_000_000)
			Expect(registry.Heartbeat(context.Background(), node.ID, &HeartbeatUpdate{AvailableVRAM: &fresh})).To(Succeed())

			fetched, _ := registry.Get(context.Background(), node.ID)
			Expect(fetched.AvailableVRAM).To(Equal(fresh),
				"heartbeat must overwrite available_vram with the worker's reading")
			Expect(fetched.ReservedVRAM).To(Equal(uint64(0)),
				"heartbeat must clear the soft reservation — worker is the source of truth")
		})

		It("UpdateMaxReplicasPerModel marks the value as a sticky override", func() {
			// The original UX bug: workers default the flag to 1, so every
			// re-registration silently reverted the admin's UI value. This
			// test pins the fix.
			node := &BackendNode{
				Name:                "override-survives",
				NodeType:            NodeTypeBackend,
				Address:             "10.0.0.240:50051",
				MaxReplicasPerModel: 1,
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Admin sets capacity to 4 via the UI.
			Expect(registry.UpdateMaxReplicasPerModel(context.Background(), node.ID, 4)).To(Succeed())

			fetched, _ := registry.Get(context.Background(), node.ID)
			Expect(fetched.MaxReplicasPerModel).To(Equal(4))
			Expect(fetched.MaxReplicasPerModelManuallySet).To(BeTrue())

			// Worker re-registers with its default of 1 (operator never set the flag).
			restart := &BackendNode{
				Name:                "override-survives",
				NodeType:            NodeTypeBackend,
				Address:             "10.0.0.240:50051",
				MaxReplicasPerModel: 1,
			}
			Expect(registry.Register(context.Background(), restart, true)).To(Succeed())

			// Override must have survived.
			fetched, _ = registry.Get(context.Background(), node.ID)
			Expect(fetched.MaxReplicasPerModel).To(Equal(4),
				"admin override must not be overwritten by worker re-registration")
			Expect(fetched.MaxReplicasPerModelManuallySet).To(BeTrue())
		})

		It("ResetMaxReplicasPerModel hands control back to the worker", func() {
			node := &BackendNode{
				Name:                "override-reset",
				NodeType:            NodeTypeBackend,
				Address:             "10.0.0.241:50051",
				MaxReplicasPerModel: 1,
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.UpdateMaxReplicasPerModel(context.Background(), node.ID, 4)).To(Succeed())

			Expect(registry.ResetMaxReplicasPerModel(context.Background(), node.ID)).To(Succeed())

			// Reset only flips the flag; the value stays until the worker
			// re-registers (we don't presume to know what the worker wants).
			fetched, _ := registry.Get(context.Background(), node.ID)
			Expect(fetched.MaxReplicasPerModelManuallySet).To(BeFalse())

			// Now worker re-registers with 8.
			restart := &BackendNode{
				Name:                "override-reset",
				NodeType:            NodeTypeBackend,
				Address:             "10.0.0.241:50051",
				MaxReplicasPerModel: 8,
			}
			Expect(registry.Register(context.Background(), restart, true)).To(Succeed())

			fetched, _ = registry.Get(context.Background(), node.ID)
			Expect(fetched.MaxReplicasPerModel).To(Equal(8),
				"after reset, the worker's value should apply")
		})

		It("FindNodeWithVRAM honors the reservation", func() {
			small := makeNode("find-vram-small", "10.0.0.236:50051", 5_000_000_000)
			big := makeNode("find-vram-big", "10.0.0.237:50051", 20_000_000_000)
			Expect(registry.Register(context.Background(), small, true)).To(Succeed())
			Expect(registry.Register(context.Background(), big, true)).To(Succeed())

			// Reserve almost all of the big node so its effective free
			// drops below the request — small isn't big enough either —
			// the call must return an error.
			Expect(registry.ReserveVRAM(context.Background(), big.ID, 18_000_000_000)).To(Succeed())

			_, err := registry.FindNodeWithVRAM(context.Background(), 8_000_000_000)
			Expect(err).To(HaveOccurred(),
				"reserved capacity must remove a node from VRAM-aware candidates")
		})
	})

	Describe("ModelLoadInfo persistence (Bug-1)", func() {
		It("survives every NodeModel row being removed", func() {
			ctx := context.Background()

			// One node with one loaded replica + per-replica blob (the legacy path).
			node := makeNode("li-1", "10.0.1.1:50051", 8_000_000_000)
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			Expect(registry.SetNodeModel(ctx, node.ID, "load-info-model", 0, "loaded", node.Address, 0)).To(Succeed())
			Expect(registry.SetNodeModelLoadInfo(ctx, node.ID, "load-info-model", 0, "llama-cpp", []byte("opts-v1"))).To(Succeed())

			// Persist per-model via the new path (the dispatch hook does this).
			Expect(registry.UpsertModelLoadInfo(ctx, "load-info-model", "llama-cpp", []byte("opts-v1"))).To(Succeed())

			// Simulate worker death + MarkOffline reaping: every NodeModel row gone.
			Expect(registry.RemoveAllNodeModelReplicas(ctx, node.ID, "load-info-model")).To(Succeed())

			bt, blob, err := registry.GetModelLoadInfo(ctx, "load-info-model")
			Expect(err).ToNot(HaveOccurred(),
				"per-model load info must survive every NodeModel row going away")
			Expect(bt).To(Equal("llama-cpp"))
			Expect(blob).To(Equal([]byte("opts-v1")))
		})

		It("ON CONFLICT updates backend type and opts (last-write-wins)", func() {
			ctx := context.Background()

			Expect(registry.UpsertModelLoadInfo(ctx, "lww", "llama-cpp", []byte("v1"))).To(Succeed())
			Expect(registry.UpsertModelLoadInfo(ctx, "lww", "vllm", []byte("v2"))).To(Succeed())

			bt, blob, err := registry.GetModelLoadInfo(ctx, "lww")
			Expect(err).ToNot(HaveOccurred())
			Expect(bt).To(Equal("vllm"))
			Expect(blob).To(Equal([]byte("v2")))
		})

		It("falls back to legacy NodeModel blob when no per-model row exists", func() {
			// Pre-fix rolling-upgrade path: a frontend that ran before the new
			// table existed only wrote the per-replica blob. The new
			// GetModelLoadInfo must still find it so an upgrade doesn't
			// regress the reconciler for already-loaded models.
			ctx := context.Background()

			node := makeNode("li-legacy", "10.0.1.2:50051", 8_000_000_000)
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			Expect(registry.SetNodeModel(ctx, node.ID, "legacy-model", 0, "loaded", node.Address, 0)).To(Succeed())
			Expect(registry.SetNodeModelLoadInfo(ctx, node.ID, "legacy-model", 0, "llama-cpp", []byte("legacy-opts"))).To(Succeed())

			bt, blob, err := registry.GetModelLoadInfo(ctx, "legacy-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(bt).To(Equal("llama-cpp"))
			Expect(blob).To(Equal([]byte("legacy-opts")))
		})

		It("returns ErrRecordNotFound when neither source has the model", func() {
			ctx := context.Background()
			_, _, err := registry.GetModelLoadInfo(ctx, "never-loaded")
			Expect(err).To(MatchError(gorm.ErrRecordNotFound))
		})

		It("rejects empty model names", func() {
			err := registry.UpsertModelLoadInfo(context.Background(), "", "llama-cpp", []byte("x"))
			Expect(err).To(HaveOccurred())
		})
	})
})
