package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseOptions", func() {
	It("defaults to quantiles 0.1, 0.5, 0.9 and the request limits", func() {
		o := parseOptions(nil)
		Expect(o.defaultQuantiles).To(Equal([]float64{0.1, 0.5, 0.9}))
		Expect(o.maxContext).To(BeZero())
		Expect(o.maxSeries).To(Equal(256))
		Expect(o.maxPoints).To(Equal(2_000_000))
	})

	It("reads every key", func() {
		o := parseOptions([]string{
			"default_quantiles: 0.25, 0.75", "max_context:512", "max_series:8", "max_total_points:1000", "unknown:x", "novalue",
		})
		Expect(o.defaultQuantiles).To(Equal([]float64{0.25, 0.75}))
		Expect(o.maxContext).To(Equal(int32(512)))
		Expect(o.maxSeries).To(Equal(8))
		Expect(o.maxPoints).To(Equal(1000))
	})

	It("keeps the default for malformed values", func() {
		o := parseOptions([]string{"default_quantiles:0.1,abc", "max_context:-1", "max_series:0", "max_total_points:x"})
		Expect(o.defaultQuantiles).To(Equal([]float64{0.1, 0.5, 0.9}))
		Expect(o.maxContext).To(BeZero())
		Expect(o.maxSeries).To(Equal(256))
		Expect(o.maxPoints).To(Equal(2_000_000))
	})

	It("rejects default levels outside (0,1)", func() {
		Expect(parseOptions([]string{"default_quantiles:0,0.5"}).defaultQuantiles).To(Equal([]float64{0.1, 0.5, 0.9}))
		Expect(parseOptions([]string{"default_quantiles:1"}).defaultQuantiles).To(Equal([]float64{0.1, 0.5, 0.9}))
	})
})
