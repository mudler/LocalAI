package ociimage

import (
	"encoding/json"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Module imports", func() {
	// The module may import the standard library and go-containerregistry only.
	// It must not import the SDK, so that a backend can use it without the root
	// module.
	It("imports only the standard library and go-containerregistry", func() {
		if _, err := exec.LookPath("go"); err != nil {
			Skip("go command not found")
		}
		out, err := exec.Command("go", "list", "-json", "./...").Output()
		Expect(err).NotTo(HaveOccurred(), "go list")
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
				if imp != "github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage" &&
					!strings.HasPrefix(imp, "github.com/google/go-containerregistry/") {
					Fail(p.ImportPath + " imports " + imp + ", which is not allowed")
				}
			}
		}
		Expect(n).NotTo(BeZero(), "no packages found")
	})
})
