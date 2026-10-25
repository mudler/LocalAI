package application

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("applyFailoverWarmTargets", func() {
	It("returns promptly even when the preload call blocks", func() {
		// Guards against a regression to a synchronous preload loop: onWarm
		// runs on the failover manager's single scheduler goroutine, so a
		// blocking loader here must not block the caller.
		started := make(chan struct{})
		release := make(chan struct{})
		orig := preloadModelByName
		preloadModelByName = func(ctx context.Context, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, name string) ([]string, error) {
			close(started)
			<-release // never released within the test's timeout
			return nil, nil
		}
		DeferCleanup(func() { preloadModelByName = orig })
		DeferCleanup(func() { close(release) })

		app := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}

		callReturned := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			app.applyFailoverWarmTargets([]string{"warm-a"})
			close(callReturned)
		}()

		Eventually(callReturned, time.Second).Should(BeClosed(), "applyFailoverWarmTargets must not wait on the preload goroutine")
		Eventually(started, time.Second).Should(BeClosed(), "the preload goroutine should still run in the background")
	})
})

type healthyBackend struct{ grpc.Backend }

func (healthyBackend) HealthCheck(context.Context) (bool, error) { return true, nil }

var _ = Describe("failoverLoadedBackend", func() {
	It("returns the running backend and never loads a model that is not loaded", func() {
		ml := model.NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: GinkgoT().TempDir()}})
		store := model.NewInMemoryModelStore()
		ml.SetModelStore(store)
		loaded := failoverLoadedBackend(ml)

		Expect(loaded(config.ModelConfig{Name: "gemma", Backend: "llama-cpp"})).To(BeNil())
		Expect(ml.ListLoadedModels()).To(BeEmpty(), "the lookup must not start a load")

		client := healthyBackend{}
		store.Set("gemma", model.NewModelWithClient("gemma", "127.0.0.1:0", client))
		Expect(loaded(config.ModelConfig{Name: "gemma", Backend: "llama-cpp"})).To(Equal(client))
	})
})
