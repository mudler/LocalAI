package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// model_swap lets a load unload another model to make room. It is on by
// default; env/CLI and the runtime setting write the same member.
var _ = Describe("model swap setting", func() {
	It("is on by default", func() {
		o := config.NewApplicationConfig()

		Expect(o.ModelSwapEnabled()).To(BeTrue())
		Expect(*o.ToRuntimeSettings().ModelSwap).To(BeTrue())
	})

	It("reports the env/CLI value through the runtime settings snapshot", func() {
		o := config.NewApplicationConfig(config.DisableModelSwap)

		Expect(o.ModelSwapEnabled()).To(BeFalse())
		Expect(*o.ToRuntimeSettings().ModelSwap).To(BeFalse())
	})

	It("is changed by the runtime setting in both directions", func() {
		o := config.NewApplicationConfig()
		off := false
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ModelSwap: &off})
		Expect(o.ModelSwapEnabled()).To(BeFalse())

		on := true
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ModelSwap: &on})
		Expect(o.ModelSwapEnabled()).To(BeTrue())
	})

	It("lets env win over the persisted file at startup in single mode", func() {
		o := config.NewApplicationConfig(config.DisableModelSwap)
		on := true
		o.ApplyRuntimeSettingsAtStartup(&config.RuntimeSettings{ModelSwap: &on})

		Expect(o.ModelSwapEnabled()).To(BeFalse())
	})
})
