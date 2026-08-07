package backoff_test

import (
	"math"
	"time"

	"github.com/mudler/LocalAI/internal/backoff"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Exponential", func() {
	It("doubles the base delay up to the maximum", func() {
		base := 50 * time.Millisecond
		maximum := 500 * time.Millisecond

		Expect(backoff.Exponential(base, maximum, 0)).To(Equal(50 * time.Millisecond))
		Expect(backoff.Exponential(base, maximum, 1)).To(Equal(100 * time.Millisecond))
		Expect(backoff.Exponential(base, maximum, 2)).To(Equal(200 * time.Millisecond))
		Expect(backoff.Exponential(base, maximum, 3)).To(Equal(400 * time.Millisecond))
		Expect(backoff.Exponential(base, maximum, 4)).To(Equal(maximum))
		Expect(backoff.Exponential(base, maximum, math.MaxUint)).To(Equal(maximum))
	})

	It("saturates without overflowing a duration", func() {
		maximum := time.Duration(math.MaxInt64)
		Expect(backoff.Exponential(maximum/2+1, maximum, 1)).To(Equal(maximum))
		Expect(backoff.Exponential(2, 5, 1)).To(Equal(time.Duration(4)))
	})

	It("returns zero when backoff is disabled", func() {
		Expect(backoff.Exponential(0, time.Second, 1)).To(BeZero())
		Expect(backoff.Exponential(time.Second, 0, 1)).To(BeZero())
	})
})
