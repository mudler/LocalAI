package providerguard_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/providerguard"
)

// finding is one place where a file names a shipped add-on.
type finding struct {
	file string // slash path from the repository root
	line int
	name string // the id or title that matched
	text string // the string literal, line or value that holds it
}

// namer matches the ids and titles of the shipped add-ons.
type namer struct {
	ids    map[string]bool
	titles []string
	words  *regexp.Regexp
}

func newNamer(addons []providerguard.Addon) *namer {
	n := &namer{ids: map[string]bool{}}
	seen := map[string]bool{}
	var quoted []string
	for _, a := range addons {
		if !a.Role {
			continue
		}
		n.ids[a.ID] = true
		for _, t := range a.Titles {
			if !seen[t] {
				seen[t] = true
				n.titles = append(n.titles, t)
				quoted = append(quoted, regexp.QuoteMeta(t))
			}
		}
	}
	// A title is a whole word and matches case sensitively: "Sandbox" is the
	// add-on, "sandbox" the ordinary noun. A hyphen or an underscore joins
	// words, so "Web-scale" and "Face_id" are not the titles "Web" and "Face".
	n.words = regexp.MustCompile(`(^|[^\p{L}\p{N}_-])(` + strings.Join(quoted, "|") + `)($|[^\p{L}\p{N}_-])`)
	return n
}

// find returns the add-on name that s holds, or "". A string equal to an id,
// or an id followed by the tool-name separator, is a comparison or a tool name.
// A declared title anywhere in the text is a name that a person would read.
func (n *namer) find(s string) string {
	if n.ids[s] {
		return s
	}
	if i := strings.Index(s, "__"); i > 0 && n.ids[s[:i]] {
		return s[:i]
	}
	if m := n.words.FindStringSubmatch(s); m != nil {
		return m[2]
	}
	return ""
}

// findQuoted is find for source text that is not parsed: it looks at the
// title anywhere and at an id only when it stands alone between quotes.
func (n *namer) findQuoted(line string) string {
	if m := n.words.FindStringSubmatch(line); m != nil {
		return m[2]
	}
	for _, q := range quotedRe.FindAllStringSubmatch(line, -1) {
		if v := n.find(q[2]); v != "" && (n.ids[q[2]] || strings.Contains(q[2], "__")) {
			return v
		}
	}
	return ""
}

var quotedRe = regexp.MustCompile("(['\"`])([^'\"`\\n]*)(['\"`])")

func walk(root, dir string, skip func(rel string, d fs.DirEntry) bool, fn func(rel string, raw []byte)) error {
	return filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if skip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn(rel, raw)
		return nil
	})
}

func scanGo(root string, n *namer, skipDirs []string) ([]finding, error) {
	var out []finding
	for _, dir := range []string{"core", "pkg"} {
		err := walk(root, dir, func(rel string, d fs.DirEntry) bool {
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "vendor" || d.Name() == "testdata" {
					return true
				}
				// A package named like storetest or agentstest holds the
				// fixtures of the tests of another package, as a _test.go
				// file does.
				if strings.HasSuffix(d.Name(), "test") {
					return true
				}
				for _, s := range skipDirs {
					if rel == s {
						return true
					}
				}
				return false
			}
			return !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go")
		}, func(rel string, raw []byte) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, rel, raw, parser.SkipObjectResolution)
			if err != nil {
				return
			}
			ast.Inspect(f, func(node ast.Node) bool {
				lit, ok := node.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, uerr := strconv.Unquote(lit.Value)
				if uerr != nil {
					s = lit.Value
				}
				if name := n.find(s); name != "" {
					out = append(out, finding{rel, fset.Position(lit.Pos()).Line, name, s})
				}
				return true
			})
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func isTestSource(rel string) bool {
	return strings.Contains(rel, ".test.") || strings.Contains(rel, ".spec.") || strings.Contains(rel, "/__tests__/")
}

func scanUI(root string, n *namer) ([]finding, error) {
	var out []finding
	err := walk(root, "core/http/react-ui/src", func(rel string, d fs.DirEntry) bool {
		if d.IsDir() {
			return d.Name() == "node_modules" || d.Name() == "vendor"
		}
		return !(strings.HasSuffix(rel, ".js") || strings.HasSuffix(rel, ".jsx")) || isTestSource(rel)
	}, func(rel string, raw []byte) {
		inBlock := false
		for i, line := range strings.Split(string(raw), "\n") {
			t := strings.TrimSpace(line)
			if inBlock {
				if strings.Contains(t, "*/") {
					inBlock = false
				}
				continue
			}
			if strings.HasPrefix(t, "/*") {
				inBlock = !strings.Contains(t, "*/")
				continue
			}
			if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "{/*") {
				continue
			}
			// A trailing line comment is prose for developers, not a string.
			if j := strings.Index(line, " // "); j >= 0 {
				line = line[:j]
			}
			if name := n.findQuoted(line); name != "" {
				out = append(out, finding{rel, i + 1, name, t})
			}
		}
	})
	return out, err
}

func scanLocales(root string, n *namer) ([]finding, error) {
	var out []finding
	err := walk(root, "core/http/react-ui/public/locales", func(rel string, d fs.DirEntry) bool {
		return !d.IsDir() && !strings.HasSuffix(rel, ".json")
	}, func(rel string, raw []byte) {
		var tree any
		if json.Unmarshal(raw, &tree) != nil {
			return
		}
		var visit func(path string, v any)
		visit = func(path string, v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, c := range x {
					visit(path+"."+k, c)
				}
			case []any:
				for _, c := range x {
					visit(path, c)
				}
			case string:
				if name := n.find(x); name != "" {
					out = append(out, finding{rel, 0, name, strings.TrimPrefix(path, ".") + " = " + x})
				}
			}
		}
		visit("", tree)
	})
	return out, err
}

func scanDocs(root string, n *namer, dirs []string) ([]finding, error) {
	var out []finding
	for _, dir := range dirs {
		err := walk(root, dir, func(rel string, d fs.DirEntry) bool {
			return !d.IsDir() && !strings.HasSuffix(rel, ".md")
		}, func(rel string, raw []byte) {
			for i, line := range strings.Split(string(raw), "\n") {
				if m := n.words.FindStringSubmatch(line); m != nil {
					out = append(out, finding{rel, i + 1, m[2], strings.TrimSpace(line)})
				}
			}
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
