package textindex

import (
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/extract"
)

var _ = ginkgo.Describe("capText", func() {
	ginkgo.It("cuts at a line within the cap and keeps the sections that start before it", func() {
		text := "## Page 1\nfirst\n\n## Page 2\nsecond\n\n## Page 3\nthird é"
		secs := []extract.Section{{Kind: "page", Name: "1", Line: 1}, {Kind: "page", Name: "2", Line: 4}, {Kind: "page", Name: "3", Line: 7}}
		got, kept, cut := capText(text, secs, 30)
		Expect(cut).To(BeTrue())
		Expect(got).To(Equal("## Page 1\nfirst\n\n## Page 2\n[The index keeps the first 30 bytes of the text.]"))
		Expect(kept).To(HaveLen(2))
		same, all, cut := capText(text, secs, 1000)
		Expect(cut).To(BeFalse())
		Expect(same).To(Equal(text))
		Expect(all).To(HaveLen(3))
	})

	ginkgo.It("checks the magic of PDFs and Office files", func() {
		Expect(magicMatches("a.pdf", []byte("junk%PDF-1.7"))).To(BeTrue())
		Expect(magicMatches("a.PDF", []byte("hello"))).To(BeFalse())
		Expect(magicMatches("a.docx", []byte("PK\x03\x04..."))).To(BeTrue())
		Expect(magicMatches("a.xlsx", []byte("%PDF-"))).To(BeFalse())
		Expect(Indexable("A.XLSM")).To(BeTrue())
		Expect(Indexable("a.epub")).To(BeFalse())
	})
})
