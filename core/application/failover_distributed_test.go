package application

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// staticPins is a nodes.PinnedModelResolver with a fixed list.
type staticPins []string

func (s staticPins) GetPinnedModelNames() []string { return s }

// failoverSource is a failover.ConfigSource over a fixed set of configs.
type failoverSource map[string]config.ModelConfig

func (s failoverSource) GetModelConfig(name string) (config.ModelConfig, bool) {
	c, ok := s[name]
	return c, ok
}

func (s failoverSource) GetAllModelsConfigs() []config.ModelConfig {
	out := make([]config.ModelConfig, 0, len(s))
	for _, c := range s {
		out = append(out, c)
	}
	return out
}

// warmChainSource has one chain whose two local targets are warm.
func warmChainSource() failoverSource {
	return failoverSource{
		"b": {Name: "b", Backend: "llama-cpp"},
		"c": {Name: "c", Backend: "llama-cpp"},
		"chain": {Name: "chain", Failover: &config.FailoverConfig{Targets: []config.FailoverTarget{
			{Model: "b", Warm: true}, {Model: "c", Warm: true},
		}}},
	}
}

var _ = Describe("failoverPinnedResolver", func() {
	It("merges config pins and warm failover targets without duplicates", func() {
		fm := failover.New(warmChainSource())
		fm.Sync()
		Expect(fm.WarmTargets()).To(Equal([]string{"b", "c"}))

		r := &failoverPinnedResolver{base: staticPins{"pinned-a", "b"}, fm: fm}
		Expect(r.GetPinnedModelNames()).To(Equal([]string{"pinned-a", "b", "c"}))
	})
})

var _ = Describe("failoverLeaderGate", func() {
	It("keeps leadership with the first frontend until it releases", func() {
		// Not PostgreSQL, so advisorylock falls back to its in-process lock,
		// which has the same try-lock semantics as pg_try_advisory_lock.
		db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		firstLock := advisorylock.NewHeldLock(db, advisorylock.KeyFailoverProber)
		secondLock := advisorylock.NewHeldLock(db, advisorylock.KeyFailoverProber)
		DeferCleanup(firstLock.Release)
		DeferCleanup(secondLock.Release)
		first, second := failoverLeaderGate(firstLock), failoverLeaderGate(secondLock)
		ctx := context.Background()

		var firstRuns, secondRuns int
		for range 5 {
			Expect(first(ctx, func() { firstRuns++ })).To(BeTrue())
			Expect(second(ctx, func() { secondRuns++ })).To(BeFalse(), "leadership must not flip between ticks")
		}
		Expect(firstRuns).To(Equal(5))
		Expect(secondRuns).To(BeZero())

		firstLock.Release()
		Expect(second(ctx, func() { secondRuns++ })).To(BeTrue(), "a released leadership passes to the next frontend")
		Expect(secondRuns).To(Equal(1))
		Expect(first(ctx, func() { firstRuns++ })).To(BeFalse())
	})
})

var _ = Describe("applyFailoverWarmTargets in distributed mode", func() {
	It("pins but does not preload on a frontend that is not the probe leader", func() {
		preloaded := make(chan string, 1)
		orig := preloadModelByName
		preloadModelByName = func(_ context.Context, _ *config.ModelConfigLoader, _ *model.ModelLoader, _ *config.ApplicationConfig, name string) ([]string, error) {
			preloaded <- name
			return nil, nil
		}
		DeferCleanup(func() { preloadModelByName = orig })

		// A gate that never grants leadership: this frontend is a follower.
		fm := failover.New(warmChainSource(),
			failover.WithLeaderGate(func(context.Context, func()) bool { return false }))
		app := &Application{
			applicationConfig: &config.ApplicationConfig{Context: context.Background()},
			distributed:       &DistributedServices{},
			failoverManager:   fm,
		}

		app.applyFailoverWarmTargets([]string{"b"})
		Consistently(preloaded, 200*time.Millisecond).ShouldNot(Receive(), "only the probe leader preloads warm targets")
	})

	It("preloads warm targets on the probe leader", func() {
		preloaded := make(chan string, 4)
		orig := preloadModelByName
		preloadModelByName = func(_ context.Context, _ *config.ModelConfigLoader, _ *model.ModelLoader, _ *config.ApplicationConfig, name string) ([]string, error) {
			preloaded <- name
			return nil, nil
		}
		DeferCleanup(func() { preloadModelByName = orig })

		app := &Application{
			applicationConfig: &config.ApplicationConfig{Context: context.Background()},
			distributed:       &DistributedServices{},
		}
		// A gate that always grants: this frontend is the leader. The warm
		// set is delivered on the first tick that wins it.
		app.failoverManager = failover.New(warmChainSource(),
			failover.WithLeaderGate(func(_ context.Context, fn func()) bool { fn(); return true }),
			failover.WithOnWarmChanged(app.applyFailoverWarmTargets))

		app.failoverManager.Tick(context.Background())
		Eventually(preloaded, time.Second).Should(Receive(Equal("b")))
		Eventually(preloaded, time.Second).Should(Receive(Equal("c")))
	})
})
