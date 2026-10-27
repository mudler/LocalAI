// SPDX-License-Identifier: MIT
package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseAddonHostOps", func() {
	It("parses the default setting", func() {
		got, err := ParseAddonHostOps(DefaultAddonHostOps)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(map[string][]string{"fine-tuning": {"finetune."}, "quantization": {"quantize."}, "sandbox": {"workspace."}, "office": {"workspace."}}))
	})

	It("trims blanks and merges repeated names", func() {
		got, err := ParseAddonHostOps(" a-b = x. , a-b=y. ,c=z.")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(map[string][]string{"a-b": {"x.", "y."}, "c": {"z."}}))
	})

	It("names nobody for an empty setting", func() {
		got, err := ParseAddonHostOps("")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeEmpty())
	})

	DescribeTable("rejects a malformed setting",
		func(bad string) {
			_, err := ParseAddonHostOps(bad)
			Expect(err).To(HaveOccurred(), "%q: accepted", bad)
		},
		Entry("no operations", "fine-tuning"),
		Entry("no add-on name", "=finetune."),
		Entry("empty prefix", "fine-tuning="),
		Entry("prefix without a dot", "fine-tuning=finetune"),
		Entry("uppercase add-on name", "Fine=finetune."),
		Entry("uppercase prefix", "fine-tuning=Finetune."),
		Entry("slash in the name", "a/b=finetune."),
	)
})

var _ = Describe("WithAddonHostOps", func() {
	It("replaces the default", func() {
		o := &ApplicationConfig{}
		Expect(o.AddonHostOps).To(BeNil(), "the field starts unset")
		WithAddonHostOps(map[string][]string{"x": {"y."}})(o)
		Expect(o.AddonHostOps).To(HaveLen(1))
	})
})
