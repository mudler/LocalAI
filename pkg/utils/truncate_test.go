package utils_test

import (
	"strings"
	"unicode/utf8"

	. "github.com/mudler/LocalAI/pkg/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TruncateUTF8", func() {
	It("keeps a short string and cuts a long one at a character boundary", func() {
		Expect(TruncateUTF8("abc", 5)).To(Equal("abc"))
		Expect(TruncateUTF8("abc", 3)).To(Equal("abc"))
		Expect(TruncateUTF8("abcd", 3)).To(Equal("abc"))
		Expect(TruncateUTF8("abc", 0)).To(BeEmpty())
		// "é" takes two bytes: a cut between them drops the whole character.
		Expect(TruncateUTF8("aé", 2)).To(Equal("a"))
		Expect(TruncateUTF8("aé", 3)).To(Equal("aé"))
		long := strings.Repeat("é", 200)
		got := TruncateUTF8(long, 255)
		Expect(utf8.ValidString(got)).To(BeTrue())
		Expect(len(got)).To(Equal(254))
	})
})
