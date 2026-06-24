package xsysinfo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseComputeCap", func() {
	DescribeTable("splits major.minor",
		func(in string, maj, min int) {
			m, n := parseComputeCap(in)
			Expect(m).To(Equal(maj))
			Expect(n).To(Equal(min))
		},
		Entry("GB10 / DGX Spark", "12.1", 12, 1),
		Entry("RTX 50-series", "12.0", 12, 0),
		Entry("Hopper", "9.0", 9, 0),
		Entry("major only", "12", 12, 0),
		Entry("whitespace", " 12.1 ", 12, 1),
		Entry("empty", "", -1, -1),
		Entry("garbage", "abc", -1, -1),
	)
})
