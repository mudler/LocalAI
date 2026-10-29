package studio_test

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// repoRoot is the root of the module, seen from this package.
const repoRoot = "../../.."

// goFiles calls fn for each non-test Go file under the given folders.
func goFiles(folders []string, fn func(path string, src []byte)) {
	for _, folder := range folders {
		err := filepath.WalkDir(filepath.Join(repoRoot, folder), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fn(filepath.ToSlash(strings.TrimPrefix(path, repoRoot+"/")), src)
			return nil
		})
		Expect(err).NotTo(HaveOccurred(), "walk %s: %v", folder, err)
	}
}

// A service shares its process with the server and with every other service.
// None of them may replace the process logger or change the environment: the
// first one that did dropped the configured log level and format.
var _ = Describe("guard", func() {
	It("no service changes global process state", func() {
		forbidden := []string{"xlog.SetLogger(", "slog.SetDefault(", "os.Setenv(", "os.Unsetenv(", "os.Clearenv(", "log.SetOutput("}
		// The worker sets one variable for its own process, before it serves.
		allowed := map[string][]string{"core/services/worker/worker.go": {"os.Setenv("}}
		goFiles([]string{"core/services", "pkg/addons", "addons"}, func(path string, src []byte) {
			for _, f := range forbidden {
				if !strings.Contains(string(src), f) || slices.Contains(allowed[path], f) {
					continue
				}
				// main packages own their process.
				if strings.Contains(path, "/cmd/") {
					continue
				}
				Fail(fmt.Sprintf("%s calls %s: a service must not change process-wide state", path, f))
			}
		})
	})

	// The core does not know the add-ons. An add-on may use the workspace packages
	// of the core, which are its public face, and nothing else of core.
	It("core and addons stay apart", func() {
		const module = "github.com/mudler/LocalAI/"
		imports := func(path string, src []byte) []string {
			f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
			Expect(err).NotTo(HaveOccurred(), "parse %s: %v", path, err)
			var out []string
			for _, spec := range f.Imports {
				p, err := strconv.Unquote(spec.Path.Value)
				Expect(err).NotTo(HaveOccurred(), "import %s: %v", spec.Path.Value, err)
				out = append(out, p)
			}
			return out
		}
		goFiles([]string{"core", "pkg"}, func(path string, src []byte) {
			for _, p := range imports(path, src) {
				Expect(p).NotTo(HavePrefix(module+"addons/"), "%s imports the add-on package %s: the core must not depend on an add-on", path, p)
			}
		})
		goFiles([]string{"addons"}, func(path string, src []byte) {
			for _, p := range imports(path, src) {
				if strings.HasPrefix(p, module+"core/") && !strings.HasPrefix(p, module+"core/services/studio/workspace") {
					Fail(fmt.Sprintf("%s imports %s: an add-on may use only the workspace packages of the core", path, p))
				}
			}
		})
	})
})
