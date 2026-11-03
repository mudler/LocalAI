package studio

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"regexp"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The file limit is decided in one place (EffectiveFileLimit) and written in
// one place (connectorlimits.go). These files talk about file limits and must
// not carry a number or a sentence of their own: a limit that is typed here
// would be wrong as soon as an add-on or an owner sets another one.
var _ = Describe("the files that use the file limit", func() {
	files := []string{"connectors.go", "connectorfiles.go", "attachfile.go", "connectorturn.go"}
	// Files with no other use for a large number.
	noLargeNumbers := []string{"connectorfiles.go", "attachfile.go", "connectorturn.go"}
	unit := regexp.MustCompile(`(?i)\b\d+(\.\d+)?\s*(bytes?|[kmgt]i?b)\b`)

	parse := func(name string) (*gotoken.FileSet, *ast.File) {
		fset := gotoken.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		Expect(err).NotTo(HaveOccurred())
		return fset, f
	}

	It("write no size in a sentence", func() {
		for _, name := range files {
			fset, f := parse(name)
			ast.Inspect(f, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == gotoken.STRING {
					s, _ := strconv.Unquote(lit.Value)
					Expect(unit.MatchString(s)).To(BeFalse(), "%s: %q names a size; build it from the limit in effect", fset.Position(lit.Pos()), s)
				}
				return true
			})
		}
	})

	It("hold no byte count", func() {
		for _, name := range noLargeNumbers {
			fset, f := parse(name)
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind == gotoken.INT {
						v, err := strconv.ParseInt(x.Value, 0, 64)
						Expect(err == nil && v < 1024).To(BeTrue(), "%s: %s looks like a byte count", fset.Position(x.Pos()), x.Value)
					}
				case *ast.BinaryExpr:
					Expect(x.Op).NotTo(Equal(gotoken.SHL), "%s: a shift builds a byte count", fset.Position(x.Pos()))
				}
				return true
			})
		}
	})
})
