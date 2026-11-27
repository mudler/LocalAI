package config_test

import (
	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the plugin settings", func() {
	It("has the defaults of the design", func() {
		o := &config.ApplicationConfig{}
		rs := o.ToRuntimeSettings()
		Expect(rs.PluginsUserInstalls).To(HaveValue(BeTrue()))
		Expect(rs.PluginsModelTiers).To(HaveValue(BeEmpty()))
		Expect(rs.PluginsHookTimeoutSeconds).To(HaveValue(Equal(60)))
		Expect(rs.PluginsMaxBytes).To(HaveValue(Equal(int64(20 << 20))))
	})

	It("takes values in range and refuses the others", func() {
		o := &config.ApplicationConfig{}
		off, timeout, max := false, 600, int64(50<<20)
		tiers := map[string]string{"opus": "qwen3-32b", "haiku": "small"}
		o.ApplyRuntimeSettings(&config.RuntimeSettings{
			PluginsUserInstalls: &off, PluginsHookTimeoutSeconds: &timeout, PluginsMaxBytes: &max, PluginsModelTiers: &tiers,
		})
		Expect(o.Plugins.DisableUserInstalls).To(BeTrue())
		Expect(o.EffectivePluginHookTimeoutSeconds()).To(Equal(600))
		Expect(o.EffectivePluginMaxBytes()).To(Equal(int64(50 << 20)))
		Expect(o.PluginModelTiers()).To(Equal(tiers))

		over, zero, tiny := 601, 0, int64(1024)
		badTiers := map[string]string{"gpt": "x"}
		emptyTier := map[string]string{"opus": " "}
		o.ApplyRuntimeSettings(&config.RuntimeSettings{PluginsHookTimeoutSeconds: &over})
		o.ApplyRuntimeSettings(&config.RuntimeSettings{PluginsHookTimeoutSeconds: &zero})
		o.ApplyRuntimeSettings(&config.RuntimeSettings{PluginsMaxBytes: &tiny})
		o.ApplyRuntimeSettings(&config.RuntimeSettings{PluginsModelTiers: &badTiers})
		o.ApplyRuntimeSettings(&config.RuntimeSettings{PluginsModelTiers: &emptyTier})
		Expect(o.EffectivePluginHookTimeoutSeconds()).To(Equal(600), "the hook timeout is at most 600 seconds")
		Expect(o.EffectivePluginMaxBytes()).To(Equal(int64(50 << 20)))
		Expect(o.PluginModelTiers()).To(Equal(tiers), "only haiku, sonnet and opus with a model")
	})

	It("does not share the tier map with the caller", func() {
		o := &config.ApplicationConfig{}
		tiers := map[string]string{"opus": "a"}
		o.ApplyRuntimeSettings(&config.RuntimeSettings{PluginsModelTiers: &tiers})
		tiers["opus"] = "b"
		got := o.PluginModelTiers()
		got["sonnet"] = "c"
		Expect(o.PluginModelTiers()).To(Equal(map[string]string{"opus": "a"}))
	})

	It("keeps the marketplace flag", func() {
		o := config.NewApplicationConfig(config.WithPluginMarketplaces("team=owner/repo"))
		Expect(o.Plugins.Marketplaces).To(Equal("team=owner/repo"))
	})
})
