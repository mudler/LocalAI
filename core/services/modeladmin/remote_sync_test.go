package modeladmin

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
)

var _ = Describe("ApplyRemoteChange", func() {
	var (
		dir    string
		loader *config.ModelConfigLoader
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		loader = config.NewModelConfigLoader(dir)
	})

	writeYAML := func(name string, body map[string]any) {
		body["name"] = name
		data, err := yaml.Marshal(body)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), data, 0644)).To(Succeed())
	}

	It("loads a peer-created config from disk on an install event", func() {
		// Peer wrote the YAML to the shared models dir; this replica has not
		// loaded it yet (empty in-memory loader).
		writeYAML("peer-alias", map[string]any{"alias": "qwen"})
		_, ok := loader.GetModelConfig("peer-alias")
		Expect(ok).To(BeFalse(), "precondition: not yet in memory")

		err := ApplyRemoteChange(loader, nil, dir, messaging.CacheInvalidateEvent{
			Element: "peer-alias", Op: "install",
		})
		Expect(err).ToNot(HaveOccurred())

		_, ok = loader.GetModelConfig("peer-alias")
		Expect(ok).To(BeTrue(), "install event must reload the new config from disk")
	})

	It("prunes a peer-deleted config that a reload-from-path cannot drop", func() {
		// Model is present in memory (loaded earlier) but its file is now gone
		// from the shared dir. LoadModelConfigsFromPath is additive, so only an
		// explicit prune can remove it - this is the cross-replica delete bug.
		writeYAML("doomed", map[string]any{"alias": "qwen"})
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		_, ok := loader.GetModelConfig("doomed")
		Expect(ok).To(BeTrue(), "precondition: in memory")
		Expect(os.Remove(filepath.Join(dir, "doomed.yaml"))).To(Succeed())

		err := ApplyRemoteChange(loader, nil, dir, messaging.CacheInvalidateEvent{
			Element: "doomed", Op: "delete",
		})
		Expect(err).ToNot(HaveOccurred())

		_, ok = loader.GetModelConfig("doomed")
		Expect(ok).To(BeFalse(), "delete event must prune the element from memory")
	})

	It("does a full reload when no element is named", func() {
		writeYAML("m1", map[string]any{"alias": "qwen"})
		writeYAML("m2", map[string]any{"alias": "qwen"})

		err := ApplyRemoteChange(loader, nil, dir, messaging.CacheInvalidateEvent{})
		Expect(err).ToNot(HaveOccurred())

		_, ok1 := loader.GetModelConfig("m1")
		_, ok2 := loader.GetModelConfig("m2")
		Expect(ok1).To(BeTrue())
		Expect(ok2).To(BeTrue())
	})

	It("loads a peer-persisted artifact binding without materializing", func() {
		const relative = ".artifacts/huggingface/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/snapshot"
		writeYAML("peer-managed", map[string]any{
			"backend": "transformers",
			"artifacts": []map[string]any{{
				"name": "model", "target": "model",
				"source": map[string]any{"type": "huggingface", "repo": "owner/repo", "revision": "main"},
				"resolved": map[string]any{
					"endpoint":  "https://huggingface.co",
					"revision":  "0123456789abcdef0123456789abcdef01234567",
					"cache_key": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				},
			}},
			"parameters": map[string]any{"model": "owner/repo"},
		})
		Expect(ApplyRemoteChange(loader, nil, dir, messaging.CacheInvalidateEvent{
			Element: "peer-managed", Op: "install",
		})).To(Succeed())
		loaded, found := loader.GetModelConfig("peer-managed")
		Expect(found).To(BeTrue())
		Expect(loaded.Model).To(Equal("owner/repo"))
		Expect(loaded.ModelFileName()).To(Equal(relative))
		Expect(loaded.Artifacts).To(HaveLen(1))
		Expect(loaded.Artifacts[0].Resolved.CacheKey).To(HaveLen(64))
	})
})
