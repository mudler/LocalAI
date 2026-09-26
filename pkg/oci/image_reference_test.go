package oci

import (
	"github.com/google/go-containerregistry/pkg/name"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI image reference normalization", func() {
	DescribeTable("accepts gallery URIs and plain registry references",
		func(input, expected string) {
			normalized := normalizeImageReference(input)
			Expect(normalized).To(Equal(expected))
			_, err := name.ParseReference(normalized)
			Expect(err).NotTo(HaveOccurred())
		},
		Entry("gallery URI", "oci://registry0.example:5556/localai-backends:v1-vllm", "registry0.example:5556/localai-backends:v1-vllm"),
		Entry("plain reference", "quay.io/go-skynet/local-ai-backends:latest-cpu", "quay.io/go-skynet/local-ai-backends:latest-cpu"),
	)

	It("requires normalization before parsing a gallery URI", func() {
		_, err := name.ParseReference("oci://registry0.example:5556/x:y")
		Expect(err).To(HaveOccurred())
	})
})
