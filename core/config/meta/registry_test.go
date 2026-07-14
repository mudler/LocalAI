package meta_test

import (
	"github.com/mudler/LocalAI/core/config/meta"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("alias field metadata", func() {
	It("registers the alias field as a model-select in the alias section", func() {
		reg := meta.DefaultRegistry()
		f, ok := reg["alias"]
		Expect(ok).To(BeTrue(), "alias field should have a registry override")
		Expect(f.Section).To(Equal("alias"))
		Expect(f.Component).To(Equal("model-select"))
	})

	It("defines an alias section", func() {
		var found bool
		for _, s := range meta.DefaultSections() {
			if s.ID == "alias" {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "DefaultSections should include an alias section")
	})
})
