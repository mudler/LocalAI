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
			result, err := TemplateMultiModal("[img-{{.ID}}]{{.Text}}", 1, "bar")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("[img-1]bar"))
		})
	})
})
