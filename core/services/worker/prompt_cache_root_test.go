package worker

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	pc "github.com/mudler/LocalAI/pkg/promptcache"
)

// rootStats says a filesystem is a network one or not.
type rootStats struct{ network bool }

func (r rootStats) Stat(string) (pc.FSStats, error) {
	return pc.FSStats{Free: 1 << 40, Total: 2 << 40, Device: 1, Network: r.network}, nil
}

var _ = Describe("the prompt cache root of a worker", func() {
	models := filepath.Join(string(filepath.Separator), "srv", "models")
	derived := filepath.Join(string(filepath.Separator), "srv", "data", config.PromptCacheDirName)

	It("takes --prompt-cache-path first", func() {
		c := Config{ModelsPath: models, DataPath: "/var/lib/localai", PromptCachePath: "/scratch/pc"}
		root, src := c.promptCacheRoot(rootStats{network: true})
		Expect(root).To(Equal(filepath.FromSlash("/scratch/pc")))
		Expect(src).To(Equal(config.PromptCacheRootExplicit))
	})

	It("then <data-path>/prompt-cache", func() {
		c := Config{ModelsPath: models, DataPath: "/var/lib/localai"}
		root, src := c.promptCacheRoot(rootStats{network: true})
		Expect(root).To(Equal(filepath.FromSlash("/var/lib/localai/prompt-cache")))
		Expect(src).To(Equal(config.PromptCacheRootDataPath))
	})

	It("then the old root next to the models when it is local", func() {
		root, src := Config{ModelsPath: models}.promptCacheRoot(rootStats{})
		Expect(root).To(Equal(derived))
		Expect(src).To(Equal(config.PromptCacheRootDerived))
	})

	It("has none when the models sit on a network share", func() {
		root, _ := Config{ModelsPath: models}.promptCacheRoot(rootStats{network: true})
		Expect(root).To(BeEmpty())
	})
})
