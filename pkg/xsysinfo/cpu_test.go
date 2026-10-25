package xsysinfo

import (
	"math"
	"runtime"

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

var _ = Describe("Default thread count", func() {
	It("keeps the physical core count when the runtime may use more CPUs", func() {
		Expect(capThreads(8, 20)).To(Equal(8))
		Expect(capThreads(8, 8)).To(Equal(8))
	})

	It("lowers the count to the CPUs the runtime may use", func() {
		Expect(capThreads(14, 2)).To(Equal(2))
		Expect(capThreads(14, 1)).To(Equal(1))
	})

	It("ignores a non-positive limit and never returns less than 1", func() {
		Expect(capThreads(8, 0)).To(Equal(8))
		Expect(capThreads(0, 0)).To(Equal(1))
		Expect(capThreads(0, 4)).To(Equal(1))
	})

	It("follows GOMAXPROCS", func() {
		prev := runtime.GOMAXPROCS(1)
		DeferCleanup(func() { runtime.GOMAXPROCS(prev) })

		Expect(DefaultThreads()).To(Equal(1))
	})
})
