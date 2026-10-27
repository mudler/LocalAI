package ociimage

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// The module may import the standard library and go-containerregistry only.
// It must not import the SDK, so that a backend can use it without the root
// module.
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
			if imp != "github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage" &&
				!strings.HasPrefix(imp, "github.com/google/go-containerregistry/") {
				t.Errorf("%s imports %s, which is not allowed", p.ImportPath, imp)
			}
		}
	}
	if n == 0 {
		t.Fatal("no packages found")
	}
}
