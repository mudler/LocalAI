package gallery

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("developmentURI", func() {
	const latest, master = "latest", "master"

	It("rewrites a released image to its branch (development) image", func() {
		got, ok := developmentURI("quay.io/go-skynet/local-ai-backends:latest-metal-darwin-arm64-llama-cpp", latest, master)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal("quay.io/go-skynet/local-ai-backends:master-metal-darwin-arm64-llama-cpp"))
	})

	It("leaves an image already on the branch tag untouched", func() {
		_, ok := developmentURI("quay.io/go-skynet/local-ai-backends:master-metal-darwin-arm64-llama-cpp", latest, master)
		Expect(ok).To(BeFalse())
	})

	It("returns ok=false when there is no released tag to swap", func() {
		_, ok := developmentURI("oci://localhost/custom-backend:edge", latest, master)
		Expect(ok).To(BeFalse())
	})
})
