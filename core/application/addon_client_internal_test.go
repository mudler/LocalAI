package application

import (
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the view of a client job", func() {
	It("cuts a long result on a character boundary", func() {
		// Three-byte characters, so the byte limit falls inside one.
		long := strings.Repeat("€", hostops.MaxClientJobResult)
		j := studio.JobInfo{Result: long}
		j.ID = "j1"
		out := clientJobView(j)
		Expect(out.Truncated).To(BeTrue())
		Expect(utf8.ValidString(out.Result)).To(BeTrue())
		Expect(len(out.Result)).To(BeNumerically("<=", hostops.MaxClientJobResult))
		Expect(len(out.Result)).To(BeNumerically(">", hostops.MaxClientJobResult-4))
	})

	It("keeps a short result as it is", func() {
		out := clientJobView(studio.JobInfo{Result: "ok"})
		Expect(out.Result).To(Equal("ok"))
		Expect(out.Truncated).To(BeFalse())
	})
})
