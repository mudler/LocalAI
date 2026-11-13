package pages_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/pages"
)

var _ = Describe("the widget block of a page", func() {
	ctx := context.Background()

	DescribeTable("reads page.json",
		func(meta string, want *pages.Widget) {
			f := newFiles()
			Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
			svc := pages.New(f)
			put(f, "alice", "/pages/clock/index.html", "<p>clock</p>")
			put(f, "alice", "/pages/clock/page.json", meta)
			p, err := svc.Get(ctx, "alice", "clock")
			Expect(err).NotTo(HaveOccurred())
			if want == nil {
				Expect(p.Widget).To(BeNil())
				return
			}
			Expect(p.Widget).To(Equal(want))
		},
		Entry("no block", `{"title":"Clock"}`, nil),
		Entry("small", `{"widget":{"size":"small"}}`, &pages.Widget{Size: "small"}),
		Entry("large with a title", `{"widget":{"size":"large","title":"  Now  "}}`, &pages.Widget{Size: "large", Title: "Now"}),
		Entry("an empty block is medium", `{"widget":{}}`, &pages.Widget{Size: "medium"}),
		Entry("an unknown size is medium", `{"widget":{"size":"huge"}}`, &pages.Widget{Size: "medium"}),
		Entry("a size in capitals", `{"widget":{"size":"Small"}}`, &pages.Widget{Size: "small"}),
		Entry("a size that is not a string", `{"widget":{"size":3,"title":["x"]}}`, &pages.Widget{Size: "medium"}),
		Entry("false", `{"widget":false}`, nil),
		Entry("null", `{"widget":null}`, nil),
		Entry("a string", `{"widget":"small"}`, nil),
		Entry("a broken file", `{"widget":{"size":"small"`, nil),
		Entry("compact true", `{"widget":{"size":"small","compact":true}}`, &pages.Widget{Size: "small", Compact: &pages.Compact{Width: 120}}),
		Entry("compact with a width", `{"widget":{"compact":{"width":160}}}`, &pages.Widget{Size: "medium", Compact: &pages.Compact{Width: 160}}),
		Entry("a compact width too small is clamped", `{"widget":{"compact":{"width":4}}}`, &pages.Widget{Size: "medium", Compact: &pages.Compact{Width: 72}}),
		Entry("a compact width too large is clamped", `{"widget":{"compact":{"width":9000}}}`, &pages.Widget{Size: "medium", Compact: &pages.Compact{Width: 220}}),
		Entry("a compact width that is not a number", `{"widget":{"compact":{"width":"wide"}}}`, &pages.Widget{Size: "medium", Compact: &pages.Compact{Width: 120}}),
		Entry("compact false", `{"widget":{"compact":false}}`, &pages.Widget{Size: "medium"}),
		Entry("compact as a string", `{"widget":{"compact":"yes"}}`, &pages.Widget{Size: "medium"}),
		Entry("a long title is cut", `{"widget":{"title":"`+longTitle+`"}}`, &pages.Widget{Size: "medium", Title: longTitle[:60]}),
	)

	It("lists the widget block with the page", func() {
		f := newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		svc := pages.New(f)
		put(f, "alice", "/pages/clock/index.html", "<p>clock</p>")
		put(f, "alice", "/pages/clock/page.json", `{"widget":{"size":"small"}}`)
		put(f, "alice", "/pages/notes/index.html", "<p>notes</p>")
		list, err := svc.List(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		Expect(list[0].Widget).To(Equal(&pages.Widget{Size: "small"}))
		Expect(list[1].Widget).To(BeNil())
	})
})

const longTitle = "A title that is far too long for the small bar of a tile on the Home page"
