package promptcachesvc_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

func expectNoTierEnv(env map[string]string) {
	Expect(env).To(HaveLen(len(config.PromptCacheEnvKeys)))
	for _, k := range config.PromptCacheEnvKeys {
		Expect(env).To(HaveKeyWithValue(k, ""), k)
	}
}

var _ = Describe("the environment of the backends of a node", func() {
	var root string
	settings := config.DefaultPromptCacheConfig()
	get := func() config.PromptCacheConfig { return settings }

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "prompt-cache")
		settings = config.DefaultPromptCacheConfig()
	})

	It("carries the whole policy of the node from the live settings", func() {
		settings.TTL = 90 * time.Minute
		settings.MinTokens = 2048
		settings.MaxSizeMiB = 4096
		settings.LowFreeMiB = 300
		settings.CriticalFreeMiB = 100
		n := promptcachesvc.NewNode(root, "", get,
			promptcachesvc.WithStats(fixedStats{free: 500 * gib, total: 1000 * gib}),
			promptcachesvc.WithUsers(func() (bool, int) { return true, 4 }))
		env := n.BackendEnv()
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheRoot, root))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheTTLHours, "2"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheMinTokens, "2048"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheMaxSizeMiB, "4096"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheQuotaMiB, "1024"), "the automatic quota: a quarter of the cap")
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheLowFreeMiB, "300"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheCriticalFreeMiB, "100"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheMinReuse, "2"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheMaxCheckpoints, "3"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheLoadOverheadMs, "50"))

		By("following a change of the settings")
		settings.UserQuotaMiB = -1
		settings.LowFreeMiB = 0
		env = n.BackendEnv()
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheQuotaMiB, "0"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheLowFreeMiB, "0"))
	})

	It("removes every key when the tier is off", func() {
		settings.DiskDisabled = true
		n := promptcachesvc.NewNode(root, "", get)
		expectNoTierEnv(n.BackendEnv())
		expectNoTierEnv(promptcachesvc.NewNode("", "", get).BackendEnv())
	})

	It("gives a worker its defaults before the first sync and the cluster settings after", func() {
		w := promptcachesvc.NewWorker(false, "inc-1")
		n := promptcachesvc.NewNode(root, "", w.Settings, promptcachesvc.WithUsers(w.Users),
			promptcachesvc.WithStats(fixedStats{free: 500 * gib, total: 1000 * gib}))
		w.Attach(n)
		env := n.BackendEnv()
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheTTLHours, "168"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheMinTokens, "1024"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheMaxSizeMiB, "20480"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheQuotaMiB, "0"))

		s := promptcachesvc.WireSettings(config.PromptCacheConfig{
			TTL: 3 * time.Hour, MinTokens: 512, MaxSizeMiB: 1000, CriticalFreeMiB: 64,
		}, true, 2)
		Expect(w.Sync(context.Background(), workerctl.PromptCacheSyncRequest{Settings: &s}).Success).To(BeTrue())
		env = n.BackendEnv()
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheTTLHours, "3"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheMinTokens, "512"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheMaxSizeMiB, "1000"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheQuotaMiB, "500"))
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheCriticalFreeMiB, "64"))

		off := s
		off.Disabled = true
		w.Sync(context.Background(), workerctl.PromptCacheSyncRequest{Settings: &off})
		expectNoTierEnv(n.BackendEnv())
	})
})
