package application

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	addonupdates "github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/upgraderun"
)

// fakeRunService records the runs the automatic passes ask for.
type fakeRunService struct {
	mu       sync.Mutex
	requests []upgraderun.StartRequest
	actors   []string
	autoCtx  []bool
	// refuse is the error of the next calls; each call takes one entry, and
	// an empty list accepts.
	refuse []error
	// held is what HeldBack answers.
	held upgraderun.HeldBack
}

func (f *fakeRunService) HeldBack(context.Context) (upgraderun.HeldBack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held, nil
}

func (f *fakeRunService) Start(ctx context.Context, actor string, req upgraderun.StartRequest) (upgraderun.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	f.actors = append(f.actors, actor)
	f.autoCtx = append(f.autoCtx, isAutoRunCtx(ctx))
	if len(f.refuse) > 0 {
		err := f.refuse[0]
		f.refuse = f.refuse[1:]
		if err != nil {
			return upgraderun.Run{}, err
		}
	}
	return upgraderun.Run{ID: "run-1", Source: req.Source}, nil
}

func (f *fakeRunService) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// retryGoroutines counts the goroutines of autoRunner retries in this
// process. The count is exact where a total goroutine count is not: other
// specs end goroutines of their own meanwhile.
func retryGoroutines() int {
	buf := make([]byte, 1<<22)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "(*autoRunner).retryLater.func")
}

func runActive() error {
	return &upgraderun.Error{Code: upgraderun.CodeRunActive, Message: "another upgrade run is running"}
}

// runnerOf builds a runner over a fake service; a nil service is "not started".
func runnerOf(svc *fakeRunService, retry time.Duration) *autoRunner {
	r := newAutoRunner(func() autoRunService {
		if svc == nil {
			return nil
		}
		return svc
	})
	r.delay, r.retries = retry, 50
	return r
}

type fakeAddonOwner struct {
	mu      sync.Mutex
	pending []addons.AutoUpgradeCandidate
	one     map[string]addons.AutoUpgradeResult
	inline  int
	upgrade []string
}

func (f *fakeAddonOwner) AutoUpgrade(context.Context) []addons.AutoUpgradeResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inline++
	return nil
}

func (f *fakeAddonOwner) AutoUpgradePending(context.Context) []addons.AutoUpgradeCandidate {
	return f.pending
}

func (f *fakeAddonOwner) AutoUpgradeOne(_ context.Context, id string) (addons.AutoUpgradeResult, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upgrade = append(f.upgrade, id)
	r, ok := f.one[id]
	return r, ok
}

// runNow runs an add-on operation at once.
type runNow struct {
	ops  []galleryop.AddonOp
	errs []error
}

func (q *runNow) EnqueueAddonOp(op galleryop.AddonOp) {
	q.ops = append(q.ops, op)
	q.errs = append(q.errs, op.Run(context.Background(), func(*galleryop.OpStatus) {}))
}

var _ = Describe("the automatic passes as upgrade runs", func() {
	ctx := context.Background()

	Describe("the add-on pass", func() {
		appWith := func(svc *fakeRunService) *Application {
			a := &Application{}
			a.autoRunnerOnce.Do(func() { a.autoRuns = runnerOf(svc, 5*time.Millisecond) })
			return a
		}
		owner := func() *fakeAddonOwner {
			return &fakeAddonOwner{pending: []addons.AutoUpgradeCandidate{
				{AddonID: "sample", From: "1", To: "2", InstalledDigest: "sha256:old", CandidateDigest: "sha256:new"},
				{AddonID: "other", From: "1", To: "2", InstalledDigest: "sha256:o1", CandidateDigest: "sha256:o2"},
			}}
		}

		It("starts one run with the source auto for what it would upgrade", func() {
			svc, o := &fakeRunService{}, owner()
			appWith(svc).autoUpgradeAddonsWith(ctx, o)
			Expect(svc.requests).To(HaveLen(1))
			Expect(svc.actors).To(Equal([]string{"system:auto-upgrade"}))
			Expect(svc.requests[0].Source).To(Equal(upgraderun.SourceAuto))
			Expect(svc.requests[0].Items).To(Equal([]upgraderun.StartItem{
				{Kind: upgraderun.KindAddon, Name: "sample", ExpectedTo: "sha256:new"},
				{Kind: upgraderun.KindAddon, Name: "other", ExpectedTo: "sha256:o2"},
			}))
			Expect(o.inline).To(BeZero(), "nothing upgrades inside the pass")
		})

		It("starts nothing when nothing is pending", func() {
			svc, o := &fakeRunService{}, &fakeAddonOwner{}
			appWith(svc).autoUpgradeAddonsWith(ctx, o)
			Expect(svc.requests).To(BeEmpty())
			Expect(o.inline).To(BeZero())
		})

		It("skips the tick while a run is running and starts the run when it ends", func() {
			svc := &fakeRunService{refuse: []error{runActive(), runActive()}}
			o := owner()
			a := appWith(svc)
			a.autoUpgradeAddonsWith(ctx, o)
			Expect(o.inline).To(BeZero(), "the items are not upgraded inline")
			Eventually(svc.count, "2s").Should(Equal(3))
			Consistently(svc.count, "100ms").Should(Equal(3), "one run is enough")
			Expect(svc.requests[2].Items).To(HaveLen(2), "no item was lost")
		})

		It("keeps one waiter for the family", func() {
			svc := &fakeRunService{refuse: []error{runActive(), runActive(), runActive(), runActive()}}
			a := appWith(svc)
			a.autoUpgradeAddonsWith(ctx, owner())
			a.autoUpgradeAddonsWith(ctx, owner())
			Eventually(svc.count, "2s").Should(BeNumerically(">=", 5))
			Eventually(func() int {
				a.autoRuns.mu.Lock()
				defer a.autoRuns.mu.Unlock()
				return len(a.autoRuns.waiting)
			}, "2s").Should(BeZero())
		})

		It("ends a waiting retry when the upgrade runs stop, and leaves no goroutine", func() {
			// Every try meets a running run, and the next try is an hour
			// away: only the stop of the runs can end the waiter.
			refuse := make([]error, 10)
			for i := range refuse {
				refuse[i] = runActive()
			}
			svc := &fakeRunService{refuse: refuse}
			a := &Application{}
			a.autoRunnerOnce.Do(func() { a.autoRuns = runnerOf(svc, time.Hour) })
			waiting := func() bool {
				a.autoRuns.mu.Lock()
				defer a.autoRuns.mu.Unlock()
				return a.autoRuns.waiting["addons"]
			}
			before := retryGoroutines()
			a.autoUpgradeAddonsWith(ctx, owner())
			Expect(svc.count()).To(Equal(1))
			Expect(waiting()).To(BeTrue(), "the retry waits")

			stopped := make(chan struct{})
			go func() {
				a.stopUpgradeRuns()
				close(stopped)
			}()
			Eventually(stopped, "2s").Should(BeClosed())
			Eventually(waiting, "2s").Should(BeFalse(), "the waiter ended")
			Eventually(retryGoroutines, "2s").Should(BeNumerically("<=", before), "no retry goroutine is left")
			Expect(svc.count()).To(Equal(1), "no retry ran")

			// A pass after the stop does not start a new waiter.
			a.autoUpgradeAddonsWith(ctx, owner())
			Expect(svc.count()).To(Equal(2))
			Expect(waiting()).To(BeFalse())
		})

		It("does not retry after another refusal", func() {
			svc := &fakeRunService{refuse: []error{&upgraderun.Error{Code: upgraderun.CodeStale, Message: "moved"}}}
			o := owner()
			appWith(svc).autoUpgradeAddonsWith(ctx, o)
			Consistently(svc.count, "100ms").Should(Equal(1))
			Expect(o.inline).To(BeZero())
		})

		It("upgrades inline as before when the run service is not available", func() {
			o := owner()
			appWith(nil).autoUpgradeAddonsWith(ctx, o)
			Expect(o.inline).To(Equal(1))
		})
	})

	Describe("the add-on items of an automatic run", func() {
		manual := &recordingStarter{}
		start := func(o *fakeAddonOwner, q *runNow) autoAddonStarter {
			return autoAddonStarter{
				inner:   manual,
				manager: func() addonAutoUpgrader { return o },
				queue:   func() addonOpQueue { return q },
			}
		}
		it := upgraderun.Item{Key: "addon:sample", Kind: upgraderun.KindAddon, Name: "sample"}
		// autoCtx is the context the runner of an automatic run gives its
		// starters.
		autoCtx := func() context.Context { return upgraderun.WithSource(ctx, upgraderun.SourceAuto) }

		It("upgrades through the owner's automatic upgrade", func() {
			o := &fakeAddonOwner{one: map[string]addons.AutoUpgradeResult{"sample": {AddonID: "sample", Upgraded: true}}}
			q := &runNow{}
			id, err := start(o, q).Start(autoCtx(), "system:auto-upgrade", it)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).NotTo(BeEmpty())
			Expect(o.upgrade).To(Equal([]string{"sample"}))
			Expect(q.ops[0].Upgrade).To(BeTrue())
			Expect(q.ops[0].InRun).To(BeTrue())
			Expect(q.errs[0]).NotTo(HaveOccurred())
			Expect(manual.calls).To(BeZero(), "the recorded path of an administrator is not used")
		})

		It("holds a release that needs an approval and never approves it", func() {
			o := &fakeAddonOwner{one: map[string]addons.AutoUpgradeResult{"sample": {AddonID: "sample", HeldForApproval: true, Err: errors.New("needs host access")}}}
			q := &runNow{}
			_, err := start(o, q).Start(autoCtx(), "system:auto-upgrade", it)
			Expect(err).NotTo(HaveOccurred())
			Expect(q.errs[0]).To(MatchError(ContainSubstring("held")))
			Expect(q.errs[0]).To(MatchError(ContainSubstring("nobody approved")))
		})

		It("does not try again a release that failed", func() {
			o := &fakeAddonOwner{one: map[string]addons.AutoUpgradeResult{"sample": {AddonID: "sample", SkippedAsFailure: true}}}
			q := &runNow{}
			_, _ = start(o, q).Start(autoCtx(), "system:auto-upgrade", it)
			Expect(q.errs[0]).To(HaveOccurred())
		})

		It("reports the failure of the upgrade", func() {
			o := &fakeAddonOwner{one: map[string]addons.AutoUpgradeResult{"sample": {AddonID: "sample", Err: errors.New("upgrade_failed")}}}
			q := &runNow{}
			_, _ = start(o, q).Start(autoCtx(), "system:auto-upgrade", it)
			Expect(q.errs[0]).To(MatchError("upgrade_failed"))
		})

		It("leaves the runs of an administrator to the recorded upgrade", func() {
			o, q := &fakeAddonOwner{}, &runNow{}
			_, err := start(o, q).Start(ctx, `{"user":"u1"}`, it)
			Expect(err).NotTo(HaveOccurred())
			Expect(manual.calls).To(Equal(1))
			Expect(q.ops).To(BeEmpty())
			Expect(o.upgrade).To(BeEmpty())
		})

		It("refuses a run that is not automatic, also with the system actor, and queues nothing", func() {
			for _, src := range []upgraderun.Source{upgraderun.SourceManual, ""} {
				o := &fakeAddonOwner{one: map[string]addons.AutoUpgradeResult{"sample": {AddonID: "sample", Upgraded: true}}}
				q := &runNow{}
				calls := manual.calls
				_, err := start(o, q).Start(upgraderun.WithSource(ctx, src), "system:auto-upgrade", it)
				var se *upgraderun.StartError
				Expect(errors.As(err, &se)).To(BeTrue(), "source %q", src)
				Expect(se.Retryable).To(BeFalse())
				Expect(q.ops).To(BeEmpty())
				Expect(o.upgrade).To(BeEmpty())
				Expect(manual.calls).To(Equal(calls), "the system actor never reaches the path of an administrator")
			}
		})

		It("says the upgrade is unavailable without the owner", func() {
			_, err := autoAddonStarter{inner: manual}.Start(autoCtx(), "system:auto-upgrade", it)
			var se *upgraderun.StartError
			Expect(errors.As(err, &se)).To(BeTrue())
			Expect(se.Retryable).To(BeTrue())
		})
	})

	Describe("the add-on checks of a run", func() {
		sts := []addonupdates.Status{
			{AddonID: "sample", State: addonupdates.UpdateAvailable, InstalledDigest: "sha256:old", CandidateDigest: "sha256:new"},
			{AddonID: "quiet", State: addonupdates.UpToDate},
		}
		It("lists the add-ons with an update on the owner, with no identity", func() {
			src := &fakeStatuses{sts: sts}
			l, err := ownerAddonChecker{src: src, owner: func() bool { return true }}.List(ctx, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(l.Complete).To(BeTrue())
			Expect(l.Candidates).To(HaveLen(1))
			Expect(l.Candidates[0].Key()).To(Equal("addon:sample"))
			Expect(l.Candidates[0].Extra).To(HaveKeyWithValue(upgraderun.ExtraCandidateDigest, "sha256:new"))
			Expect(src.fresh).To(Equal([]bool{true}))
		})

		It("adds no add-on item on a process that is not the owner", func() {
			src := &fakeStatuses{sts: sts}
			_, err := ownerAddonChecker{src: src, owner: func() bool { return false }}.List(ctx, false)
			Expect(err).To(HaveOccurred())
			Expect(src.fresh).To(BeEmpty(), "the owner's list is not read")
		})

		It("sends an automatic run to the owner and every other run to its own path", func() {
			auto, manual := &fakeChecker{}, &fakeChecker{}
			c := addonCheckerBySource{auto: auto, manual: manual, isAuto: isAutoRunCtx}
			_, _ = c.List(withAutoRun(ctx), false)
			Expect(auto.calls).To(Equal(1))
			Expect(manual.calls).To(BeZero(), "the system actor never reaches the gateway path")
			_, _ = c.List(ctx, false)
			_, _ = c.List(ctx, true)
			Expect(auto.calls).To(Equal(1))
			Expect(manual.calls).To(Equal(2), "a manual run keeps its own path")

			// The later checks of an automatic run carry only its actor.
			_, _ = c.List(upgraderun.WithActor(ctx, autoRunActor), false)
			Expect(auto.calls).To(Equal(2))
			_, _ = c.List(upgraderun.WithActor(ctx, "someone"), false)
			Expect(manual.calls).To(Equal(3))
		})
	})

	Describe("the backend pass", func() {
		gate := func(on bool, svc *fakeRunService, lib libraryUpgrader) (*UpgradeChecker, *scriptedManager) {
			bm := &scriptedManager{result: map[string]gallery.UpgradeInfo{
				"llama-cpp": {BackendName: "llama-cpp", InstalledVersion: "1", AvailableVersion: "2"},
				"whisper":   {BackendName: "whisper", InstalledVersion: "1", AvailableVersion: "2"},
			}}
			uc := NewUpgradeChecker(&config.ApplicationConfig{AutoUpgradeBackends: on}, nil, nil, func() galleryop.BackendManager { return bm })
			if lib != nil {
				uc.SetLibraryBackends(func() libraryUpgrader { return lib })
			}
			if svc != nil {
				uc.SetAutoRuns(runnerOf(svc, 5*time.Millisecond))
			}
			return uc, bm
		}

		It("starts one run for the gallery and library backends instead of installing inline", func() {
			svc := &fakeRunService{}
			lib := &fakeLibrary{updates: []backendlib.Update{{Name: "tts", SourceID: "official", Version: "3"}}}
			uc, bm := gate(true, svc, lib)
			pass := uc.runCheck(ctx, true)
			Expect(bm.upgrade.Load()).To(BeZero(), "no inline install")
			Expect(lib.installed).To(BeEmpty())
			Expect(svc.requests).To(HaveLen(1))
			Expect(svc.requests[0].Source).To(Equal(upgraderun.SourceAuto))
			Expect(svc.requests[0].Items).To(Equal([]upgraderun.StartItem{
				{Kind: upgraderun.KindGallery, Name: "llama-cpp"},
				{Kind: upgraderun.KindGallery, Name: "whisper"},
				{Kind: upgraderun.KindLibrary, Name: "official/tts"},
			}))
			Expect(pass.outcomes).To(BeEmpty(), "the run's own rows tell the outcome")
		})

		It("skips the tick while a run is running and keeps the items for the retry", func() {
			svc := &fakeRunService{refuse: []error{runActive()}}
			uc, bm := gate(true, svc, nil)
			uc.runCheck(ctx, true)
			Expect(bm.upgrade.Load()).To(BeZero())
			Eventually(svc.count, "2s").Should(Equal(2))
			Expect(svc.requests[1].Items).To(HaveLen(2))
		})

		It("neither waits nor retries when every release is held back", func() {
			svc := &fakeRunService{refuse: []error{&upgraderun.Error{Code: upgraderun.CodeHeldBack, Message: "held back"}}}
			uc, bm := gate(true, svc, nil)
			uc.runCheck(ctx, true)
			Consistently(svc.count, "100ms").Should(Equal(1))
			Expect(bm.upgrade.Load()).To(BeZero(), "no inline install either")
		})

		It("marks the held-back release in the read model while the automatic update is on", func() {
			svc := &fakeRunService{held: upgraderun.HeldBack{"gallery:llama-cpp": {"2": true}, "gallery:whisper": {"1.5": true}}}
			uc, _ := gate(true, svc, nil)
			uc.runCheck(ctx, false)
			byID := map[string]bool{}
			for _, it := range uc.UpdatesSnapshot(ctx).Items {
				byID[it.ID] = it.HeldBack
			}
			Expect(byID).To(HaveKeyWithValue("gallery:llama-cpp", true))
			Expect(byID).To(HaveKeyWithValue("gallery:whisper", false), "another release of whisper is listed now")

			off, _ := gate(false, svc, nil)
			off.runCheck(ctx, false)
			for _, it := range off.UpdatesSnapshot(ctx).Items {
				Expect(it.HeldBack).To(BeFalse(), "without the automatic update nothing is held back")
			}
		})

		It("starts nothing with the setting off", func() {
			svc := &fakeRunService{}
			uc, _ := gate(false, svc, nil)
			uc.runCheck(ctx, true)
			Expect(svc.requests).To(BeEmpty())
		})

		It("starts nothing for a check that installs nothing", func() {
			svc := &fakeRunService{}
			uc, _ := gate(true, svc, nil)
			uc.runCheck(ctx, false)
			Expect(svc.requests).To(BeEmpty())
		})

		It("installs inline as before when the run service is not available", func() {
			lib := &fakeLibrary{updates: []backendlib.Update{{Name: "tts", SourceID: "official", Version: "3"}}}
			uc, bm := gate(true, nil, lib)
			uc.SetAutoRuns(runnerOf(nil, time.Millisecond))
			pass := uc.runCheck(ctx, true)
			Expect(bm.upgrade.Load()).To(Equal(int32(2)))
			Expect(lib.installed).To(Equal([]string{"official/tts@3"}))
			Expect(pass.outcomes).To(HaveLen(3))
		})
	})
})

type recordingStarter struct{ calls int }

func (r *recordingStarter) Start(context.Context, string, upgraderun.Item) (string, error) {
	r.calls++
	return "job", nil
}

type fakeStatuses struct {
	sts   []addonupdates.Status
	fresh []bool
}

func (f *fakeStatuses) AutoUpgradeStatuses(_ context.Context, fresh bool) ([]addonupdates.Status, bool, error) {
	f.fresh = append(f.fresh, fresh)
	return f.sts, true, nil
}

type fakeChecker struct{ calls int }

func (f *fakeChecker) Kinds() []upgraderun.Kind { return []upgraderun.Kind{upgraderun.KindAddon} }
func (f *fakeChecker) List(context.Context, bool) (upgraderun.Listing, error) {
	f.calls++
	return upgraderun.Listing{Complete: true}, nil
}

var _ = Describe("the retention settings of upgrade runs", func() {
	DescribeTable("read zero as the default and a negative value as no limit",
		func(keep, days int, want upgraderun.Retention) {
			Expect(*upgradeRunRetention(keep, days)).To(Equal(want))
		},
		Entry("defaults", 0, 0, upgraderun.Retention{MaxAge: 30 * 24 * time.Hour, Keep: 200}),
		Entry("set", 50, 7, upgraderun.Retention{MaxAge: 7 * 24 * time.Hour, Keep: 50}),
		Entry("no limits", -1, -1, upgraderun.Retention{MaxAge: 0, Keep: -1}),
	)
})
