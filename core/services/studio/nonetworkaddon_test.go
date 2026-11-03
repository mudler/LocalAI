// SPDX-License-Identifier: MIT
package studio_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The network of the commands an add-on runs is named from what the add-on
// declares (capabilities.network, or the title of its commands tool). These
// specs fail when a file that builds that text or sets those rules writes the
// name of one add-on again. Tests, comments, documentation and the add-on's
// own data are the places that may.
var _ = Describe("the network code of the host", func() {
	// stringsOf returns the string literals of the given Go files of dir,
	// leaving out import paths.
	stringsOf := func(dir string, files ...string) map[string][]string {
		out := map[string][]string{}
		for _, name := range files {
			path := filepath.Join(dir, name)
			_, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred(), "the guard lists a file that is gone: %s", path)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			Expect(err).NotTo(HaveOccurred())
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.ImportSpec:
					return false
				case *ast.BasicLit:
					if n.Kind == token.STRING {
						v, err := strconv.Unquote(n.Value)
						Expect(err).NotTo(HaveOccurred())
						out[path] = append(out[path], v)
					}
				}
				return true
			})
		}
		return out
	}

	It("writes no add-on name in the text of the network, the built-in bash or the host-exec setting", func() {
		groups := map[string][]string{
			".":                           {"egress.go", "egressapi.go", "egresscards.go", "egresscheck.go", "egressjson.go", "hostbash.go"},
			"egress":                      {"egress.go"},
			"hostexec":                    {"hostexec.go"},
			"../../config":                {"chats_egress.go"},
			"../../../pkg/egresspolicy":   {"policy.go"},
			"../../http/endpoints/studio": {"studio.go"},
		}
		for dir, files := range groups {
			for path, values := range stringsOf(dir, files...) {
				for _, v := range values {
					Expect(strings.ToLower(v)).NotTo(ContainSubstring("sandbox"), "%s writes an add-on name into a string; take it from the manifest", path)
				}
			}
		}
	})
})
