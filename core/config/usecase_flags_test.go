package config

import (
	"math/bits"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Usecase flags", func() {
	It("gives every usecase flag its own bit", func() {
		seen := map[ModelConfigUsecase]string{}
		for name, flag := range GetAllModelConfigUsecases() {
			// FLAG_LLM is a composite of other flags by design.
			if name == "FLAG_LLM" {
				continue
			}
			Expect(bits.OnesCount64(uint64(flag))).To(Equal(1), "%s must be a single bit", name)
			other, dup := seen[flag]
			Expect(dup).To(BeFalse(), "%s and %s share a bit", name, other)
			seen[flag] = name
		}
	})
})
