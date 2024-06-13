package oci_test

import (
	. "github.com/go-skynet/LocalAI/pkg/oci" // Update with your module path
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI", func() {
	Context("ollama", func() {
		FIt("pulls model files", func() {
			err := OllamaFetchModel("gemma:2b", "/tmp/foo", nil)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
