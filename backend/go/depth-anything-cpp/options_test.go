package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("optionValue",
	func(opts []string, key, want string) {
		Expect(optionValue(opts, key)).To(Equal(want))
	},
	Entry("present", []string{"foo:bar", "metric_model:m.gguf"}, "metric_model", "m.gguf"),
	Entry("absent", []string{"foo:bar"}, "metric_model", ""),
	Entry("nil", []string(nil), "metric_model", ""),
	Entry("trims space", []string{"metric_model:  m.gguf  "}, "metric_model", "m.gguf"),
	Entry("value with colon", []string{"metric_model:a:b.gguf"}, "metric_model", "a:b.gguf"),
	Entry("first wins", []string{"metric_model:first.gguf", "metric_model:second.gguf"}, "metric_model", "first.gguf"),
	Entry("empty value", []string{"metric_model:"}, "metric_model", ""),
	Entry("prefix not key", []string{"metric_model_extra:x"}, "metric_model", ""),
)
