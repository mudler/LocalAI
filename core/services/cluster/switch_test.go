package cluster_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// fakeWorkers is the list of workers that the switch reads.
type fakeWorkers struct {
	mu      sync.Mutex
	workers []cluster.WorkerInfo
}

func (f *fakeWorkers) Workers(context.Context) ([]cluster.WorkerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.workers), nil
}

func (f *fakeWorkers) set(w ...cluster.WorkerInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workers = w
}

type fakeWork struct{ inflight cluster.InFlight }

func (f *fakeWork) InFlight(context.Context) (cluster.InFlight, error) { return f.inflight, nil }

var _ = Describe("The switch of the carrier", func() {
	var (
		db      *gorm.DB
		ctx     context.Context
		store   *cluster.CarrierStore
		reg     *cluster.Registry
		workers *fakeWorkers
		work    *fakeWork
		sw      *cluster.Switch
		timings cluster.Timings
	)

	// replica registers a live replica. It reports the carriers it can build.
	replica := func(id string, canTunnel, canNATS string) {
		GinkgoHelper()
		Expect(reg.Register(ctx, id, "v-"+id, 0, "")).To(Succeed())
		Expect(reg.ReportAvailability(ctx, id, map[cluster.Carrier]string{
			cluster.CarrierTunnel: canTunnel,
			cluster.CarrierNATS:   canNATS,
		})).To(Succeed())
	}
	ready := func(id string, epoch int64, reason string) {
		GinkgoHelper()
		Expect(reg.ReportReady(ctx, id, epoch, reason)).To(Succeed())
	}
	row := func() cluster.CarrierRow {
		GinkgoHelper()
		r, err := store.Get(ctx)
		Expect(err).ToNot(HaveOccurred())
		return r
	}
	request := func(target cluster.Carrier, force bool) (cluster.CarrierRow, cluster.Report, error) {
		return sw.Request(ctx, cluster.Request{Target: target, By: "admin", Force: force})
	}

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		var err error
		store, err = cluster.NewCarrierStore(db)
		Expect(err).ToNot(HaveOccurred())
		_, _, err = store.Seed(ctx, cluster.CarrierNATS, "replica-a")
		Expect(err).ToNot(HaveOccurred())
		reg = cluster.NewRegistry(db)
		workers, work = &fakeWorkers{}, &fakeWork{}
		timings = cluster.Timings{PrepareTimeout: time.Minute, TransitionWindow: time.Minute, MaxDrain: time.Minute}
		sw, err = cluster.NewSwitch(cluster.SwitchOptions{
			Store: store, Registry: reg, Workers: workers, Work: work,
			Timings: func() cluster.Timings { return timings },
		})
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("the preflight", func() {
		It("passes when every live replica can build the target and every worker can follow", func() {
			replica("a", "", "")
			replica("b", "", "")
			workers.set(cluster.WorkerInfo{ID: "w1", Name: "w1", Attached: []cluster.Carrier{cluster.CarrierNATS}, Reports: true,
				Follow: []cluster.Carrier{cluster.CarrierNATS, cluster.CarrierTunnel}})

			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.Blockers).To(BeEmpty())
			Expect(report.OK).To(BeTrue())
			Expect(report.Active).To(Equal(cluster.CarrierNATS))
			Expect(report.Target).To(Equal(cluster.CarrierTunnel))
		})

		It("lists the live replicas with their versions, so the admin can check the list is complete", func() {
			replica("a", "", "")
			replica("b", "", "")
			Expect(reg.Register(ctx, "dead", "v-dead", 0, "")).To(Succeed())
			Expect(db.Exec(`UPDATE instances SET last_seen = now() - interval '10 minutes' WHERE id = 'dead'`).Error).To(Succeed())

			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			var ids []string
			for _, r := range report.Replicas {
				ids = append(ids, r.ID)
				Expect(r.Version).To(Equal("v-" + r.ID))
			}
			Expect(ids).To(Equal([]string{"a", "b"}))
		})

		It("blocks on a replica that never said whether it can build the target", func() {
			Expect(reg.Register(ctx, "a", "v1", 0, "")).To(Succeed())
			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.OK).To(BeFalse())
			Expect(report.Blockers).To(ConsistOf(HaveField("ID", "a")))
			Expect(report.Blockers[0].Kind).To(Equal(cluster.BlockerReplica))
			Expect(report.Blockers[0].Reason).To(ContainSubstring("has not reported"))
		})

		It("blocks on a replica that says why it cannot build the target", func() {
			replica("a", "", "")
			replica("b", "LISTEN is refused by the database", "")
			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.Blockers).To(ConsistOf(SatisfyAll(
				HaveField("ID", "b"),
				HaveField("Reason", ContainSubstring("LISTEN is refused by the database")),
			)))
		})

		It("ignores what a replica reported long ago", func() {
			replica("a", "", "")
			Expect(db.Exec(`UPDATE instances SET availability_at = now() - interval '1 hour' WHERE id = 'a'`).Error).To(Succeed())
			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.Blockers).To(ConsistOf(HaveField("Reason", ContainSubstring("stale"))))
		})

		It("blocks on a worker that reports no capabilities, and says why", func() {
			replica("a", "", "")
			workers.set(cluster.WorkerInfo{ID: "old", Name: "old-worker", Attached: []cluster.Carrier{cluster.CarrierNATS}})
			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.OK).To(BeFalse())
			Expect(report.Blockers).To(ConsistOf(SatisfyAll(
				HaveField("Kind", cluster.BlockerWorker),
				HaveField("ID", "old"),
				HaveField("Reason", ContainSubstring("predates")),
			)))
			Expect(report.Workers).To(ConsistOf(SatisfyAll(HaveField("ID", "old"), HaveField("CanFollow", false))))
		})

		It("blocks on a worker that cannot follow, with the reason it reported", func() {
			replica("a", "", "")
			workers.set(cluster.WorkerInfo{ID: "w", Name: "nats-only", Reports: true, Attached: []cluster.Carrier{cluster.CarrierNATS},
				Follow: []cluster.Carrier{cluster.CarrierNATS}, FollowError: "has no tunnel credential"})
			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.Blockers).To(ConsistOf(SatisfyAll(
				HaveField("ID", "w"),
				HaveField("Reason", ContainSubstring("has no tunnel credential")),
			)))
		})

		It("does not block on a worker that is attached to the target already", func() {
			replica("a", "", "")
			workers.set(cluster.WorkerInfo{ID: "w", Name: "w", Attached: []cluster.Carrier{cluster.CarrierTunnel}})
			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.Blockers).To(BeEmpty())
		})

		It("reports what is in flight, and does not block on it", func() {
			replica("a", "", "")
			work.inflight = cluster.InFlight{Loads: 2, Jobs: 3, PendingClaims: 4, ClaimedClaims: 1}
			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.OK).To(BeTrue())
			Expect(report.InFlight).To(Equal(work.inflight))
		})

		It("refuses a target that is already active", func() {
			replica("a", "", "")
			report, err := sw.Preflight(ctx, cluster.CarrierNATS)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.Blockers).To(ConsistOf(HaveField("Kind", cluster.BlockerSame)))
			Expect(report.Blockers[0].Forceable).To(BeFalse())
		})

		It("refuses a carrier that the cluster does not know", func() {
			_, err := sw.Preflight(ctx, cluster.Carrier("pigeon"))
			Expect(err).To(MatchError(cluster.ErrInvalidCarrier))
		})

		It("says the cluster is busy while a change is under way", func() {
			replica("a", "", "")
			_, _, err := request(cluster.CarrierTunnel, false)
			Expect(err).ToNot(HaveOccurred())
			report, err := sw.Preflight(ctx, cluster.CarrierTunnel)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.Blockers).To(ConsistOf(HaveField("Kind", cluster.BlockerBusy)))
		})

		It("warns that two drains overlap when a change starts before the last one has drained", func() {
			replica("a", "", "")
			_, _, err := request(cluster.CarrierTunnel, false)
			Expect(err).ToNot(HaveOccurred())
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed()) // commit
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed()) // stable, draining
			Expect(row().Draining).To(Equal(cluster.CarrierNATS))

			report, err := sw.Preflight(ctx, cluster.CarrierNATS)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.OK).To(BeTrue())
			Expect(report.Warnings).To(ContainElement(ContainSubstring("still draining")))
		})
	})

	Describe("a request", func() {
		It("moves a stable cluster to prepare and names the target", func() {
			replica("a", "", "")
			got, report, err := request(cluster.CarrierTunnel, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(report.OK).To(BeTrue())
			Expect(got.State).To(Equal(cluster.StatePrepare))
			Expect(got.Target).To(Equal(cluster.CarrierTunnel))
			Expect(got.Active).To(Equal(cluster.CarrierNATS))
			Expect(got.Epoch).To(Equal(int64(2)))
			Expect(got.ChangedBy).To(Equal("admin"))
			Expect(got.Force).To(BeFalse())
		})

		It("changes nothing and returns the report when the preflight blocks", func() {
			replica("a", "", "")
			workers.set(cluster.WorkerInfo{ID: "old", Name: "old"})
			_, _, err := request(cluster.CarrierTunnel, false)
			var blocked *cluster.BlockedError
			Expect(errors.As(err, &blocked)).To(BeTrue())
			Expect(blocked.Report.Blockers).ToNot(BeEmpty())
			Expect(row().Epoch).To(Equal(int64(1)))
			Expect(row().State).To(Equal(cluster.StateStable))
		})

		It("goes ahead past a blocker that can be forced, and records that it was forced", func() {
			replica("a", "", "")
			workers.set(cluster.WorkerInfo{ID: "old", Name: "old"})
			got, _, err := request(cluster.CarrierTunnel, true)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.State).To(Equal(cluster.StatePrepare))
			Expect(got.Force).To(BeTrue())
		})

		It("does not let force start a change to the carrier that is active", func() {
			replica("a", "", "")
			_, _, err := request(cluster.CarrierNATS, true)
			var blocked *cluster.BlockedError
			Expect(errors.As(err, &blocked)).To(BeTrue())
		})

		It("is busy while another change is under way", func() {
			replica("a", "", "")
			_, _, err := request(cluster.CarrierTunnel, false)
			Expect(err).ToNot(HaveOccurred())
			_, _, err = request(cluster.CarrierTunnel, false)
			Expect(err).To(MatchError(cluster.ErrBusy))
		})

		It("lets one of two concurrent requests win", func() {
			replica("a", "", "")
			var won, busy atomic.Int32
			start := make(chan struct{})
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					<-start
					_, _, err := request(cluster.CarrierTunnel, false)
					switch {
					case err == nil:
						won.Add(1)
					case errors.Is(err, cluster.ErrBusy):
						busy.Add(1)
					default:
						Fail(err.Error())
					}
				})
			}
			close(start)
			wg.Wait()
			Expect(won.Load()).To(Equal(int32(1)))
			Expect(busy.Load()).To(Equal(int32(3)))
			Expect(row().Epoch).To(Equal(int64(2)))
		})
	})

	Describe("the status", func() {
		It("reports the row, the replicas and the workers without a target, and the time the drain has to go", func() {
			replica("a", "", "")
			workers.set(cluster.WorkerInfo{ID: "w1", Name: "w1", Attached: []cluster.Carrier{cluster.CarrierNATS}, Reports: true, Follow: []cluster.Carrier{cluster.CarrierNATS}})
			status, err := sw.Status(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(status.Active).To(Equal(cluster.CarrierNATS))
			Expect(status.Target).To(BeEmpty())
			Expect(status.Blockers).To(BeEmpty(), "a status is not a dry run")
			Expect(status.Replicas).To(HaveLen(1))
			Expect(status.Workers).To(HaveLen(1))
			Expect(status.DrainRemaining).To(BeZero())

			_, _, err = request(cluster.CarrierTunnel, false)
			Expect(err).ToNot(HaveOccurred())
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			status, err = sw.Status(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(status.Row.Draining).To(Equal(cluster.CarrierNATS))
			Expect(status.DrainRemaining).To(BeNumerically(">", 0))
			Expect(status.DrainRemaining).To(BeNumerically("<=", timings.MaxDrain))
		})
	})

	Describe("the hint", func() {
		It("tells the hook about every move of the row, and about no move that failed", func() {
			var epochs []int64
			var mu sync.Mutex
			hinted, err := cluster.NewSwitch(cluster.SwitchOptions{
				Store: store, Registry: reg, Workers: workers, Work: work,
				Timings:  func() cluster.Timings { return timings },
				OnChange: func(r cluster.CarrierRow) { mu.Lock(); epochs = append(epochs, r.Epoch); mu.Unlock() },
			})
			Expect(err).ToNot(HaveOccurred())
			replica("a", "", "")

			_, _, err = hinted.Request(ctx, cluster.Request{Target: cluster.CarrierTunnel, By: "admin"})
			Expect(err).ToNot(HaveOccurred())
			_, _, err = hinted.Request(ctx, cluster.Request{Target: cluster.CarrierTunnel, By: "admin"})
			Expect(err).To(MatchError(cluster.ErrBusy))
			ready("a", row().Epoch, "")
			Expect(hinted.Drive(ctx)).To(Succeed())
			Expect(hinted.Drive(ctx)).To(Succeed()) // nothing is due: a has not confirmed

			mu.Lock()
			defer mu.Unlock()
			Expect(epochs).To(Equal([]int64{2, 3}))
		})
	})

	Describe("an abort", func() {
		It("returns a cluster in prepare to stable on the carrier it had", func() {
			replica("a", "", "")
			_, _, err := request(cluster.CarrierTunnel, false)
			Expect(err).ToNot(HaveOccurred())

			got, err := sw.Abort(ctx, "admin-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.State).To(Equal(cluster.StateStable))
			Expect(got.Active).To(Equal(cluster.CarrierNATS))
			Expect(got.Target).To(BeEmpty())
			Expect(got.Epoch).To(Equal(int64(3)))
			Expect(got.Note).To(ContainSubstring("aborted by admin-2"))
		})

		It("is refused when nothing is being prepared", func() {
			_, err := sw.Abort(ctx, "admin")
			Expect(err).To(MatchError(cluster.ErrNotAbortable))
		})

		It("is refused after the commit, because the target is active", func() {
			replica("a", "", "")
			_, _, err := request(cluster.CarrierTunnel, false)
			Expect(err).ToNot(HaveOccurred())
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			Expect(row().State).To(Equal(cluster.StateCommit))
			_, err = sw.Abort(ctx, "admin")
			Expect(err).To(MatchError(cluster.ErrNotAbortable))
		})
	})

	Describe("the leader", func() {
		prepare := func(force bool) {
			GinkgoHelper()
			_, _, err := request(cluster.CarrierTunnel, force)
			Expect(err).ToNot(HaveOccurred())
		}

		It("does nothing to a stable cluster", func() {
			replica("a", "", "")
			Expect(sw.Drive(ctx)).To(Succeed())
			Expect(row().Epoch).To(Equal(int64(1)))
		})

		It("commits when every live replica is ready for the epoch of the prepare", func() {
			replica("a", "", "")
			replica("b", "", "")
			prepare(false)
			epoch := row().Epoch
			ready("a", epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			Expect(row().State).To(Equal(cluster.StatePrepare), "b is not ready")

			ready("b", epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			got := row()
			Expect(got.State).To(Equal(cluster.StateCommit))
			Expect(got.Active).To(Equal(cluster.CarrierTunnel))
			Expect(got.Draining).To(Equal(cluster.CarrierNATS))
			Expect(got.DrainingUntil).ToNot(BeNil())
			Expect(got.Epoch).To(Equal(epoch + 1))
		})

		It("does not count a replica that is ready for another epoch", func() {
			replica("a", "", "")
			prepare(false)
			ready("a", row().Epoch-1, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			Expect(row().State).To(Equal(cluster.StatePrepare))
		})

		It("ignores a replica that stopped heartbeating", func() {
			replica("a", "", "")
			replica("b", "", "")
			prepare(false)
			ready("a", row().Epoch, "")
			Expect(db.Exec(`UPDATE instances SET last_seen = now() - interval '10 minutes' WHERE id = 'b'`).Error).To(Succeed())
			Expect(sw.Drive(ctx)).To(Succeed())
			Expect(row().State).To(Equal(cluster.StateCommit))
		})

		It("aborts at once when a replica says it cannot build the target", func() {
			replica("a", "", "")
			replica("b", "", "")
			prepare(false)
			epoch := row().Epoch
			ready("a", epoch, "")
			ready("b", epoch, "cannot reach the NATS server")
			Expect(sw.Drive(ctx)).To(Succeed())
			got := row()
			Expect(got.State).To(Equal(cluster.StateStable))
			Expect(got.Active).To(Equal(cluster.CarrierNATS))
			Expect(got.Note).To(ContainSubstring("b"))
			Expect(got.Note).To(ContainSubstring("cannot reach the NATS server"))
		})

		It("aborts when a replica is not ready within the prepare timeout, and names it", func() {
			timings.PrepareTimeout = 300 * time.Millisecond
			replica("a", "", "")
			replica("b", "", "")
			prepare(false)
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			Expect(row().State).To(Equal(cluster.StatePrepare))

			Eventually(func() cluster.State {
				Expect(sw.Drive(ctx)).To(Succeed())
				return row().State
			}, "10s", "100ms").Should(Equal(cluster.StateStable))
			got := row()
			Expect(got.Active).To(Equal(cluster.CarrierNATS))
			Expect(got.Note).To(ContainSubstring("b"))
			Expect(got.Note).To(ContainSubstring("not ready"))
		})

		It("commits without a replica that is late, when the request was forced, and names it", func() {
			timings.PrepareTimeout = 300 * time.Millisecond
			replica("a", "", "")
			replica("b", "", "")
			prepare(true)
			ready("a", row().Epoch, "")

			Eventually(func() cluster.State {
				Expect(sw.Drive(ctx)).To(Succeed())
				return row().State
			}, "10s", "100ms").Should(Equal(cluster.StateCommit))
			got := row()
			Expect(got.Active).To(Equal(cluster.CarrierTunnel))
			Expect(got.Note).To(ContainSubstring("without"))
			Expect(got.Note).To(ContainSubstring("b"))
		})

		It("does not wait for the timeout for a forced change when a replica says it cannot build the target", func() {
			replica("a", "", "")
			replica("b", "", "")
			prepare(true)
			epoch := row().Epoch
			ready("a", epoch, "")
			ready("b", epoch, "cannot reach the NATS server")
			Expect(sw.Drive(ctx)).To(Succeed())
			got := row()
			Expect(got.State).To(Equal(cluster.StateCommit))
			Expect(got.Note).To(ContainSubstring("b"))
		})

		It("settles to stable when every live replica confirms the commit", func() {
			replica("a", "", "")
			replica("b", "", "")
			prepare(false)
			ready("a", row().Epoch, "")
			ready("b", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			commit := row()
			ready("a", commit.Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			Expect(row().State).To(Equal(cluster.StateCommit), "b has not confirmed")

			ready("b", commit.Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			got := row()
			Expect(got.State).To(Equal(cluster.StateStable))
			Expect(got.Active).To(Equal(cluster.CarrierTunnel))
			Expect(got.Draining).To(Equal(cluster.CarrierNATS), "the previous carrier stays attached")
			Expect(got.DrainingUntil).To(Equal(commit.DrainingUntil))
		})

		It("settles to stable after the transition window when a replica never confirms", func() {
			timings.TransitionWindow = 300 * time.Millisecond
			replica("a", "", "")
			prepare(false)
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			Eventually(func() cluster.State {
				Expect(sw.Drive(ctx)).To(Succeed())
				return row().State
			}, "10s", "100ms").Should(Equal(cluster.StateStable))
			Expect(row().Note).To(ContainSubstring("a"))
		})

		It("ends the drain when its time is over, and not before", func() {
			timings.MaxDrain = 500 * time.Millisecond
			replica("a", "", "")
			prepare(false)
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			stable := row()
			Expect(stable.State).To(Equal(cluster.StateStable))
			Expect(stable.Draining).To(Equal(cluster.CarrierNATS))

			Expect(sw.Drive(ctx)).To(Succeed())
			Expect(row().Epoch).To(Equal(stable.Epoch), "the drain is not over")

			Eventually(func() cluster.Carrier {
				Expect(sw.Drive(ctx)).To(Succeed())
				return row().Draining
			}, "10s", "100ms").Should(BeEmpty())
			got := row()
			Expect(got.State).To(Equal(cluster.StateStable))
			Expect(got.DrainingUntil).To(BeNil())
			Expect(got.Active).To(Equal(cluster.CarrierTunnel))
			Expect(got.Epoch).To(Equal(stable.Epoch + 1))
		})

		It("makes one transition when two leaders run the same step", func() {
			replica("a", "", "")
			prepare(false)
			ready("a", row().Epoch, "")
			epoch := row().Epoch
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() { _ = sw.Drive(ctx) })
			}
			wg.Wait()
			Expect(row().Epoch).To(Equal(epoch+1), "the step is a compare-and-set, so a second leader changes nothing")
			Expect(row().State).To(Equal(cluster.StateCommit))
		})

		It("goes on from the row alone, as a leader that takes over would", func() {
			replica("a", "", "")
			prepare(false)
			ready("a", row().Epoch, "")
			// A new Switch over the same database has no memory of the first.
			other, err := cluster.NewSwitch(cluster.SwitchOptions{Store: store, Registry: reg, Timings: func() cluster.Timings { return timings }})
			Expect(err).ToNot(HaveOccurred())
			Expect(other.Drive(ctx)).To(Succeed())
			Expect(row().State).To(Equal(cluster.StateCommit))
		})

		It("keeps the previous drain when a change starts and ends before it has drained", func() {
			replica("a", "", "")
			prepare(false)
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			ready("a", row().Epoch, "")
			Expect(sw.Drive(ctx)).To(Succeed())
			draining := row()

			_, _, err := request(cluster.CarrierNATS, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(row().Draining).To(Equal(draining.Draining), "the previous drain is not forgotten by the next prepare")
			_, err = sw.Abort(ctx, "admin")
			Expect(err).ToNot(HaveOccurred())
			Expect(row().Draining).To(Equal(draining.Draining))
			Expect(row().DrainingUntil).ToNot(BeNil())
		})
	})
})
