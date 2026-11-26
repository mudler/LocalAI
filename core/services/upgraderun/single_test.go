package upgraderun_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/distributed"
	. "github.com/mudler/LocalAI/core/services/upgraderun"
)

var _ = Describe("The outcome of a run of one item", func() {
	run := Run{ID: "run-1", State: RunRunning}

	It("is started when the item has a job", func() {
		out := SingleOutcome(run, Item{Key: "gallery:a", Phase: PhaseRunning, JobID: "job-1"})
		Expect(out.State).To(Equal(SingleStarted))
		Expect(out.JobID).To(Equal("job-1"))
		Expect(out.Err()).NotTo(HaveOccurred())
	})

	It("is refused when the item ended without a job, with its key and error", func() {
		out := SingleOutcome(run, Item{Key: "gallery:a", Phase: PhaseFailed, ErrorKey: "download", Error: "registry down"})
		Expect(out.State).To(Equal(SingleRefused))
		Expect(out.ErrorKey).To(Equal("download"))
		Expect(out.Error).To(Equal("registry down"))
		var refused *RefusedError
		Expect(errors.As(out.Err(), &refused)).To(BeTrue())
		Expect(refused.ErrorKey).To(Equal("download"))
	})

	It("gives the unknown key to a refusal without one", func() {
		out := SingleOutcome(run, Item{Key: "gallery:a", Phase: PhaseSkipped})
		Expect(out.State).To(Equal(SingleRefused))
		Expect(out.ErrorKey).To(Equal(ErrKeyUnknown))
	})

	DescribeTable("is pending while the item has no job and did not end",
		func(phase Phase) {
			out := SingleOutcome(run, Item{Key: "gallery:a", Phase: phase})
			Expect(out.State).To(Equal(SinglePending))
			Expect(out.JobID).To(BeEmpty())
			Expect(out.RunID).To(Equal("run-1"))
			var pending *PendingError
			Expect(errors.As(out.Err(), &pending)).To(BeTrue())
			Expect(pending.RunID).To(Equal("run-1"))
			Expect(out.Err().Error()).NotTo(ContainSubstring("started"))
		},
		Entry("queued", PhaseQueued),
		Entry("running, not yet accepted", PhaseRunning),
	)

	It("answers pending, not started, when StartOne waits out before the operation is accepted", func() {
		h := newHarness(harnessOpts{})
		h.backends.add(Candidate{Kind: KindGallery, Name: "slow"})
		gate := make(chan struct{})
		DeferCleanup(func() { close(gate) })
		h.starter.block(gate)
		ctx := context.Background()
		run, item, err := h.svc.StartOne(ctx, "admin", si(KindGallery, "slow"), SourceManual, 100*time.Millisecond)
		Expect(err).NotTo(HaveOccurred())
		out := SingleOutcome(run, item)
		Expect(out.State).To(Equal(SinglePending))
		Expect(out.RunID).To(Equal(run.ID))
	})

	It("stores the extra data of a start item with the item, beside the candidate's", func() {
		h := newHarness(harnessOpts{})
		h.addons.add(Candidate{Kind: KindAddon, Name: "notes", Extra: map[string]string{ExtraCandidateDigest: "sha256:b"}})
		gate := make(chan struct{})
		DeferCleanup(func() { close(gate) })
		h.starter.block(gate)
		item := si(KindAddon, "notes")
		item.Extra = map[string]string{ExtraOperationID: "op-1"}
		run, err := h.svc.Start(context.Background(), "admin", StartRequest{Items: []StartItem{item}})
		Expect(err).NotTo(HaveOccurred())
		_, rows, err := h.store.Get(run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Extra).To(ContainSubstring(`"operation_id":"op-1"`))
		Expect(rows[0].Extra).To(ContainSubstring(`"candidate_digest":"sha256:b"`))
	})

	It("holds the lock of a new run before its row exists, so no sweep can take it over", func() {
		db := openSQLite()
		h := newHarness(harnessOpts{db: db})
		h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
		var probed []bool
		Expect(db.Callback().Create().After("gorm:create").Register("test:probe-run-lock", func(tx *gorm.DB) {
			rec, ok := tx.Statement.Dest.(*distributed.UpgradeRunRecord)
			if !ok {
				return
			}
			// Another frontend's sweep tries the lock of the run as soon as
			// its row is written.
			other := advisorylock.NewHeldLock(db, advisorylock.KeyFromString("upgrade-run:"+rec.ID))
			got, err := other.TryAcquire(context.Background())
			Expect(err).NotTo(HaveOccurred())
			if got {
				other.Close()
			}
			probed = append(probed, got)
		})).To(Succeed())
		h.start(si(KindGallery, "kokoro"))
		Expect(probed).To(Equal([]bool{false}), "the creating frontend holds the lock before the row is written")
	})

	It("shows the page an item whose update the server still checks", func() {
		raw, err := json.Marshal(Item{Key: "gallery:a", Phase: PhaseUnconfirmed, Verifying: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"verifying":true`))
		raw, err = json.Marshal(Item{Key: "gallery:a", Phase: PhaseUnconfirmed})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(`verifying`))
	})

	It("waits in Close for the start of an operation it began", func() {
		h := newHarness(harnessOpts{})
		h.backends.add(Candidate{Kind: KindGallery, Name: "slow"})
		gate := make(chan struct{})
		DeferCleanup(func() { close(gate) })
		h.starter.block(gate)
		var returned atomic.Bool
		h.starter.onReturn(func() {
			time.Sleep(100 * time.Millisecond)
			returned.Store(true)
		})
		r := h.start(si(KindGallery, "slow"))
		Eventually(func() Phase { return h.phase(r.ID, "gallery:slow") }).Should(Equal(PhaseRunning))
		time.Sleep(50 * time.Millisecond)
		h.svc.Close()
		Expect(returned.Load()).To(BeTrue(), "Close returned before the start goroutine ended")
	})
})
