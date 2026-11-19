package modelupdates_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/pkg/downloader"
)

// clusterLocks is the advisory lock of a cluster: one holder per model, across
// every frontend that shares it.
type clusterLocks struct {
	mu     sync.Mutex
	holder map[string]string
}

func newClusterLocks() *clusterLocks { return &clusterLocks{holder: map[string]string{}} }

func (c *clusterLocks) lockFor(frontend string) gallery.ModelLock {
	return func(_ context.Context, name string) (func(), error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if _, held := c.holder[name]; held {
			return nil, gallery.ErrModelLocked
		}
		c.holder[name] = frontend
		return func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.holder[name] == frontend {
				delete(c.holder, name)
			}
		}, nil
	}
}

func (c *clusterLocks) heldBy(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.holder[name]
}

var _ = Describe("Ending the changes of a stopped frontend", func() {
	var (
		f     *fixture
		ctx   context.Context
		locks *clusterLocks
		now   time.Time
	)

	frontend := func(name string, rt modelupdates.Runtime) *modelupdates.Upgrader {
		return &modelupdates.Upgrader{SystemState: f.systemState, ListGallery: f.list, Runtime: rt,
			Lock: locks.lockFor(name), Instance: name, Now: func() time.Time { return now }}
	}
	journal := func() *gallery.Journal {
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		return sc.Upgrade
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(f.dir, name))
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}

	BeforeEach(func() {
		f = newFixture()
		ctx = context.Background()
		locks = newClusterLocks()
		now = time.Now().UTC()
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v1")))
		f.install("tiny")
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
	})

	It("never touches an upgrade that a live frontend runs, however old its journal looks", func() {
		staging := make(chan struct{})
		finish := make(chan struct{})
		a := frontend("a", newRuntime("tiny"))
		a.Download = func(c context.Context, tasks []downloader.FileTask, st func(string, string, string, float64)) error {
			close(staging)
			<-finish
			return downloader.DownloadFilesWithContext(c, tasks, st)
		}
		done := make(chan error, 1)
		go func() { done <- a.Upgrade(ctx, modelupdates.Request{Name: "tiny"}) }()
		Eventually(staging).Should(BeClosed())

		b := frontend("b", newRuntime())
		now = now.Add(time.Hour)
		recovered, err := b.RecoverAbandoned(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(recovered).To(BeEmpty())
		Expect(journal().Phase).To(Equal(gallery.PhaseStaging))

		By("refusing a second upgrade of the model on another frontend")
		Expect(b.Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(MatchError(modelupdates.ErrBusy))

		close(finish)
		Expect(<-done).To(Succeed())
		Expect(journal().Phase).To(Equal(gallery.PhaseConfirmed))
		Expect(locks.heldBy("tiny")).To(BeEmpty(), "the upgrade let the lock go")
	})

	It("puts the old version back when the frontend of an upgrade died after the stop", func() {
		writeJournal(f, &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseLoading, Instance: "a",
			UpdatedAt: now.Add(-5 * time.Minute), WasLoaded: true,
			Previous: &gallery.Previous{HadConfig: true, ConfigYAML: b64(read("tiny.yaml"))}})
		rt := newRuntime()
		b := frontend("b", rt)

		By("leaving it while the lock of the dead frontend is not yet free")
		release, err := locks.lockFor("a")(ctx, "tiny")
		Expect(err).ToNot(HaveOccurred())
		recovered, err := b.RecoverAbandoned(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(recovered).To(BeEmpty())

		release()
		recovered, err = b.RecoverAbandoned(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(recovered).To(ConsistOf("tiny"))
		Expect(journal().Phase).To(Equal(gallery.PhaseRolledBack))
		Expect(rt.calls).To(ContainElements("reload", "load"), "the old version runs again")
	})

	It("leaves a journal that was touched recently, even with its lock free", func() {
		writeJournal(f, &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseStaging, Instance: "a", UpdatedAt: now})
		recovered, err := frontend("b", newRuntime()).RecoverAbandoned(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(recovered).To(BeEmpty())
		Expect(journal().Phase).To(Equal(gallery.PhaseStaging))
	})

	It("finishes an install that a dead frontend left after writing the new config", func() {
		cfg := read("tiny.yaml")
		writeJournal(f, &gallery.Journal{Kind: gallery.JournalInstall, Phase: gallery.PhaseCommitting, Instance: "a",
			UpdatedAt: now.Add(-5 * time.Minute), ToConfigSHA256: gallery.SHA256Hex([]byte(cfg))})
		rt := newRuntime()
		recovered, err := frontend("b", rt).RecoverAbandoned(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(recovered).To(ConsistOf("tiny"))
		Expect(journal()).To(BeNil())
		Expect(read("tiny.yaml")).To(Equal(cfg))
		Expect(rt.calls).To(ContainElement("reload"), "every frontend reads the config again")
	})

	It("does nothing without a cluster lock", func() {
		writeJournal(f, &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseStaging, Instance: "a", UpdatedAt: now.Add(-time.Hour)})
		u := frontend("b", newRuntime())
		u.Lock = nil
		recovered, err := u.RecoverAbandoned(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(recovered).To(BeEmpty())
	})
})

func writeJournal(f *fixture, j *gallery.Journal) {
	sc, err := gallery.ReadSidecar(f.dir, "tiny")
	Expect(err).ToNot(HaveOccurred())
	sc.Upgrade = j
	Expect(gallery.WriteSidecar(f.dir, "tiny", sc)).To(Succeed())
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
