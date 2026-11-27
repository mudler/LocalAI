package config_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

var _ = Describe("disk prompt cache settings", func() {
	It("has the documented defaults", func() {
		o := config.NewApplicationConfig()
		s := o.ToRuntimeSettings()
		Expect(*s.PromptCacheDisk).To(BeTrue())
		Expect(*s.PromptCacheDiskMaxSizeMiB).To(BeZero())
		Expect(*s.PromptCacheDiskTTL).To(Equal("168h0m0s"))
		Expect(*s.PromptCacheDiskUserQuotaMiB).To(BeZero())
		Expect(*s.PromptCacheDiskMinTokens).To(Equal(1024))
		Expect(*s.PromptCacheDiskShared).To(BeFalse())
	})

	It("reports the env/CLI values through the runtime settings snapshot", func() {
		o := config.NewApplicationConfig(
			config.WithPromptCacheDisk(false),
			config.WithPromptCacheDiskMaxSizeMiB(512),
			config.WithPromptCacheDiskTTL(time.Hour),
			config.WithPromptCacheDiskUserQuotaMiB(-1),
			config.WithPromptCacheDiskMinTokens(2048),
			config.WithPromptCacheDiskShared(true),
		)
		s := o.ToRuntimeSettings()
		Expect(*s.PromptCacheDisk).To(BeFalse())
		Expect(*s.PromptCacheDiskMaxSizeMiB).To(Equal(512))
		Expect(*s.PromptCacheDiskTTL).To(Equal("1h0m0s"))
		Expect(*s.PromptCacheDiskUserQuotaMiB).To(Equal(-1))
		Expect(*s.PromptCacheDiskMinTokens).To(Equal(2048))
		Expect(*s.PromptCacheDiskShared).To(BeTrue())
	})

	It("applies runtime values and refuses bad ones", func() {
		o := config.NewApplicationConfig()
		off, ttl, bad, minTok, neg := false, "2h", "soon", 0, -5
		o.ApplyRuntimeSettings(&config.RuntimeSettings{PromptCacheDisk: &off, PromptCacheDiskTTL: &ttl})
		Expect(o.PromptCache.DiskDisabled).To(BeTrue())
		Expect(o.PromptCache.TTL).To(Equal(2 * time.Hour))

		o.ApplyRuntimeSettings(&config.RuntimeSettings{
			PromptCacheDiskTTL: &bad, PromptCacheDiskMinTokens: &minTok, PromptCacheDiskMaxSizeMiB: &neg,
		})
		Expect(o.PromptCache.TTL).To(Equal(2 * time.Hour))
		Expect(o.PromptCache.MinTokens).To(Equal(1024))
		Expect(o.PromptCache.MaxSizeMiB).To(BeZero())
	})

	It("puts the root under the data path, and has none without one", func() {
		Expect(config.NewApplicationConfig().PromptCacheRoot()).To(BeEmpty())
		o := config.NewApplicationConfig(config.WithDataPath("/srv/data"))
		Expect(o.PromptCacheRoot()).To(Equal("/srv/data/prompt-cache"))
	})

	It("sizes the cap from the free disk, at most 20 GiB", func() {
		Expect(config.PromptCacheMaxBytes(100, 0)).To(Equal(int64(100) << 20))
		Expect(config.PromptCacheMaxBytes(0, 50<<30)).To(Equal(int64(5) << 30))
		Expect(config.PromptCacheMaxBytes(0, 1<<40)).To(Equal(int64(20) << 30))
	})

	It("shares the cap among users only when auth has more than one", func() {
		const capBytes = int64(40) << 30
		Expect(config.PromptCacheQuotaBytes(0, capBytes, true, 4)).To(Equal(int64(10) << 30))
		Expect(config.PromptCacheQuotaBytes(0, capBytes, true, 1)).To(BeZero())
		Expect(config.PromptCacheQuotaBytes(0, capBytes, false, 4)).To(BeZero())
		Expect(config.PromptCacheQuotaBytes(-1, capBytes, true, 4)).To(BeZero())
		Expect(config.PromptCacheQuotaBytes(64, capBytes, true, 4)).To(Equal(int64(64) << 20))
	})
})

var _ = Describe("disk prompt cache model options", func() {
	pc := config.DefaultPromptCacheConfig()

	It("adds the global values to a llama.cpp model that sets none", func() {
		out := config.WithPromptCacheDiskOptions([]string{"cache_ram:4096"}, "llama-cpp", pc)
		Expect(out).To(ConsistOf("cache_ram:4096", "prompt_cache_disk:true",
			"prompt_cache_disk_ttl_hours:168", "prompt_cache_disk_min_tokens:1024"))
	})

	It("keeps every option the model sets, so prompt_cache_disk:false wins", func() {
		in := []string{"prompt_cache_disk:false", "prompt_cache_disk_ttl_hours:2"}
		out := config.WithPromptCacheDiskOptions(in, "", pc)
		Expect(out).To(ContainElements("prompt_cache_disk:false", "prompt_cache_disk_ttl_hours:2"))
		Expect(out).ToNot(ContainElement("prompt_cache_disk:true"))
		Expect(in).To(HaveLen(2), "the input slice is not changed")
	})

	It("passes the global off switch and an explicit quota", func() {
		g := pc
		g.DiskDisabled = true
		g.UserQuotaMiB = 300
		out := config.WithPromptCacheDiskOptions(nil, "llama-cpp", g)
		Expect(out).To(ContainElements("prompt_cache_disk:false", "prompt_cache_disk_quota_mib:300"))
	})

	It("leaves other backends alone", func() {
		Expect(config.WithPromptCacheDiskOptions([]string{"a:b"}, "vllm", pc)).To(Equal([]string{"a:b"}))
	})

	It("reads the shared switch and the model time to live", func() {
		Expect(config.PromptCacheShared([]string{"prompt_cache_disk_shared:true"}, pc)).To(BeTrue())
		Expect(config.PromptCacheShared([]string{"prompt_cache_disk_shared:false"}, pc)).To(BeFalse())
		g := pc
		g.Shared = true
		Expect(config.PromptCacheShared(nil, g)).To(BeTrue())
		d, ok := config.PromptCacheModelTTL([]string{"prompt_cache_disk_ttl_hours:12"})
		Expect(ok).To(BeTrue())
		Expect(d).To(Equal(12 * time.Hour))
		_, ok = config.PromptCacheModelTTL(nil)
		Expect(ok).To(BeFalse())
	})
})
