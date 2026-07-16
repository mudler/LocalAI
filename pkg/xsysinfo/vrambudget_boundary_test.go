package xsysinfo_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/vrambudget"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

var _ = Describe("Default VRAM budget", func() {
	AfterEach(func() {
		xsysinfo.SetDefaultVRAMBudget(vrambudget.Budget{}) // reset to no cap
	})

	It("defaults to unset", func() {
		Expect(xsysinfo.DefaultVRAMBudget().IsSet()).To(BeFalse())
	})

	It("stores and returns a set budget", func() {
		b, _ := vrambudget.Parse("80%")
		xsysinfo.SetDefaultVRAMBudget(b)
		Expect(xsysinfo.DefaultVRAMBudget().IsSet()).To(BeTrue())
		Expect(xsysinfo.DefaultVRAMBudget().String()).To(Equal("80%"))
	})
})
