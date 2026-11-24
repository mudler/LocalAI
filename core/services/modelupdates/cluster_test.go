package modelupdates_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/modelupdates"
)

// fakeWorkers records what an upgrade asks of the workers of a cluster. It
// shares the call list of the runtime, so a spec sees the order of both.
type fakeWorkers struct {
	mu        sync.Mutex
	rt        *fakeRuntime
	staged    []modelupdates.WorkerFile
	committed []modelupdates.WorkerFile
	discards  []bool // the evict flag of each discard
	commitErr error
}

func (w *fakeWorkers) record(call string) {
	w.rt.mu.Lock()
	defer w.rt.mu.Unlock()
	w.rt.calls = append(w.rt.calls, call)
}

func (w *fakeWorkers) Stage(_ context.Context, _ string, files []modelupdates.WorkerFile) {
	w.mu.Lock()
	w.staged = append(w.staged, files...)
	w.mu.Unlock()
	w.record("workers:stage")
}

func (w *fakeWorkers) Commit(_ context.Context, _ string, files []modelupdates.WorkerFile) error {
	w.mu.Lock()
	w.committed = append(w.committed, files...)
	w.mu.Unlock()
	w.record("workers:commit")
	return w.commitErr
}

func (w *fakeWorkers) Discard(_ context.Context, _ string, _ []modelupdates.WorkerFile, evict bool) {
	w.mu.Lock()
	w.discards = append(w.discards, evict)
	w.mu.Unlock()
	if evict {
		w.record("workers:evict")
		return
	}
	w.record("workers:discard")
}

var _ = Describe("A model upgrade in a cluster", func() {
	var (
		f       *fixture
		ctx     context.Context
		rt      *fakeRuntime
		workers *fakeWorkers
	)

	upgrader := func() *modelupdates.Upgrader {
		return &modelupdates.Upgrader{SystemState: f.systemState, ListGallery: f.list, Runtime: rt, Workers: workers, Instance: "a"}
	}
	journal := func() *gallery.Journal {
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		return sc.Upgrade
	}

	BeforeEach(func() {
		f = newFixture()
		ctx = context.Background()
		rt = newRuntime("tiny")
		workers = &fakeWorkers{rt: rt}
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v1")))
		f.install("tiny")
	})

	It("has the workers stage the new file with its checksum, and place it after the commit, before the load", func() {
		v2 := f.weights("tiny.gguf", "v2")
		f.publish(tinyEntry(f, v2))
		Expect(upgrader().Upgrade(ctx, modelupdates.Request{OpID: "op", Name: "tiny"})).To(Succeed())

		Expect(workers.staged).To(ConsistOf(modelupdates.WorkerFile{Path: "tiny.gguf", URI: v2.URI, SHA256: v2.SHA256}))
		Expect(workers.committed).To(ConsistOf(modelupdates.WorkerFile{Path: "tiny.gguf", URI: v2.URI, SHA256: v2.SHA256}))
		Expect(rt.calls).To(Equal([]string{"workers:stage", "unload", "workers:commit", "reload", "load"}))
		Expect(workers.discards).To(BeEmpty())
	})

	It("drops the staged files on the workers when the upgrade fails before the commit", func() {
		bad := f.weights("tiny.gguf.src", "v2")
		bad.Filename = "tiny.gguf"
		bad.SHA256 = gallery.SHA256Hex([]byte("something else"))
		f.publish(tinyEntry(f, bad))
		Expect(upgrader().Upgrade(ctx, modelupdates.Request{Name: "tiny"})).ToNot(Succeed())

		Expect(workers.discards).To(Equal([]bool{false}), "nothing was placed, so nothing is evicted")
		Expect(workers.committed).To(BeEmpty())
		Expect(journal().Phase).To(Equal(gallery.PhaseFailed))
	})

	It("evicts the new copies on the workers when the load check fails, before the old version loads", func() {
		rt.loadFails = 1
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		Expect(upgrader().Upgrade(ctx, modelupdates.Request{Name: "tiny"})).ToNot(Succeed())

		Expect(workers.discards).To(Equal([]bool{true}))
		Expect(rt.calls).To(Equal([]string{"workers:stage", "unload", "workers:commit", "reload", "load", "workers:evict", "reload", "load"}))
		Expect(journal().Phase).To(Equal(gallery.PhaseRolledBack))
	})

	It("rolls back when the workers cannot place the files", func() {
		workers.commitErr = errors.New("object storage refused the delete")
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		err := upgrader().Upgrade(ctx, modelupdates.Request{Name: "tiny"})
		Expect(err).To(MatchError(ContainSubstring("object storage refused the delete")))

		data, rerr := os.ReadFile(filepath.Join(f.dir, "tiny.gguf"))
		Expect(rerr).ToNot(HaveOccurred())
		Expect(string(data)).To(Equal("v1"))
		Expect(journal().Phase).To(Equal(gallery.PhaseRolledBack))
		Expect(workers.discards).To(Equal([]bool{true}))
	})

	It("asks nothing of the workers for a config-only upgrade", func() {
		entry := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		entry.Overrides["context_size"] = 4096
		f.publish(entry)
		Expect(upgrader().Upgrade(ctx, modelupdates.Request{Name: "tiny", ConfigOnly: true})).To(Succeed())
		Expect(workers.staged).To(BeEmpty())
		Expect(workers.committed).To(BeEmpty())
	})

	It("says that canary is not available yet, records it, and stops and starts", func() {
		var messages []string
		u := upgrader()
		u.StrategyNote = func(context.Context, string) string { return modelupdates.CanaryNotAvailable }
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tiny", Progress: func(p modelupdates.Progress) {
			messages = append(messages, p.Message)
		}})).To(Succeed())

		Expect(messages).To(ContainElement(ContainSubstring("canary is not available yet")))
		Expect(journal().Note).To(Equal(modelupdates.CanaryNotAvailable))
		Expect(rt.calls).To(ContainElements("unload", "reload", "load"))

		svc := f.service(modelupdates.Options{})
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.Upgrade).ToNot(BeNil())
		Expect(it.Upgrade.Note).To(Equal(modelupdates.CanaryNotAvailable))
	})
})
