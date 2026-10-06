package nodes

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Reservation uncertainty authority", func() {
	for _, order := range []string{"failure-first", "config-first", "concurrent", "passive", "confirmed"} {
		order := order
		It("keeps all reservation projections consistent: "+order, func() {
			ctx := context.Background()
			registry, err := NewNodeRegistry(testutil.SetupTestDB())
			Expect(err).NotTo(HaveOccurred())
			node := &BackendNode{Name: "capacity-worker", NodeType: NodeTypeBackend, Address: "addr", MaxReplicasPerModel: 1}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			job, claimed, err := registry.ClaimLoadJob(ctx, "capacity-model", "owner")
			Expect(err).NotTo(HaveOccurred())
			Expect(claimed).To(BeTrue())
			owned := context.WithValue(ctx, loadOwnershipKey{}, job.Ref())
			Expect(registry.SetNodeModelRevision(owned, node.ID, job.TrackingKey, 0, "loaded", "addr", 0, "old", "")).To(Succeed())
			check := func(protected bool) {
				count, err := registry.CountReplicasOnNode(ctx, node.ID, job.TrackingKey)
				Expect(err).NotTo(HaveOccurred())
				free, err := registry.FindNodesWithFreeSlot(ctx, job.TrackingKey, []string{node.ID})
				Expect(err).NotTo(HaveOccurred())
				capacity, err := registry.ClusterCapacityForModel(ctx, job.TrackingKey, []string{node.ID})
				Expect(err).NotTo(HaveOccurred())
				idx, err := registry.NextFreeReplicaIndex(ctx, node.ID, job.TrackingKey, 1)
				if protected {
					Expect(count).To(Equal(1))
					Expect(free).To(BeEmpty())
					Expect(capacity).To(Equal(0))
					Expect(err).To(MatchError(ErrNoFreeSlot))
				} else {
					Expect(count).To(Equal(0))
					Expect(free).To(HaveLen(1))
					Expect(capacity).To(Equal(1))
					Expect(err).NotTo(HaveOccurred())
					Expect(idx).To(Equal(0))
				}
			}
			fail := func() error { return registry.FailLoadJob(ctx, job.Ref(), "uncertain") }
			change := func() error { _, err := registry.AdvanceModelConfigRevision(ctx, job.TrackingKey, "new"); return err }
			switch order {
			case "failure-first":
				Expect(fail()).To(Succeed())
				Expect(change()).To(Succeed())
			case "config-first":
				Expect(change()).To(Succeed())
				check(true)
				Expect(fail()).To(Succeed())
			case "concurrent":
				start := make(chan struct{})
				errs := make(chan error, 2)
				var wg sync.WaitGroup
				for _, f := range []func() error{fail, change} {
					wg.Add(1)
					go func(f func() error) { defer wg.Done(); <-start; errs <- f() }(f)
				}
				close(start)
				wg.Wait()
				Expect(<-errs).To(Succeed())
				Expect(<-errs).To(Succeed())
			case "passive":
				Expect(change()).To(Succeed())
				check(true)
				Expect(registry.db.Model(&ModelLoadJob{}).Where("tracking_key = ?", job.TrackingKey).Update("last_progress", time.Now().Add(-2*loadJobOrphanWindow)).Error).To(Succeed())
				result, err := (&LoadRecoveryService{Registry: registry}).Reconcile(ctx, job.Ref())
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Outcome).To(Equal(LoadUncertain))
			case "confirmed":
				Expect(registry.DeleteLoadJob(ctx, job.Ref())).To(Succeed())
				Expect(change()).To(Succeed())
			}
			check(order != "confirmed")
			row, err := registry.GetNodeModel(ctx, node.ID, job.TrackingKey, 0)
			Expect(err).NotTo(HaveOccurred())
			removed, err := registry.RemoveClaimedModelCleanup(ctx, *row)
			Expect(err).NotTo(HaveOccurred())
			Expect(removed).To(Equal(order == "confirmed"))
			Expect(registry.SetNodeModelRevision(owned, node.ID, job.TrackingKey, 0, "loaded", "late", 0, "old", "")).To(MatchError(ErrStaleLoadJob))
		})
	}

	It("keeps a published but failed-uncertain generation reserved after a config edit", func() {
		ctx := context.Background()
		registry, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		job, claimed, err := registry.ClaimLoadJob(ctx, "published-model", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		owned := context.WithValue(ctx, loadOwnershipKey{}, job.Ref())
		Expect(registry.SetNodeModelRevision(owned, "node", "published-model", 0, "loading", "addr", 0, "old", "")).To(Succeed())
		Expect(registry.SetNodeModelRevision(owned, "node", "published-model", 0, "loaded", "addr", 0, "old", "")).To(Succeed())
		_, err = (&LoadRecoveryService{Registry: registry}).Fail(ctx, job.Ref(), "supervisor deadline raced publication")
		Expect(err).NotTo(HaveOccurred())
		rows, err := registry.AdvanceModelConfigRevision(ctx, "published-model", "new")
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		removed, err := registry.RemoveClaimedModelCleanup(ctx, rows[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(BeFalse())
		_, err = registry.NextFreeReplicaIndex(ctx, "node", "published-model", 1)
		Expect(err).To(MatchError(ErrNoFreeSlot), "a job-uncertain reservation must not be available merely because config changed")
	})
	It("rejects old completion after a normal successful cleanup and a new claim", func() {
		ctx := context.Background()
		registry, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		a, _, err := registry.ClaimLoadJob(ctx, "aba-model", "a")
		Expect(err).NotTo(HaveOccurred())
		actx := context.WithValue(ctx, loadOwnershipKey{}, a.Ref())
		Expect(registry.SetNodeModel(actx, "node", "aba-model", 0, "loaded", "a", 0)).To(Succeed())
		Expect(registry.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
		Expect(registry.RemoveNodeModel(ctx, "node", "aba-model", 0)).To(Succeed())
		idx, err := registry.NextFreeReplicaIndex(ctx, "node", "aba-model", 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(Equal(0))
		b, claimed, err := registry.ClaimLoadJob(ctx, "aba-model", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		bctx := context.WithValue(ctx, loadOwnershipKey{}, b.Ref())
		Expect(registry.SetNodeModel(bctx, "node", "aba-model", 0, "loading", "b", 0)).To(Succeed())
		Expect(registry.SetNodeModel(actx, "node", "aba-model", 0, "loaded", "late-a", 0)).To(MatchError(ErrStaleLoadJob))
		Expect(registry.RemoveNodeModel(actx, "node", "aba-model", 0)).To(MatchError(ErrStaleLoadJob))
		row, err := registry.GetNodeModel(ctx, "node", "aba-model", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(row.LoadGeneration).To(Equal(b.Generation))
		Expect(row.Address).To(Equal("b"))
	})
})
