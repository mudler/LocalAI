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
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// countingBackendManager reports one available upgrade and counts the calls
// that change a backend.
type countingBackendManager struct {
	galleryop.BackendManager // unused methods panic on purpose

	mu       sync.Mutex
	checks   int
	upgrades []string
	checkErr error
}

func (m *countingBackendManager) CheckUpgrades(context.Context) (map[string]gallery.UpgradeInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checks++
	if m.checkErr != nil {
		return nil, m.checkErr
	}
	return map[string]gallery.UpgradeInfo{"llama-cpp": {BackendName: "llama-cpp", AvailableVersion: "2"}}, nil
}

func (m *countingBackendManager) UpgradeBackend(_ context.Context, op *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upgrades = append(m.upgrades, op.GalleryElementName)
	return nil
}

func (m *countingBackendManager) counts() (checks int, upgrades []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checks, append([]string(nil), m.upgrades...)
}

// Only the leader may install: the first check of a frontend and the on-demand
// checks run on every frontend, so they may refresh the cache but must not
// upgrade when several frontends share a cluster.
var _ = Describe("who runs the automatic backend upgrade", func() {
	newChecker := func(db *gorm.DB, bm *countingBackendManager) *UpgradeChecker {
		uc := NewUpgradeChecker(&config.ApplicationConfig{AutoUpgradeBackends: true}, nil, db,
			func() galleryop.BackendManager { return bm })
		uc.initialDelay = time.Millisecond
		return uc
	}

	// A cluster frontend: db is set but never reached, because the leader
	// loop only touches it after a full interval.
	runFrontend := func(uc *UpgradeChecker) (stop func()) {
		ctx, cancel := context.WithCancel(context.Background())
		go uc.Run(ctx)
		return func() { cancel(); <-uc.done }
	}

	It("does not upgrade from the first check of a cluster frontend", func() {
		bm := &countingBackendManager{}
		stop := runFrontend(newChecker(&gorm.DB{}, bm))
		defer stop()

		Eventually(func() int { c, _ := bm.counts(); return c }, 2*time.Second).Should(BeNumerically(">=", 1))
		Consistently(func() []string { _, u := bm.counts(); return u }, 300*time.Millisecond).Should(BeEmpty())
	})

	It("does not upgrade from an on-demand check of a cluster frontend", func() {
		bm := &countingBackendManager{}
		uc := newChecker(&gorm.DB{}, bm)
		stop := runFrontend(uc)
		defer stop()
		Eventually(func() int { c, _ := bm.counts(); return c }, 2*time.Second).Should(BeNumerically(">=", 1))

		before, _ := bm.counts()
		uc.TriggerCheck()
		Eventually(func() int { c, _ := bm.counts(); return c }, 2*time.Second).Should(BeNumerically(">", before))
		Consistently(func() []string { _, u := bm.counts(); return u }, 300*time.Millisecond).Should(BeEmpty())
		Expect(uc.GetAvailableUpgrades()).To(HaveKey("llama-cpp"), "the cache is still refreshed")
	})

	It("upgrades when the leader runs its periodic pass", func() {
		bm := &countingBackendManager{}
		uc := newChecker(&gorm.DB{}, bm)
		uc.runLeaderPass(context.Background())
		_, upgrades := bm.counts()
		Expect(upgrades).To(Equal([]string{"llama-cpp"}))
	})

	It("keeps upgrading from the first check and from triggers in standalone mode", func() {
		bm := &countingBackendManager{}
		uc := newChecker(nil, bm)
		stop := runFrontend(uc)
		defer stop()
		Eventually(func() int { _, u := bm.counts(); return len(u) }, 2*time.Second).Should(BeNumerically(">=", 1))

		var n atomic.Int32
		_, u := bm.counts()
		n.Store(int32(len(u)))
		uc.TriggerCheck()
		Eventually(func() int { _, u := bm.counts(); return len(u) }, 2*time.Second).Should(BeNumerically(">", int(n.Load())))
	})
})

var _ = Describe("the record of the last check", func() {
	It("keeps the time and the error of a failed check, and clears the error on success", func() {
		bm := &countingBackendManager{checkErr: errors.New("registry unreachable")}
		uc := NewUpgradeChecker(&config.ApplicationConfig{}, nil, nil, func() galleryop.BackendManager { return bm })
		Expect(uc.LastCheckTime()).To(BeZero())
		Expect(uc.LastCheckError()).NotTo(HaveOccurred())

		uc.runCheck(context.Background(), false)
		Expect(uc.LastCheckTime()).NotTo(BeZero())
		Expect(uc.LastCheckError()).To(MatchError("registry unreachable"))

		bm.mu.Lock()
		bm.checkErr = nil
		bm.mu.Unlock()
		uc.runCheck(context.Background(), false)
		Expect(uc.LastCheckError()).NotTo(HaveOccurred())
		Expect(uc.GetAvailableUpgrades()).To(HaveKey("llama-cpp"))
	})

	It("is safe on a nil checker", func() {
		var uc *UpgradeChecker
		Expect(uc.LastCheckTime()).To(BeZero())
		Expect(uc.LastCheckError()).NotTo(HaveOccurred())
	})
})
