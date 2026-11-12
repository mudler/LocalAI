package application

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/updatenotify"
)

// announced records what the checker asks the inbox to write.
type announced struct {
	mu      sync.Mutex
	digests []backendupdates.Snapshot
	failed  [][]string
	results []updatenotify.Result
}

func (a *announced) Digest(_ context.Context, s backendupdates.Snapshot) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.digests = append(a.digests, s)
	return nil
}

func (a *announced) CheckFailed(_ context.Context, sources []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failed = append(a.failed, sources)
	return nil
}

func (a *announced) Result(_ context.Context, r updatenotify.Result) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.results = append(a.results, r)
	return nil
}

func (a *announced) calls() (digests, failed, results int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.digests), len(a.failed), len(a.results)
}

// passManager reports the upgrades it is given and fails the ones named.
type passManager struct {
	galleryop.BackendManager
	mu       sync.Mutex
	upgrades map[string]gallery.UpgradeInfo
	failing  map[string]error
	checkErr error
}

func (m *passManager) CheckUpgrades(context.Context) (map[string]gallery.UpgradeInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.checkErr != nil {
		return nil, m.checkErr
	}
	out := map[string]gallery.UpgradeInfo{}
	for k, v := range m.upgrades {
		out[k] = v
	}
	return out, nil
}

func (m *passManager) UpgradeBackend(_ context.Context, op *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.failing[op.GalleryElementName]; err != nil {
		return err
	}
	// The backend is current after a good upgrade.
	delete(m.upgrades, op.GalleryElementName)
	return nil
}

func (m *passManager) set(f func(*passManager)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(m)
}

type passLibrary struct {
	updates []backendlib.Update
	failing map[string]error
}

func (l *passLibrary) Updates(context.Context) ([]backendlib.Update, error) { return l.updates, nil }
func (l *passLibrary) Install(_ context.Context, _, backend, _ string, _ backendlib.Progress) error {
	return l.failing[backend]
}

var _ = Describe("what the update pass tells the inbox", func() {
	ctx := context.Background()
	up := func(name, from, to string) gallery.UpgradeInfo {
		return gallery.UpgradeInfo{BackendName: name, InstalledVersion: from, AvailableVersion: to}
	}
	newChecker := func(auto bool, db *gorm.DB, bm *passManager) (*UpgradeChecker, *announced) {
		uc := NewUpgradeChecker(&config.ApplicationConfig{AutoUpgradeBackends: auto}, nil, db, func() galleryop.BackendManager { return bm })
		a := &announced{}
		uc.SetAnnouncer(a)
		return uc, a
	}

	It("writes the digest of the leader pass when the automatic update is off, and no result", func() {
		bm := &passManager{upgrades: map[string]gallery.UpgradeInfo{"llama-cpp": up("llama-cpp", "1", "2")}}
		uc, a := newChecker(false, &gorm.DB{}, bm)
		uc.runLeaderPass(ctx)
		d, _, r := a.calls()
		Expect(d).To(Equal(1))
		Expect(r).To(BeZero(), "nothing was installed")
		_, ids, complete := backendupdates.PendingUpdates(a.digests[0])
		Expect(ids).To(Equal([]string{"gallery:llama-cpp@2"}))
		Expect(complete).To(BeTrue())
	})

	It("writes one result for the pass, with what was updated and what failed, and sends the digest for the rest", func() {
		bm := &passManager{
			upgrades: map[string]gallery.UpgradeInfo{"llama-cpp": up("llama-cpp", "1", "2"), "whisper": up("whisper", "1", "3")},
			failing:  map[string]error{"whisper": errors.New("download interrupted")},
		}
		uc, a := newChecker(true, &gorm.DB{}, bm)
		uc.runLeaderPass(ctx)
		_, _, r := a.calls()
		Expect(r).To(Equal(1), "one result for the pass")
		res := a.results[0]
		Expect(res.Family).To(Equal(updatenotify.FamilyBackends))
		Expect(res.Outcomes).To(ConsistOf(
			updatenotify.Outcome{Kind: updatenotify.OutcomeUpdated, ID: "gallery:llama-cpp@2", Name: "llama-cpp", From: "1", To: "2"},
			updatenotify.Outcome{Kind: updatenotify.OutcomeFailed, ID: "gallery:whisper@3", Name: "whisper", From: "1", To: "3", Err: "download interrupted"},
		))
		_, ids, _ := backendupdates.PendingUpdates(a.digests[0])
		Expect(ids).To(BeEmpty(), "the automatic update covers the backends: no digest")
	})

	It("reports library backends in the same result", func() {
		bm := &passManager{upgrades: map[string]gallery.UpgradeInfo{}}
		uc, a := newChecker(true, &gorm.DB{}, bm)
		lib := &passLibrary{updates: []backendlib.Update{
			{SourceID: "acme", Name: "piper", InstalledVersion: "1", Version: "2"},
			{SourceID: "acme", Name: "kokoro", InstalledVersion: "1", Version: "2"},
		}, failing: map[string]error{"kokoro": errors.New("signature refused")}}
		uc.SetLibraryBackends(func() libraryUpgrader { return lib })
		uc.runLeaderPass(ctx)
		Expect(a.results).To(HaveLen(1))
		Expect(a.results[0].Outcomes).To(ConsistOf(
			updatenotify.Outcome{Kind: updatenotify.OutcomeUpdated, ID: "library:acme/piper@2", Name: "piper", From: "1", To: "2"},
			updatenotify.Outcome{Kind: updatenotify.OutcomeFailed, ID: "library:acme/kokoro@2", Name: "kokoro", From: "1", To: "2", Err: "signature refused"},
		))
	})

	It("writes nothing from the first check or an on-demand check of a cluster frontend, and nothing from CheckNow", func() {
		bm := &passManager{upgrades: map[string]gallery.UpgradeInfo{"llama-cpp": up("llama-cpp", "1", "2")}}
		uc, a := newChecker(true, &gorm.DB{}, bm)
		uc.runOnDemand(ctx)
		uc.CheckNow(ctx)
		d, f, r := a.calls()
		Expect([]int{d, f, r}).To(Equal([]int{0, 0, 0}))
	})

	It("writes from every installing pass of a standalone process", func() {
		bm := &passManager{upgrades: map[string]gallery.UpgradeInfo{"llama-cpp": up("llama-cpp", "1", "2")}}
		uc, a := newChecker(true, nil, bm)
		uc.runOnDemand(ctx)
		d, _, r := a.calls()
		Expect([]int{d, r}).To(Equal([]int{1, 1}))
	})

	It("says a source failed only after two passes in a row, and forgets it after a good one", func() {
		bm := &passManager{upgrades: map[string]gallery.UpgradeInfo{}, checkErr: errors.New("registry unreachable")}
		uc, a := newChecker(false, &gorm.DB{}, bm)
		uc.runLeaderPass(ctx)
		Expect(a.failed[0]).To(BeEmpty(), "one failure is not enough")
		uc.runLeaderPass(ctx)
		Expect(a.failed[1]).To(Equal([]string{"gallery"}))
		uc.runLeaderPass(ctx)
		Expect(a.failed[2]).To(Equal([]string{"gallery"}))
		bm.set(func(m *passManager) { m.checkErr = nil })
		uc.runLeaderPass(ctx)
		Expect(a.failed[3]).To(BeEmpty())
		bm.set(func(m *passManager) { m.checkErr = errors.New("again") })
		uc.runLeaderPass(ctx)
		Expect(a.failed[4]).To(BeEmpty(), "the count starts over")
	})

	It("does not fail on a checker with no announcer", func() {
		bm := &passManager{upgrades: map[string]gallery.UpgradeInfo{"llama-cpp": up("llama-cpp", "1", "2")}}
		uc := NewUpgradeChecker(&config.ApplicationConfig{}, nil, &gorm.DB{}, func() galleryop.BackendManager { return bm })
		Expect(func() { uc.runLeaderPass(ctx) }).NotTo(Panic())
	})

	Describe("through the real inbox", func() {
		var (
			svc   *notify.Service
			hooks int
		)
		BeforeEach(func() {
			st, err := notify.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			svc = notify.NewService(notify.Options{Store: st})
			hooks = 0
			svc.OnAdd(func(notify.Notification) { hooks++ })
		})
		checker := func(auto bool, bm *passManager) *UpgradeChecker {
			uc := NewUpgradeChecker(&config.ApplicationConfig{AutoUpgradeBackends: auto}, nil, &gorm.DB{}, func() galleryop.BackendManager { return bm })
			uc.SetAnnouncer(&updatenotify.Emitter{
				Inbox:  func() *notify.Service { return svc },
				Admins: func(context.Context) ([]string, error) { return []string{"admin"}, nil },
			})
			return uc
		}
		inbox := func() []notify.Notification {
			got, err := svc.List(ctx, "admin", false, 0)
			Expect(err).NotTo(HaveOccurred())
			return got
		}

		It("posts the digest once and stays quiet at the next pass", func() {
			bm := &passManager{upgrades: map[string]gallery.UpgradeInfo{"llama-cpp": up("llama-cpp", "1", "2")}}
			uc := checker(false, bm)
			uc.runLeaderPass(ctx)
			uc.runLeaderPass(ctx)
			Expect(inbox()).To(HaveLen(1))
			Expect(inbox()[0].Title).To(Equal("1 backend update available"))
			Expect(hooks).To(Equal(1))
		})

		It("posts a result and no digest with the automatic update on, and a failure only once", func() {
			bm := &passManager{
				upgrades: map[string]gallery.UpgradeInfo{"whisper": up("whisper", "1", "3")},
				failing:  map[string]error{"whisper": errors.New("download interrupted")},
			}
			uc := checker(true, bm)
			uc.runLeaderPass(ctx)
			uc.runLeaderPass(ctx)
			got := inbox()
			Expect(got).To(HaveLen(1), "the same failure at the next pass is silent")
			Expect(got[0].Title).To(Equal("Backends: 1 failed"))
			Expect(got[0].Link).To(Equal(updatenotify.LinkNeedsYou))
			Expect(hooks).To(Equal(1))
		})
	})
})

var _ = Describe("what the add-on pass tells the inbox", func() {
	It("reports updated, failed and held add-ons, and skips a candidate that failed before", func() {
		out := addonOutcomes([]addons.AutoUpgradeResult{
			{AddonID: "tables", From: "2.3.0", To: "2.4.0", Upgraded: true, CandidateDigest: "sha256:a"},
			{AddonID: "notes", From: "0.8.0", To: "0.8.2", Err: errors.New("digest mismatch"), CandidateDigest: "sha256:b"},
			{AddonID: "mail", From: "1.0.0", To: "1.1.0", Err: errors.New("needs host access"), HeldForApproval: true, CandidateDigest: "sha256:c"},
			{AddonID: "old", From: "1", To: "2", SkippedAsFailure: true, CandidateDigest: "sha256:d"},
		})
		Expect(out).To(Equal([]updatenotify.Outcome{
			{Kind: updatenotify.OutcomeUpdated, ID: "tables@sha256:a", Name: "tables", From: "2.3.0", To: "2.4.0"},
			{Kind: updatenotify.OutcomeFailed, ID: "notes@sha256:b", Name: "notes", From: "0.8.0", To: "0.8.2", Err: "digest mismatch"},
			{Kind: updatenotify.OutcomeHeld, ID: "mail@sha256:c", Name: "mail", From: "1.0.0", To: "1.1.0"},
		}))
		Expect(addonOutcomes(nil)).To(BeEmpty())
	})
})

var _ = Describe("who is told about updates", func() {
	ctx := context.Background()

	It("is every active administrator, and nobody else", func() {
		db, err := auth.OpenSQLiteFile(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
		Expect(db.AutoMigrate(&auth.User{})).To(Succeed())
		for _, u := range []auth.User{
			{ID: "a1", Role: auth.RoleAdmin, Status: auth.StatusActive},
			{ID: "a2", Role: auth.RoleAdmin, Status: auth.StatusActive},
			{ID: "a3", Role: auth.RoleAdmin, Status: auth.StatusPending},
			{ID: "u1", Role: auth.RoleUser, Status: auth.StatusActive},
		} {
			Expect(db.Create(&u).Error).To(Succeed())
		}
		app := &Application{authDB: db}
		ids, err := app.updateAdmins(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).To(Equal([]string{"a1", "a2"}))
	})

	It("is the anonymous principal when authentication is off", func() {
		ids, err := (&Application{}).updateAdmins(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).To(Equal([]string{studio.AnonymousPrincipal}))
	})

	It("writes nothing, and does not fail, while the chats are off", func() {
		app := &Application{}
		Expect(app.updateEmitter().Digest(ctx, backendupdates.Snapshot{})).To(Succeed())
	})

})
