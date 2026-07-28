package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

var _ = Describe("Worker registration body", func() {
	BeforeEach(func() {
		originalTotalAvailableVRAM := totalAvailableVRAM
		originalGetGPUAggregateInfo := getGPUAggregateInfo
		originalGetSystemRAMInfo := getSystemRAMInfo
		DeferCleanup(func() {
			totalAvailableVRAM = originalTotalAvailableVRAM
			getGPUAggregateInfo = originalGetGPUAggregateInfo
			getSystemRAMInfo = originalGetSystemRAMInfo
		})
	})

	It("includes the VRAM budget in the registration body when set", func() {
		cfg := &Config{VRAMBudget: "80%"}
		body := cfg.registrationBody()
		Expect(body["vram_budget"]).To(Equal("80%"))
	})

	It("omits an empty VRAM budget", func() {
		cfg := &Config{}
		body := cfg.registrationBody()
		_, present := body["vram_budget"]
		Expect(present).To(BeFalse())
	})

	It("reports raw total_vram regardless of the budget", func() {
		// The worker reports RAW VRAM; the server resolves and enforces the
		// budget. It must never pre-cap total_vram locally, so total_vram equals
		// the raw xsysinfo reading whether or not a budget is set.
		rawTotal, _ := xsysinfo.TotalAvailableVRAM()
		cfg := &Config{VRAMBudget: "50%"}
		body := cfg.registrationBody()
		Expect(body["total_vram"]).To(Equal(rawTotal))
	})

	It("reports RAM when GPU memory is visible", func() {
		totalAvailableVRAM = func() (uint64, error) {
			return 24_000, nil
		}
		getSystemRAMInfo = func() (*xsysinfo.SystemRAMInfo, error) {
			return &xsysinfo.SystemRAMInfo{Total: 64_000, Available: 48_000}, nil
		}

		body := (&Config{}).registrationBody()

		Expect(body["total_vram"]).To(Equal(uint64(24_000)))
		Expect(body["total_ram"]).To(Equal(uint64(64_000)))
		Expect(body["available_ram"]).To(Equal(uint64(48_000)))
	})

	It("reports current RAM in heartbeats when GPU memory is visible", func() {
		getGPUAggregateInfo = func() xsysinfo.GPUAggregateInfo {
			return xsysinfo.GPUAggregateInfo{TotalVRAM: 24_000, FreeVRAM: 12_000}
		}
		getSystemRAMInfo = func() (*xsysinfo.SystemRAMInfo, error) {
			return &xsysinfo.SystemRAMInfo{Total: 64_000, Available: 40_000}, nil
		}

		body := (&Config{}).heartbeatBody()

		Expect(body["available_vram"]).To(Equal(uint64(12_000)))
		Expect(body["available_ram"]).To(Equal(uint64(40_000)))
	})
})
