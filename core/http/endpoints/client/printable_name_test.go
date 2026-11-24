package client

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("printableName", func() {
	It("keeps the printable characters of any script and drops control characters", func() {
		Expect(printableName("Küche 台所")).To(Equal("Küche 台所"))
		Expect(printableName("a\x00b\nc\x7f")).To(Equal("abc"))
		Expect(printableName("bad\xffbyte")).To(Equal("badbyte"))
	})

	It("cuts at 64 bytes on a character boundary", func() {
		got := printableName(strings.Repeat("ü", 40)) // 80 bytes
		Expect(len(got)).To(BeNumerically("<=", 64))
		Expect(got).To(Equal(strings.Repeat("ü", 32)))
	})
})
