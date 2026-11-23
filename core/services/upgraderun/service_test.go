package upgraderun_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	. "github.com/mudler/LocalAI/core/services/upgraderun"
)

var _ = Describe("Service", func() {
	var h *harness
	ctx := context.Background()

	BeforeEach(func() {
		h = newHarness(harnessOpts{})
	})

	Describe("one run at a time", func() {
		It("refuses a start with run_active before it checks the items", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro", To: "v2"})
			h.start(StartItem{Kind: KindGallery, Name: "kokoro"})
			// An item no check lists: without the early refusal it answered
			// no_update, and a route fell back to a direct upgrade beside the
			// run.
			_, err := h.svc.Start(ctx, "admin", StartRequest{Items: []StartItem{{Kind: KindGallery, Name: "not-listed"}}})
			Expect(CodeOf(err)).To(Equal(CodeRunActive))
			Expect(CodeOf(h.svc.RefuseIfActive())).To(Equal(CodeRunActive))
		})

		It("lets a direct upgrade through when no run runs", func() {
			Expect(h.svc.RefuseIfActive()).To(Succeed())
		})
	})

	Describe("Preview", func() {
		It("orders backends, models and add-ons, selects backends and add-ons, and says why an item cannot start", func() {
			h.addons.add(Candidate{Kind: KindAddon, Name: "notes"})
			h.models.add(Candidate{Kind: KindModel, Name: "qwen3-8b"})
			h.models.add(Candidate{Kind: KindModel, Name: "held-one", Held: true})
			h.models.add(Candidate{Kind: KindModel, Name: "edited-one", EditedKeys: []string{"parameters.temperature"}})
			h.backends.add(Candidate{Kind: KindLibrary, Name: "lib/whisper", Risks: []string{RiskCluster}})
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro", To: "v3"})

			plan, err := h.svc.Preview(ctx, "admin", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(plan.Complete).To(BeTrue())
			keys := []string{}
			for _, it := range plan.Items {
				keys = append(keys, it.Key)
			}
			Expect(keys).To(Equal([]string{
				"gallery:kokoro", "library:lib/whisper", "model:edited-one", "model:held-one", "model:qwen3-8b", "addon:notes",
			}))
			byKey := map[string]PlanItem{}
			for _, it := range plan.Items {
				byKey[it.Key] = it
			}
			Expect(byKey["gallery:kokoro"].ExpectedTo).To(Equal("v3"))
			Expect(byKey["gallery:kokoro"].Selected).To(BeTrue())
			Expect(byKey["addon:notes"].Selected).To(BeTrue())
			Expect(byKey["addon:notes"].Stage).To(Equal(StageAddons))
			Expect(byKey["model:qwen3-8b"].Startable).To(BeTrue())
			Expect(byKey["model:qwen3-8b"].Selected).To(BeFalse(), "models are not selected by default")
			Expect(byKey["model:held-one"].Reason).To(Equal(ReasonHeld))
			Expect(byKey["model:edited-one"].Reason).To(Equal(ReasonEditedKeys))
			Expect(byKey["library:lib/whisper"].Risks).To(ConsistOf(RiskCluster))
			Expect(byKey["model:qwen3-8b"].Order).To(Equal(4))
		})

		It("shows the versions and the title of an add-on, and pins the digest of its candidate", func() {
			h.addons.add(AddonCandidate(AddonVersions{ID: "notes", Title: "Notes sync",
				InstalledVersion: "0.8.1", InstalledDigest: "sha256:aa", CandidateVersion: "0.8.2", CandidateDigest: "sha256:bb"}))
			plan, err := h.svc.Preview(ctx, "admin", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(plan.Items).To(HaveLen(1))
			it := plan.Items[0]
			Expect(it.Title).To(Equal("Notes sync"))
			Expect(it.From).To(Equal("0.8.1"))
			Expect(it.To).To(Equal("0.8.2"))
			Expect(it.ExpectedTo).To(Equal("sha256:bb"), "a request pins the digest, not the version")

			// The version is not the pin: a version tag that moved to other
			// content would otherwise pass.
			_, err = h.svc.Start(ctx, "admin", StartRequest{Items: []StartItem{{Kind: KindAddon, Name: "notes", ExpectedTo: "0.8.2"}}})
			Expect(CodeOf(err)).To(Equal(CodeStale))

			run := h.start(StartItem{Kind: KindAddon, Name: "notes", ExpectedTo: "sha256:bb"})
			item := run.Item("addon:notes")
			Expect(item.From).To(Equal("0.8.1"))
			Expect(item.To).To(Equal("0.8.2"))
			Expect(item.ExpectedTo).To(Equal("sha256:bb"))
			Expect(item.DisplayName()).To(Equal("Notes sync"))
			_, rows, err := h.store.Get(run.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(rows[0].Title).To(Equal("Notes sync"), "the title is stored with the item")

			// The fresh check after the job compares the digest too: the
			// add-on is updated once the check no longer lists that digest.
			job := h.jobOf("addon:notes")
			h.addons.remove("addon:notes")
			h.jobs.complete(job)
			Eventually(func() Phase { return h.phase(run.ID, "addon:notes") }).Should(Equal(PhaseUpdated))
		})

		It("lists a requested item with no update as not startable, and flags items of the running run", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a"})
			h.backends.add(Candidate{Kind: KindGallery, Name: "b"})
			h.start(si(KindGallery, "a"))
			plan, err := h.svc.Preview(ctx, "admin", []ItemRef{{Kind: KindGallery, Name: "a"}, {Kind: KindGallery, Name: "b"}, {Kind: KindModel, Name: "gone"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(plan.ActiveRunID).NotTo(BeEmpty())
			Expect(plan.Items).To(HaveLen(3))
			Expect(plan.Items[0].Reason).To(Equal(ReasonInRun))
			Expect(plan.Items[1].Startable).To(BeTrue())
			Expect(plan.Items[2].Reason).To(Equal(ReasonNoUpdate))
		})

		It("says the plan is incomplete when a source did not answer", func() {
			h.models.setIncomplete(true)
			plan, err := h.svc.Preview(ctx, "admin", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(plan.Complete).To(BeFalse())
		})
	})

	Describe("Start", func() {
		It("refuses an item without an update, a moved target and an empty request", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a", To: "v2"})
			_, err := h.svc.Start(ctx, "admin", StartRequest{Items: []StartItem{{Kind: KindGallery, Name: "zzz"}}})
			Expect(CodeOf(err)).To(Equal(CodeNoUpdate))
			_, err = h.svc.Start(ctx, "admin", StartRequest{Items: []StartItem{{Kind: KindGallery, Name: "a", ExpectedTo: "v1"}}})
			Expect(CodeOf(err)).To(Equal(CodeStale))
			Expect(errors.Is(err, &Error{Code: CodeStale})).To(BeTrue())
			_, err = h.svc.Start(ctx, "admin", StartRequest{})
			Expect(CodeOf(err)).To(Equal(CodeInvalid))
			_, err = h.svc.Start(ctx, "admin", StartRequest{Items: []StartItem{{Kind: "nope", Name: "x"}}})
			Expect(CodeOf(err)).To(Equal(CodeInvalid))
			_, err = h.svc.Start(ctx, "admin", StartRequest{Source: "cron", Items: []StartItem{{Kind: KindGallery, Name: "a"}}})
			Expect(CodeOf(err)).To(Equal(CodeInvalid))
		})

		It("says the source did not answer instead of no update", func() {
			h.models.setIncomplete(true)
			_, err := h.svc.Start(ctx, "admin", StartRequest{Items: []StartItem{si(KindModel, "m")}})
			Expect(CodeOf(err)).To(Equal(CodeSourceUnchecked))
		})

		It("refuses an item the server cannot install", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a", Blocked: "installs_disabled"})
			_, err := h.svc.Start(ctx, "admin", StartRequest{Items: []StartItem{si(KindGallery, "a")}})
			Expect(CodeOf(err)).To(Equal(CodeNoUpdate))
		})

		It("refuses a second run while one runs", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a"})
			h.start(si(KindGallery, "a"))
			_, err := h.svc.Start(ctx, "admin", StartRequest{Items: []StartItem{si(KindGallery, "a")}})
			Expect(CodeOf(err)).To(Equal(CodeRunActive))
		})

		It("keeps held and edited models as skipped items", func() {
			h.models.add(Candidate{Kind: KindModel, Name: "held", Held: true})
			h.models.add(Candidate{Kind: KindModel, Name: "edited", EditedKeys: []string{"k"}})
			r := h.start(si(KindModel, "held"), si(KindModel, "edited"))
			Expect(r.Item("model:held").Phase).To(Equal(PhaseSkipped))
			Expect(r.Item("model:held").ErrorKey).To(Equal(ErrKeyHeld))
			Expect(r.Item("model:edited").ErrorKey).To(Equal(ErrKeyEditedKeys))
			Expect(r.State).To(Equal(RunFinished))
			Expect(h.starter.started()).To(BeEmpty())
		})
	})

	Describe("the executor", func() {
		It("calls an item updated only after its operation ended and a fresh check no longer lists it", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
			r := h.start(si(KindGallery, "kokoro"))
			job := h.jobOf("gallery:kokoro")
			Expect(r.Source).To(Equal(SourceManual))

			h.jobs.set(job, galleryop.OpStatus{Phase: "fetching", Message: "downloading", Progress: 40, Cancellable: true})
			Eventually(func() int { return h.item(r.ID, "gallery:kokoro").Progress }).Should(Equal(40))
			Expect(h.item(r.ID, "gallery:kokoro").Message).To(Equal("downloading"))
			Expect(h.item(r.ID, "gallery:kokoro").Cancellable).To(BeTrue())

			h.jobs.complete(job)
			// Still listed: the operation ended but the item did not move.
			Eventually(func() Phase { return h.phase(r.ID, "gallery:kokoro") }).Should(Equal(PhaseFailed))
			Expect(h.item(r.ID, "gallery:kokoro").ErrorKey).To(Equal(ErrKeyStale))
			Expect(h.state(r.ID)).To(Equal(RunFinished))
		})

		It("marks the item updated when the check no longer lists it", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
			r := h.start(si(KindGallery, "kokoro"))
			job := h.jobOf("gallery:kokoro")
			h.backends.remove("gallery:kokoro")
			h.jobs.complete(job)
			Eventually(func() Phase { return h.phase(r.ID, "gallery:kokoro") }).Should(Equal(PhaseUpdated))
			Eventually(func() RunState { return h.state(r.ID) }).Should(Equal(RunFinished))
			Expect(h.get(r.ID).FinishedAt).NotTo(BeNil())
			Expect(h.starter.actorList()).To(Equal([]string{"admin"}))
		})

		It("leaves the item unconfirmed when no check can answer", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
			r := h.start(si(KindGallery, "kokoro"))
			job := h.jobOf("gallery:kokoro")
			h.backends.setIncomplete(true)
			h.jobs.complete(job)
			Eventually(func() Phase { return h.phase(r.ID, "gallery:kokoro") }).Should(Equal(PhaseUnconfirmed))
			Eventually(func() RunState { return h.state(r.ID) }).Should(Equal(RunFinished))
		})

		It("fails the item with the key of the phase its operation failed in", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
			r := h.start(si(KindGallery, "kokoro"))
			job := h.jobOf("gallery:kokoro")
			h.jobs.set(job, galleryop.OpStatus{Phase: "fetching", Progress: 10, Cancellable: true})
			Eventually(func() int { return h.item(r.ID, "gallery:kokoro").Progress }).Should(Equal(10))
			h.jobs.set(job, galleryop.OpStatus{Processed: true, Error: errors.New("connection reset"), Message: "error: connection reset"})
			Eventually(func() Phase { return h.phase(r.ID, "gallery:kokoro") }).Should(Equal(PhaseFailed))
			it := h.item(r.ID, "gallery:kokoro")
			Expect(it.ErrorKey).To(Equal(ErrKeyDownload))
			Expect(it.Error).To(Equal("connection reset"))
			Expect(it.Retryable).To(BeTrue())
		})

		It("passes the failure code of the add-on owner through", func() {
			h.addons.add(Candidate{Kind: KindAddon, Name: "notes"})
			r := h.start(si(KindAddon, "notes"))
			h.jobs.fail(h.jobOf("addon:notes"), "", "the upgrade failed (recovery_required)")
			Eventually(func() Phase { return h.phase(r.ID, "addon:notes") }).Should(Equal(PhaseFailed))
			Expect(h.item(r.ID, "addon:notes").ErrorKey).To(Equal("recovery_required"))
		})

		It("rolls back an item whose backend upgrade put the previous build back, with the result of each node", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
			r := h.start(si(KindGallery, "kokoro"))
			h.jobs.set(h.jobOf("gallery:kokoro"), galleryop.OpStatus{
				Processed: true, Error: errors.New("the new build did not start on 1 node"), ErrorKey: "start", RolledBack: true,
				Nodes: []galleryop.NodeProgress{
					{NodeID: "n1", NodeName: "worker-1", Status: galleryop.NodeStatusRolledBack, Error: "did not start", ErrorKey: "start"},
					{NodeID: "n2", NodeName: "worker-2", Status: "success", Percentage: 100},
				},
			})
			Eventually(func() Phase { return h.phase(r.ID, "gallery:kokoro") }).Should(Equal(PhaseRolledBack))
			it := h.item(r.ID, "gallery:kokoro")
			Expect(it.ErrorKey).To(Equal(ErrKeyStart))
			Expect(it.Retryable).To(BeTrue())
			Expect(it.Nodes).To(ConsistOf(
				NodeState{ID: "n1", Name: "worker-1", Phase: "rolled_back", Message: "did not start", ErrorKey: "start"},
				NodeState{ID: "n2", Name: "worker-2", Phase: "success", Progress: 100},
			))
		})

		It("makes an item unconfirmed when the reply of its worker was lost", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
			r := h.start(si(KindGallery, "kokoro"))
			h.backends.setIncomplete(true)
			h.jobs.set(h.jobOf("gallery:kokoro"), galleryop.OpStatus{Processed: true, Unconfirmed: true, Message: "worker still installing in background"})
			Eventually(func() Phase { return h.phase(r.ID, "gallery:kokoro") }).Should(Equal(PhaseUnconfirmed))
			Eventually(func() RunState { return h.state(r.ID) }).Should(Equal(RunFinished))
			Expect(h.phase(r.ID, "gallery:kokoro")).To(Equal(PhaseUnconfirmed))
		})

		It("fails an item whose starter refuses, and skips one the starter says to skip", func() {
			h.models.add(Candidate{Kind: KindModel, Name: "m"})
			h.starter.setErr(&StartError{ErrorKey: ErrKeyHeld, Skip: true, Err: errors.New("held")})
			r := h.start(si(KindModel, "m"))
			Eventually(func() Phase { return h.phase(r.ID, "model:m") }).Should(Equal(PhaseSkipped))
		})

		It("fails an item whose target moved after the request, before its operation starts", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a"})
			h.models.add(Candidate{Kind: KindModel, Name: "m"})
			r := h.start(si(KindGallery, "a"), si(KindModel, "m"))
			job := h.jobOf("gallery:a")
			h.models.add(Candidate{Kind: KindModel, Name: "m", To: "3"})
			h.backends.remove("gallery:a")
			h.jobs.complete(job)
			Eventually(func() Phase { return h.phase(r.ID, "model:m") }).Should(Equal(PhaseFailed))
			Expect(h.item(r.ID, "model:m").ErrorKey).To(Equal(ErrKeyStale))
			Expect(h.starter.started()).To(Equal([]string{"gallery:a"}))
		})

		It("counts a status that went away as lost and lets a check decide", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a"})
			r := h.start(si(KindGallery, "a"))
			job := h.jobOf("gallery:a")
			Eventually(func() string { return h.item(r.ID, "gallery:a").JobID }).Should(Equal(job))
			h.backends.remove("gallery:a")
			h.jobs.drop(job)
			Eventually(func() Phase { return h.phase(r.ID, "gallery:a") }).Should(Equal(PhaseUpdated))
		})

		It("tells the observers of every change", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a"})
			r := h.start(si(KindGallery, "a"))
			job := h.jobOf("gallery:a")
			h.backends.remove("gallery:a")
			h.jobs.complete(job)
			Eventually(func() RunState { return h.state(r.ID) }).Should(Equal(RunFinished))
			var seen []Change
			Eventually(func() bool {
				for {
					select {
					case c := <-h.changes:
						seen = append(seen, c)
					default:
						return len(seen) > 0 && seen[len(seen)-1].Run.State == RunFinished
					}
				}
			}).Should(BeTrue())
			for _, c := range seen {
				Expect(c.Local).To(BeTrue())
				Expect(c.Run.ID).To(Equal(r.ID))
			}
		})
	})

	Describe("the planner at work", func() {
		It("runs backends up to the limit, never one backend twice, then models, then add-ons one at a time", func() {
			h = newHarness(harnessOpts{limit: 1})
			h.backends.add(Candidate{Kind: KindGallery, Name: "whisper"})
			h.backends.add(Candidate{Kind: KindLibrary, Name: "lib/whisper"})
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
			h.models.add(Candidate{Kind: KindModel, Name: "m"})
			h.addons.add(Candidate{Kind: KindAddon, Name: "notes"})
			h.addons.add(Candidate{Kind: KindAddon, Name: "files"})
			r := h.start(si(KindAddon, "notes"), si(KindAddon, "files"), si(KindModel, "m"),
				si(KindGallery, "whisper"), si(KindLibrary, "lib/whisper"), si(KindGallery, "kokoro"))

			finish := func(key string) {
				job := h.jobOf(key)
				kind, _, _ := ParseKey(key)
				switch kind {
				case KindModel:
					h.models.remove(key)
				case KindAddon:
					h.addons.remove(key)
				default:
					h.backends.remove(key)
				}
				h.jobs.complete(job)
				Eventually(func() Phase { return h.phase(r.ID, key) }).Should(Equal(PhaseUpdated))
			}

			Eventually(h.starter.started).Should(Equal([]string{"gallery:kokoro"}))
			view := h.get(r.ID)
			Expect(view.Item("gallery:whisper").QueuedReason).To(Equal(WaitBackendLimit))
			Expect(view.Item("model:m").QueuedReason).To(Equal(WaitAfterBackends))
			Expect(view.Item("addon:notes").QueuedReason).To(Equal(WaitAfterBackends))

			finish("gallery:kokoro")
			Eventually(h.starter.started).Should(HaveLen(2))
			Expect(h.starter.started()[1]).To(Equal("gallery:whisper"))
			Expect(h.item(r.ID, "library:lib/whisper").QueuedReason).To(Equal(WaitSameBackend))

			finish("gallery:whisper")
			finish("library:lib/whisper")
			Eventually(h.starter.started).Should(HaveLen(4))
			Expect(h.starter.started()[3]).To(Equal("model:m"))
			Expect(h.item(r.ID, "addon:files").QueuedReason).To(Equal(WaitAfterModels))

			finish("model:m")
			Eventually(h.starter.started).Should(HaveLen(5))
			Expect(h.starter.started()[4]).To(Equal("addon:files"))
			Expect(h.item(r.ID, "addon:notes").QueuedReason).To(Equal(WaitOneAddonAtATime))
			Consistently(h.starter.started, 50*time.Millisecond).Should(HaveLen(5))

			finish("addon:files")
			finish("addon:notes")
			Eventually(func() RunState { return h.state(r.ID) }).Should(Equal(RunFinished))
		})

		It("never runs two operations of one backend at once even with room", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "whisper"})
			h.backends.add(Candidate{Kind: KindLibrary, Name: "lib/whisper"})
			r := h.start(si(KindGallery, "whisper"), si(KindLibrary, "lib/whisper"))
			Eventually(h.starter.started).Should(Equal([]string{"gallery:whisper"}))
			Consistently(h.starter.started, 50*time.Millisecond).Should(HaveLen(1))
			Expect(h.item(r.ID, "library:lib/whisper").QueuedReason).To(Equal(WaitSameBackend))
		})
	})

	Describe("cancel", func() {
		It("drops a queued item and cancels a running one through its operation", func() {
			h = newHarness(harnessOpts{limit: 1})
			h.backends.add(Candidate{Kind: KindGallery, Name: "a"})
			h.backends.add(Candidate{Kind: KindGallery, Name: "b"})
			r := h.start(si(KindGallery, "a"), si(KindGallery, "b"))
			job := h.jobOf("gallery:a")
			Eventually(func() bool { return h.item(r.ID, "gallery:a").Cancellable }).Should(BeTrue())

			Expect(h.svc.CancelItem(ctx, r.ID, "gallery:b")).To(Succeed())
			Eventually(func() Phase { return h.phase(r.ID, "gallery:b") }).Should(Equal(PhaseCancelled))

			Expect(h.svc.CancelItem(ctx, r.ID, "gallery:a")).To(Succeed())
			Eventually(h.jobs.cancelled).Should(Equal([]string{job}))
			Eventually(func() Phase { return h.phase(r.ID, "gallery:a") }).Should(Equal(PhaseCancelled))
			Eventually(func() RunState { return h.state(r.ID) }).Should(Equal(RunFinished))
			Expect(h.starter.started()).To(Equal([]string{"gallery:a"}))
		})

		It("refuses to cancel an item whose operation cannot stop", func() {
			h.models.add(Candidate{Kind: KindModel, Name: "m"})
			r := h.start(si(KindModel, "m"))
			job := h.jobOf("model:m")
			h.jobs.set(job, galleryop.OpStatus{Phase: "committing", Progress: 80, Cancellable: false})
			Eventually(func() int { return h.item(r.ID, "model:m").Progress }).Should(Equal(80))
			Expect(CodeOf(h.svc.CancelItem(ctx, r.ID, "model:m"))).To(Equal(CodeNotCancellable))
			Expect(CodeOf(h.svc.CancelItem(ctx, r.ID, "model:zzz"))).To(Equal(CodeNotFound))
			Expect(CodeOf(h.svc.CancelItem(ctx, "no-run", "model:m"))).To(Equal(CodeNotFound))
		})

		It("cancels the run and ends it cancelled", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a"})
			h.models.add(Candidate{Kind: KindModel, Name: "m"})
			r := h.start(si(KindGallery, "a"), si(KindModel, "m"))
			job := h.jobOf("gallery:a")
			Eventually(func() bool { return h.item(r.ID, "gallery:a").Cancellable }).Should(BeTrue())
			Expect(h.svc.Cancel(ctx, r.ID)).To(Succeed())
			Eventually(func() RunState { return h.state(r.ID) }).Should(Equal(RunCancelled))
			Expect(h.jobs.cancelled()).To(Equal([]string{job}))
			Expect(h.phase(r.ID, "model:m")).To(Equal(PhaseCancelled))
			Expect(CodeOf(h.svc.Cancel(ctx, r.ID))).To(Equal(CodeRunFinished))
			Expect(CodeOf(h.svc.Cancel(ctx, "no-run"))).To(Equal(CodeNotFound))
		})
	})

	Describe("retry and dismiss", func() {
		It("starts a new run with the retryable items that still have an update and leaves the old run as it was", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a"})
			h.backends.add(Candidate{Kind: KindGallery, Name: "b"})
			r := h.start(si(KindGallery, "a"), si(KindGallery, "b"))
			Expect(CodeOf(func() error { _, err := h.svc.Retry(ctx, "admin", r.ID); return err }())).To(Equal(CodeRunNotFinished))
			Expect(CodeOf(h.svc.Dismiss(ctx, r.ID))).To(Equal(CodeRunNotFinished))

			h.jobs.fail(h.jobOf("gallery:a"), "fetching", "boom")
			h.backends.remove("gallery:b")
			h.jobs.complete(h.jobOf("gallery:b"))
			Eventually(func() RunState { return h.state(r.ID) }).Should(Equal(RunFinished))

			again, err := h.svc.Retry(ctx, "admin", r.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(again.ID).NotTo(Equal(r.ID))
			Expect(again.Items).To(HaveLen(1))
			Expect(again.Items[0].Key).To(Equal("gallery:a"))
			Expect(h.phase(r.ID, "gallery:a")).To(Equal(PhaseFailed), "the old run is immutable")

			Expect(h.svc.Dismiss(ctx, r.ID)).To(Succeed())
			runs, err := h.svc.List(ctx, ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(runs).To(HaveLen(1))
			Expect(runs[0].ID).To(Equal(again.ID))
			runs, err = h.svc.List(ctx, ListOptions{WithDismissed: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(runs).To(HaveLen(2))
			Expect(runs[0].ID).To(Equal(again.ID), "the running run comes first")
		})

		It("refuses a retry with nothing to retry", func() {
			h.backends.add(Candidate{Kind: KindGallery, Name: "a"})
			r := h.start(si(KindGallery, "a"))
			job := h.jobOf("gallery:a")
			h.backends.remove("gallery:a")
			h.jobs.complete(job)
			Eventually(func() RunState { return h.state(r.ID) }).Should(Equal(RunFinished))
			_, err := h.svc.Retry(ctx, "admin", r.ID)
			Expect(CodeOf(err)).To(Equal(CodeNothingToRetry))
		})
	})

	Describe("restart", func() {
		It("brings back a stored run with a running item as unconfirmed until a check decides", func() {
			db := openSQLite()
			store, err := distributed.NewUpgradeRunStore(db)
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Create(&distributed.UpgradeRunRecord{ID: "r1", Source: "manual", State: "running", CreatedAt: time.Now()},
				[]distributed.UpgradeRunItemRecord{
					{Key: "gallery:a", Kind: "gallery", Name: "a", To: "2", ExpectedTo: "2", Phase: "running", JobID: "job-of-dead-process"},
					{Key: "model:m", Kind: "model", Name: "m", To: "2", ExpectedTo: "2", Phase: "queued", QueuedReason: WaitAfterBackends},
				})).To(Succeed())

			h = newHarness(harnessOpts{db: db})
			gate := make(chan struct{})
			h.backends.setGate(gate)
			h.models.add(Candidate{Kind: KindModel, Name: "m"})
			h.svc.Sweep(ctx)

			Eventually(func() Phase { return h.phase("r1", "gallery:a") }).Should(Equal(PhaseUnconfirmed))
			Consistently(func() Phase { return h.phase("r1", "gallery:a") }, 50*time.Millisecond).Should(Equal(PhaseUnconfirmed))
			Expect(h.phase("r1", "model:m")).To(Equal(PhaseQueued))
			Expect(h.starter.started()).To(BeEmpty(), "the old operation is not started again")

			close(gate)
			Eventually(func() Phase { return h.phase("r1", "gallery:a") }).Should(Equal(PhaseUpdated), "the fake check no longer lists it")
			Eventually(h.starter.started).Should(Equal([]string{"model:m"}))
		})
	})
})
