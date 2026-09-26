package application

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/config"
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
	It("lets only one frontend lead at a time on the same database", func() {
		// Not PostgreSQL, so advisorylock falls back to its in-process lock,
		// which has the same try-lock semantics as pg_try_advisory_lock.
		db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		first, second := failoverLeaderGate(db), failoverLeaderGate(db)
		ctx := context.Background()

		var secondInside, secondRan bool
		Expect(first(ctx, func() {
			secondInside = second(ctx, func() { secondRan = true })
		})).To(BeTrue())
		Expect(secondInside).To(BeFalse(), "the second gate must not lead while the first holds the lock")
		Expect(secondRan).To(BeFalse())

		Expect(second(ctx, func() { secondRan = true })).To(BeTrue())
		Expect(secondRan).To(BeTrue())
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
})
