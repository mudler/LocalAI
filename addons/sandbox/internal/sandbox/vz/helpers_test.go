package vz

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

// shortDir is a temporary folder that the test removes.
func shortDir() string {
	GinkgoHelper()
	d, err := os.MkdirTemp("", "vz")
	Expect(err).To(Succeed())
	DeferCleanup(func() { _ = os.RemoveAll(d) })
	return d
}
