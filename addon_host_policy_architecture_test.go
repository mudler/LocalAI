// SPDX-License-Identifier: MIT
package localai_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostPolicyArchitecture(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Host policy architecture")
}

var _ = Describe("generic host policy architecture", func() {
	It("keeps official addon identities out of generic CLI and config policy code", func() {
		// Tests may retain historical fixtures. Deployment policy is deliberately
		// outside these packages; unrelated model fields (e.g. quantization) are not IDs.
		ids := []string{"fine-tuning", "quantization", "sandbox", "office", "web", "telegram"}
		for _, root := range []string{"core/cli", "core/config"} {
			err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				if err != nil {
					return err
				}
				policyCode := strings.Contains(filepath.Base(path), "addon_host_") || path == "core/cli/run.go"
				ast.Inspect(file, func(node ast.Node) bool {
					literal, ok := node.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						return true
					}
					value, err := strconv.Unquote(literal.Value)
					Expect(err).NotTo(HaveOccurred())
					for _, id := range ids {
						Expect(value).NotTo(ContainSubstring(id+"="), "%s embeds an official grant", path)
						if policyCode {
							Expect(value).NotTo(Equal(id), "%s embeds an official addon identity", path)
						}
					}
					Expect(value).NotTo(ContainSubstring("default:\"fine-tuning="), "%s embeds a CLI default", path)
					return true
				})
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		}
	})
})
