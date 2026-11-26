// SPDX-License-Identifier: MIT
package kubeapi_test

import (
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the text cap", func() {
	It("cuts on a rune boundary and flags the cut", func() {
		s := strings.Repeat("é", 10) // 20 bytes
		out, cut := kubeapi.Cap(s, 7)
		Expect(cut).To(BeTrue())
		Expect(out).To(Equal("ééé"))
		out, cut = kubeapi.Cap("short", 7)
		Expect(cut).To(BeFalse())
		Expect(out).To(Equal("short"))
	})

	It("replaces invalid UTF-8", func() {
		out, cut := kubeapi.Cap("a\xffb", 100)
		Expect(cut).To(BeFalse())
		Expect(utf8.ValidString(out)).To(BeTrue())
		Expect(out).To(Equal("a�b"))
	})
})
