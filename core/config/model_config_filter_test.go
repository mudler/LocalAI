package config_test

import (
	. "github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BuildNameFilterFn", func() {
	It("returns a pass-all filter when the filter string is empty", func() {
		fn, err := BuildNameFilterFn("")
		Expect(err).ToNot(HaveOccurred())
		Expect(fn("any-name", nil)).To(BeTrue())
		Expect(fn("another-model", &ModelConfig{Name: "another-model"})).To(BeTrue())
	})

	It("returns an error for an invalid regex", func() {
		fn, err := BuildNameFilterFn("[invalid-regex")
		Expect(err).To(HaveOccurred())
		Expect(fn).To(BeNil())
	})

	It("matches only models whose name satisfies the regex", func() {
		fn, err := BuildNameFilterFn("llama")
		Expect(err).ToNot(HaveOccurred())

		Expect(fn("", &ModelConfig{Name: "llama-7b"})).To(BeTrue())
		Expect(fn("", &ModelConfig{Name: "my-llama-model"})).To(BeTrue())
		Expect(fn("", &ModelConfig{Name: "gpt-4"})).To(BeFalse())
	})
})
