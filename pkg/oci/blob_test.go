package oci_test

import (
	. "github.com/go-skynet/LocalAI/pkg/oci" // Update with your module path
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI", func() {
	Context("pulling images", func() {
		It("should fetch blobs correctly", func() {
			err := FetchImageBlob("registry.ollama.ai/library/gemma", "sha256:c1864a5eb19305c40519da12cc543519e48a0697ecd30e15d5ac228644957d12", "/tmp/foo", nil)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
