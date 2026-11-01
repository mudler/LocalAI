package chv

import (
	"encoding/json"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// allowed lists the imports outside the standard library that the non-test
// files of this module may use. An entry ending in "/..." allows that path
// and every path below it.
var allowed = []string{
	"github.com/mudler/LocalAI/addons/internal/atomicfile",
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox",
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent",
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage",
	"github.com/mudler/LocalAI/core/services/studio/workspace",
	"github.com/google/go-containerregistry/...",
	"github.com/Microsoft/hcsshim/ext4/tar2ext4",
}

var _ = Describe("module imports", func() {
	It("uses only the allowed imports outside the standard library", func() {
		if _, err := exec.LookPath("go"); err != nil {
			Skip("go command not found")
		}
		out, err := exec.Command("go", "list", "-json", "./...").Output()
		Expect(err).To(Succeed(), "go list")
		dec := json.NewDecoder(strings.NewReader(string(out)))
		n := 0
		for dec.More() {
			var p struct {
				ImportPath string
				Imports    []string
			}
			Expect(dec.Decode(&p)).To(Succeed())
			n++
			for _, imp := range p.Imports {
				if first, _, _ := strings.Cut(imp, "/"); !strings.Contains(first, ".") {
					continue // standard library
				}
				Expect(allowedImport(imp)).To(BeTrue(), "%s imports %s, which is not allowed", p.ImportPath, imp)
			}
		}
		Expect(n).NotTo(BeZero(), "no packages found")
	})
})

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
