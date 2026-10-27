package chv

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// allowed lists the imports outside the standard library that the non-test
// files of this module may use. An entry ending in "/..." allows that path
// and every path below it.
var allowed = []string{
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox",
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent",
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage",
	"github.com/mudler/LocalAI/core/services/studio/workspace",
	"github.com/google/go-containerregistry/...",
	"github.com/Microsoft/hcsshim/ext4/tar2ext4",
}

func TestModuleImports(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
	out, err := exec.Command("go", "list", "-json", "./...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	n := 0
	for dec.More() {
		var p struct {
			ImportPath string
			Imports    []string
		}
		if err := dec.Decode(&p); err != nil {
			t.Fatal(err)
		}
		n++
		for _, imp := range p.Imports {
			if first, _, _ := strings.Cut(imp, "/"); !strings.Contains(first, ".") {
				continue // standard library
			}
			if !allowedImport(imp) {
				t.Errorf("%s imports %s, which is not allowed", p.ImportPath, imp)
			}
		}
	}
	if n == 0 {
		t.Fatal("no packages found")
	}
}

func allowedImport(p string) bool {
	for _, a := range allowed {
		if base, ok := strings.CutSuffix(a, "/..."); ok {
			if p == base || strings.HasPrefix(p, base+"/") {
				return true
			}
		} else if p == a {
			return true
		}
	}
	return false
}
