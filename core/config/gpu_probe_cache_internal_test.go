package config

import (
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/vrambudget"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("gpuProbeCache", func() {
	It("probes once per TTL", func() {
		calls := 0
		c := &gpuProbeCache{ttl: time.Hour, probe: func() (string, uint64) {
			calls++
			return "nvidia", 16 << 30
		}}
		for range 100 {
			vendor, vram := c.get()
			Expect(vendor).To(Equal("nvidia"))
			Expect(vram).To(Equal(uint64(16 << 30)))
		}
		Expect(calls).To(Equal(1))

		c.at = time.Now().Add(-2 * time.Hour)
		c.get()
		Expect(calls).To(Equal(2))
	})

	It("applies the current VRAM budget to the cached figure", func() {
		orig := xsysinfo.DefaultVRAMBudget()
		DeferCleanup(func() { xsysinfo.SetDefaultVRAMBudget(orig) })

		xsysinfo.SetDefaultVRAMBudget(vrambudget.Budget{})
		Expect(budgetedVRAM(16 << 30)).To(Equal(uint64(16 << 30)))
		Expect(budgetedVRAM(0)).To(BeZero())

		b, err := vrambudget.Parse("8GiB")
		Expect(err).ToNot(HaveOccurred())
		xsysinfo.SetDefaultVRAMBudget(b)
		want, _ := b.Apply(16<<30, 16<<30)
		Expect(budgetedVRAM(16 << 30)).To(Equal(want))
		Expect(want).To(Equal(uint64(8 << 30)))
	})
})

// BenchmarkLocalGPU measures the GPU lookup SetDefaults does for every
// request: a probe each time, as before the cache, and the cached lookup.
func BenchmarkLocalGPU(b *testing.B) {
	b.Run("probe", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, v := probeLocalGPU()
			_ = budgetedVRAM(v)
		}
	})
	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = localGPU()
		}
	})
}
