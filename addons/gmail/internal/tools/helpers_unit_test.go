// SPDX-License-Identifier: MIT
package tools_test

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/addons/gmail/internal/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the q expression", func() {
	DescribeTable("quotes a value as one phrase", func(in, want string) {
		got, ok := tools.Quote(in)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(want))
	},
		Entry("a word", "invoice", `"invoice"`),
		Entry("a quote is dropped", `a"b`, `"a b"`),
		Entry("a backslash is dropped", `a\b`, `"a b"`),
		Entry("a line break is a space", "a\r\nb", `"a b"`),
		Entry("an operator stays inside the phrase", `x" OR in:anywhere`, `"x OR in:anywhere"`),
		Entry("hidden characters are dropped", "a​b", `"a b"`),
	)

	It("refuses a value with nothing left", func() {
		_, ok := tools.Quote(`"" \ `)
		Expect(ok).To(BeFalse())
	})

	It("makes a time into seconds and refuses text that is not a time", func() {
		q, err := tools.BuildQuery(tools.QueryFilters{After: "2026-01-02T00:00:00Z", Before: "2026-01-03"})
		Expect(err).NotTo(HaveOccurred())
		Expect(q).To(Equal("after:1767312000 before:1767398400"))
		_, err = tools.BuildQuery(tools.QueryFilters{After: "1767312000 OR in:anywhere"})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("text from HTML", func() {
	It("keeps the words and nothing that loads or hides", func() {
		out := tools.HTMLToText(`<body><p>a</p><p hidden>b</p><span aria-hidden="true">c</span><div style="Visibility: Hidden">d</div>
<p style="font-size: 0.9em">e</p><p style="opacity:0.5">f</p><svg><text>g</text></svg><iframe src="x">h</iframe><!-- i --><p>j</p></body>`)
		Expect(strings.Fields(tools.Tidy(out))).To(Equal([]string{"a", "e", "f", "j"}))
	})
	It("survives broken markup and deep nesting", func() {
		Expect(tools.HTMLToText(strings.Repeat("<div>", 5000) + "x")).To(BeAssignableToTypeOf(""))
		Expect(tools.HTMLToText("<p>unclosed <b>bold")).To(ContainSubstring("unclosed bold"))
	})
})

var _ = Describe("tidy and window", func() {
	It("drops format characters and folds blank lines", func() {
		Expect(tools.Tidy("a​‮b\r\n\r\n\r\n\r\nc  \n")).To(Equal("ab\n\nc"))
	})
	It("cuts at a character boundary and joins without a gap", func() {
		text := strings.Repeat("é", 10)
		a, _, next := tools.Window(text, 0, 5)
		Expect(a).To(Equal("éé"))
		b, start, _ := tools.Window(text, next, 100)
		Expect(a + b).To(Equal(text))
		Expect(start).To(Equal(4))
		c, start, _ := tools.Window(text, 1, 100)
		Expect(start).To(Equal(2), "an offset inside a character moves on")
		Expect(c).To(Equal(strings.Repeat("é", 9)))
	})
})

var _ = Describe("headers", func() {
	It("decodes words and removes line breaks and hidden characters", func() {
		Expect(tools.HeaderText("=?UTF-8?B?w4lyaWM=?= \r\n <e@example.test>​", 100)).To(Equal("Éric <e@example.test>"))
	})
	It("makes a reply subject once", func() {
		Expect(tools.ReplySubj("Plan")).To(Equal("Re: Plan"))
		Expect(tools.ReplySubj("re: Plan")).To(Equal("re: Plan"))
	})
	DescribeTable("accepts only a plain address", func(a string, ok bool) {
		if ok {
			Expect(tools.CheckAddr(a)).To(Succeed())
		} else {
			Expect(tools.CheckAddr(a)).To(HaveOccurred())
		}
	},
		Entry("plain", "a.b+c@example.test", true),
		Entry("a name", "Al <al@example.test>", false),
		Entry("two", "a@example.test,b@example.test", false),
		Entry("a line break", "a@example.test\r\nBcc: x@example.test", false),
		Entry("a space", "a b@example.test", false),
		Entry("no domain", "a@", false),
		Entry("a quoted local part", `"a b"@example.test`, false),
		Entry("non ASCII", "é@example.test", false),
		Entry("an angle bracket", "<a@example.test>", false),
	)
	It("knows text attachments", func() {
		Expect(tools.IsText("text/csv", "")).To(BeTrue())
		Expect(tools.IsText("application/json", "")).To(BeTrue())
		Expect(tools.IsText("text/html", "")).To(BeFalse())
		Expect(tools.IsText("image/png", "a.txt")).To(BeFalse())
		Expect(tools.IsText("application/octet-stream", "a.log")).To(BeTrue())
		Expect(tools.IsText("application/pdf", "a.pdf")).To(BeFalse())
	})
})

// The host keeps the first 64 characters of a refusal, and of those only
// letters, digits, space, dot, dash and underscore. A reason that does not fit
// reaches the model cut or mangled, so every fixed reason of the add-on is
// held to that.
var _ = Describe("the refusal reasons of the add-on", func() {
	It("fit what the host passes on", func() {
		fset := gotoken.NewFileSet()
		files, err := filepath.Glob("*.go")
		Expect(err).NotTo(HaveOccurred())
		verb := regexp.MustCompile(`%[sd]`)
		allowed := regexp.MustCompile(`^[A-Za-z0-9 _.-]+$`)
		checked := 0
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, name, nil, 0)
			Expect(err).NotTo(HaveOccurred())
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := call.Fun.(*ast.Ident)
				if !ok || id.Name != "bad" || len(call.Args) == 0 {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok {
					return true
				}
				format := strings.Trim(lit.Value, "\"`")
				text := verb.ReplaceAllString(format, "modifiedBefore")
				checked++
				Expect(len(text)).To(BeNumerically("<=", 64), "%s: %q", fset.Position(call.Pos()), format)
				Expect(text).To(MatchRegexp(allowed.String()), "%s: %q", fset.Position(call.Pos()), format)
				return true
			})
		}
		Expect(checked).To(BeNumerically(">=", 30), "the scan found the reasons")
	})
})
