package slotcache_test

import (
	"strings"

	. "github.com/mudler/LocalAI/pkg/slotcache"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateID", func() {
	DescribeTable("accepts an ID the backend can issue",
		func(id string) { Expect(ValidateID(id)).To(Succeed()) },
		Entry("mixed digits and letters", "0123456789abcdef0123456789abcdef"),
		Entry("all digits", strings.Repeat("7", 32)),
		Entry("all letters", strings.Repeat("f", 32)),
	)

	DescribeTable("rejects anything else",
		func(id string) { Expect(ValidateID(id)).To(MatchError(ErrInvalidID)) },
		Entry("empty", ""),
		Entry("uppercase", "0123456789ABCDEF0123456789abcdef"),
		Entry("too short", "0123456789abcdef"),
		Entry("too long", "0123456789abcdef0123456789abcdef0"),
		Entry("non-hex letter", "0123456789abcdef0123456789abcdeg"),
		Entry("dot dot", strings.Repeat(".", 32)),
		Entry("parent traversal", "../../../../../../../../etc/pass"),
		Entry("slash", "0123456789abcdef/123456789abcdef"),
		Entry("backslash", `0123456789abcdef\123456789abcdef`),
		Entry("NUL byte", "0123456789abcdef0123456789abcd\x00f"),
		Entry("newline", "0123456789abcdef0123456789abcde\n"),
		Entry("space", "0123456789abcdef0123456789abcde "),
		Entry("non-ASCII digit", "0123456789abcdef0123456789abcd\u0663"),
		Entry("file name", "prompt.bin"),
	)
})

var _ = Describe("DirFromOptions", func() {
	It("returns empty when the option is absent", func() {
		Expect(DirFromOptions([]string{"cache_ram:1024", "use_jinja"})).To(BeEmpty())
		Expect(DirFromOptions(nil)).To(BeEmpty())
	})

	It("returns the value, keeping any colon in it", func() {
		Expect(DirFromOptions([]string{"slot_save_path:/var/cache/slots"})).To(Equal("/var/cache/slots"))
		Expect(DirFromOptions([]string{`slot_save_path:C:\slots`})).To(Equal(`C:\slots`))
	})

	It("lets a later option win, as the backend does", func() {
		Expect(DirFromOptions([]string{"slot_save_path:/a", "slot_save_path:/b"})).To(Equal("/b"))
	})

	It("treats the bare flag form as unset", func() {
		Expect(DirFromOptions([]string{"slot_save_path"})).To(BeEmpty())
	})
})
