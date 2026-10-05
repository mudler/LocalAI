// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Load recovery generation", func() {
	It("does not treat an expired lease as proof remote work stopped", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		_, claimed, err := registry.ClaimLoadJob(ctx, "synthetic-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", "synthetic-model").Update("last_progress", time.Now().Add(-2*loadJobOrphanWindow)).Error).To(Succeed())
		job, claimed, err := registry.ClaimLoadJob(ctx, "synthetic-model", "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse(), "lease expiry cannot authorize overlapping remote work")
		Expect(job.OwnerReplica).To(Equal("frontend-a"))
	})

	It("fences delayed A writes after B and another A claim", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		a, _, err := registry.ClaimLoadJob(ctx, "aba-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Generation).NotTo(BeEmpty())
		Expect(registry.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
		b, _, err := registry.ClaimLoadJob(ctx, "aba-model", "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.DeleteLoadJob(ctx, b.Ref())).To(Succeed())
		replacement, claimed, err := registry.ClaimLoadJob(ctx, "aba-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(replacement.Generation).NotTo(Equal(a.Generation))
		for _, stale := range []LoadJobRef{a.Ref(), b.Ref(), {TrackingKey: "aba-model"}} {
			Expect(registry.UpdateLoadJob(ctx, stale, LoadJobUpdate{State: LoadJobStateLoading})).To(MatchError(ErrStaleLoadJob))
			Expect(registry.FailLoadJob(ctx, stale, "late failure")).To(MatchError(ErrStaleLoadJob))
			Expect(registry.DeleteLoadJob(ctx, stale)).To(MatchError(ErrStaleLoadJob))
		}
		current, err := registry.GetLoadJob(ctx, "aba-model")
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Ref()).To(Equal(replacement.Ref()))
		Expect(current.State).To(Equal(LoadJobStatePending))
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		writeErr := registry.UpdateLoadJob(cancelled, replacement.Ref(), LoadJobUpdate{})
		Expect(writeErr).To(HaveOccurred())
		Expect(writeErr).NotTo(MatchError(ErrStaleLoadJob))
	})

	It("quarantines legacy rows rather than assigning them apparent ownership", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		legacy := ModelLoadJob{TrackingKey: "legacy-model", State: LoadJobStateStaging, OwnerReplica: "legacy-frontend", LastProgress: time.Now().Add(-24 * time.Hour)}
		Expect(db.Create(&legacy).Error).To(Succeed())
		job, claimed, err := registry.ClaimLoadJob(ctx, legacy.TrackingKey, "new-frontend")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse())
		Expect(job.Generation).To(BeEmpty())
		Expect(registry.UpdateLoadJob(ctx, job.Ref(), LoadJobUpdate{})).To(MatchError(ErrStaleLoadJob))
		Expect(registry.FailLoadJob(ctx, job.Ref(), "failure")).To(MatchError(ErrStaleLoadJob))
		Expect(registry.DeleteLoadJob(ctx, job.Ref())).To(MatchError(ErrStaleLoadJob))
	})

	It("retains failure and uncertainty durably without relying on a grace timer", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		job, _, err := registry.ClaimLoadJob(ctx, "terminal-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.FailLoadJob(ctx, job.Ref(), "remote outcome unknown")).To(Succeed())
		restarted, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		terminal, claimed, err := restarted.ClaimLoadJob(ctx, job.TrackingKey, "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse())
		Expect(terminal.TerminalUntil).NotTo(BeNil())
		Expect(terminal.LastError).To(Equal("remote outcome unknown"))
		Expect(registry.UpdateLoadJob(ctx, job.Ref(), LoadJobUpdate{})).To(MatchError(ErrStaleLoadJob))
		Expect(registry.DeleteLoadJob(ctx, job.Ref())).To(MatchError(ErrStaleLoadJob))
		Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", job.TrackingKey).Update("terminal_until", time.Now().Add(-time.Hour)).Error).To(Succeed())
		_, claimed, err = restarted.ClaimLoadJob(ctx, job.TrackingKey, "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse(), "expired grace does not prove remote termination")
		// Stand in for future operation reconciliation proving remote work stopped.
		Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ? AND generation = ?", job.TrackingKey, job.Generation).Update("work_uncertain", false).Error).To(Succeed())
		replacement, claimed, err := restarted.ClaimLoadJob(ctx, job.TrackingKey, "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(replacement.Generation).NotTo(Equal(job.Generation))
		Expect(registry.DeleteLoadJob(ctx, job.Ref())).To(MatchError(ErrStaleLoadJob))
	})

	It("does not wake replacement waiters on a delayed successful completion", func() {
		store := &fakeModelRouter{}
		ctx := context.Background()
		a, _, err := store.ClaimLoadJob(ctx, "runner-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(store.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
		b, _, err := store.ClaimLoadJob(ctx, "runner-model", "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		router := NewSmartRouter(store, SmartRouterOptions{})
		waiter := router.loadWaiterChan("runner-model")
		router.finishLoadJob(ctx, a.Ref())
		select {
		case <-waiter:
			Fail("stale completion woke replacement waiter")
		default:
		}
		current, err := store.GetLoadJob(ctx, "runner-model")
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Ref()).To(Equal(b.Ref()))
		router.finishLoadJob(ctx, b.Ref())
		Eventually(waiter).Should(BeClosed())
	})
})

// Run the same contract against the real registry and every embedding of the router fake.
// Direct fixture insertion represents durable reconciliation evidence; elapsed
// lease time alone must never supply that evidence.
var _ = Describe("Load job store parity", func() {
	for _, implementation := range []string{"registry", "fake", "model router", "smart router"} {
		Context(implementation, func() {
			var store LoadJobStore
			var seed func(ModelLoadJob)
			BeforeEach(func() {
				if implementation == "registry" {
					db := testutil.SetupTestDB()
					registry, err := NewNodeRegistry(db)
					Expect(err).NotTo(HaveOccurred())
					store = registry
					seed = func(job ModelLoadJob) { Expect(db.Create(&job).Error).To(Succeed()) }
					return
				}
				var fake *fakeLoadJobStore
				switch implementation {
				case "fake":
					fake = &fakeLoadJobStore{}
					store = fake
				case "model router":
					router := &fakeModelRouter{}
					store, fake = router, &router.fakeLoadJobStore
				case "smart router":
					router := newFakeModelRouterForSmartRouter()
					store, fake = router, &router.fakeLoadJobStore
				}
				seed = func(job ModelLoadJob) {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.jobs = map[string]*ModelLoadJob{job.TrackingKey: &job}
				}
			})

			Context("lifecycle fields", func() {
				var original *ModelLoadJob
				ctx := context.Background()
				BeforeEach(func() {
					past := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
					seed(ModelLoadJob{TrackingKey: "lifecycle", Generation: "a", OwnerReplica: "owner",
						State: LoadJobStateLoading, CreatedAt: past, UpdatedAt: past, LastProgress: past,
						StartedAt: past, NodeID: "node", NodeName: "worker", ReplicaIndex: 2,
						BytesSent: 10, TotalBytes: 100, FileIndex: 1, TotalFiles: 3})
					var err error
					original, err = store.GetLoadJob(ctx, "lifecycle")
					Expect(err).NotTo(HaveOccurred())
				})
				for _, uncertain := range []bool{false, true} {
					It(fmt.Sprintf("records all failure fields (previous uncertainty %t)", uncertain), func() {
						if uncertain {
							Expect(store.DeleteLoadJob(ctx, original.Ref())).To(Succeed())
							original.WorkUncertain = true
							seed(*original)
						}
						before := time.Now().Add(-time.Microsecond)
						Expect(store.FailLoadJob(ctx, original.Ref(), "remote failure")).To(Succeed())
						after := time.Now().Add(time.Microsecond)
						current, err := store.GetLoadJob(ctx, original.TrackingKey)
						Expect(err).NotTo(HaveOccurred())
						Expect(current.WorkUncertain).To(BeTrue())
						Expect(current.LastProgress).To(BeTemporally(">=", before))
						Expect(current.LastProgress).To(BeTemporally("<=", after))
						Expect(current.UpdatedAt).To(BeTemporally("==", current.LastProgress))
						Expect(current.TerminalUntil).NotTo(BeNil())
						Expect(*current.TerminalUntil).To(BeTemporally("==", current.LastProgress.Add(loadJobFailureGrace)))
						expected := *original
						expected.State, expected.LastError, expected.WorkUncertain = LoadJobStateFailed, "remote failure", true
						expected.LastProgress, expected.UpdatedAt, expected.TerminalUntil = current.LastProgress, current.UpdatedAt, current.TerminalUntil
						Expect(current).To(Equal(&expected))
						Expect(store.UpdateLoadJob(ctx, original.Ref(), LoadJobUpdate{})).To(MatchError(ErrStaleLoadJob))
						Expect(store.FailLoadJob(ctx, original.Ref(), "late failure")).To(MatchError(ErrStaleLoadJob))
						Expect(store.DeleteLoadJob(ctx, original.Ref())).To(MatchError(ErrStaleLoadJob))
						job, claimed, err := store.ClaimLoadJob(ctx, original.TrackingKey, "other")
						Expect(err).NotTo(HaveOccurred())
						Expect(claimed).To(BeFalse())
						Expect(job).To(Equal(current))
					})
				}
				for _, heartbeat := range []bool{false, true} {
					It(fmt.Sprintf("updates all supplied fields (heartbeat %t)", heartbeat), func() {
						u := LoadJobUpdate{State: LoadJobStateStaging, NodeID: "new-node", NodeName: "new-name", ReplicaIndex: 4,
							StartedAt: original.StartedAt.Add(time.Minute), BytesSent: 20, TotalBytes: 200, FileIndex: 2, TotalFiles: 4}
						expected := *original
						if heartbeat {
							u = LoadJobUpdate{ReplicaIndex: 9}
						} else {
							expected.State, expected.NodeID, expected.NodeName = u.State, u.NodeID, u.NodeName
							expected.ReplicaIndex, expected.StartedAt = u.ReplicaIndex, u.StartedAt
						}
						expected.BytesSent, expected.TotalBytes, expected.FileIndex, expected.TotalFiles = u.BytesSent, u.TotalBytes, u.FileIndex, u.TotalFiles
						before := time.Now().Add(-time.Microsecond)
						Expect(store.UpdateLoadJob(ctx, original.Ref(), u)).To(Succeed())
						after := time.Now().Add(time.Microsecond)
						current, err := store.GetLoadJob(ctx, original.TrackingKey)
						Expect(err).NotTo(HaveOccurred())
						Expect(current.LastProgress).To(BeTemporally(">=", before))
						Expect(current.LastProgress).To(BeTemporally("<=", after))
						Expect(current.UpdatedAt).To(BeTemporally("==", current.LastProgress))
						expected.LastProgress, expected.UpdatedAt = current.LastProgress, current.UpdatedAt
						Expect(current).To(Equal(&expected))
					})
				}
			})

			for _, scenario := range []struct {
				name                                                    string
				legacy, terminal, uncertain, future, reclaim, deletable bool
			}{
				{name: "active", uncertain: true, deletable: true},
				{name: "active confirmed", deletable: true},
				{name: "legacy active", legacy: true},
				{name: "legacy confirmed terminal", legacy: true, terminal: true},
				{name: "expired uncertain", terminal: true, uncertain: true},
				{name: "within grace confirmed", terminal: true, future: true},
				{name: "within grace uncertain", terminal: true, future: true, uncertain: true},
				{name: "expired confirmed", terminal: true, reclaim: true, deletable: true},
			} {
				Context(scenario.name, func() {
					var original ModelLoadJob
					BeforeEach(func() {
						past := time.Now().Add(-time.Hour)
						original = ModelLoadJob{TrackingKey: "parity-model", Generation: "generation-a", OwnerReplica: "frontend-a", State: LoadJobStateLoading, LastProgress: past, WorkUncertain: scenario.uncertain}
						if scenario.legacy {
							original.Generation = ""
						}
						if scenario.terminal {
							until := past
							if scenario.future {
								until = time.Now().Add(time.Hour)
							}
							original.TerminalUntil, original.State = &until, LoadJobStateFailed
						}
						seed(original)
					})
					It("rejects stale mutations without changing any fields or timestamps", func() {
						ctx := context.Background()
						before, err := store.GetLoadJob(ctx, original.TrackingKey)
						Expect(err).NotTo(HaveOccurred())
						refs := []LoadJobRef{{TrackingKey: original.TrackingKey, Generation: "stale"}, {TrackingKey: original.TrackingKey}}
						if scenario.terminal || scenario.legacy {
							refs = append(refs, original.Ref())
						}
						for _, ref := range refs {
							Expect(store.UpdateLoadJob(ctx, ref, LoadJobUpdate{State: LoadJobStateStaging, BytesSent: 99})).To(MatchError(ErrStaleLoadJob))
							Expect(store.FailLoadJob(ctx, ref, "stale failure")).To(MatchError(ErrStaleLoadJob))
							current, err := store.GetLoadJob(ctx, original.TrackingKey)
							Expect(err).NotTo(HaveOccurred())
							Expect(current).To(Equal(before))
						}
					})
					It("claims only after grace and confirmed termination", func() {
						ctx := context.Background()
						job, claimed, err := store.ClaimLoadJob(ctx, original.TrackingKey, "frontend-b")
						Expect(err).NotTo(HaveOccurred())
						Expect(claimed).To(Equal(scenario.reclaim))
						if !claimed {
							Expect(job.Ref()).To(Equal(original.Ref()))
							Expect(job.OwnerReplica).To(Equal(original.OwnerReplica))
							return
						}
						Expect(job.Generation).NotTo(BeEmpty())
						Expect(job.Generation).NotTo(Equal(original.Generation))
						Expect(job.OwnerReplica).To(Equal("frontend-b"))
						Expect(job.State).To(Equal(LoadJobStatePending))
						Expect(job.TerminalUntil).To(BeNil())
						Expect(job.WorkUncertain).To(BeTrue())
						Expect(store.UpdateLoadJob(ctx, original.Ref(), LoadJobUpdate{})).To(MatchError(ErrStaleLoadJob))
						Expect(store.FailLoadJob(ctx, original.Ref(), "late failure")).To(MatchError(ErrStaleLoadJob))
						Expect(store.DeleteLoadJob(ctx, original.Ref())).To(MatchError(ErrStaleLoadJob))
						current, err := store.GetLoadJob(ctx, original.TrackingKey)
						Expect(err).NotTo(HaveOccurred())
						Expect(current.Ref()).To(Equal(job.Ref()))
						Expect(current.State).To(Equal(job.State))
						Expect(current.OwnerReplica).To(Equal(job.OwnerReplica))
						Expect(current.TerminalUntil).To(BeNil())
						Expect(current.WorkUncertain).To(BeTrue())
					})
					It("conditionally deletes only the eligible generation", func() {
						ctx := context.Background()
						for _, ref := range []LoadJobRef{{TrackingKey: original.TrackingKey, Generation: "stale"}, {TrackingKey: original.TrackingKey}} {
							Expect(store.DeleteLoadJob(ctx, ref)).To(MatchError(ErrStaleLoadJob))
						}
						err := store.DeleteLoadJob(ctx, original.Ref())
						if scenario.deletable {
							Expect(err).NotTo(HaveOccurred())
						} else {
							Expect(err).To(MatchError(ErrStaleLoadJob))
						}
						current, err := store.GetLoadJob(ctx, original.TrackingKey)
						Expect(err).NotTo(HaveOccurred())
						if scenario.deletable {
							Expect(current).To(BeNil())
							Expect(store.DeleteLoadJob(ctx, original.Ref())).To(MatchError(ErrStaleLoadJob))
						} else {
							Expect(current.Ref()).To(Equal(original.Ref()))
						}
					})
				})
			}
		})
	}
})
