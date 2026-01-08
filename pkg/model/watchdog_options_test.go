package model_test

import (
	"time"

	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WatchDogOptions", func() {
	Context("DefaultWatchDogOptions", func() {
		It("should return sensible defaults", func() {
			opts := model.DefaultWatchDogOptions()

			Expect(opts).ToNot(BeNil())
		})
	})

	Context("NewWatchDogOptions", func() {
		It("should apply options in order", func() {
			pm := newMockProcessManager()
			opts := model.NewWatchDogOptions(
				model.WithProcessManager(pm),
				model.WithBusyTimeout(10*time.Minute),
				model.WithIdleTimeout(20*time.Minute),
				model.WithBusyCheck(true),
				model.WithIdleCheck(true),
				model.WithLRULimit(5),
				model.WithMemoryReclaimer(true, 0.85),
			)

			Expect(opts).ToNot(BeNil())
		})

		It("should allow overriding options", func() {
			opts := model.NewWatchDogOptions(
				model.WithLRULimit(3),
				model.WithLRULimit(7), // override
			)

			// Create watchdog to verify
			wd := model.NewWatchDog(
				model.WithProcessManager(newMockProcessManager()),
				model.WithLRULimit(3),
				model.WithLRULimit(7), // override
			)
			Expect(wd.GetLRULimit()).To(Equal(7))

			Expect(opts).ToNot(BeNil())
		})
	})

	Context("Individual Options", func() {
		var pm *mockProcessManager

		BeforeEach(func() {
			pm = newMockProcessManager()
		})

		It("WithProcessManager should set process manager", func() {
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
			)
			Expect(wd).ToNot(BeNil())
		})

		It("WithBusyTimeout should set busy timeout", func() {
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithBusyTimeout(7*time.Minute),
			)
			Expect(wd).ToNot(BeNil())
		})

		It("WithIdleTimeout should set idle timeout", func() {
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithIdleTimeout(25*time.Minute),
			)
			Expect(wd).ToNot(BeNil())
		})

		It("WithBusyCheck should enable busy checking", func() {
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithBusyCheck(true),
			)
			Expect(wd).ToNot(BeNil())
		})

		It("WithIdleCheck should enable idle checking", func() {
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithIdleCheck(true),
			)
			Expect(wd).ToNot(BeNil())
		})

		It("WithLRULimit should set LRU limit", func() {
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithLRULimit(10),
			)
			Expect(wd.GetLRULimit()).To(Equal(10))
		})

		It("WithMemoryReclaimer should set both enabled and threshold", func() {
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithMemoryReclaimer(true, 0.88),
			)
			enabled, threshold := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeTrue())
			Expect(threshold).To(Equal(0.88))
		})

		It("WithMemoryReclaimerEnabled should set enabled flag only", func() {
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithMemoryReclaimerEnabled(true),
			)
			enabled, _ := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeTrue())
		})

		It("WithMemoryReclaimerThreshold should set threshold only", func() {
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithMemoryReclaimerThreshold(0.75),
			)
			_, threshold := wd.GetMemoryReclaimerSettings()
			Expect(threshold).To(Equal(0.75))
		})
	})

	Context("Option Combinations", func() {
		It("should work with all options combined", func() {
			pm := newMockProcessManager()
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithBusyTimeout(3*time.Minute),
				model.WithIdleTimeout(10*time.Minute),
				model.WithBusyCheck(true),
				model.WithIdleCheck(true),
				model.WithLRULimit(2),
				model.WithMemoryReclaimerEnabled(true),
				model.WithMemoryReclaimerThreshold(0.92),
			)

			Expect(wd).ToNot(BeNil())
			Expect(wd.GetLRULimit()).To(Equal(2))

			enabled, threshold := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeTrue())
			Expect(threshold).To(Equal(0.92))
		})

		It("should work with no options (all defaults)", func() {
			wd := model.NewWatchDog()

			Expect(wd).ToNot(BeNil())
			Expect(wd.GetLRULimit()).To(Equal(0))

			enabled, threshold := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeFalse())
			Expect(threshold).To(Equal(model.DefaultMemoryReclaimerThreshold)) // default
		})

		It("should allow partial configuration", func() {
			pm := newMockProcessManager()
			wd := model.NewWatchDog(
				model.WithProcessManager(pm),
				model.WithLRULimit(3),
			)

			Expect(wd).ToNot(BeNil())
			Expect(wd.GetLRULimit()).To(Equal(3))

			// Memory reclaimer should use defaults
			enabled, threshold := wd.GetMemoryReclaimerSettings()
			Expect(enabled).To(BeFalse())
			Expect(threshold).To(Equal(model.DefaultMemoryReclaimerThreshold))
		})
	})
})
