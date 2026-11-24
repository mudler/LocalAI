package application

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// scriptedManager answers CheckUpgrades from a script and can block.
type scriptedManager struct {
	galleryop.BackendManager // unused methods panic on purpose

	calls   atomic.Int32
	upgrade atomic.Int32
	gate    chan struct{} // when set, a check waits for it or for its context
	mu      sync.Mutex
	result  map[string]gallery.UpgradeInfo
	err     error
}

func (m *scriptedManager) CheckUpgrades(ctx context.Context) (map[string]gallery.UpgradeInfo, error) {
	m.calls.Add(1)
	if m.gate != nil {
		select {
		case <-m.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func (m *scriptedManager) UpgradeBackend(context.Context, *galleryop.ManagementOp[gallery.GalleryBackend, any], galleryop.ProgressCallback) error {
	m.upgrade.Add(1)
	return nil
}

func (m *scriptedManager) set(res map[string]gallery.UpgradeInfo, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.result, m.err = res, err
}

type scriptedLibrary struct {
	fakeLibrary
	err error
}

func (l *scriptedLibrary) Updates(ctx context.Context) ([]backendlib.Update, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.fakeLibrary.Updates(ctx)
}

var _ = Describe("the backend update read model", func() {
	oneUpdate := map[string]gallery.UpgradeInfo{"llama-cpp": {BackendName: "llama-cpp", InstalledVersion: "1", AvailableVersion: "2"}}

	newChecker := func(db *gorm.DB, bm *scriptedManager) *UpgradeChecker {
		return NewUpgradeChecker(&config.ApplicationConfig{}, nil, db, func() galleryop.BackendManager { return bm })
	}

	BeforeEach(func() {
		old := checkNowCooldown
		DeferCleanup(func() { checkNowCooldown = old })
		checkNowCooldown = 0
	})

	It("answers on a nil checker with both sources disabled", func() {
		var uc *UpgradeChecker
		for _, snap := range []backendupdates.Snapshot{uc.UpdatesSnapshot(context.Background()), uc.CheckNow(context.Background())} {
			Expect(snap.Items).To(BeEmpty())
			Expect(snap.Sources["gallery"].State).To(Equal(backendupdates.StateDisabled))
			Expect(snap.Sources["library"].State).To(Equal(backendupdates.StateDisabled))
			Expect(snap.Sources["gallery"].Reason).NotTo(BeEmpty())
		}
	})

	It("says never checked before the first check, and does not contact a registry to say so", func() {
		bm := &scriptedManager{result: oneUpdate}
		uc := newChecker(nil, bm)
		snap := uc.UpdatesSnapshot(context.Background())
		Expect(snap.Sources["gallery"].State).To(Equal(backendupdates.StateNever))
		Expect(snap.CheckedAt).To(BeZero())
		Expect(bm.calls.Load()).To(BeZero())
	})

	It("check now waits for the check and returns fresh data with checkedAt", func() {
		bm := &scriptedManager{result: oneUpdate}
		uc := newChecker(nil, bm)
		before := time.Now()
		snap := uc.CheckNow(context.Background())
		Expect(bm.calls.Load()).To(Equal(int32(1)))
		Expect(snap.Items).To(HaveLen(1))
		Expect(snap.Items[0].Name).To(Equal("llama-cpp"))
		Expect(snap.Sources["gallery"].State).To(Equal(backendupdates.StateOK))
		Expect(snap.CheckedAt).To(BeTemporally(">=", before))
		Expect(bm.upgrade.Load()).To(BeZero(), "a check installs nothing")
	})

	It("never installs from a check, even with the automatic update on", func() {
		bm := &scriptedManager{result: oneUpdate}
		uc := NewUpgradeChecker(&config.ApplicationConfig{AutoUpgradeBackends: true}, nil, nil, func() galleryop.BackendManager { return bm })
		snap := uc.CheckNow(context.Background())
		Expect(snap.AutoUpdate.Backends).To(BeTrue())
		Expect(bm.upgrade.Load()).To(BeZero())
	})

	It("shows a failed check as an error in the source and keeps the old list, marked stale", func() {
		bm := &scriptedManager{result: oneUpdate}
		uc := newChecker(nil, bm)
		uc.CheckNow(context.Background())

		bm.set(nil, errors.New("registry unreachable"))
		snap := uc.CheckNow(context.Background())
		src := snap.Sources["gallery"]
		Expect(src.State).To(Equal(backendupdates.StateFailed))
		Expect(src.Error).To(ContainSubstring("registry unreachable"))
		Expect(src.Stale).To(BeTrue())
		Expect(src.LastSuccessAt).NotTo(BeZero())
		Expect(snap.Items).To(HaveLen(1), "the last good list stays")

		bm.set(oneUpdate, nil)
		snap = uc.CheckNow(context.Background())
		Expect(snap.Sources["gallery"].State).To(Equal(backendupdates.StateOK))
		Expect(snap.Sources["gallery"].Error).To(BeEmpty())
	})

	It("is throttled inside the cool-down and says so", func() {
		checkNowCooldown = time.Minute
		bm := &scriptedManager{result: oneUpdate}
		uc := newChecker(nil, bm)
		first := uc.CheckNow(context.Background())
		Expect(first.Throttled).To(BeFalse())
		second := uc.CheckNow(context.Background())
		Expect(second.Throttled).To(BeTrue())
		Expect(second.Items).To(HaveLen(1))
		Expect(bm.calls.Load()).To(Equal(int32(1)))
	})

	It("lets concurrent callers share one check", func() {
		bm := &scriptedManager{result: oneUpdate, gate: make(chan struct{})}
		uc := newChecker(nil, bm)
		var wg sync.WaitGroup
		results := make([]backendupdates.Snapshot, 4)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = uc.CheckNow(context.Background())
			}()
		}
		Eventually(bm.calls.Load).Should(BeNumerically(">=", 1))
		time.Sleep(50 * time.Millisecond)
		close(bm.gate)
		wg.Wait()
		Expect(bm.calls.Load()).To(Equal(int32(1)))
		for _, r := range results {
			Expect(r.Items).To(HaveLen(1))
		}
	})

	It("turns a timeout into the error of the source", func() {
		old := checkNowTimeout
		DeferCleanup(func() { checkNowTimeout = old })
		checkNowTimeout = 50 * time.Millisecond
		bm := &scriptedManager{result: oneUpdate, gate: make(chan struct{})}
		defer close(bm.gate)
		uc := newChecker(nil, bm)
		snap := uc.CheckNow(context.Background())
		Expect(snap.Sources["gallery"].State).To(Equal(backendupdates.StateFailed))
		Expect(snap.Sources["gallery"].Error).To(ContainSubstring("timed out"))
	})

	It("gives a caller that leaves its context back without waiting for the check", func() {
		bm := &scriptedManager{result: oneUpdate, gate: make(chan struct{})}
		defer close(bm.gate)
		uc := newChecker(nil, bm)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		start := time.Now()
		uc.CheckNow(ctx)
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
	})

	Describe("the library source", func() {
		var lib *scriptedLibrary
		withLibrary := func(uc *UpgradeChecker) {
			uc.SetLibraryBackends(func() libraryUpgrader { return lib })
		}
		BeforeEach(func() {
			lib = &scriptedLibrary{fakeLibrary: fakeLibrary{updates: []backendlib.Update{{Name: "piper", SourceID: "official-backends", InstalledVersion: "1", Version: "2", Digest: "sha256:aa"}}}}
		})

		It("is disabled on a host without a library", func() {
			uc := newChecker(nil, &scriptedManager{result: oneUpdate})
			snap := uc.CheckNow(context.Background())
			Expect(snap.Sources["library"].State).To(Equal(backendupdates.StateDisabled))
			Expect(snap.Sources["library"].Reason).NotTo(BeEmpty())
		})

		It("refreshes the library index first and lists its updates", func() {
			uc := newChecker(nil, &scriptedManager{result: oneUpdate})
			withLibrary(uc)
			var refreshed atomic.Int32
			uc.SetLibraryRefresh(func(context.Context) error { refreshed.Add(1); return nil })
			snap := uc.CheckNow(context.Background())
			Expect(refreshed.Load()).To(Equal(int32(1)))
			Expect(snap.Sources["library"].State).To(Equal(backendupdates.StateOK))
			Expect(snap.Sources["library"].Note).To(BeEmpty(), "standalone sees all its library backends")
			Expect(snap.Items).To(HaveLen(2))
			Expect(snap.Items[1].ID).To(Equal("library:official-backends/piper"))
		})

		It("does not refresh the index on a periodic check", func() {
			uc := newChecker(nil, &scriptedManager{result: oneUpdate})
			withLibrary(uc)
			uc.SetLibraryRefresh(func(context.Context) error { Fail("refreshed"); return nil })
			uc.runCheck(context.Background(), false)
			Expect(uc.UpdatesSnapshot(context.Background()).Items).To(HaveLen(2))
		})

		It("reports a failed refresh as the error of the library alone and keeps its old list", func() {
			uc := newChecker(nil, &scriptedManager{result: oneUpdate})
			withLibrary(uc)
			uc.SetLibraryRefresh(func(context.Context) error { return nil })
			uc.CheckNow(context.Background())

			uc.SetLibraryRefresh(func(context.Context) error { return errors.New("library not reachable") })
			snap := uc.CheckNow(context.Background())
			Expect(snap.Sources["gallery"].State).To(Equal(backendupdates.StateOK))
			lsrc := snap.Sources["library"]
			Expect(lsrc.State).To(Equal(backendupdates.StateFailed))
			Expect(lsrc.Error).To(ContainSubstring("library not reachable"))
			Expect(lsrc.Stale).To(BeTrue())
			Expect(snap.Items).To(HaveLen(2))
		})

		It("reports a failed comparison as the error of the library", func() {
			uc := newChecker(nil, &scriptedManager{result: oneUpdate})
			withLibrary(uc)
			lib.err = errors.New("owner not reachable")
			snap := uc.CheckNow(context.Background())
			Expect(snap.Sources["library"].State).To(Equal(backendupdates.StateFailed))
			Expect(snap.Sources["library"].Stale).To(BeFalse(), "it never had a good list")
			Expect(snap.Sources["gallery"].State).To(Equal(backendupdates.StateOK))
		})
	})

	Describe("in a cluster", func() {
		// db is set but never reached: these are frontends that do not hold the
		// leader lock.
		It("lets a frontend that is not the leader run a fresh check against its own view and cache", func() {
			mine := &scriptedManager{result: oneUpdate}
			other := &scriptedManager{result: map[string]gallery.UpgradeInfo{}}
			frontendA := NewUpgradeChecker(&config.ApplicationConfig{AutoUpgradeBackends: true}, nil, &gorm.DB{}, func() galleryop.BackendManager { return mine })
			frontendB := NewUpgradeChecker(&config.ApplicationConfig{AutoUpgradeBackends: true}, nil, &gorm.DB{}, func() galleryop.BackendManager { return other })

			snap := frontendA.CheckNow(context.Background())
			Expect(snap.Items).To(HaveLen(1))
			Expect(mine.upgrade.Load()).To(BeZero(), "only the leader pass installs")

			// The other frontend did not learn from it: the cache is per instance.
			Expect(frontendB.UpdatesSnapshot(context.Background()).Sources["gallery"].State).To(Equal(backendupdates.StateNever))
			Expect(other.calls.Load()).To(BeZero())
			Expect(frontendB.CheckNow(context.Background()).Items).To(BeEmpty())
		})

		It("says that library backends on workers are not listed", func() {
			uc := newChecker(&gorm.DB{}, &scriptedManager{result: oneUpdate})
			uc.SetLibraryBackends(func() libraryUpgrader { return &scriptedLibrary{} })
			Expect(uc.CheckNow(context.Background()).Sources["library"].Note).To(ContainSubstring("not listed"))
		})

		It("names the models of a node that run the backend", func() {
			info := gallery.UpgradeInfo{BackendName: "llama-cpp", InstalledVersion: "1", AvailableVersion: "2",
				Nodes: []gallery.NodeVersionInfo{{NodeID: "n1", NodeName: "gpu-a", Version: "1"}}}
			uc := newChecker(&gorm.DB{}, &scriptedManager{result: map[string]gallery.UpgradeInfo{"llama-cpp": info}})
			uc.SetLoadedModels(func(context.Context) ([]backendupdates.LoadedModel, error) {
				return []backendupdates.LoadedModel{{NodeID: "n1", Model: "qwen", Backend: "llama-cpp"}}, nil
			})
			snap := uc.CheckNow(context.Background())
			Expect(snap.Items[0].Nodes).To(HaveLen(1))
			Expect(snap.Items[0].Nodes[0].Models).To(Equal([]string{"qwen"}))
		})

		It("still answers when the model list fails", func() {
			info := gallery.UpgradeInfo{BackendName: "llama-cpp", AvailableVersion: "2", Nodes: []gallery.NodeVersionInfo{{NodeID: "n1"}}}
			uc := newChecker(&gorm.DB{}, &scriptedManager{result: map[string]gallery.UpgradeInfo{"llama-cpp": info}})
			uc.SetLoadedModels(func(context.Context) ([]backendupdates.LoadedModel, error) { return nil, errors.New("db down") })
			snap := uc.CheckNow(context.Background())
			Expect(snap.Items).To(HaveLen(1))
			Expect(snap.Items[0].Nodes[0].Models).To(BeEmpty())
		})
	})
})
