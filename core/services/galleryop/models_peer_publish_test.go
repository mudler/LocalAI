package galleryop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// blockingMaterializer holds the preload of an already-installed model open
// until the spec releases it, so a spec can look at both replicas while the
// originator is still inside the preload.
type blockingMaterializer struct {
	entered chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
}

func (m *blockingMaterializer) Ensure(ctx context.Context, _ string, spec modelartifacts.Spec) (modelartifacts.Result, error) {
	m.once.Do(func() { close(m.entered) })
	select {
	case <-m.release:
	case <-ctx.Done():
		return modelartifacts.Result{}, ctx.Err()
	}
	if m.err != nil {
		return modelartifacts.Result{}, m.err
	}
	return modelartifacts.Result{Spec: spec}, nil
}

// writingInstallManager stands in for a gallery install: it writes one new
// config file into the shared models directory.
type writingInstallManager struct{ dir string }

func (m *writingInstallManager) InstallModel(context.Context, *ManagementOp[gallery.GalleryModel, gallery.ModelConfig], ProgressCallback) error {
	return os.WriteFile(filepath.Join(m.dir, "fresh.yaml"), []byte("name: fresh\nbackend: llama-cpp\n"), 0644)
}
func (m *writingInstallManager) DeleteModel(string) error { return nil }

// peerReloadingBus reloads a peer replica's loader from the shared directory
// whenever the models invalidation is published, the way a subscribed peer
// does in distributed mode.
type peerReloadingBus struct {
	mu        sync.Mutex
	published int
	onPublish func()
}

func (b *peerReloadingBus) Publish(subject string, _ any) error {
	if subject != messaging.SubjectCacheInvalidateModels {
		return nil
	}
	b.mu.Lock()
	b.published++
	b.mu.Unlock()
	if b.onPublish != nil {
		b.onPublish()
	}
	return nil
}
func (b *peerReloadingBus) count() int { b.mu.Lock(); defer b.mu.Unlock(); return b.published }
func (b *peerReloadingBus) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return nil, nil
}
func (b *peerReloadingBus) QueueSubscribe(string, string, func([]byte)) (messaging.Subscription, error) {
	return nil, nil
}
func (b *peerReloadingBus) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return nil, nil
}
func (b *peerReloadingBus) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return nil, nil
}
func (b *peerReloadingBus) Request(string, []byte, time.Duration) ([]byte, error) { return nil, nil }
func (b *peerReloadingBus) IsConnected() bool                                     { return true }
func (b *peerReloadingBus) Close()                                                {}

var _ = Describe("gallery install as seen by a peer replica", func() {
	var (
		dir          string
		appConfig    *config.ApplicationConfig
		materializer *blockingMaterializer
		originator   *config.ModelConfigLoader
		peer         *config.ModelConfigLoader
		bus          *peerReloadingBus
		service      *GalleryService
		op           *ManagementOp[gallery.GalleryModel, gallery.ModelConfig]
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		appConfig = &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}

		// An already-installed model whose preload is slow: it stands in for
		// the checksums and remote lookups the preload does for every model.
		Expect(os.WriteFile(filepath.Join(dir, "existing.yaml"), []byte(
			"name: existing\nbackend: llama-cpp\nartifacts:\n  - name: model\n    target: model\n    source: {type: huggingface, repo: owner/repo}\n"), 0644)).To(Succeed())

		materializer = &blockingMaterializer{entered: make(chan struct{}), release: make(chan struct{})}
		originator = config.NewModelConfigLoader(dir, config.WithArtifactMaterializer(materializer))
		Expect(originator.LoadModelConfigsFromPath(dir)).To(Succeed())

		// The peer learns about changes only through the broadcast.
		peer = config.NewModelConfigLoader(dir)
		Expect(peer.LoadModelConfigsFromPath(dir)).To(Succeed())
		bus = &peerReloadingBus{onPublish: func() {
			defer GinkgoRecover()
			Expect(peer.LoadModelConfigsFromPath(dir)).To(Succeed())
		}}

		service = NewGalleryService(appConfig, nil)
		service.SetNATSClient(bus)
		service.SetModelManager(&writingInstallManager{dir: dir})
		op = &ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID: "install-op", GalleryElementName: "localai@fresh", Context: context.Background(),
		}
	})

	It("tells peers about the new model before the slow preload finishes", func() {
		done := make(chan error, 1)
		go func() { done <- service.modelHandler(op, originator, appConfig.SystemState) }()
		Eventually(materializer.entered, 5*time.Second).Should(BeClosed())

		// The preload of an unrelated model is still in progress here.
		_, onOriginator := originator.GetModelConfig("fresh")
		_, onPeer := peer.GetModelConfig("fresh")
		published := bus.count()
		close(materializer.release)
		Expect(<-done).To(Succeed())

		Expect(onOriginator).To(BeTrue(), "precondition: the originator already lists the new model")
		Expect(published).To(Equal(1), "the originator listed the model before it published the invalidation")
		Expect(onPeer).To(BeTrue(), "the peer does not list a model the originator already lists")
	})

	It("still tells peers when the preload fails, and reports the failure", func() {
		materializer.err = errors.New("injected preload failure")
		close(materializer.release)

		err := service.modelHandler(op, originator, appConfig.SystemState)
		Expect(err).To(MatchError(ContainSubstring("injected preload failure")))
		Expect(bus.count()).To(Equal(1))
		_, onOriginator := originator.GetModelConfig("fresh")
		_, onPeer := peer.GetModelConfig("fresh")
		Expect(onOriginator).To(BeTrue())
		Expect(onPeer).To(BeTrue())
	})
})
