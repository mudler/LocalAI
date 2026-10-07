package carrier_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Only the files of a carrier may import the library of that carrier. The code
// above the seams, and the holders in this package, name interfaces only, so a
// second carrier is a new implementation and not a change to its callers.
//
// The scan reads the import lines of every non-test Go file under the module
// root. It does not use `go list`: that skips a file whose build constraints do
// not match the host, and an import behind a constraint would pass unseen. It
// does not use a linter rule either: the repository does not run depguard, and
// a spec fails in the same place every other spec does.
//
// Test files are exempt. They start servers and mint credentials with the
// library on purpose.

const natsLibraries = "github.com/nats-io/"

// natsAllowlist is every non-test file or package directory allowed to import
// natsLibraries, as a slash path from the module root. Add to it only for a
// file that is part of the NATS carrier. A new carrier library gets its own
// list in the slice that adds the carrier.
var natsAllowlist = []string{
	"core/services/messaging/client.go",
	"core/services/messaging/tls.go",
	"core/services/nodes/control_nats.go",
	"pkg/natsauth/", // credential minting and decoding for the NATS carrier
}

// importers returns the slash paths, from root, of the non-test Go files that
// import a path with the given prefix. It skips hidden directories,
// node_modules and nested modules.
func importers(root, prefix string) ([]string, error) {
	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root {
				if strings.HasPrefix(name, ".") || name == "node_modules" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(p, prefix) {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				found = append(found, filepath.ToSlash(rel))
				return nil
			}
		}
		return nil
	})
	slices.Sort(found)
	return found, err
}

func allowed(file string, allowlist []string) bool {
	for _, a := range allowlist {
		if file == a || (strings.HasSuffix(a, "/") && strings.HasPrefix(file, a)) {
			return true
		}
	}
	return false
}

// moduleRoot walks up from the working directory of the spec to go.mod.
func moduleRoot() string {
	dir, err := os.Getwd()
	Expect(err).ToNot(HaveOccurred())
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		Expect(parent).ToNot(Equal(dir), "no go.mod above the spec")
		dir = parent
	}
}

var _ = Describe("Import boundary of the NATS libraries", func() {
	It("lets only the files of the NATS carrier import them", func() {
		files, err := importers(moduleRoot(), natsLibraries)
		Expect(err).ToNot(HaveOccurred())

		var stray []string
		for _, f := range files {
			if !allowed(f, natsAllowlist) {
				stray = append(stray, f)
			}
		}
		Expect(stray).To(BeEmpty(), "these files import %s but are not in natsAllowlist; code above the seams must use messaging and nodes interfaces", natsLibraries)
	})

	It("keeps the allowlist honest: every entry still imports the libraries", func() {
		files, err := importers(moduleRoot(), natsLibraries)
		Expect(err).ToNot(HaveOccurred())

		for _, a := range natsAllowlist {
			used := slices.ContainsFunc(files, func(f string) bool { return allowed(f, []string{a}) })
			Expect(used).To(BeTrue(), "%s no longer imports %s; remove it from natsAllowlist", a, natsLibraries)
		}
	})

	Describe("the scan", func() {
		var root string

		write := func(rel, body string) {
			path := filepath.Join(root, filepath.FromSlash(rel))
			Expect(os.MkdirAll(filepath.Dir(path), 0o750)).To(Succeed())
			Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())
		}

		BeforeEach(func() { root = GinkgoT().TempDir() })

		It("finds an import in any non-test file, including one behind a build constraint", func() {
			write("a/plain.go", "package a\nimport _ \"github.com/nats-io/nats.go\"\n")
			write("a/tagged.go", "//go:build never\n\npackage a\nimport _ \"github.com/nats-io/nkeys\"\n")
			write("b/clean.go", "package b\nimport _ \"fmt\"\n")

			files, err := importers(root, natsLibraries)
			Expect(err).ToNot(HaveOccurred())
			Expect(files).To(Equal([]string{"a/plain.go", "a/tagged.go"}))
		})

		It("ignores test files, hidden directories and nested modules", func() {
			write("a/x_test.go", "package a\nimport _ \"github.com/nats-io/nats.go\"\n")
			write(".hidden/h.go", "package h\nimport _ \"github.com/nats-io/nats.go\"\n")
			write("nested/go.mod", "module nested\n")
			write("nested/n.go", "package n\nimport _ \"github.com/nats-io/nats.go\"\n")

			files, err := importers(root, natsLibraries)
			Expect(err).ToNot(HaveOccurred())
			Expect(files).To(BeEmpty())
		})

		It("reports a file it cannot parse", func() {
			write("a/broken.go", "package a\nimport (\n")
			_, err := importers(root, natsLibraries)
			Expect(err).To(HaveOccurred())
		})

		It("matches an allowlist entry as a file or as a directory", func() {
			Expect(allowed("pkg/natsauth/mint.go", []string{"pkg/natsauth/"})).To(BeTrue())
			Expect(allowed("pkg/natsauthx/mint.go", []string{"pkg/natsauth/"})).To(BeFalse())
			Expect(allowed("core/a.go", []string{"core/a.go"})).To(BeTrue())
			Expect(allowed("core/ab.go", []string{"core/a.go"})).To(BeFalse())
		})
	})
})
