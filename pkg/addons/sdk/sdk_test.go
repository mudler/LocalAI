// SPDX-License-Identifier: MIT
package sdk_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var sdkPackages = []string{"protocol", "hostclient", "idle", "atomicfile", "argutil"}

const (
	modulePath  = "github.com/mudler/LocalAI/"
	contractPkg = modulePath + "pkg/addons/contract"
)

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

// TestDependencyRule runs go list -deps, so a dependency that reaches core/ or
// an internal path through another pkg/ package fails too. Every package of
// this module that an SDK package needs must be below pkg/ and not internal.
func TestDependencyRule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the go command is not available")
	}
	args := []string{"list", "-deps", "-f", "{{.ImportPath}}"}
	for _, n := range sdkPackages {
		args = append(args, modulePath+"pkg/addons/sdk/"+n)
	}
	cmd := exec.Command("go", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, stderr.String())
	}
	for _, p := range strings.Fields(string(out)) {
		if !strings.HasPrefix(p, modulePath) {
			continue
		}
		rest := strings.TrimPrefix(p, modulePath)
		if !strings.HasPrefix(rest, "pkg/") || strings.Contains(p, "/internal/") || strings.HasSuffix(p, "/internal") {
			t.Errorf("an SDK package depends on %s: only packages below pkg/ that are not internal are allowed", p)
		}
	}
}

// TestAPISurface compares the exported API of the SDK packages, and of the
// contract types that their signatures expose, with testdata/api.txt. The
// record holds names, signatures, struct fields with their tags (the JSON wire
// names), embedded fields and interface members, constant values, variable
// types and type parameters. To accept an intended change, run
// UPDATE_SDK_API=1 go test ./pkg/addons/sdk and review the diff: removing or
// changing a name, a field or a tag needs a new manifest api version.
func TestAPISurface(t *testing.T) {
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "source", nil)
	var lines []string
	exposed := map[*types.TypeName]bool{}
	for _, name := range sdkPackages {
		pkg := checkPackage(t, fset, imp, name)
		lines = append(lines, surface(pkg, name, exposed)...)
	}
	lines = append(lines, contractSurface(exposed)...)
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
		t.Fatalf("the exported API of pkg/addons/sdk changed.\nIf it is an addition, run UPDATE_SDK_API=1 go test ./pkg/addons/sdk and commit testdata/api.txt.\nIf it removes or changes a name, a field or a tag, it needs a new manifest api version.\n--- recorded\n%s--- current\n%s", want, got)
	}
}

func checkPackage(t *testing.T, fset *token.FileSet, imp types.Importer, dir string) *types.Package {
	t.Helper()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, p := range pkgs {
		for _, f := range p.Files {
			files = append(files, f)
		}
	}
	sort.Slice(files, func(i, j int) bool { return fset.File(files[i].Pos()).Name() < fset.File(files[j].Pos()).Name() })
	conf := types.Config{Importer: imp}
	pkg, err := conf.Check(modulePath+"pkg/addons/sdk/"+dir, fset, files, nil)
	if err != nil {
		t.Fatalf("type check %s: %v", dir, err)
	}
	return pkg
}

// qualifier names a package by its last element, so the record does not hold
// the module path.
func qualifier(p *types.Package) string { return p.Name() }

func ts(t types.Type) string { return types.TypeString(t, qualifier) }

// surface lists the exported objects of one package. Contract named types that
// appear in a signature are added to exposed.
func surface(pkg *types.Package, name string, exposed map[*types.TypeName]bool) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, name+": "+fmt.Sprintf(format, a...)) }
	scope := pkg.Scope()
	for _, n := range scope.Names() {
		obj := scope.Lookup(n)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.Const:
			add("const %s %s = %s", o.Name(), ts(o.Type()), o.Val().ExactString())
			collect(o.Type(), exposed)
		case *types.Var:
			add("var %s %s", o.Name(), ts(o.Type()))
			collect(o.Type(), exposed)
		case *types.Func:
			add("func %s%s", o.Name(), ts(o.Type())[len("func"):])
			collect(o.Type(), exposed)
		case *types.TypeName:
			out = append(out, typeLines(name+": ", o, exposed)...)
		}
	}
	return out
}

// typeLines describes one named type: its kind and type parameters, its fields
// or interface members, and its exported methods.
func typeLines(prefix string, o *types.TypeName, exposed map[*types.TypeName]bool) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, prefix+fmt.Sprintf(format, a...)) }
	named, ok := o.Type().(*types.Named)
	if !ok { // an alias
		add("type %s = %s", o.Name(), ts(o.Type()))
		collect(o.Type(), exposed)
		return out
	}
	tp := ""
	if l := named.TypeParams(); l.Len() > 0 {
		var parts []string
		for i := 0; i < l.Len(); i++ {
			parts = append(parts, l.At(i).Obj().Name()+" "+ts(l.At(i).Constraint()))
		}
		tp = "[" + strings.Join(parts, ", ") + "]"
	}
	switch u := named.Underlying().(type) {
	case *types.Struct:
		add("type %s%s struct", o.Name(), tp)
		for i := 0; i < u.NumFields(); i++ {
			f := u.Field(i)
			if !f.Exported() {
				continue
			}
			kind := "field"
			if f.Embedded() {
				kind = "embedded"
			}
			tag := ""
			if u.Tag(i) != "" {
				tag = " " + strconv.Quote(u.Tag(i))
			}
			add("%s %s.%s %s%s", kind, o.Name(), f.Name(), ts(f.Type()), tag)
			collect(f.Type(), exposed)
		}
	case *types.Interface:
		add("type %s%s interface", o.Name(), tp)
		for i := 0; i < u.NumEmbeddeds(); i++ {
			add("embeds %s.%s", o.Name(), ts(u.EmbeddedType(i)))
			collect(u.EmbeddedType(i), exposed)
		}
		for i := 0; i < u.NumExplicitMethods(); i++ {
			m := u.ExplicitMethod(i)
			add("imethod %s.%s%s", o.Name(), m.Name(), ts(m.Type())[len("func"):])
			collect(m.Type(), exposed)
		}
	default:
		add("type %s%s %s", o.Name(), tp, ts(named.Underlying()))
		collect(named.Underlying(), exposed)
	}
	ms := types.NewMethodSet(types.NewPointer(named))
	for i := 0; i < ms.Len(); i++ {
		m := ms.At(i).Obj().(*types.Func)
		if !m.Exported() {
			continue
		}
		recv := "(" + o.Name() + ")"
		if _, ptr := m.Type().(*types.Signature).Recv().Type().(*types.Pointer); ptr {
			recv = "(*" + o.Name() + ")"
		}
		add("method %s %s%s", recv, m.Name(), ts(m.Type())[len("func"):])
		collect(m.Type(), exposed)
	}
	return out
}

// collect adds every named type of the contract package that t mentions.
func collect(t types.Type, exposed map[*types.TypeName]bool) {
	seen := map[types.Type]bool{}
	var walk func(types.Type)
	walk = func(t types.Type) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		switch x := t.(type) {
		case *types.Named:
			if o := x.Obj(); o.Pkg() != nil && o.Pkg().Path() == contractPkg {
				exposed[o] = true
			}
			if ta := x.TypeArgs(); ta != nil {
				for i := 0; i < ta.Len(); i++ {
					walk(ta.At(i))
				}
			}
		case *types.Pointer:
			walk(x.Elem())
		case *types.Slice:
			walk(x.Elem())
		case *types.Array:
			walk(x.Elem())
		case *types.Chan:
			walk(x.Elem())
		case *types.Map:
			walk(x.Key())
			walk(x.Elem())
		case *types.Signature:
			for _, tu := range []*types.Tuple{x.Params(), x.Results()} {
				for i := 0; i < tu.Len(); i++ {
					walk(tu.At(i).Type())
				}
			}
		case *types.Struct:
			for i := 0; i < x.NumFields(); i++ {
				walk(x.Field(i).Type())
			}
		}
	}
	walk(t)
}

// contractSurface describes the contract types that the SDK signatures expose,
// and every contract type that those types mention in turn.
func contractSurface(exposed map[*types.TypeName]bool) []string {
	var out []string
	done := map[*types.TypeName]bool{}
	for {
		var todo []*types.TypeName
		for o := range exposed {
			if !done[o] {
				todo = append(todo, o)
			}
		}
		if len(todo) == 0 {
			break
		}
		sort.Slice(todo, func(i, j int) bool { return todo[i].Name() < todo[j].Name() })
		for _, o := range todo {
			done[o] = true
			out = append(out, typeLines("contract: ", o, exposed)...)
		}
	}
	return out
}
