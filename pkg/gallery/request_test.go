package gallery_test

import (
	. "github.com/go-skynet/LocalAI/pkg/gallery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Gallery API tests", func() {
	Context("requests", func() {
		It("parses github with a branch", func() {
			req := GalleryModel{URL: "github:go-skynet/model-gallery/gpt4all-j.yaml@main"}
			e, err := GetGalleryConfigFromURL(req.URL)
			Expect(err).ToNot(HaveOccurred())
			Expect(e.Name).To(Equal("gpt4all-j"))
		})
	})
})
