package session

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("the ID of a tool card", func() {
	ginkgo.It("keeps a files_change card whose path has letters outside ASCII", func() {
		for _, id := range []string{"7:/report.docx", "7:/Documenti/città.txt", "7:/文档/说明.md", "7:/" + strings.Repeat("a", 200)} {
			c, ok := cleanCard(ToolCard{Kind: ChangeCardKind, ID: id})
			Expect(ok).To(BeTrue(), id)
			Expect(c.ID).To(Equal(id))
		}
	})

	ginkgo.It("refuses a files_change card with a control character, bad UTF-8 or a path that is too long", func() {
		for _, id := range []string{"", "7:/a\nb", "7:/a\x00b", "7:/\xff", "7:/" + strings.Repeat("a", 1100)} {
			_, ok := cleanCard(ToolCard{Kind: ChangeCardKind, ID: id})
			Expect(ok).To(BeFalse(), id)
		}
	})

	ginkgo.It("still holds the other cards to 128 printable ASCII characters", func() {
		_, ok := cleanCard(ToolCard{Kind: "trigger_change", ID: "città"})
		Expect(ok).To(BeFalse())
		_, ok = cleanCard(ToolCard{Kind: "trigger_change", ID: strings.Repeat("a", 129)})
		Expect(ok).To(BeFalse())
		_, ok = cleanCard(ToolCard{Kind: "trigger_change", ID: strings.Repeat("a", 128)})
		Expect(ok).To(BeTrue())
	})
})
