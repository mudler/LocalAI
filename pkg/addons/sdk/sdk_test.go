// SPDX-License-Identifier: MIT
package sdk_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var sdkPackages = []string{"protocol", "hostclient", "idle", "atomicfile", "argutil"}

const modulePath = "github.com/mudler/LocalAI/"

// TestImportRule fails when a package below pkg/addons/sdk imports core/ or
// an internal path. Such an import would stop the package from being used by
// an add-on in another module.
func TestImportRule(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			rest := strings.TrimPrefix(p, modulePath)
			switch {
			case strings.HasPrefix(rest, "core/"), rest == "core":
				t.Errorf("%s imports %s: the SDK must not depend on core/", path, p)
			case strings.HasPrefix(rest, "internal/"), strings.Contains(p, "/internal/"), strings.HasSuffix(p, "/internal"):
				t.Errorf("%s imports %s: the SDK must not import an internal path", path, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestAPISurface compares the exported names of the SDK packages with the
// recorded list in testdata/api.txt. To accept an intended change, run
// UPDATE_SDK_API=1 go test ./pkg/addons/sdk and review the diff: removing or
// changing a name needs a new manifest api version.
func TestAPISurface(t *testing.T) {
	var lines []string
	for _, name := range sdkPackages {
		lines = append(lines, surface(t, name)...)
	}
	got := strings.Join(lines, "\n") + "\n"
	golden := filepath.Join("testdata", "api.txt")
	if os.Getenv("UPDATE_SDK_API") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read %s: %v (run with UPDATE_SDK_API=1 to create it)", golden, err)
	}
	if string(want) != got {
		t.Fatalf("the exported API of pkg/addons/sdk changed.\nIf it is an addition, run UPDATE_SDK_API=1 go test ./pkg/addons/sdk and commit testdata/api.txt.\nIf it removes or changes a name, it needs a new manifest api version.\n--- recorded\n%s--- current\n%s", want, got)
	}
}

func surface(t *testing.T, pkg string) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, pkg, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	add := func(format string, a ...any) {
		out = append(out, pkg+": "+fmt.Sprintf(format, a...))
	}
	for _, p := range pkgs {
		for _, f := range p.Files {
			for _, decl := range f.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if !d.Name.IsExported() {
						continue
					}
					if d.Recv != nil {
						recv := exprString(d.Recv.List[0].Type)
						if !ast.IsExported(strings.TrimPrefix(recv, "*")) {
							continue
						}
						add("method (%s) %s%s", recv, d.Name.Name, signature(d.Type))
						continue
					}
					add("func %s%s", d.Name.Name, signature(d.Type))
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if !s.Name.IsExported() {
								continue
							}
							add("type %s %s", s.Name.Name, typeKind(s))
							switch tt := s.Type.(type) {
							case *ast.StructType:
								for _, fld := range tt.Fields.List {
									for _, n := range fld.Names {
										if n.IsExported() {
											add("field %s.%s %s", s.Name.Name, n.Name, exprString(fld.Type))
										}
									}
								}
							case *ast.InterfaceType:
								for _, m := range tt.Methods.List {
									for _, n := range m.Names {
										if n.IsExported() {
											add("imethod %s.%s%s", s.Name.Name, n.Name, signature(m.Type.(*ast.FuncType)))
										}
									}
								}
							}
						case *ast.ValueSpec:
							kind := "var"
							if d.Tok == token.CONST {
								kind = "const"
							}
							for _, n := range s.Names {
								if n.IsExported() {
									add("%s %s", kind, n.Name)
								}
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func typeKind(s *ast.TypeSpec) string {
	switch tt := s.Type.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return "= " + exprString(tt)
	}
}

func signature(ft *ast.FuncType) string {
	return "(" + fieldList(ft.Params) + ")" + results(ft.Results)
}

func results(fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	if len(fl.List) == 1 && len(fl.List[0].Names) == 0 {
		return " " + exprString(fl.List[0].Type)
	}
	return " (" + fieldList(fl) + ")"
}

func fieldList(fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		// Parameter names are not part of the API; only the types are.
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			parts = append(parts, exprString(f.Type))
		}
	}
	return strings.Join(parts, ", ")
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return "*" + exprString(x.X)
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.ArrayType:
		if x.Len == nil {
			return "[]" + exprString(x.Elt)
		}
		return "[" + exprString(x.Len) + "]" + exprString(x.Elt)
	case *ast.MapType:
		return "map[" + exprString(x.Key) + "]" + exprString(x.Value)
	case *ast.Ellipsis:
		return "..." + exprString(x.Elt)
	case *ast.BasicLit:
		return x.Value
	case *ast.FuncType:
		return "func" + signature(x)
	case *ast.InterfaceType:
		return "interface{...}"
	case *ast.StructType:
		return "struct{...}"
	case *ast.ChanType:
		return "chan " + exprString(x.Value)
	case *ast.IndexExpr:
		return exprString(x.X) + "[" + exprString(x.Index) + "]"
	default:
		return fmt.Sprintf("%T", e)
	}
}
