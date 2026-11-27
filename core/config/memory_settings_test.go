package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

var _ = Describe("memory settings split", func() {
	It("puts the memory keys on one side and the rest on the other", func() {
		swap, three, threads := false, 3, 8
		mem, rest := config.SplitMemorySettings(config.RuntimeSettings{
			ModelSwap: &swap, MaxActiveBackends: &three, Threads: &threads,
		})

		Expect(mem.SetKeys()).To(ConsistOf("max_active_backends", "model_swap"))
		Expect(rest.SetKeys()).To(ConsistOf("threads"))
		Expect(config.RuntimeSettings{}.IsEmpty()).To(BeTrue())
		Expect(mem.IsEmpty()).To(BeFalse())
	})

	It("puts the disk prompt cache settings with the cluster settings", func() {
		off, ttl, quota := false, "2h", -1
		mem, rest := config.SplitMemorySettings(config.RuntimeSettings{
			PromptCacheDisk: &off, PromptCacheDiskTTL: &ttl, PromptCacheDiskUserQuotaMiB: &quota,
		})
		Expect(mem.SetKeys()).To(ConsistOf("prompt_cache_disk", "prompt_cache_disk_ttl", "prompt_cache_disk_user_quota_mib"))
		Expect(rest.IsEmpty()).To(BeTrue())
		Expect(config.MemorySettingKeys).To(ContainElements(config.PromptCacheSettingKeys))
	})

	It("names only real settings", func() {
		all := config.NewApplicationConfig().ToRuntimeSettings()
		Expect(all.SetKeys()).To(ContainElements(config.MemorySettingKeys))
	})

	It("reports the memory settings that env set", func() {
		// run.go always passes the reclaimer threshold, like the baseline.
		o := config.NewApplicationConfig(config.WithMemoryReclaimer(false, 0.95), config.DisableModelSwap, config.SetMaxActiveBackends(2))

		env := config.EnvMemorySettings(o)

		Expect(env.SetKeys()).To(ConsistOf("max_active_backends", "single_backend", "model_swap"))
		Expect(*env.MaxActiveBackends).To(Equal(2))
		Expect(config.EnvMemorySettings(config.DefaultRuntimeBaseline()).IsEmpty()).To(BeTrue())
	})
})

var _ = Describe("memory settings validation", func() {
	It("refuses what the server would ignore", func() {
		bad, neg, high := "soon", -1, 1.5
		Expect(config.ValidateMemorySettings(config.RuntimeSettings{WatchdogIdleTimeout: &bad})).ToNot(Succeed())
		Expect(config.ValidateMemorySettings(config.RuntimeSettings{MaxActiveBackends: &neg})).ToNot(Succeed())
		Expect(config.ValidateMemorySettings(config.RuntimeSettings{MemoryReclaimerThreshold: &high})).ToNot(Succeed())
		Expect(config.ValidateMemorySettings(config.RuntimeSettings{VRAMBudget: &bad})).ToNot(Succeed())
		zero := 0
		Expect(config.ValidateMemorySettings(config.RuntimeSettings{PromptCacheDiskTTL: &bad})).ToNot(Succeed())
		Expect(config.ValidateMemorySettings(config.RuntimeSettings{PromptCacheDiskMaxSizeMiB: &neg})).ToNot(Succeed())
		Expect(config.ValidateMemorySettings(config.RuntimeSettings{PromptCacheDiskMinTokens: &zero})).ToNot(Succeed())
		Expect(config.ValidateMemorySettings(config.RuntimeSettings{PromptCacheDiskUserQuotaMiB: &neg})).To(Succeed(), "-1 is no quota")
		ok, empty := "10m", ""
		Expect(config.ValidateMemorySettings(config.RuntimeSettings{WatchdogIdleTimeout: &ok, VRAMBudget: &empty})).To(Succeed())
	})
})
