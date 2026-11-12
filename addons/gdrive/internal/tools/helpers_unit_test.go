// SPDX-License-Identifier: MIT
package tools_test

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/addons/gdrive/internal/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the string literal of a query", func() {
	DescribeTable("escapes a backslash and a single quote", func(in, want string) {
		Expect(tools.Literal(in)).To(Equal(want))
	},
		Entry("plain text", `plain`, `'plain'`),
		Entry("a quote", `it's`, `'it\'s'`),
		Entry("a backslash", `a\b`, `'a\\b'`),
		Entry("a backslash and a quote", `\'`, `'\\\''`),
		Entry("an attempt to end the literal", `' or 1=1 '`, `'\' or 1=1 \''`),
		Entry("nothing", ``, `''`),
	)
})

var _ = Describe("the media types that are text", func() {
	DescribeTable("tells text from the rest", func(mime string, want bool) {
		Expect(tools.IsText(mime)).To(Equal(want))
	},
		Entry("plain text", "text/plain", true),
		Entry("csv with a charset", "text/csv; charset=utf-8", true),
		Entry("json", "application/json", true),
		Entry("a json suffix", "application/ld+json", true),
		Entry("pdf", "application/pdf", false),
		Entry("png", "image/png", false),
		Entry("bytes", "application/octet-stream", false),
		Entry("a native document", "application/vnd.google-apps.document", false),
	)
})

var _ = Describe("a window of text", func() {
	It("drops a character that the end of the window cut in two", func() {
		b := []byte("aé")
		Expect(string(tools.TrimPartial(b[:2]))).To(Equal("a"))
		Expect(string(tools.TrimPartial(b))).To(Equal("aé"))
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
				format, _ := strconv.Unquote(lit.Value)
				if format == "%s" {
					// A reason from the API package, checked with its own spec.
					return true
				}
				// Fill each verb with a value of a plausible length.
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
