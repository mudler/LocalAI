package skills

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Parse", func() {
	long := strings.Repeat("x", MaxDescription+1)
	DescribeTable("reads the frontmatter",
		func(in, wantName, wantDesc string, wantErr error) {
			name, desc, err := Parse([]byte(in))
			if wantErr == nil {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(MatchError(wantErr))
			}
			Expect(name).To(Equal(wantName))
			Expect(desc).To(Equal(wantDesc))
		},
		Entry("plain", "---\nname: invoices\ndescription: How we file supplier invoices.\n---\nBody.\n", "invoices", "How we file supplier invoices.", nil),
		Entry("double quotes", "---\nname: \"invoices\"\ndescription: \"Use when: filing \\\"paid\\\" invoices\"\n---\n", "invoices", `Use when: filing "paid" invoices`, nil),
		Entry("single quotes", "---\nname: 'invoices'\ndescription: 'It''s a procedure: step one'\n---\n", "invoices", "It's a procedure: step one", nil),
		Entry("literal block", "---\nname: report\ndescription: |\n  First line.\n  Second line.\nlicense: MIT\n---\n", "report", "First line. Second line.", nil),
		Entry("folded block", "---\nname: report\ndescription: >-\n  Folded\n  text here.\n---\n", "report", "Folded text here.", nil),
		Entry("unknown keys, nesting and comments are ignored", "---\n# a comment\nlicense: MIT\nmetadata:\n  version: \"2\"\nname: report\ndescription: Weekly report.\nallowed-tools: Read\n---\n", "report", "Weekly report.", nil),
		Entry("windows line ends and a BOM", "\xef\xbb\xbf---\r\nname: report\r\ndescription: Weekly report.\r\n---\r\n", "report", "Weekly report.", nil),
		Entry("the last value of a repeated key wins", "---\nname: a\nname: b\ndescription: D.\n---\n", "b", "D.", nil),
		Entry("no frontmatter", "# Title\nname: x\n", "", "", ErrNoFrontmatter),
		Entry("frontmatter never closed", "---\nname: x\ndescription: D.\n", "", "", ErrNoFrontmatter),
		Entry("no name", "---\ndescription: D.\n---\n", "", "", ErrNoName),
		Entry("empty name", "---\nname:\ndescription: D.\n---\n", "", "", ErrNoName),
		Entry("bad name: capital", "---\nname: Invoices\ndescription: D.\n---\n", "", "", ErrBadName),
		Entry("bad name: slash", "---\nname: ../x\ndescription: D.\n---\n", "", "", ErrBadName),
		Entry("bad name: too long", "---\nname: "+strings.Repeat("a", 65)+"\ndescription: D.\n---\n", "", "", ErrBadName),
		Entry("name of 64 characters", "---\nname: "+strings.Repeat("a", 64)+"\ndescription: D.\n---\n", strings.Repeat("a", 64), "D.", nil),
		Entry("no description", "---\nname: x\n---\n", "", "", ErrNoDescription),
		Entry("description of only invisible characters", "---\nname: x\ndescription: \"\xe2\x80\x8b\xe2\x80\xae\"\n---\n", "", "", ErrNoDescription),
		Entry("description too long", "---\nname: x\ndescription: "+long+"\n---\n", "", "", ErrDescriptionTooLong),
		Entry("description of exactly the limit", "---\nname: x\ndescription: "+long[1:]+"\n---\n", "x", long[1:], nil),
		Entry("control and bidi characters are removed", "---\nname: x\ndescription: \"a\x00b\xe2\x80\xaec\xe2\x80\x8bd\x07\"\n---\n", "x", "abcd", nil),
		Entry("white space runs become one space", "---\nname: x\ndescription: \"a \t\xc2\xa0 b\"\n---\n", "x", "a b", nil),
		Entry("too large", "---\nname: x\ndescription: D.\n---\n"+strings.Repeat("y", MaxFileSize), "", "", ErrTooLarge),
	)
})

var _ = Describe("Clean", func() {
	It("drops invalid UTF-8", func() {
		Expect(Clean("a\xffb \n c")).To(Equal("ab c"))
	})
})

var _ = Describe("ValidName", func() {
	It("accepts valid names and rejects invalid ones", func() {
		for _, ok := range []string{"a", "docx", "a-b-1", "0day"} {
			Expect(ValidName(ok)).To(BeTrue(), "%q should be valid", ok)
		}
		for _, bad := range []string{"", "-a", "A", "a_b", "a b", "a/b", ".hidden"} {
			Expect(ValidName(bad)).To(BeFalse(), "%q should be invalid", bad)
		}
	})
})

var _ = Describe("Requires", func() {
	DescribeTable("reads the capabilities a skill needs",
		func(in string, want []string) {
			Expect(Requires([]byte(in))).To(Equal(want))
		},
		Entry("a flow list", "---\nname: xlsx\ndescription: D.\nrequires: [exec]\n---\n", []string{"exec"}),
		Entry("words split by commas", "---\nname: x\ndescription: D.\nrequires: exec, net\n---\n", []string{"exec", "net"}),
		Entry("quoted values and repeats", "---\nname: x\ndescription: D.\nrequires: [\"exec\", 'exec']\n---\n", []string{"exec"}),
		Entry("a value that is not a capability word is dropped", "---\nname: x\ndescription: D.\nrequires: [exec, ../x, EXEC!]\n---\n", []string{"exec"}),
		Entry("no key", "---\nname: x\ndescription: D.\n---\n", nil),
		Entry("no frontmatter", "Body only.\n", nil),
	)
})
