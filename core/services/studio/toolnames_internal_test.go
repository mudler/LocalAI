// SPDX-License-Identifier: MIT
package studio

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("add-on stand-in identities", func() {
	It("recognizes valid long stand-ins but rejects malformed identities", func() {
		for _, id := range []string{"runner", "r" + strings.Repeat("x", 63)} {
			Expect(standInRunner([]session.Tool{{Name: unavailableToolName(id), StandInFor: []string{"bash"}}})).To(Equal(id))
		}
		for _, name := range []string{"addon____unavailable", "addon__bad_id__unavailable", "addon__9runner__unavailable", "addon__runner__bash", "addon__runner__unavailable__extra", "runner__unavailable"} {
			Expect(standInRunner([]session.Tool{{Name: name, StandInFor: []string{"bash"}}})).To(BeEmpty(), name)
		}
		Expect(standInRunner([]session.Tool{{Name: unavailableToolName("runner")}})).To(BeEmpty())
	})
})
