package config_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

const gib int64 = 1 << 30

var _ = Describe("ModelLoadTimeoutForSize", func() {
	// The remote LoadModel deadline used to be a fixed 5m. That is a model-size
	// cliff: a 70 GB video checkpoint on a Jetson Thor worker failed
	// reproducibly with DeadlineExceeded because its weight load and pipeline
	// init alone exceed 5 minutes. Raising the constant only moves the cliff, so
	// the budget is derived from the bytes the worker has to read.

	It("keeps a small checkpoint close to the historical 5m default", func() {
		// A wedged 2 GB model must still fail fast: inflating every load's
		// budget is a real regression in failure latency.
		Expect(config.ModelLoadTimeoutForSize(2 * gib)).To(BeNumerically("<", 10*time.Minute))
	})

	It("gives a 70 GB checkpoint materially more budget than a 2 GB one", func() {
		small := config.ModelLoadTimeoutForSize(2 * gib)
		big := config.ModelLoadTimeoutForSize(70 * gib)
		Expect(big).To(BeNumerically(">", small*3))
		// The measured production failure had ~5m of load budget and needed
		// more; anything under 20m would still be a cliff for this exact model.
		Expect(big).To(BeNumerically(">=", 20*time.Minute))
	})

	It("scales monotonically with size", func() {
		Expect(config.ModelLoadTimeoutForSize(600 * gib)).
			To(BeNumerically(">", config.ModelLoadTimeoutForSize(70*gib)))
	})

	It("still gives a 600 GB checkpoint hours, not minutes", func() {
		Expect(config.ModelLoadTimeoutForSize(600 * gib)).To(BeNumerically(">=", 3*time.Hour))
	})

	It("falls back to the plain default when the size is unknown", func() {
		Expect(config.ModelLoadTimeoutForSize(0)).To(Equal(config.DefaultModelLoadTimeout))
		Expect(config.ModelLoadTimeoutForSize(-1)).To(Equal(config.DefaultModelLoadTimeout))
	})

	It("never exceeds the absolute maximum, however absurd the size", func() {
		Expect(config.ModelLoadTimeoutForSize(100_000 * gib)).To(Equal(config.MaxModelLoadTimeout))
	})
})
