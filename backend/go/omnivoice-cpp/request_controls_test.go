// SPDX-License-Identifier: MIT
package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"math"
	"strconv"
)

func durationString(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

var invalidDurations = []string{"", " ", " 1", "1 ", "bad", "0", "-0", "-1", "NaN", "Inf", "+Inf", "-Inf", "1e309", "1e300", "2147483647", "85899346", "1e-999", durationString(math.Nextafter(30, math.Inf(1)))}
var invalidPostprocess = []string{"", "TRUE", "False", "True", "FALSE", " true", "false ", "1", "0", "yes", "no", "garbage"}

var _ = Describe("request controls", func() {
	It("preserves omission and ignores unrelated parameters", func() {
		for _, p := range []map[string]string{nil, {}, {"unrelated": "value"}} {
			for _, stream := range []bool{false, true} {
				c, err := parseRequestControls(p, stream)
				Expect(err).NotTo(HaveOccurred())
				Expect(c).To(Equal(requestControls{postprocess: -1}))
			}
		}
	})
	It("quantizes finite positive durations with float64 arithmetic", func() {
		cases := map[string]int32{"1": 25, "2.5": 62, "0.04": 1, "30": 750, "5e-324": 1, "0x1p+0": 25, "+1": 25}
		for _, v := range []float64{math.Nextafter(.08, 0), .08, math.Nextafter(.08, 1), math.Nextafter(1, 0), math.Nextafter(1, 2), math.Nextafter(30, 0)} {
			cases[durationString(v)] = int32(math.Max(1, math.Floor(v*25)))
		}
		for value, frames := range cases {
			for _, stream := range []bool{false, true} {
				c, err := parseRequestControls(map[string]string{"duration": value}, stream)
				Expect(err).NotTo(HaveOccurred(), value)
				Expect(c.durationFrames).To(Equal(frames), value)
				Expect(c.postprocess).To(Equal(int32(-1)))
			}
		}
	})
	It("rejects invalid duration and exact-boolean violations", func() {
		for _, stream := range []bool{false, true} {
			for _, value := range invalidDurations {
				_, err := parseRequestControls(map[string]string{"duration": value}, stream)
				Expect(err).To(HaveOccurred(), value)
			}
			for _, value := range invalidPostprocess {
				_, err := parseRequestControls(map[string]string{"postprocess": value}, stream)
				Expect(err).To(HaveOccurred(), value)
			}
		}
	})
	It("distinguishes omission, true, and false", func() {
		for _, value := range []string{"true", "false"} {
			c, err := parseRequestControls(map[string]string{"postprocess": value}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.postprocess).To(Equal(int32(boolToInt(value == "true"))))
			_, err = parseRequestControls(map[string]string{"postprocess": value}, true)
			if value == "false" {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		}
	})
})
