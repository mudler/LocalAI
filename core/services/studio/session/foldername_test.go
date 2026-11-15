package session_test

import (
	"strings"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("FolderName", func() {
	day := time.Date(2026, 10, 18, 23, 30, 0, 0, time.UTC)

	DescribeTable("makes a date and an ASCII slug",
		func(title, want string) {
			Expect(session.FolderName(day, title)).To(Equal(want))
		},
		Entry("a plain title", "Budget", "2026-10-18-budget"),
		Entry("spaces become one dash", "Q1   budget  review", "2026-10-18-q1-budget-review"),
		Entry("a middle dot separates words", "Report · Q1", "2026-10-18-report-q1"),
		Entry("punctuation is dropped", "What's up? (draft)", "2026-10-18-what-s-up-draft"),
		Entry("accents lose their mark", "Café crème à la Zoë", "2026-10-18-cafe-creme-a-la-zoe"),
		Entry("letters with no decomposition are spelled out", "Straße Œuvre", "2026-10-18-strasse-oeuvre"),
		Entry("a slash is a separator", "a/b", "2026-10-18-a-b"),
		Entry("digits are kept", "2026 plan v2", "2026-10-18-2026-plan-v2"),
		Entry("a title with no ASCII letter falls back", "日本語 · 한국어", "2026-10-18-new-chat"),
		Entry("an empty title falls back", "", "2026-10-18-new-chat"),
		Entry("a title of punctuation falls back", " - · - ", "2026-10-18-new-chat"),
	)

	It("uses the creation date in UTC", func() {
		late := time.Date(2026, 10, 18, 23, 30, 0, 0, time.FixedZone("x", -5*3600))
		Expect(session.FolderName(late, "x")).To(Equal("2026-10-19-x"))
	})

	It("never holds a space, an upper-case letter or a character outside ASCII", func() {
		for _, title := range []string{"Café · Zoë", "a b", "日本 語", "ÀÉÎÕÜ", "x\ty", "tab\nnewline", "𝔘𝔫𝔦𝔠𝔬𝔡𝔢"} {
			name := session.FolderName(day, title)
			Expect(name).To(MatchRegexp(`^2026-10-18-[a-z0-9]+(-[a-z0-9]+)*$`), "title %q", title)
		}
	})

	It("stays inside the byte budget of a name, with room for a suffix", func() {
		name := session.FolderName(day, strings.Repeat("abcdefghij ", 30))
		Expect(utf8.RuneCountInString(name)).To(BeNumerically("<=", 255-len("-999")))
		Expect(name).NotTo(HaveSuffix("-"))
	})
})
