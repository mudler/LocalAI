package cluster_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The epoch that Registry.Claim returns is unique, and it is not ordered
// between claims. The only comparison that is correct is equality. This is a
// syntactic check: it finds an operand whose name contains "epoch" under <, >,
// <= or >=. It cannot see a comparison that goes through another name, and the
// doc comment on Claim is the rule that it backs up.

// orderedEpochComparisons returns the positions of the ordered comparisons in
// src that have an operand named like an epoch.
func orderedEpochComparisons(filename string, src any) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		switch bin.Op {
		case token.LSS, token.GTR, token.LEQ, token.GEQ:
		default:
			return true
		}
		if namesEpoch(bin.X) || namesEpoch(bin.Y) {
			found = append(found, fset.Position(bin.Pos()).String())
		}
		return true
	})
	return found, nil
}

// namesEpoch reports whether the operand is an identifier or a field whose name
// contains "epoch", at any depth of the operand.
func namesEpoch(e ast.Expr) bool {
	named := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Ident:
			named = named || strings.Contains(strings.ToLower(v.Name), "epoch")
		case *ast.SelectorExpr:
			named = named || strings.Contains(strings.ToLower(v.Sel.Name), "epoch")
		}
		return !named
	})
	return named
}

var _ = Describe("Comparing epochs", func() {
	It("finds an ordered comparison of an epoch", func() {
		for _, body := range []string{
			`if conn.Epoch > held {}`,
			`if mine < row.Epoch {}`,
			`if epoch >= 3 {}`,
			`if a.readyEpoch <= b {}`,
		} {
			found, err := orderedEpochComparisons("x.go", "package x\nfunc f() {\n"+body+"\n}\n")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(HaveLen(1), body)
		}
	})

	It("lets equality and unrelated comparisons through", func() {
		found, err := orderedEpochComparisons("x.go", `package x
func f() {
	if conn.Epoch == held || a != b.Epoch {}
	if n > 0 {}
}
`)
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeEmpty())
	})

	It("is not done anywhere in the services", func() {
		root := ".." // core/services
		var offenders []string
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			found, err := orderedEpochComparisons(path, nil)
			if err != nil {
				return err
			}
			offenders = append(offenders, found...)
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(offenders).To(BeEmpty(),
			"epochs are unique and not ordered; compare them with == or !=")
	})
})
