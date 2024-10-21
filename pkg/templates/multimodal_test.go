package templates_test

import (
	. "github.com/mudler/LocalAI/pkg/templates" // Update with your module path

	// Update with your module path
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("EvaluateTemplate", func() {
	Context("templating simple strings for multimodal chat", func() {
		It("should template messages correctly", func() {
			result, err := TemplateMultiModal("", MultiModalOptions{
				TotalImages:     1,
				TotalAudios:     0,
				TotalVideos:     0,
				ImagesInMessage: 1,
				AudiosInMessage: 0,
				VideosInMessage: 0,
			}, "bar")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("[img-0]bar"))
		})

		It("should handle messages with more images correctly", func() {
			result, err := TemplateMultiModal("", MultiModalOptions{
				TotalImages:     2,
				TotalAudios:     0,
				TotalVideos:     0,
				ImagesInMessage: 2,
				AudiosInMessage: 0,
				VideosInMessage: 0,
			}, "bar")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("[img-0][img-1]bar"))
		})
		It("should handle messages with more images correctly", func() {
			result, err := TemplateMultiModal("", MultiModalOptions{
				TotalImages:     4,
				TotalAudios:     1,
				TotalVideos:     0,
				ImagesInMessage: 2,
				AudiosInMessage: 1,
				VideosInMessage: 0,
			}, "bar")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("[audio-0][img-2][img-3]bar"))
		})
		It("should handle messages with more images correctly", func() {
			result, err := TemplateMultiModal("", MultiModalOptions{
				TotalImages:     3,
				TotalAudios:     1,
				TotalVideos:     0,
				ImagesInMessage: 1,
				AudiosInMessage: 1,
				VideosInMessage: 0,
			}, "bar")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("[audio-0][img-2]bar"))
		})
		It("should handle messages with more images correctly", func() {
			result, err := TemplateMultiModal("", MultiModalOptions{
				TotalImages:     0,
				TotalAudios:     0,
				TotalVideos:     0,
				ImagesInMessage: 0,
				AudiosInMessage: 0,
				VideosInMessage: 0,
			}, "bar")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("bar"))
		})
	})
	Context("templating with custom defaults", func() {
		It("should handle messages with more images correctly", func() {
			result, err := TemplateMultiModal("{{ range .Audio }}[audio-{{ add1 .ID}}]{{end}}{{ range .Images }}[img-{{ add1 .ID}}]{{end}}{{ range .Video }}[vid-{{ add1 .ID}}]{{end}}{{.Text}}", MultiModalOptions{
				TotalImages:     1,
				TotalAudios:     0,
				TotalVideos:     0,
				ImagesInMessage: 1,
				AudiosInMessage: 0,
				VideosInMessage: 0,
			}, "bar")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("[img-1]bar"))
		})
	})
})
