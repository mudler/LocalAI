// SPDX-License-Identifier: MIT
package connector_test

import (
	"strings"

	"github.com/mudler/LocalAI/addons/internal/connector"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Split", func() {
	It("keeps parts under the limit and whole words", func() {
		Expect(connector.Split("  ", 10)).To(BeNil())
		Expect(connector.Split("short", 10)).To(Equal([]string{"short"}))
		for _, p := range connector.Split(strings.Repeat("alpha beta\n", 50), 40) {
			Expect(len([]rune(p))).To(BeNumerically("<=", 40))
			Expect(p).NotTo(HavePrefix("lpha"))
			Expect(p).NotTo(HaveSuffix("alph"))
		}
	})
	It("cuts on a character boundary when there is no break", func() {
		got := connector.Split(strings.Repeat("é", 25), 10)
		Expect(got).To(HaveLen(3))
		Expect([]int{len([]rune(got[0])), len([]rune(got[2]))}).To(Equal([]int{10, 5}))
	})
})

var _ = Describe("SplitFenced", func() {
	It("closes a code block at the end of a part and opens it again in the next", func() {
		code := "```go\n" + strings.Repeat("x := 1\n", 40) + "```\nafter"
		parts := connector.SplitFenced(code, 100)
		Expect(len(parts)).To(BeNumerically(">", 2))
		for i, p := range parts {
			Expect(len([]rune(p))).To(BeNumerically("<=", 100))
			Expect(strings.Count(p, "```")%2).To(Equal(0), "part %d has an open block: %q", i, p)
		}
		Expect(parts[len(parts)-1]).To(HaveSuffix("after"))
	})
	It("leaves text without code as Split does, and a tiny limit to Split", func() {
		text := strings.Repeat("word ", 60)
		Expect(connector.SplitFenced(text, 100)).To(Equal(connector.Split(text, 100)))
		Expect(connector.SplitFenced("```\nabc\n```", 10)).To(Equal(connector.Split("```\nabc\n```", 10)))
	})
})
