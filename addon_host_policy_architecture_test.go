// SPDX-License-Identifier: MIT
package localai_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

var _ = Describe("typed extension architecture", func() {
	It("keeps provider and Home dispatch independent of official addon IDs", func() {
		// Only generic live dispatch surfaces are guarded. Legacy URL redirects,
		// pin migration, native Studio code, deployment policy, and tests are not
		// live addon dispatch and deliberately do not belong to this list.
		paths := []string{"core/services/addons/apps", "core/services/addons/gateway/binding.go", "core/services/studio/executionselection.go"}
		for _, root := range paths {
			Expect(filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				if err != nil {
					return err
				}
				ast.Inspect(f, func(n ast.Node) bool {
					lit, ok := n.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return true
					}
					value, err := strconv.Unquote(lit.Value)
					Expect(err).NotTo(HaveOccurred())
					Expect(value).NotTo(BeElementOf("studio", "sandbox"), "%s hardcodes a product ID in generic dispatch", path)
					return true
				})
				return nil
			})).To(Succeed())
		}
		data, err := os.ReadFile("core/http/react-ui/src/addons/homeCommands.js")
		Expect(err).NotTo(HaveOccurred())
		for _, literal := range []string{"'studio'", `"studio"`, "'sandbox'", `"sandbox"`} {
			Expect(string(data)).NotTo(ContainSubstring(literal))
		}
	})
})

var _ = Describe("provider-neutral host code", func() {
	// officialIDs are the IDs of the add-ons the repository ships. Host code
	// outside deployment policy must not name them as literals.
	officialIDs := []string{
		"face", "fine-tuning", "github", "memory", "office", "quantization", "sandbox",
		"slack", "studio", "tables", "talk", "telegram", "voice", "web",
	}
	// allowed maps "path: literal" to the reason that the literal is not an
	// add-on identity. Keep every entry narrow and justified.
	allowed := map[string]string{
		"core/http/auth/models.go: github":                    "the host's own sign-in identity provider, not an add-on",
		"core/http/auth/permissions.go: quantization":         "built-in permission name for the host's native feature, not an add-on dispatch",
		"core/http/auth/usage.go: web":                        "usage source label for a session-cookie request, not an add-on",
		"core/services/studio/egress/registries.json: github": "egress preset id for well-known release hosts, not an add-on dispatch",
	}

	scan := func(root string) []string {
		var hits []string
		add := func(path, value string) {
			for _, id := range officialIDs {
				if value == id {
					hits = append(hits, path+": "+id)
				}
			}
		}
		Expect(filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			switch {
			case strings.HasSuffix(path, ".go"):
				f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				if err != nil {
					return err
				}
				ast.Inspect(f, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						value, err := strconv.Unquote(lit.Value)
						Expect(err).NotTo(HaveOccurred())
						add(path, value)
					}
					return true
				})
			case strings.HasSuffix(path, ".json"):
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				// The presets file allows comments and trailing commas, so read
				// its quoted strings directly instead of parsing it.
				for _, m := range quotedString.FindAllStringSubmatch(string(data), -1) {
					add(path, m[1])
				}
			}
			return nil
		})).To(Succeed())
		return hits
	}

	It("keeps official add-on IDs out of auth, connectors and egress presets", func() {
		used := map[string]bool{}
		for _, root := range []string{"core/http/auth", "core/services/studio/connectors", "core/services/studio/egress/registries.json"} {
			for _, hit := range scan(root) {
				if _, ok := allowed[hit]; ok {
					used[hit] = true
					continue
				}
				Fail("official add-on ID literal in host code: " + hit)
			}
		}
		for entry := range allowed {
			Expect(used).To(HaveKey(entry), "stale allowlist entry %q", entry)
		}
	})
})

var quotedString = regexp.MustCompile(`"([^"\\]*)"`)
