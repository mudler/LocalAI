package modeladmin

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// lockedRevisionLifecycle records transitions and is safe to read while a
// reconnect pass runs on another goroutine.
type lockedRevisionLifecycle struct {
	mu          sync.Mutex
	transitions []ModelRevisionTransition
}

func (l *lockedRevisionLifecycle) ApplyConfigRevisions(_ context.Context, transitions []ModelRevisionTransition) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.transitions = append(l.transitions, transitions...)
	return 0, nil
}

func (l *lockedRevisionLifecycle) names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	names := make([]string, 0, len(l.transitions))
	for _, t := range l.transitions {
		names = append(names, t.ModelName)
	}
	return names
}

func writeModelFile(dir, name, body string) {
	Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0644)).To(Succeed())
}

func aliasTarget(loader *config.ModelConfigLoader, name string) string {
	cfg, ok := loader.GetModelConfig(name)
	if !ok {
		return ""
	}
	return cfg.Alias
}

var _ = Describe("ApplyRemoteChange with models defined outside the models directory", func() {
	It("keeps a --config-file model and publishes no deletion for it", func() {
		dir := GinkgoT().TempDir()
		cfgFile := filepath.Join(GinkgoT().TempDir(), "models.yaml")
		Expect(os.WriteFile(cfgFile, []byte("- name: from-config-file\n  backend: llama-cpp\n"), 0644)).To(Succeed())
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadMultipleModelConfigsSingleFile(cfgFile)).To(Succeed())

		writeModelFile(dir, "fresh", "name: fresh\nbackend: llama-cpp\n")
		lifecycle := &lockedRevisionLifecycle{}
		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{Element: "fresh", Op: "install"}, lifecycle)).To(Succeed())

		_, ok := loader.GetModelConfig("from-config-file")
		Expect(ok).To(BeTrue())
		_, ok = loader.GetModelConfig("fresh")
		Expect(ok).To(BeTrue())
		Expect(lifecycle.names()).To(ConsistOf("fresh"))
	})

	It("lets a --config-file model win over a models directory file of the same name", func() {
		dir := GinkgoT().TempDir()
		cfgFile := filepath.Join(GinkgoT().TempDir(), "models.yaml")
		Expect(os.WriteFile(cfgFile, []byte("- name: shared\n  backend: from-config-file\n"), 0644)).To(Succeed())
		writeModelFile(dir, "shared", "name: shared\nbackend: from-directory\n")
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		Expect(loader.LoadMultipleModelConfigsSingleFile(cfgFile)).To(Succeed())

		lifecycle := &lockedRevisionLifecycle{}
		Expect(ApplyRemoteChange(context.Background(), loader, dir, messaging.CacheInvalidateEvent{Element: "shared", Op: "install"}, lifecycle)).To(Succeed())

		cfg, ok := loader.GetModelConfig("shared")
		Expect(ok).To(BeTrue())
		Expect(cfg.Backend).To(Equal("from-config-file"))
		Expect(lifecycle.names()).To(BeEmpty())
	})
})

var _ = Describe("DirectoryResync", func() {
	var (
		dir        string
		originator *config.ModelConfigLoader
		peer       *config.ModelConfigLoader
		lifecycle  *lockedRevisionLifecycle
		resync     *DirectoryResync
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		writeModelFile(dir, "model-a", "name: model-a\nbackend: llama-cpp\n")
		writeModelFile(dir, "model-b", "name: model-b\nbackend: llama-cpp\n")
		writeModelFile(dir, "stable", "name: stable\nalias: model-a\n")

		originator = config.NewModelConfigLoader(dir)
		Expect(originator.LoadModelConfigsFromPath(dir)).To(Succeed())
		peer = config.NewModelConfigLoader(dir)
		Expect(peer.LoadModelConfigsFromPath(dir)).To(Succeed())

		lifecycle = &lockedRevisionLifecycle{}
		resync = NewDirectoryResync(peer, dir, lifecycle)
	})

	// repointAlias changes the alias on the originator and announces it on
	// bus. The peer only hears it if it is subscribed.
	repointAlias := func(bus *testutil.FakeBus) {
		writeModelFile(dir, "stable", "name: stable\nalias: model-b\n")
		Expect(originator.LoadModelConfigsFromPath(dir)).To(Succeed())
		Expect(bus.Publish(messaging.SubjectCacheInvalidateModels, messaging.CacheInvalidateEvent{Element: "stable", Op: "install"})).To(Succeed())
	}

	It("catches up on a change whose invalidation the peer missed", func() {
		bus := testutil.NewFakeBus()
		// The peer is not subscribed: the message is lost, as it is when the
		// peer is disconnected from NATS at publish time.
		repointAlias(bus)
		Expect(aliasTarget(originator, "stable")).To(Equal("model-b"))
		Expect(aliasTarget(peer, "stable")).To(Equal("model-a"), "precondition: the peer missed the change")

		Expect(resync.Resync(context.Background(), false)).To(Succeed())
		Expect(aliasTarget(peer, "stable")).To(Equal("model-b"))
		Expect(lifecycle.names()).To(ConsistOf("stable"), "only the changed model gets a revision transition")
	})

	It("changes nothing when the directory did not change", func() {
		Expect(resync.Resync(context.Background(), false)).To(Succeed())
		Expect(lifecycle.names()).To(BeEmpty())

		// Even a forced pass over an unchanged directory is a no-op.
		Expect(resync.Resync(context.Background(), true)).To(Succeed())
		Expect(lifecycle.names()).To(BeEmpty())
		_, ok := peer.GetModelConfig("model-a")
		Expect(ok).To(BeTrue())
	})

	It("resyncs after a NATS reconnect", func() {
		bus := testutil.NewFakeBus()
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		// A long interval, so only the reconnect can trigger the pass.
		resync.Start(ctx, time.Hour, bus)

		repointAlias(bus)
		Expect(aliasTarget(peer, "stable")).To(Equal("model-a"), "precondition: the peer missed the change")

		bus.TriggerReconnect()
		Eventually(func() string { return aliasTarget(peer, "stable") }, 5*time.Second, 10*time.Millisecond).Should(Equal("model-b"))
	})

	It("runs the pass periodically", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		resync.Start(ctx, 20*time.Millisecond, nil)

		repointAlias(testutil.NewFakeBus())
		Eventually(func() string { return aliasTarget(peer, "stable") }, 5*time.Second, 10*time.Millisecond).Should(Equal("model-b"))
	})

	It("reports a failing directory once, and retries until it is fixed", func() {
		writeModelFile(dir, "half-written", "name: [unterminated\n")
		Expect(resync.Resync(context.Background(), false)).NotTo(Succeed())
		Expect(resync.Resync(context.Background(), false)).To(Succeed(), "the same failure is not reported twice")

		writeModelFile(dir, "half-written", "name: half-written\nbackend: llama-cpp\n")
		Expect(resync.Resync(context.Background(), false)).To(Succeed())
		_, ok := peer.GetModelConfig("half-written")
		Expect(ok).To(BeTrue())
	})

	It("keeps --config-file models across passes", func() {
		cfgFile := filepath.Join(GinkgoT().TempDir(), "models.yaml")
		Expect(os.WriteFile(cfgFile, []byte("- name: from-config-file\n  backend: llama-cpp\n"), 0644)).To(Succeed())
		Expect(peer.LoadMultipleModelConfigsSingleFile(cfgFile)).To(Succeed())

		repointAlias(testutil.NewFakeBus())
		Expect(resync.Resync(context.Background(), true)).To(Succeed())
		_, ok := peer.GetModelConfig("from-config-file")
		Expect(ok).To(BeTrue())
		Expect(lifecycle.names()).NotTo(ContainElement("from-config-file"))
	})
})
