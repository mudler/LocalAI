package projects

import (
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the placeholder slug of a project whose folder is gone", func() {
	It("stays inside the slug column for a 250-byte name", func() {
		r := Project{ID: "0123456789abcdef0123456789abcdef", Slug: strings.Repeat("a", 250), Status: StatusBroken}
		got := placeholderSlug(r)
		Expect(len(got)).To(BeNumerically("<=", 200))
		Expect(got).To(HaveSuffix(goneMark + r.ID))
		Expect(placeholderSlug(Project{ID: r.ID, Slug: got})).To(Equal(got), "a second pass does not change it")
	})

	It("never cuts inside a multi-byte character", func() {
		// 3-byte runes: every cut position inside one must back off.
		for pad := 0; pad < 3; pad++ {
			r := Project{ID: "0123456789abcdef0123456789abcdef", Slug: strings.Repeat("a", pad) + strings.Repeat("€", 100)}
			got := placeholderSlug(r)
			Expect(utf8.ValidString(got)).To(BeTrue(), "pad %d", pad)
			Expect(len(got)).To(BeNumerically("<=", 200))
		}
	})

	It("keeps a short slug whole", func() {
		Expect(placeholderSlug(Project{ID: "id1", Slug: "acme"})).To(Equal("acme~gone-id1"))
	})
})
