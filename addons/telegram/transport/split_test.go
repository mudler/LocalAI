// SPDX-License-Identifier: MIT
package transport_test

import (
	"strings"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Split", func() {
	It("keeps parts under the limit and whole words", func() {
		Expect(transport.Split("  ", 10)).To(BeNil())
		Expect(transport.Split("short", 10)).To(Equal([]string{"short"}))
		for _, p := range transport.Split(strings.Repeat("alpha beta\n", 50), 40) {
			Expect(len([]rune(p))).To(BeNumerically("<=", 40))
			Expect(p).NotTo(HavePrefix("lpha"))
			Expect(p).NotTo(HaveSuffix("alph"))
		}
	})
	It("cuts on a character boundary when there is no break", func() {
		got := transport.Split(strings.Repeat("é", 25), 10)
		Expect(got).To(HaveLen(3))
		Expect([]int{len([]rune(got[0])), len([]rune(got[2]))}).To(Equal([]int{10, 5}))
	})
})
