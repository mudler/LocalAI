package modeladmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("ApplyRemoteChange", func() {
	var (
		dir    string
		loader *config.ModelConfigLoader
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		loader = config.NewModelConfigLoader(dir)
	})

	writeYAML := func(name string, body map[string]any) {
		body["name"] = name
		data, err := yaml.Marshal(body)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), data, 0644)).To(Succeed())
	}

	It("loads a peer-created config from disk on an install event", func() {
		// Peer wrote the YAML to the shared models dir; this replica has not
		// loaded it yet (empty in-memory loader).
		writeYAML("peer-alias", map[string]any{"alias": "qwen"})
		_, ok := loader.GetModelConfig("peer-alias")
		Expect(ok).To(BeFalse(), "precondition: not yet in memory")

		err := ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{
			Element: "peer-alias", Op: "install",
		}, nil)
		Expect(err).ToNot(HaveOccurred())

		_, ok = loader.GetModelConfig("peer-alias")
		Expect(ok).To(BeTrue(), "install event must reload the new config from disk")
	})

	It("idempotently reconciles the authoritative revision instead of the event revision", func() {
		writeYAML("peer-alias", map[string]any{"alias": "qwen"})
		lifecycle := &fakeRevisionLifecycle{}
		evt := messaging.CacheInvalidateEvent{Element: "peer-alias", Op: "install", ConfigRevision: "stale-event-revision"}

		Expect(ApplyRemoteChange(context.Background(), loader, dir, evt, lifecycle)).To(Succeed())
		Expect(ApplyRemoteChange(context.Background(), loader, dir, evt, lifecycle)).To(Succeed())
		Expect(lifecycle.calls).To(HaveLen(2))
		_, ok := loader.GetModelConfig("peer-alias")
		Expect(ok).To(BeTrue())
		revision, err := loader.RevisionForPath("peer-alias", dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(lifecycle.calls[0].revision).To(Equal(revision))
		Expect(lifecycle.calls[1].revision).To(Equal(revision))
	})

	It("uses the authoritative installed config for reordered install events", func() {
		writeYAML("peer-alias", map[string]any{"backend": "llama-cpp", "context_size": 8192})
		lifecycle := &fakeRevisionLifecycle{}
		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{
			Element: "peer-alias", Op: "install", ConfigRevision: "old",
		}, lifecycle)).To(Succeed())

		writeYAML("peer-alias", map[string]any{"backend": "llama-cpp", "context_size": 10000})
		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{
			Element: "peer-alias", Op: "install", ConfigRevision: "new-event-arrived-first",
		}, lifecycle)).To(Succeed())
		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{
			Element: "peer-alias", Op: "install", ConfigRevision: "old-event-arrived-late",
		}, lifecycle)).To(Succeed())

		loaded, ok := loader.GetModelConfig("peer-alias")
		Expect(ok).To(BeTrue())
		Expect(loaded.ContextSize).To(HaveValue(Equal(10000)))
		revision, err := loader.RevisionForPath(loaded.Name, dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(lifecycle.calls).To(HaveLen(3))
		Expect(lifecycle.calls[1].revision).To(Equal(revision))
		Expect(lifecycle.calls[2].revision).To(Equal(revision))
	})

	It("does not let a delayed delete prune a reinstalled config", func() {
		writeYAML("reinstalled", map[string]any{"backend": "llama-cpp", "context_size": 10000})
		lifecycle := &fakeRevisionLifecycle{}
		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{
			Element: "reinstalled", Op: "delete", ConfigRevision: "obsolete-delete",
		}, lifecycle)).To(Succeed())

		loaded, ok := loader.GetModelConfig("reinstalled")
		Expect(ok).To(BeTrue())
		revision, err := loader.RevisionForPath(loaded.Name, dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(lifecycle.calls).To(HaveLen(1))
		Expect(lifecycle.calls[0].revision).To(Equal(revision))
		Expect(lifecycle.calls[0].disabled).To(BeFalse())
	})

	It("does not let a delayed install resurrect an authoritative delete", func() {
		writeYAML("deleted", map[string]any{"backend": "llama-cpp"})
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		Expect(os.Remove(filepath.Join(dir, "deleted.yaml"))).To(Succeed())
		lifecycle := &fakeRevisionLifecycle{}

		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{
			Element: "deleted", Op: "install", ConfigRevision: "obsolete-install",
		}, lifecycle)).To(Succeed())

		_, ok := loader.GetModelConfig("deleted")
		Expect(ok).To(BeFalse())
		Expect(lifecycle.calls).To(HaveLen(1))
		Expect(lifecycle.calls[0].disabled).To(BeTrue())
		Expect(lifecycle.calls[0].revision).To(Equal(DeletedModelConfigRevision("deleted")))
	})

	It("prunes a peer-deleted config that a reload-from-path cannot drop", func() {
		// Model is present in memory (loaded earlier) but its file is now gone
		// from the shared dir. LoadModelConfigsFromPath is additive, so only an
		// explicit prune can remove it - this is the cross-replica delete bug.
		writeYAML("doomed", map[string]any{"alias": "qwen"})
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		_, ok := loader.GetModelConfig("doomed")
		Expect(ok).To(BeTrue(), "precondition: in memory")
		Expect(os.Remove(filepath.Join(dir, "doomed.yaml"))).To(Succeed())

		err := ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{
			Element: "doomed", Op: "delete",
		}, nil)
		Expect(err).ToNot(HaveOccurred())

		_, ok = loader.GetModelConfig("doomed")
		Expect(ok).To(BeFalse(), "delete event must prune the element from memory")
	})

	It("does a full reload when no element is named", func() {
		writeYAML("m1", map[string]any{"alias": "qwen"})
		writeYAML("m2", map[string]any{"alias": "qwen"})

		err := ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{}, nil)
		Expect(err).ToNot(HaveOccurred())

		_, ok1 := loader.GetModelConfig("m1")
		_, ok2 := loader.GetModelConfig("m2")
		Expect(ok1).To(BeTrue())
		Expect(ok2).To(BeTrue())
	})

	It("authoritatively reconciles changed and deleted configs when no element is named", func() {
		writeYAML("changed", map[string]any{"backend": "llama-cpp", "context_size": 8192})
		writeYAML("deleted", map[string]any{"backend": "llama-cpp"})
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())

		writeYAML("changed", map[string]any{"backend": "llama-cpp", "context_size": 10000})
		Expect(os.Remove(filepath.Join(dir, "deleted.yaml"))).To(Succeed())
		lifecycle := &fakeRevisionLifecycle{}

		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{}, lifecycle)).To(Succeed())

		loaded, ok := loader.GetModelConfig("changed")
		Expect(ok).To(BeTrue())
		Expect(loaded.ContextSize).To(HaveValue(Equal(10000)))
		_, ok = loader.GetModelConfig("deleted")
		Expect(ok).To(BeFalse())
		changedRevision, err := loader.RevisionForPath(loaded.Name, dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(lifecycle.calls).To(ConsistOf(
			revisionLifecycleCall{oldName: "changed", newName: "changed", revision: changedRevision},
			revisionLifecycleCall{oldName: "deleted", newName: "deleted", revision: DeletedModelConfigRevision("deleted"), disabled: true},
		))
		Expect(lifecycle.batches).To(HaveLen(1))
		Expect(lifecycle.batches[0]).To(HaveLen(2))
	})

	It("keeps the complete live snapshot unchanged when a batched transition fails", func() {
		writeYAML("changed", map[string]any{"backend": "llama-cpp", "context_size": 8192})
		writeYAML("deleted", map[string]any{"backend": "llama-cpp"})
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())

		writeYAML("changed", map[string]any{"backend": "llama-cpp", "context_size": 10000})
		Expect(os.Remove(filepath.Join(dir, "deleted.yaml"))).To(Succeed())
		lifecycle := &fakeRevisionLifecycle{err: errors.New("injected second transition failure")}

		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{}, lifecycle)).To(
			MatchError(ContainSubstring("injected second transition failure")),
		)
		Expect(lifecycle.batches).To(HaveLen(1))
		Expect(lifecycle.batches[0]).To(HaveLen(2))
		loaded, ok := loader.GetModelConfig("changed")
		Expect(ok).To(BeTrue())
		Expect(loaded.ContextSize).To(HaveValue(Equal(8192)))
		_, ok = loader.GetModelConfig("deleted")
		Expect(ok).To(BeTrue())
	})

	It("serializes authoritative reads through lifecycle publication", func() {
		writeYAML("ordered", map[string]any{"backend": "llama-cpp", "context_size": 8192})
		lifecycle := newBlockingRevisionLifecycle()
		firstDone := make(chan error, 1)
		secondDone := make(chan error, 1)

		go func() {
			firstDone <- ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{Element: "ordered"}, lifecycle)
		}()
		Eventually(lifecycle.entered).Should(Receive())

		writeYAML("ordered", map[string]any{"backend": "llama-cpp", "context_size": 10000})
		go func() {
			secondDone <- ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{}, lifecycle)
		}()
		Consistently(lifecycle.entered).ShouldNot(Receive())

		close(lifecycle.release)
		Eventually(firstDone).Should(Receive(Succeed()))
		Eventually(secondDone).Should(Receive(Succeed()))
		Eventually(lifecycle.entered).Should(Receive())

		loaded, ok := loader.GetModelConfig("ordered")
		Expect(ok).To(BeTrue())
		Expect(loaded.ContextSize).To(HaveValue(Equal(10000)))
		revision, err := loader.RevisionForPath(loaded.Name, dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(lifecycle.revisions()).To(HaveLen(2))
		Expect(lifecycle.revisions()[1]).To(Equal(revision))
	})

	It("serializes peer publication before a newer local edit", func() {
		writeYAML("ordered", map[string]any{"backend": "llama-cpp", "context_size": 8192})
		lifecycle := newBlockingRevisionLifecycle()
		appConfig := &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
		svc := NewConfigService(loader, appConfig, lifecycle)
		peerDone := make(chan error, 1)
		localDone := make(chan error, 1)

		go func() {
			peerDone <- ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{Element: "ordered"}, lifecycle)
		}()
		Eventually(lifecycle.entered).Should(Receive())

		go func() {
			_, err := svc.EditYAML(context.Background(), "ordered", []byte("name: ordered\nbackend: llama-cpp\ncontext_size: 10000\n"))
			localDone <- err
		}()
		Consistently(localDone).ShouldNot(Receive())
		Expect(readMap(filepath.Join(dir, "ordered.yaml"))).To(HaveKeyWithValue("context_size", 8192))

		close(lifecycle.release)
		Eventually(peerDone).Should(Receive(Succeed()))
		Eventually(localDone).Should(Receive(Succeed()))
		Eventually(lifecycle.entered).Should(Receive())

		loaded, ok := loader.GetModelConfig("ordered")
		Expect(ok).To(BeTrue())
		Expect(loaded.ContextSize).To(HaveValue(Equal(10000)))
		Expect(readMap(filepath.Join(dir, "ordered.yaml"))).To(HaveKeyWithValue("context_size", 10000))
		revision, err := loader.RevisionForPath(loaded.Name, dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(lifecycle.revisions()).To(HaveLen(2))
		Expect(lifecycle.revisions()[1]).To(Equal(revision))
	})

	It("loads a peer-persisted artifact binding without materializing", func() {
		const relative = ".artifacts/huggingface/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/snapshot"
		writeYAML("peer-managed", map[string]any{
			"backend": "transformers",
			"artifacts": []map[string]any{{
				"name": "model", "target": "model",
				"source": map[string]any{"type": "huggingface", "repo": "owner/repo", "revision": "main"},
				"resolved": map[string]any{
					"endpoint":  "https://huggingface.co",
					"revision":  "0123456789abcdef0123456789abcdef01234567",
					"cache_key": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				},
			}},
			"parameters": map[string]any{"model": "owner/repo"},
		})
		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{
			Element: "peer-managed", Op: "install",
		}, nil)).To(Succeed())
		loaded, found := loader.GetModelConfig("peer-managed")
		Expect(found).To(BeTrue())
		Expect(loaded.Model).To(Equal("owner/repo"))
		Expect(loaded.ModelFileName()).To(Equal(relative))
		Expect(loaded.Artifacts).To(HaveLen(1))
		Expect(loaded.Artifacts[0].Resolved.CacheKey).To(HaveLen(64))
	})
})

type blockingRevisionLifecycle struct {
	mu      sync.Mutex
	calls   []string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingRevisionLifecycle() *blockingRevisionLifecycle {
	return &blockingRevisionLifecycle{entered: make(chan struct{}, 2), release: make(chan struct{})}
}

func (l *blockingRevisionLifecycle) ApplyConfigRevisions(_ context.Context, transitions []ModelRevisionTransition) (int, error) {
	l.mu.Lock()
	for _, transition := range transitions {
		l.calls = append(l.calls, transition.ConfigRevision)
	}
	l.mu.Unlock()
	l.entered <- struct{}{}
	l.once.Do(func() { <-l.release })
	return 0, nil
}

func (l *blockingRevisionLifecycle) revisions() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}
