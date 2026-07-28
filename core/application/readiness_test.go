package application

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
)

// blockingMaterializer parks inside Ensure until release is closed, so a spec
// can hold the startup preload open and observe readiness while it is still
// running. This mirrors how a real managed-artifact preload behaves: since
// #10949 it downloads a HuggingFace snapshot, which for a large model is tens
// of GB and can take a long time.
type blockingMaterializer struct {
	seen    chan struct{}
	release chan struct{}
}

func (b *blockingMaterializer) Ensure(ctx context.Context, _ string, spec modelartifacts.Spec) (modelartifacts.Result, error) {
	select {
	case b.seen <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return modelartifacts.Result{}, ctx.Err()
	}
	spec.Resolved = &modelartifacts.Resolved{
		Endpoint: "https://huggingface.co",
		Revision: "0123456789abcdef0123456789abcdef01234567",
		CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	return modelartifacts.Result{Spec: spec}, nil
}

var _ = Describe("application readiness", func() {
	var modelsPath string
	var state *system.SystemState

	BeforeEach(func() {
		modelsPath = GinkgoT().TempDir()
		var err error
		state, err = system.GetSystemState(
			system.WithModelPath(modelsPath),
			system.WithBackendPath(GinkgoT().TempDir()),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	It("is not ready before the startup sequence has run", func() {
		app := newApplication(&config.ApplicationConfig{
			Context:     context.Background(),
			SystemState: state,
		})

		// A constructed-but-unstarted application must never advertise itself
		// as able to serve traffic: /readyz reads this, and a load balancer
		// that believes it will route requests to a process that cannot answer.
		Expect(app.Ready()).To(BeFalse())
	})

	It("stays not-ready while the startup model preload is still running", func() {
		blocker := &blockingMaterializer{
			seen:    make(chan struct{}, 1),
			release: make(chan struct{}),
		}
		app := newApplication(&config.ApplicationConfig{
			Context:                   context.Background(),
			SystemState:               state,
			ModelArtifactMaterializer: blocker,
		})

		Expect(os.WriteFile(filepath.Join(modelsPath, "managed.yaml"), []byte(`
name: managed
backend: transformers
artifacts:
  - source: {type: huggingface, repo: owner/repo}
parameters: {model: owner/repo}
`), 0644)).To(Succeed())
		Expect(app.ModelConfigLoader().LoadModelConfigsFromPath(modelsPath)).To(Succeed())

		preloadDone := make(chan error, 1)
		go func() {
			preloadDone <- app.ModelConfigLoader().PreloadWithContext(context.Background(), modelsPath)
		}()

		// Preload is now parked inside artifact materialization — exactly the
		// window in which a Kubernetes Service would otherwise send this
		// replica half the cluster's traffic.
		Eventually(blocker.seen).Should(Receive())
		Consistently(app.Ready).Should(BeFalse())

		close(blocker.release)
		Expect(<-preloadDone).NotTo(HaveOccurred())

		// Preload finished, but the rest of the startup sequence has not, so
		// the application is still not ready.
		Expect(app.Ready()).To(BeFalse())
	})

	It("becomes ready once the startup sequence completes", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		app, err := New(
			config.WithContext(ctx),
			config.WithSystemState(state),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Shutdown() })

		Expect(app.Ready()).To(BeTrue())
	})
})
