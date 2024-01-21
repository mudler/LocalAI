package downloader_test

import (
	. "github.com/go-skynet/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Gallery API tests", func() {
	Context("URI", func() {
		It("parses github with a branch", func() {
			Expect(
				GetURI("github:go-skynet/model-gallery/gpt4all-j.yaml", func(url string, i []byte) error {
					Expect(url).To(Equal("https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml"))
					return nil
				}),
			).ToNot(HaveOccurred())
		})
		It("parses github without a branch", func() {
			Expect(
				GetURI("github:go-skynet/model-gallery/gpt4all-j.yaml@main", func(url string, i []byte) error {
					Expect(url).To(Equal("https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml"))
					return nil
				}),
			).ToNot(HaveOccurred())
		})
		It("parses github with urls", func() {
			Expect(
				GetURI("https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml", func(url string, i []byte) error {
					Expect(url).To(Equal("https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml"))
					return nil
				}),
			).ToNot(HaveOccurred())
		})
	})
})
