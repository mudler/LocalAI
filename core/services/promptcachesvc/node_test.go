package promptcachesvc_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/promptcache"
	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	pc "github.com/mudler/LocalAI/pkg/promptcache"
)

func TestPromptCacheSvc(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Prompt cache node test suite")
}

type fixedStats struct{ free, total uint64 }

func (f fixedStats) Stat(string) (pc.FSStats, error) {
	return pc.FSStats{Free: f.free, Total: f.total, Device: 1}, nil
}

const gib = uint64(1) << 30

func writeEntry(root, model, ns string) string {
	dir := filepath.Join(root, "llama-cpp", "1111111111111111-"+promptcache.ModelID(model))
	Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
	p := filepath.Join(dir, "cccccccccccccccc-"+promptcache.NSID(promptcache.Salt(ns))+"-"+strings.Repeat("a", 32)+".cache")
	Expect(os.WriteFile(p, []byte("kv"), 0o600)).To(Succeed())
	return p
}

var _ = Describe("the prompt cache node", func() {
	var root string
	settings := config.DefaultPromptCacheConfig()
	get := func() config.PromptCacheConfig { return settings }

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "prompt-cache")
		settings = config.DefaultPromptCacheConfig()
	})

	It("gives backends the root, and removes it when the switch is off", func() {
		n := promptcachesvc.NewNode(root, "", get)
		Expect(n.BackendEnv()).To(HaveKeyWithValue(config.EnvPromptCacheRoot, root))
		settings.DiskDisabled = true
		Expect(n.BackendEnv()).To(HaveKeyWithValue(config.EnvPromptCacheRoot, ""))
		Expect(n.BackendEnv()).To(HaveKeyWithValue(config.EnvPromptCacheReadMBps, ""))
	})

	It("is off without a root", func() {
		n := promptcachesvc.NewNode("", "", get)
		Expect(n.Enabled()).To(BeFalse())
		Expect(n.BackendEnv()).To(HaveKeyWithValue(config.EnvPromptCacheRoot, ""))
		res, err := n.Purge(context.Background(), promptcachesvc.Scope{All: true})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(BeZero())
	})

	It("builds the policy from the settings, the free disk at start and the users", func() {
		n := promptcachesvc.NewNode(root, "", get,
			promptcachesvc.WithStats(fixedStats{free: 100 * gib, total: 1000 * gib}),
			promptcachesvc.WithUsers(func() (bool, int) { return true, 4 }),
			promptcachesvc.WithModelTTLs(func() map[string]time.Duration {
				return map[string]time.Duration{"aaaaaaaaaaaaaaaa": time.Hour}
			}))
		p := n.Policy()
		Expect(p.TTL).To(Equal(7 * 24 * time.Hour))
		Expect(p.MaxBytes).To(Equal(int64(10 * gib)))
		Expect(p.QuotaBytes).To(Equal(int64(10*gib) / 4))
		Expect(p.ModelTTL).To(HaveKeyWithValue("aaaaaaaaaaaaaaaa", time.Hour))

		settings.MaxSizeMiB = 1024
		settings.UserQuotaMiB = -1
		p = n.Policy()
		Expect(p.MaxBytes).To(Equal(int64(1 << 30)))
		Expect(p.QuotaBytes).To(BeZero())
	})

	It("purges one model, some namespaces or everything, and reports the usage", func() {
		n := promptcachesvc.NewNode(root, "", get, promptcachesvc.WithStats(fixedStats{free: 500 * gib, total: 1000 * gib}))
		a := writeEntry(root, "a.gguf", "user:1")
		b := writeEntry(root, "b.gguf", "user:2")
		c := writeEntry(root, "b.gguf", "client:k")
		s := writeEntry(root, "a.gguf", promptcache.Shared)

		_, err := n.Sweep(context.Background())
		Expect(err).ToNot(HaveOccurred())
		nodes := promptcachesvc.Local{Node: n}
		reports, err := nodes.Usage(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(reports).To(HaveLen(1))
		Expect(reports[0].Files).To(Equal(4))
		Expect(reports[0].Status.State).To(Equal(pc.StateOn))

		res, err := nodes.Purge(context.Background(), promptcachesvc.NamespaceScope("user:2", "client:k", promptcache.Shared))
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(Equal(2))
		Expect(b).ToNot(BeAnExistingFile())
		Expect(c).ToNot(BeAnExistingFile())
		Expect(s).To(BeAnExistingFile(), "the shared namespace belongs to no principal")

		res, err = nodes.Purge(context.Background(), promptcachesvc.ModelScope("a.gguf"))
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(Equal(2))
		Expect(a).ToNot(BeAnExistingFile())

		_, err = nodes.Purge(context.Background(), promptcachesvc.Scope{})
		Expect(err).To(MatchError(promptcachesvc.ErrNoScope))
	})
})
