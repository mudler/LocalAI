package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

var _ = Describe("Worker registration body", func() {
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
})
