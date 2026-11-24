package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// The disk-headroom admission check has two operator surfaces that must not
// drift into two sources of truth: an env/CLI flag that sets the boot value,
// and a runtime setting that can override it live. Both write the SAME
// ApplicationConfig member, which the SmartRouter reads on every scheduling
// decision.
var _ = Describe("distributed disk-headroom check setting", func() {
	It("is enabled by default", func() {
		o := config.NewApplicationConfig()

		Expect(o.Distributed.DiskHeadroomDisabled).To(BeFalse(),
			"the check must default ON — it prevents a measured production failure")
		Expect(*o.ToRuntimeSettings().DistributedDiskHeadroomCheck).To(BeTrue())
	})

	It("reports the env/CLI value through the runtime settings snapshot", func() {
		o := config.NewApplicationConfig(config.DisableDiskHeadroomCheck)

		Expect(*o.ToRuntimeSettings().DistributedDiskHeadroomCheck).To(BeFalse())
	})

	It("lets a runtime override beat the env value, in both directions", func() {
		// Env said "off"; the operator turns it back on at runtime.
		o := config.NewApplicationConfig(config.DisableDiskHeadroomCheck)
		on := true
		o.ApplyRuntimeSettings(&config.RuntimeSettings{DistributedDiskHeadroomCheck: &on})
		Expect(o.Distributed.DiskHeadroomDisabled).To(BeFalse())

		// Env said "on" (the default); the operator turns it off at runtime.
		o = config.NewApplicationConfig()
		off := false
		o.ApplyRuntimeSettings(&config.RuntimeSettings{DistributedDiskHeadroomCheck: &off})
		Expect(o.Distributed.DiskHeadroomDisabled).To(BeTrue())
	})

	It("leaves the value alone when the runtime settings do not mention it", func() {
		o := config.NewApplicationConfig(config.DisableDiskHeadroomCheck)

		o.ApplyRuntimeSettings(&config.RuntimeSettings{})

		Expect(o.Distributed.DiskHeadroomDisabled).To(BeTrue())
	})
})
