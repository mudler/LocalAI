package config_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

var _ = Describe("the root of the disk prompt cache", func() {
	local := func(string) bool { return true }
	remote := func(string) bool { return false }

	DescribeTable("is chosen in order",
		func(explicit, dataPath, derived string, derivedLocal func(string) bool, root, source string) {
			r, s := config.ResolvePromptCacheRoot(explicit, dataPath, derived, derivedLocal)
			Expect(r).To(Equal(root))
			Expect(s).To(Equal(source))
		},
		Entry("the explicit path wins over everything", "/fast/pc/", "/data", "/models/../data/prompt-cache", local,
			"/fast/pc", config.PromptCacheRootExplicit),
		Entry("then the data path", "", "/data", "/models/../data/prompt-cache", local,
			"/data/prompt-cache", config.PromptCacheRootDataPath),
		Entry("a data path of - is none", "", "-", "", local, "", ""),
		Entry("then the derived root when it is local", "", "", "/srv/models/../data/prompt-cache", local,
			"/srv/data/prompt-cache", config.PromptCacheRootDerived),
		Entry("but not when it is on a network filesystem", "", "", "/srv/models/../data/prompt-cache", remote, "", ""),
		Entry("and not without a check", "", "", "/srv/models/../data/prompt-cache", nil, "", ""),
		Entry("nothing at all turns the tier off", "", "", "", local, "", ""),
	)

	It("lets --prompt-cache-path win over the data path of a frontend", func() {
		o := config.NewApplicationConfig(config.WithDataPath("/srv/data"), config.WithPromptCachePath("/local/pc"))
		Expect(o.PromptCacheRoot()).To(Equal("/local/pc"))
		o = config.NewApplicationConfig(config.WithPromptCachePath("/local/pc"))
		Expect(o.PromptCacheRoot()).To(Equal("/local/pc"), "a frontend with no data path can still have a root")
	})

	It("rounds the time to live up to whole hours", func() {
		Expect(config.PromptCacheTTLHours(90 * time.Minute)).To(Equal(2))
		Expect(config.PromptCacheTTLHours(0)).To(Equal(168))
	})
})
