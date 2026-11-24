package upgraderun_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/mudler/LocalAI/core/services/upgraderun"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func env() Env { return Env{Config: Config{BackendLimit: 2, ModelLimit: 1}, Now: t0} }

func item(kind Kind, name string) Item {
	return Item{Key: Key(kind, name), Kind: kind, Name: name, From: "1", To: "2", ExpectedTo: "2"}
}

// step applies an event that must be accepted.
func step(r Run, ev Event) (Run, []Effect) {
	res := Reduce(r, ev, env())
	ExpectWithOffset(1, res.Err).ToNot(HaveOccurred())
	return res.Run, res.Effects
}

func newRun(items ...Item) (Run, []Effect) {
	return step(Run{}, RunRequested{ID: "run-1", Source: SourceManual, Actor: "admin", Items: items})
}

func phaseOf(r Run, key string) Phase { return r.Item(key).Phase }

func starts(fx []Effect) []string {
	var out []string
	for _, f := range fx {
		if s, ok := f.(StartJob); ok {
			out = append(out, s.Key)
		}
	}
	return out
}

// started runs an item to the point where its operation has a job.
func started(r Run, key, job string, cancellable bool) Run {
	r, _ = step(r, ItemStarted{Key: key, JobID: job, Cancellable: cancellable})
	return r
}

var _ = Describe("Reduce", func() {
	It("creates a running run, keeps skipped items and starts what may start", func() {
		held := item(KindModel, "held-model")
		held.Phase = PhaseSkipped
		r, fx := newRun(item(KindGallery, "a"), held)
		Expect(r.State).To(Equal(RunRunning))
		Expect(r.CreatedAt).To(Equal(t0))
		Expect(phaseOf(r, "gallery:a")).To(Equal(PhaseRunning))
		Expect(phaseOf(r, "model:held-model")).To(Equal(PhaseSkipped))
		Expect(starts(fx)).To(Equal([]string{"gallery:a"}))
	})

	It("drops duplicate keys", func() {
		r, _ := newRun(item(KindGallery, "a"), item(KindGallery, "a"))
		Expect(r.Items).To(HaveLen(1))
	})

	It("finishes at once when every item is skipped", func() {
		s := item(KindModel, "m")
		s.Phase = PhaseSkipped
		r, fx := newRun(s)
		Expect(r.State).To(Equal(RunFinished))
		Expect(r.FinishedAt).ToNot(BeNil())
		Expect(fx).To(ContainElement(RunEnded{State: RunFinished}))
	})

	It("never changes its input", func() {
		r, _ := newRun(item(KindGallery, "a"))
		before := r.Clone()
		_ = Reduce(r, ItemStarted{Key: "gallery:a", JobID: "j"}, env())
		Expect(r).To(Equal(before))
	})

	Describe("the updated invariant", func() {
		It("is updated only after the job ended without error and the check no longer lists it", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j1", true)
			// A check answer before the job ended is ignored.
			res := Reduce(r, ItemVerified{Key: "gallery:a", Result: VerifyGone}, env())
			Expect(res.Changed).To(BeFalse())
			Expect(phaseOf(res.Run, "gallery:a")).To(Equal(PhaseRunning))

			r, fx := step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobOK})
			Expect(phaseOf(r, "gallery:a")).To(Equal(PhaseRunning), "an ended job is not an update yet")
			Expect(fx).To(ContainElement(VerifyItem{Key: "gallery:a"}))
			Expect(r.State).To(Equal(RunRunning))

			r, fx = step(r, ItemVerified{Key: "gallery:a", Result: VerifyGone})
			Expect(phaseOf(r, "gallery:a")).To(Equal(PhaseUpdated))
			Expect(r.State).To(Equal(RunFinished))
			Expect(fx).To(ContainElement(RunEnded{State: RunFinished}))
		})

		It("fails as stale when the check still lists the item", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j1", true)
			r, _ = step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobOK})
			r, _ = step(r, ItemVerified{Key: "gallery:a", Result: VerifyListed})
			it := r.Item("gallery:a")
			Expect(it.Phase).To(Equal(PhaseFailed))
			Expect(it.ErrorKey).To(Equal(ErrKeyStale))
			Expect(it.Retryable).To(BeTrue())
		})

		It("is unconfirmed when the check cannot answer, and the run still ends", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j1", true)
			r, _ = step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobOK})
			r, _ = step(r, ItemVerified{Key: "gallery:a", Result: VerifyUnanswered})
			Expect(phaseOf(r, "gallery:a")).To(Equal(PhaseUnconfirmed))
			Expect(r.Item("gallery:a").Retryable).To(BeTrue())
			Expect(r.State).To(Equal(RunFinished))
		})

		It("fails without a check when the job failed, whatever a check would say", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j1", true)
			r, fx := step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobError, ErrorKey: ErrKeyDownload, Error: "network"})
			Expect(fx).ToNot(ContainElement(VerifyItem{Key: "gallery:a"}))
			it := r.Item("gallery:a")
			Expect(it.Phase).To(Equal(PhaseFailed))
			Expect(it.ErrorKey).To(Equal(ErrKeyDownload))
			Expect(it.Error).To(Equal("network"))
			res := Reduce(r, ItemVerified{Key: "gallery:a", Result: VerifyGone}, env())
			Expect(res.Err).To(HaveOccurred(), "the run is over")
			Expect(phaseOf(res.Run, "gallery:a")).To(Equal(PhaseFailed))
		})

		It("marks a failure that restored the old version as rolled back", func() {
			r, _ := newRun(item(KindModel, "m"))
			r = started(r, "model:m", "j", true)
			r, _ = step(r, ItemJobEnded{Key: "model:m", Outcome: JobError, RolledBack: true, ErrorKey: ErrKeyStart})
			Expect(phaseOf(r, "model:m")).To(Equal(PhaseRolledBack))
		})

		It("uses the unknown key when the failure has none", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j", true)
			r, _ = step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobError})
			Expect(r.Item("gallery:a").ErrorKey).To(Equal(ErrKeyUnknown))
			Expect(r.Item("gallery:a").Retryable).To(BeTrue())
		})

		DescribeTable("decides from the error key whether a failure can be retried",
			func(key string, rolledBack, retryable bool) {
				r, _ := newRun(item(KindAddon, "notes"))
				r = started(r, "addon:notes", "j", false)
				r, _ = step(r, ItemJobEnded{Key: "addon:notes", Outcome: JobError, ErrorKey: key, RolledBack: rolledBack, Error: "x"})
				Expect(r.Item("addon:notes").Retryable).To(Equal(retryable))
			},
			Entry("download", ErrKeyDownload, false, true),
			Entry("verify", ErrKeyVerify, false, true),
			Entry("swap", ErrKeySwap, false, true),
			Entry("start, rolled back", ErrKeyStart, true, true),
			Entry("unavailable", "unavailable", false, true),
			Entry("upgrade_failed", "upgrade_failed", false, true),
			Entry("interrupted", "interrupted", false, true),
			Entry("incompatible", "incompatible", false, false),
			Entry("integrity_error", "integrity_error", false, false),
			Entry("recovery_required", "recovery_required", true, false),
			Entry("forbidden", "forbidden", false, false),
			Entry("not_human_surface", "not_human_surface", false, false),
			Entry("approval_required", "approval_required", false, false),
			Entry("approval_stale", "approval_stale", false, false),
			Entry("upgrade_conflict", "upgrade_conflict", false, false),
			Entry("a code this build does not know", "future_code", false, false),
		)

		It("never marks a start failure retryable when its key says a retry is useless", func() {
			r, _ := newRun(item(KindAddon, "notes"), item(KindAddon, "tables"))
			r, _ = step(r, ItemStartFailed{Key: "addon:notes", ErrorKey: "incompatible", Error: "x", Retryable: true})
			Expect(r.Item("addon:notes").Retryable).To(BeFalse())
			r, _ = step(r, ItemStartFailed{Key: "addon:tables", ErrorKey: "unavailable", Error: "x", Retryable: true})
			Expect(r.Item("addon:tables").Retryable).To(BeTrue())
		})

		It("checks an item whose job status was lost", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j", true)
			r, fx := step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobLost})
			Expect(phaseOf(r, "gallery:a")).To(Equal(PhaseUnconfirmed))
			Expect(fx).To(ContainElement(VerifyItem{Key: "gallery:a"}))
			Expect(r.State).To(Equal(RunRunning))
		})
	})

	Describe("start failures", func() {
		It("fails the item with the key it was given", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r, _ = step(r, ItemStartFailed{Key: "gallery:a", ErrorKey: ErrKeyStale, Error: "moved", Retryable: true})
			Expect(phaseOf(r, "gallery:a")).To(Equal(PhaseFailed))
			Expect(r.Item("gallery:a").Retryable).To(BeTrue())
		})
		It("skips the item when asked", func() {
			r, _ := newRun(item(KindModel, "m"))
			r, _ = step(r, ItemStartFailed{Key: "model:m", ErrorKey: ErrKeyHeld, Skip: true})
			Expect(phaseOf(r, "model:m")).To(Equal(PhaseSkipped))
			Expect(r.Item("model:m").Retryable).To(BeFalse())
		})
	})

	Describe("progress", func() {
		It("records progress, message and nodes, clamped", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j", true)
			r, _ = step(r, ItemProgress{Key: "gallery:a", Progress: 140, Message: "downloading", Cancellable: true,
				Nodes: []NodeState{{ID: "n1", Name: "worker-1", Phase: "downloading", Progress: 30}}})
			it := r.Item("gallery:a")
			Expect(it.Progress).To(Equal(100))
			Expect(it.Message).To(Equal("downloading"))
			Expect(it.Nodes).To(HaveLen(1))
			Expect(it.Cancellable).To(BeTrue())
		})
		It("updates one node by id", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r, _ = step(r, NodeResult{Key: "gallery:a", Node: NodeState{ID: "n1", Phase: "running"}})
			r, _ = step(r, NodeResult{Key: "gallery:a", Node: NodeState{ID: "n2", Phase: "running"}})
			r, _ = step(r, NodeResult{Key: "gallery:a", Node: NodeState{ID: "n1", Phase: "done", Progress: 100}})
			Expect(r.Item("gallery:a").Nodes).To(Equal([]NodeState{{ID: "n1", Phase: "done", Progress: 100}, {ID: "n2", Phase: "running"}}))
		})
	})

	Describe("cancel", func() {
		It("drops a queued item without an operation to cancel", func() {
			r, _ := newRun(item(KindGallery, "a"), item(KindModel, "m"))
			Expect(phaseOf(r, "model:m")).To(Equal(PhaseQueued))
			r, fx := step(r, CancelItem{Key: "model:m"})
			Expect(phaseOf(r, "model:m")).To(Equal(PhaseCancelled))
			Expect(fx).ToNot(ContainElement(BeAssignableToTypeOf(CancelJob{})))
		})

		It("cancels a running item only while its operation says it can stop", func() {
			r, _ := newRun(item(KindModel, "m"))
			res := Reduce(r, CancelItem{Key: "model:m"}, env())
			Expect(CodeOf(res.Err)).To(Equal(CodeNotCancellable), "no job yet")

			r = started(r, "model:m", "j", true)
			r, _ = step(r, ItemProgress{Key: "model:m", Progress: 60, Cancellable: false})
			res = Reduce(r, CancelItem{Key: "model:m"}, env())
			Expect(CodeOf(res.Err)).To(Equal(CodeNotCancellable))

			r, _ = step(r, ItemProgress{Key: "model:m", Progress: 10, Cancellable: true})
			r, fx := step(r, CancelItem{Key: "model:m"})
			Expect(fx).To(ContainElement(CancelJob{Key: "model:m", JobID: "j"}))
			Expect(phaseOf(r, "model:m")).To(Equal(PhaseRunning), "it is cancelled when the job says so")

			_, fx = step(r, CancelItem{Key: "model:m"})
			Expect(fx).ToNot(ContainElement(BeAssignableToTypeOf(CancelJob{})), "asked once")

			r, _ = step(r, ItemJobEnded{Key: "model:m", Outcome: JobCancelled})
			Expect(phaseOf(r, "model:m")).To(Equal(PhaseCancelled))
			Expect(r.State).To(Equal(RunFinished))
		})

		It("refuses to cancel a settled item", func() {
			r, _ := newRun(item(KindGallery, "a"), item(KindGallery, "b"))
			r = started(r, "gallery:a", "j", true)
			r, _ = step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobError})
			res := Reduce(r, CancelItem{Key: "gallery:a"}, env())
			Expect(CodeOf(res.Err)).To(Equal(CodeNotCancellable))
			res = Reduce(r, CancelItem{Key: "gallery:zzz"}, env())
			Expect(CodeOf(res.Err)).To(Equal(CodeNotFound))
		})

		It("cancels the run: queued items drop, running ones stop when they can, and the run ends cancelled", func() {
			r, _ := newRun(item(KindGallery, "a"), item(KindGallery, "b"), item(KindModel, "m"))
			r = started(r, "gallery:a", "ja", true)
			r = started(r, "gallery:b", "jb", false)
			r, fx := step(r, CancelRequested{})
			Expect(phaseOf(r, "model:m")).To(Equal(PhaseCancelled))
			Expect(fx).To(ContainElement(CancelJob{Key: "gallery:a", JobID: "ja"}))
			Expect(fx).ToNot(ContainElement(CancelJob{Key: "gallery:b", JobID: "jb"}))
			Expect(r.State).To(Equal(RunRunning))

			// b becomes cancellable: the wanted cancel goes out then.
			r, fx = step(r, ItemProgress{Key: "gallery:b", Progress: 5, Cancellable: true})
			Expect(fx).To(ContainElement(CancelJob{Key: "gallery:b", JobID: "jb"}))

			r, _ = step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobCancelled})
			r, fx = step(r, ItemJobEnded{Key: "gallery:b", Outcome: JobOK})
			Expect(fx).To(ContainElement(VerifyItem{Key: "gallery:b"}), "an item that finished anyway is still checked")
			r, fx = step(r, ItemVerified{Key: "gallery:b", Result: VerifyGone})
			Expect(phaseOf(r, "gallery:b")).To(Equal(PhaseUpdated))
			Expect(r.State).To(Equal(RunCancelled))
			Expect(fx).To(ContainElement(RunEnded{State: RunCancelled}))
		})

		It("sends a wanted cancel as soon as the job id is known", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r, _ = step(r, CancelRequested{})
			_, fx := step(r, ItemStarted{Key: "gallery:a", JobID: "j", Cancellable: true})
			Expect(fx).To(ContainElement(CancelJob{Key: "gallery:a", JobID: "j"}))
		})

		It("starts nothing after a cancel", func() {
			r, _ := newRun(item(KindGallery, "a"), item(KindGallery, "b"), item(KindGallery, "c"))
			r = started(r, "gallery:a", "ja", false)
			r, _ = step(r, CancelRequested{})
			_, fx := step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobError})
			Expect(starts(fx)).To(BeEmpty())
		})
	})

	Describe("a finished run", func() {
		finished := func() Run {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j", true)
			r, _ = step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobError})
			Expect(r.State).To(Equal(RunFinished))
			return r
		}

		It("refuses every event but dismiss", func() {
			r := finished()
			for _, ev := range []Event{
				ItemProgress{Key: "gallery:a"}, ItemStarted{Key: "gallery:a"}, CancelRequested{}, CancelItem{Key: "gallery:a"},
				RetryRequested{}, RestartRecovered{}, NodeResult{Key: "gallery:a"}, ItemJobEnded{Key: "gallery:a", Outcome: JobOK},
			} {
				res := Reduce(r, ev, env())
				Expect(CodeOf(res.Err)).To(Equal(CodeRunFinished), "%T", ev)
				Expect(res.Run).To(Equal(r))
			}
		})

		It("can be dismissed once", func() {
			r := finished()
			r, _ = step(r, Dismiss{})
			Expect(r.Dismissed).To(BeTrue())
			res := Reduce(r, Dismiss{}, env())
			Expect(res.Changed).To(BeFalse())
		})

		It("cannot be dismissed while it runs", func() {
			r, _ := newRun(item(KindGallery, "a"))
			res := Reduce(r, Dismiss{}, env())
			Expect(CodeOf(res.Err)).To(Equal(CodeRunNotFinished))
		})
	})

	Describe("retry", func() {
		It("queues again only the retryable items", func() {
			r, _ := newRun(item(KindGallery, "a"), item(KindGallery, "b"), item(KindGallery, "c"), item(KindModel, "m"))
			r = started(r, "gallery:a", "ja", true)
			r = started(r, "gallery:b", "jb", true)
			r, _ = step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobError, Error: "boom"})
			// c starts in a's slot; cancel it while it runs.
			r = started(r, "gallery:c", "jc", true)
			r, _ = step(r, CancelItem{Key: "gallery:c"})
			r, _ = step(r, ItemJobEnded{Key: "gallery:c", Outcome: JobCancelled})
			Expect(phaseOf(r, "gallery:c")).To(Equal(PhaseCancelled))

			r, fx := step(r, RetryRequested{})
			Expect(starts(fx)).To(Equal([]string{"gallery:a"}))
			a := r.Item("gallery:a")
			Expect(a.Phase).To(Equal(PhaseRunning))
			Expect(a.Error).To(BeEmpty())
			Expect(a.JobID).To(BeEmpty())
			Expect(phaseOf(r, "gallery:c")).To(Equal(PhaseCancelled), "a cancelled item is not retryable")
		})

		It("does not retry an item whose failure needs a person first", func() {
			r, _ := newRun(item(KindAddon, "notes"))
			r = started(r, "addon:notes", "j", false)
			r, _ = step(r, ItemJobEnded{Key: "addon:notes", Outcome: JobError, ErrorKey: "recovery_required", Error: "x"})
			Expect(r.Active()).To(BeFalse())
			Expect(r.Item("addon:notes").RetryEligible()).To(BeFalse())
		})

		It("refuses when nothing can be retried", func() {
			r, _ := newRun(item(KindGallery, "a"), item(KindGallery, "b"))
			res := Reduce(r, RetryRequested{}, env())
			Expect(CodeOf(res.Err)).To(Equal(CodeNothingToRetry))
		})
	})

	Describe("restart recovery", func() {
		It("makes running items unconfirmed and checks them; queued items go on", func() {
			r, _ := newRun(item(KindGallery, "a"), item(KindModel, "m"))
			r = started(r, "gallery:a", "j", true)
			r, fx := step(r, RestartRecovered{})
			a := r.Item("gallery:a")
			Expect(a.Phase).To(Equal(PhaseUnconfirmed))
			Expect(a.Cancellable).To(BeFalse())
			Expect(fx).To(ContainElement(VerifyItem{Key: "gallery:a"}))
			Expect(phaseOf(r, "model:m")).To(Equal(PhaseQueued), "models still wait for the backend")

			r, fx = step(r, ItemVerified{Key: "gallery:a", Result: VerifyGone})
			Expect(phaseOf(r, "gallery:a")).To(Equal(PhaseUpdated))
			Expect(starts(fx)).To(Equal([]string{"model:m"}))
		})

		It("keeps following an item whose operation is still live, instead of checking it at once", func() {
			r, _ := newRun(item(KindGallery, "a"), item(KindGallery, "b"))
			r = started(r, "gallery:a", "ja", true)
			r = started(r, "gallery:b", "jb", true)
			r, fx := step(r, RestartRecovered{Live: map[string]bool{"gallery:a": true}})
			a := r.Item("gallery:a")
			Expect(a.Phase).To(Equal(PhaseRunning), "the operation goes on: the new runner follows it")
			Expect(a.JobID).To(Equal("ja"))
			Expect(fx).NotTo(ContainElement(VerifyItem{Key: "gallery:a"}))
			// An item whose operation has no status any more is checked.
			Expect(phaseOf(r, "gallery:b")).To(Equal(PhaseUnconfirmed))
			Expect(fx).To(ContainElement(VerifyItem{Key: "gallery:b"}))

			// The followed operation ends later and is checked then.
			r, fx = step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobOK})
			Expect(fx).To(ContainElement(VerifyItem{Key: "gallery:a"}))
			r, _ = step(r, ItemVerified{Key: "gallery:a", Result: VerifyGone})
			Expect(phaseOf(r, "gallery:a")).To(Equal(PhaseUpdated))
		})

		It("fails a recovered item the check still lists as interrupted by the restart", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j", true)
			r, _ = step(r, RestartRecovered{})
			r, _ = step(r, ItemVerified{Key: "gallery:a", Result: VerifyListed})
			a := r.Item("gallery:a")
			Expect(a.Phase).To(Equal(PhaseFailed))
			// The operation did not end: the restart stopped it.
			Expect(a.ErrorKey).To(Equal(ErrKeyInterrupted))
			Expect(a.Error).To(Equal("the update was interrupted by a restart"))
			Expect(a.Retryable).To(BeTrue())
		})

		It("keeps stale for an item whose operation ended and that the check still lists", func() {
			r, _ := newRun(item(KindGallery, "a"))
			r = started(r, "gallery:a", "j", true)
			r, _ = step(r, ItemJobEnded{Key: "gallery:a", Outcome: JobOK})
			r, _ = step(r, ItemVerified{Key: "gallery:a", Result: VerifyListed})
			Expect(r.Item("gallery:a").ErrorKey).To(Equal(ErrKeyStale))
		})
	})
})
