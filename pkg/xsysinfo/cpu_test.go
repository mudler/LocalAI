package xsysinfo

import (
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CPU telemetry", func() {
	It("reports logical cores, utilization, and one-minute load", func() {
		info, err := GetCPUInfo()

		Expect(err).ToNot(HaveOccurred())
		Expect(info.LogicalCores).To(BeNumerically(">", 0))
		Expect(info.UsagePercent).To(BeNumerically(">=", 0))
		Expect(info.UsagePercent).To(BeNumerically("<=", 100))
		Expect(math.IsNaN(info.Load1)).To(BeFalse())
	})
})
