// SPDX-License-Identifier: MIT
package artifacts

import (
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
