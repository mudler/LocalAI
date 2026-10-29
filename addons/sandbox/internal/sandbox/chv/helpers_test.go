package chv

import (
	"os"
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func needTools(tools ...string) {
	GinkgoHelper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			Skip(tool + " not found")
		}
	}
}

// shortDir is a temporary folder with a short path, for Unix sockets.
func shortDir() string {
	GinkgoHelper()
	d, err := os.MkdirTemp("", "chv")
	Expect(err).To(Succeed())
	DeferCleanup(func() { os.RemoveAll(d) })
	return d
}
