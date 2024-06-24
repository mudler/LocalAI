package oci_test

import (
	"os"

	. "github.com/mudler/LocalAI/pkg/oci" // Update with your module path
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI", func() {
	Context("ollama", func() {
		It("pulls model files", func() {
			f, err := os.CreateTemp("", "ollama")
			Expect(err).NotTo(HaveOccurred())
			defer os.RemoveAll(f.Name())
			err = OllamaFetchModel("gemma:2b", f.Name(), nil)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
