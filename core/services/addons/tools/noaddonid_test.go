// SPDX-License-Identifier: MIT
package tools_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Core offers abilities to add-ons by declaration. It names no add-on: the
// only exception is the marked compatibility file listed below.
var _ = Describe("add-on ids in core code", func() {
	dirs := []string{".", "../apps", "../gateway", "../resources", "../../../../pkg/addons/contract"}
	allowed := map[string]bool{"legacy_resource_list.go": true}

	It("keeps the id of the Tables add-on out of string literals, outside tests and the marked shim", func() {
		var hits []string
		for _, dir := range dirs {
			files, err := filepath.Glob(filepath.Join(dir, "*.go"))
			Expect(err).NotTo(HaveOccurred())
			for _, path := range files {
				if strings.HasSuffix(path, "_test.go") || allowed[filepath.Base(path)] {
					continue
				}
				f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				Expect(err).NotTo(HaveOccurred())
				ast.Inspect(f, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if s, err := strconv.Unquote(lit.Value); err == nil && strings.EqualFold(s, "tables") {
							hits = append(hits, path)
						}
					}
					return true
				})
			}
		}
		Expect(hits).To(BeEmpty())
	})
})
