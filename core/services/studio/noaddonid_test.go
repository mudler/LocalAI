// SPDX-License-Identifier: MIT
package studio_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Core decides what an add-on may do from what the add-on declares and what it
// reports at run time. These specs fail when a file that sets the rules for
// command execution names one add-on again. Tests, comments, documentation and
// the add-on's own data are the places that may.
var _ = Describe("the command execution code of the host", func() {
	// literals returns the string literals of the non-test Go files of dir.
	literals := func(dir string, only ...string) map[string][]string {
		out := map[string][]string{}
		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			if len(only) > 0 && !slices.Contains(only, name) {
				continue
			}
			path := filepath.Join(dir, name)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			Expect(err).NotTo(HaveOccurred())
			ast.Inspect(file, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, err := strconv.Unquote(lit.Value)
					Expect(err).NotTo(HaveOccurred())
					out[path] = append(out[path], v)
				}
				return true
			})
		}
		return out
	}

	It("compares no string with the ID of the add-on that runs commands", func() {
		// Other add-on IDs join this list as their special cases are removed.
		ids := []string{"sandbox", "office"}
		for _, dir := range []string{".", "../addons/tools"} {
			for path, values := range literals(dir) {
				for _, v := range values {
					Expect(ids).NotTo(ContainElement(v), "%s compares with the ID of an add-on", path)
				}
			}
		}
	})

	It("names no add-on in the text it builds for the commands of a chat", func() {
		for path, values := range literals(".", "capabilities.go", "addonskills.go", "loadskill.go", "shadow.go") {
			for _, v := range values {
				Expect(strings.ToLower(v)).NotTo(ContainSubstring("sandbox"), "%s writes a provider name into a sentence; take it from the manifest", path)
			}
		}
	})

	It("holds no list of document formats: slots come from the add-ons and from data", func() {
		var formats []string
		for _, sl := range contract.DefaultSlots() {
			formats = append(formats, sl.Name, sl.Library)
			formats = append(formats, sl.Extensions...)
			formats = append(formats, sl.Words...)
		}
		for path, values := range literals(".", "binaryhint.go", "addonskills.go", "loadskill.go", "toolsadmin.go") {
			for _, v := range values {
				Expect(formats).NotTo(ContainElement(v), "%s names a document format; read it from the slot", path)
			}
		}
	})
})
