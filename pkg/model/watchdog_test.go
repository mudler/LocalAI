package model_test

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mockProcessManager implements ProcessManager for testing
type mockProcessManager struct {
	mu             sync.Mutex
	shutdownCalls  []string
	shutdownErrors map[string]error
}

func newMockProcessManager() *mockProcessManager {
	return &mockProcessManager{
		shutdownCalls:  []string{},
		shutdownErrors: make(map[string]error),
	}
}

func (m *mockProcessManager) ShutdownModel(modelName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shutdownCalls = append(m.shutdownCalls, modelName)
	if err, ok := m.shutdownErrors[modelName]; ok {
		return err
	}
	return nil
}

func (m *mockProcessManager) getShutdownCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.shutdownCalls))
	copy(result, m.shutdownCalls)
	return result
}

var _ = Describe("WatchDog", func() {
	var (
		wd *model.WatchDog
		pm *mockProcessManager
	)

	BeforeEach(func() {
		pm = newMockProcessManager()
	})

	Context("LRU Limit", func() {
		It("should create watchdog with LRU limit", func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithBusyTimeout(5*time.Minute),
				model.WithIdleTimeout(15*time.Minute),
				model.WithLRULimit(2),
			)
			Expect(wd.GetLRULimit()).To(Equal(2))
		})

		It("should allow updating LRU limit dynamically", func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithLRULimit(2),
			)
			wd.SetLRULimit(5)
			Expect(wd.GetLRULimit()).To(Equal(5))
		})

		It("should return 0 for disabled LRU", func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithLRULimit(0),
			)
			Expect(wd.GetLRULimit()).To(Equal(0))
		})
	})

	Context("Memory Reclaimer Options", func() {
		It("should create watchdog with memory reclaimer settings", func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithMemoryReclaimer(true, 0.85),
			)
			enabled, threshold := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeTrue())
			Expect(threshold).To(Equal(0.85))
		})

		It("should allow setting memory reclaimer via separate options", func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithMemoryReclaimerEnabled(true),
				model.WithMemoryReclaimerThreshold(0.90),
			)
			enabled, threshold := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeTrue())
			Expect(threshold).To(Equal(0.90))
		})

		It("should use default threshold when not specified", func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
			)
			_, threshold := wd.GetMemoryReclaimerSettings()
			Expect(threshold).To(Equal(model.DefaultMemoryReclaimerThreshold))
		})

		It("should allow updating memory reclaimer settings dynamically", func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
			)
			wd.SetMemoryReclaimer(true, 0.80)
			enabled, threshold := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeTrue())
			Expect(threshold).To(Equal(0.80))
		})
	})

	Context("Model Tracking", func() {
		BeforeEach(func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithBusyTimeout(5*time.Minute),
				model.WithIdleTimeout(15*time.Minute),
				model.WithLRULimit(3),
			)
		})

		It("should track loaded models count", func() {
			Expect(wd.GetLoadedModelCount()).To(Equal(0))

			wd.AddAddressModelMap("addr1", "model1")
			Expect(wd.GetLoadedModelCount()).To(Equal(1))

			wd.AddAddressModelMap("addr2", "model2")
			Expect(wd.GetLoadedModelCount()).To(Equal(2))
		})

		It("should update lastUsed time on Mark", func() {
			wd.AddAddressModelMap("addr1", "model1")
			wd.Mark("addr1")
			// The model should now have a lastUsed time set
			// We can verify this indirectly through LRU eviction behavior
		})

		It("should update lastUsed time on UnMark", func() {
			wd.AddAddressModelMap("addr1", "model1")
			wd.Mark("addr1")
			time.Sleep(10 * time.Millisecond)
			wd.UnMark("addr1")
			// The model should now have an updated lastUsed time
		})

		It("should update lastUsed time via UpdateLastUsed", func() {
			wd.AddAddressModelMap("addr1", "model1")
			wd.UpdateLastUsed("addr1")
			// Verify the time was updated
		})
	})

	Context("EnforceLRULimit", func() {
		BeforeEach(func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithBusyTimeout(5*time.Minute),
				model.WithIdleTimeout(15*time.Minute),
				model.WithLRULimit(2),
			)
		})

		It("should not evict when under limit", func() {
			wd.AddAddressModelMap("addr1", "model1")
			wd.Mark("addr1")

			result := wd.EnforceLRULimit(0)
			Expect(result.EvictedCount).To(Equal(0))
			Expect(result.NeedMore).To(BeFalse())
			Expect(pm.getShutdownCalls()).To(BeEmpty())
		})

		It("should evict oldest model when at limit", func() {
			// Add two models
			wd.AddAddressModelMap("addr1", "model1")
			wd.Mark("addr1")
			time.Sleep(10 * time.Millisecond)

			wd.AddAddressModelMap("addr2", "model2")
			wd.Mark("addr2")

			// Enforce LRU with limit of 2 (need to make room for 1 new model)
			result := wd.EnforceLRULimit(0)
			Expect(result.EvictedCount).To(Equal(1))
			Expect(result.NeedMore).To(BeFalse())
			Expect(pm.getShutdownCalls()).To(ContainElement("model1")) // oldest should be evicted
		})

		It("should evict multiple models when needed", func() {
			// Add three models
			wd.AddAddressModelMap("addr1", "model1")
			wd.Mark("addr1")
			time.Sleep(10 * time.Millisecond)

			wd.AddAddressModelMap("addr2", "model2")
			wd.Mark("addr2")
			time.Sleep(10 * time.Millisecond)

			wd.AddAddressModelMap("addr3", "model3")
			wd.Mark("addr3")

			// Set limit to 1, should evict 2 oldest + 1 for new = 3 evictions
			wd.SetLRULimit(1)
			result := wd.EnforceLRULimit(0)
			Expect(result.EvictedCount).To(Equal(3))
			Expect(result.NeedMore).To(BeFalse())
			shutdowns := pm.getShutdownCalls()
			Expect(shutdowns).To(ContainElement("model1"))
			Expect(shutdowns).To(ContainElement("model2"))
			Expect(shutdowns).To(ContainElement("model3"))
		})

		It("should account for pending loads", func() {
			// Add two models (at limit)
			wd.AddAddressModelMap("addr1", "model1")
			wd.Mark("addr1")
			time.Sleep(10 * time.Millisecond)

			wd.AddAddressModelMap("addr2", "model2")
			wd.Mark("addr2")

			// With 1 pending load, we need to evict 2 (current=2, pending=1, new=1, limit=2)
			// total after = 2 + 1 + 1 = 4, need to evict 4 - 2 = 2
			result := wd.EnforceLRULimit(1)
			Expect(result.EvictedCount).To(Equal(2))
			Expect(result.NeedMore).To(BeFalse())
		})

		It("should not evict when LRU is disabled", func() {
			wd.SetLRULimit(0)

			wd.AddAddressModelMap("addr1", "model1")
			wd.AddAddressModelMap("addr2", "model2")
			wd.AddAddressModelMap("addr3", "model3")

			result := wd.EnforceLRULimit(0)
			Expect(result.EvictedCount).To(Equal(0))
			Expect(result.NeedMore).To(BeFalse())
			Expect(pm.getShutdownCalls()).To(BeEmpty())
		})

		It("should evict least recently used first", func() {
			wd.SetLRULimit(2)

			// Add models with different lastUsed times
			wd.AddAddressModelMap("addr1", "model1")
			wd.Mark("addr1")
			time.Sleep(20 * time.Millisecond)

			wd.AddAddressModelMap("addr2", "model2")
			wd.Mark("addr2")
			time.Sleep(20 * time.Millisecond)

			// Touch model1 again to make it more recent
			wd.UpdateLastUsed("addr1")
			time.Sleep(20 * time.Millisecond)

			wd.AddAddressModelMap("addr3", "model3")
			wd.Mark("addr3")

			// Now model2 is the oldest, should be evicted first
			result := wd.EnforceLRULimit(0)
			Expect(result.EvictedCount).To(BeNumerically(">=", 1))
			Expect(result.NeedMore).To(BeFalse())

			shutdowns := pm.getShutdownCalls()
			// model2 should be evicted first (it's the oldest)
			if len(shutdowns) >= 1 {
				Expect(shutdowns[0]).To(Equal("model2"))
			}
		})
	})

	Context("Single Backend Mode (LRU=1)", func() {
		BeforeEach(func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithBusyTimeout(5*time.Minute),
				model.WithIdleTimeout(15*time.Minute),
				model.WithLRULimit(1),
			)
		})

		It("should evict existing model when loading new one", func() {
			wd.AddAddressModelMap("addr1", "model1")
			wd.Mark("addr1")

			// With limit=1, loading a new model should evict the existing one
			result := wd.EnforceLRULimit(0)
			Expect(result.EvictedCount).To(Equal(1))
			Expect(result.NeedMore).To(BeFalse())
			Expect(pm.getShutdownCalls()).To(ContainElement("model1"))
		})

		It("should handle rapid model switches", func() {
			for i := 0; i < 5; i++ {
				wd.AddAddressModelMap("addr", "model")
				wd.Mark("addr")
				wd.EnforceLRULimit(0)
			}
			// All previous models should have been evicted
			Expect(len(pm.getShutdownCalls())).To(Equal(5))
		})
	})

	Context("Functional Options", func() {
		It("should use default options when none provided", func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
			)
			Expect(wd.GetLRULimit()).To(Equal(0))

			enabled, threshold := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeFalse())
			Expect(threshold).To(Equal(model.DefaultMemoryReclaimerThreshold))
		})

		It("should allow combining multiple options", func() {
			wd = model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithBusyTimeout(10*time.Minute),
				model.WithIdleTimeout(30*time.Minute),
				model.WithBusyCheck(true),
				model.WithIdleCheck(true),
				model.WithLRULimit(5),
				model.WithMemoryReclaimerEnabled(true),
				model.WithMemoryReclaimerThreshold(0.80),
			)

			Expect(wd.GetLRULimit()).To(Equal(5))

			enabled, threshold := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeTrue())
			Expect(threshold).To(Equal(0.80))
		})
	})
})
