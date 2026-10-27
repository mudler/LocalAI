package studio_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// repoRoot is the root of the module, seen from this package.
const repoRoot = "../../.."

// goFiles calls fn for each non-test Go file under the given folders.
func goFiles(t *testing.T, folders []string, fn func(path string, src []byte)) {
	t.Helper()
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
		if err != nil {
			t.Fatalf("walk %s: %v", folder, err)
		}
	}
}

// A service shares its process with the server and with every other service.
// None of them may replace the process logger or change the environment: the
// first one that did dropped the configured log level and format.
func TestNoServiceChangesGlobalProcessState(t *testing.T) {
	forbidden := []string{"xlog.SetLogger(", "slog.SetDefault(", "os.Setenv(", "os.Unsetenv(", "os.Clearenv(", "log.SetOutput("}
	// The worker sets one variable for its own process, before it serves.
	allowed := map[string][]string{"core/services/worker/worker.go": {"os.Setenv("}}
	goFiles(t, []string{"core/services", "pkg/addons", "addons"}, func(path string, src []byte) {
		for _, f := range forbidden {
			if !strings.Contains(string(src), f) || slices.Contains(allowed[path], f) {
				continue
			}
			// main packages own their process.
			if strings.Contains(path, "/cmd/") {
				continue
			}
			t.Errorf("%s calls %s: a service must not change process-wide state", path, f)
		}
	})
}

// The core does not know the add-ons. An add-on may use the workspace packages
// of the core, which are its public face, and nothing else of core.
func TestCoreAndAddonsStayApart(t *testing.T) {
	const module = "github.com/mudler/LocalAI/"
	imports := func(path string, src []byte) []string {
		f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		var out []string
		for _, spec := range f.Imports {
			p, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("import %s: %v", spec.Path.Value, err)
			}
			out = append(out, p)
		}
		return out
	}
	goFiles(t, []string{"core", "pkg"}, func(path string, src []byte) {
		for _, p := range imports(path, src) {
			if strings.HasPrefix(p, module+"addons/") {
				t.Errorf("%s imports the add-on package %s: the core must not depend on an add-on", path, p)
			}
		}
	})
	goFiles(t, []string{"addons"}, func(path string, src []byte) {
		for _, p := range imports(path, src) {
			if strings.HasPrefix(p, module+"core/") && !strings.HasPrefix(p, module+"core/services/studio/workspace") {
				t.Errorf("%s imports %s: an add-on may use only the workspace packages of the core", path, p)
			}
		}
	})
}
