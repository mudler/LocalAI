package promptcachesvc_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pc "github.com/mudler/LocalAI/pkg/promptcache"
)

// networkStats is a filesystem that says it is a network one.
type networkStats struct{}

func (networkStats) Stat(string) (pc.FSStats, error) {
	return pc.FSStats{Free: 500 * gib, Total: 1000 * gib, Device: 1, FSType: "nfs", Network: true}, nil
}

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
		Expect(n.OffReason()).To(Equal(promptcachesvc.OffSwitchedOff))
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

var _ = Describe("the start checks of the root", func() {
	settings := config.DefaultPromptCacheConfig
	var root string

	BeforeEach(func() { root = filepath.Join(GinkgoT().TempDir(), "prompt-cache") })

	It("turns the tier off on a network filesystem and says why in the report", func() {
		n := promptcachesvc.NewNode(root, "", settings, promptcachesvc.WithStats(networkStats{}))
		Expect(n.CheckRoot()).To(Equal(promptcachesvc.OffNetworkDisk))
		Expect(n.Enabled()).To(BeFalse())
		expectNoTierEnv(n.BackendEnv())
		Expect(root).ToNot(BeADirectory(), "a refused root is never written")

		reports, err := promptcachesvc.Local{Node: n}.Usage(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(reports).To(HaveLen(1))
		Expect(reports[0].Enabled).To(BeFalse())
		Expect(reports[0].OffReason).To(Equal(promptcachesvc.OffNetworkDisk))

		rep, err := promptcachesvc.Report(context.Background(), promptcachesvc.Local{Node: n}, true, false, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(rep.Nodes[0].OffReason).To(Equal(promptcachesvc.OffNetworkDisk))

		res, err := n.Purge(context.Background(), promptcachesvc.Scope{All: true})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(BeZero())
	})

	It("turns the tier off when the root cannot be created", func() {
		file := filepath.Join(GinkgoT().TempDir(), "a-file")
		Expect(os.WriteFile(file, []byte("x"), 0o600)).To(Succeed())
		n := promptcachesvc.NewNode(filepath.Join(file, "prompt-cache"), "", settings,
			promptcachesvc.WithStats(fixedStats{free: 500 * gib, total: 1000 * gib}))
		Expect(n.CheckRoot()).To(Equal(promptcachesvc.OffNotWritable))
		Expect(n.OffReason()).To(Equal(promptcachesvc.OffNotWritable))
		expectNoTierEnv(n.BackendEnv())
	})

	It("turns the tier off when the write check fails", func() {
		n := promptcachesvc.NewNode(root, "", settings,
			promptcachesvc.WithStats(fixedStats{free: 500 * gib, total: 1000 * gib}),
			promptcachesvc.WithWritableCheck(func(string) error { return errors.New("read-only file system") }))
		Expect(n.CheckRoot()).To(Equal(promptcachesvc.OffNotWritable))
		Expect(n.Enabled()).To(BeFalse())
	})

	It("keeps a local, writable root and creates it", func() {
		n := promptcachesvc.NewNode(root, "", settings,
			promptcachesvc.WithStats(fixedStats{free: 500 * gib, total: 1000 * gib}))
		Expect(n.CheckRoot()).To(BeEmpty())
		Expect(n.Enabled()).To(BeTrue())
		Expect(root).To(BeADirectory())
		entries, err := os.ReadDir(root)
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(BeEmpty(), "the write check leaves nothing behind")
	})

	It("sends the reason of a worker to the frontend", func() {
		w := promptcachesvc.NewWorker(false, "inc-1")
		n := promptcachesvc.NewNode(root, "", w.Settings, promptcachesvc.WithStats(networkStats{}))
		n.CheckRoot()
		w.Attach(n)
		reply := w.Usage(context.Background())
		Expect(reply.Enabled).To(BeFalse())
		Expect(reply.OffReason).To(Equal(promptcachesvc.OffNetworkDisk))

		w2 := promptcachesvc.NewWorker(false, "inc-2")
		w2.Attach(promptcachesvc.NewNode("", "", w2.Settings))
		Expect(w2.Usage(context.Background()).OffReason).To(Equal(promptcachesvc.OffNoPath))
	})
})

// oneWorker sends the verbs straight to one Worker half.
type oneWorker struct{ w *promptcachesvc.Worker }

func (o oneWorker) PromptCacheSync(ctx context.Context, _ string, req workerctl.PromptCacheSyncRequest, _ time.Duration) (*workerctl.PromptCacheSyncReply, error) {
	r := o.w.Sync(ctx, req)
	return &r, nil
}

func (o oneWorker) PromptCacheUsage(ctx context.Context, _ string, _ time.Duration) (*workerctl.PromptCacheUsageReply, error) {
	r := o.w.Usage(ctx)
	return &r, nil
}

var _ = Describe("the reason a worker is off, in the cluster report", func() {
	It("reaches the report of the frontend", func() {
		root := filepath.Join(GinkgoT().TempDir(), "prompt-cache")
		w := promptcachesvc.NewWorker(false, "inc-1")
		n := promptcachesvc.NewNode(root, "", w.Settings, promptcachesvc.WithStats(networkStats{}))
		n.CheckRoot()
		w.Attach(n)
		d := &promptcachesvc.Distributed{
			Sender: oneWorker{w},
			Peers: func(context.Context) ([]promptcachesvc.Peer, error) {
				return []promptcachesvc.Peer{{ID: "n1", Name: "gpu-1", Online: true}}, nil
			},
		}
		rep, err := promptcachesvc.Report(context.Background(), d, true, false, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(rep.Nodes).To(HaveLen(1))
		Expect(rep.Nodes[0].Enabled).To(BeFalse())
		Expect(rep.Nodes[0].OffReason).To(Equal(promptcachesvc.OffNetworkDisk))
	})
})
