// SPDX-License-Identifier: MIT
package artifacts

import (
	"time"

	"fmt"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("an output the add-on refuses to open", func() {
	It("fails the artifact with a fixed reason that says it was refused, not that the add-on is down", func() {
		state, reason := failure(fmt.Errorf("open output: %w", &addonruntime.Refusal{Status: 400, Reason: "invalid request"}), false, false)
		Expect(state).To(Equal(gateway.ArtifactFailed))
		Expect(reason).To(Equal("output refused as invalid"))
		state, reason = failure(fmt.Errorf("open output: %w", addonruntime.ErrRuntime), false, false)
		Expect(state).To(Equal(gateway.ArtifactFailed))
		Expect(reason).To(Equal("output unavailable"))
	})
})

var _ = Describe("a configured artifact lifetime", func() {
	It("shortens the lifetime and keeps the bounds consistent, and zero keeps the defaults", func() {
		base := DefaultLimits()
		Expect(base.WithExpiry(0)).To(Equal(base))
		short := base.WithExpiry(4 * time.Second)
		Expect(short.Expiry).To(Equal(4 * time.Second))
		Expect(short.TransferTimeout).To(BeNumerically("<", short.Expiry))
		Expect(short.SweepInterval).To(BeNumerically("<=", short.Expiry/2))
		Expect(short.Validate()).To(Succeed())
		// A lifetime the store would refuse is not silently accepted.
		Expect(base.WithExpiry(time.Millisecond).Validate()).NotTo(Succeed())
	})
})
