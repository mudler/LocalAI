// SPDX-License-Identifier: MIT
package config

import (
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"slices"
)

var _ = Describe("Addon model capability vocabulary", func() {
	It("matches every concrete non-native host model flag", func() {
		var expected []string
		for name := range GetAllModelConfigUsecases() {
			if name != "FLAG_CHAT" && name != "FLAG_COMPLETION" && name != "FLAG_LLM" {
				expected = append(expected, name)
			}
		}
		Expect(contract.ModelActionCapabilities()).To(ConsistOf(expected))
		Expect(contract.MaxModelActions).To(Equal(len(expected)))
		flags := contract.ModelActionCapabilities()
		slices.Sort(flags)
		Expect(slices.Compact(flags)).To(HaveLen(len(expected)))
	})
})
