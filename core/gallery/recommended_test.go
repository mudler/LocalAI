package gallery_test

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config/harnessctx"
	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("curated recommended models", func() {
	var (
		list    gallery.RecommendedList
		entries map[string]gallery.GalleryModel
	)

	BeforeEach(func() {
		var err error
		list, err = gallery.RecommendedModels()
		Expect(err).ToNot(HaveOccurred())

		data, err := os.ReadFile("../../gallery/index.yaml")
		Expect(err).ToNot(HaveOccurred())
		var all []gallery.GalleryModel
		Expect(yaml.Unmarshal(data, &all)).To(Succeed())
		entries = map[string]gallery.GalleryModel{}
		for _, m := range all {
			entries[m.Name] = m
		}
	})

	It("has one default and two alternatives in every tier", func() {
		Expect(list.Tiers).To(HaveLen(len(gallery.RecommendedTiers)))
		for _, name := range gallery.RecommendedTiers {
			tier, ok := list.Tiers[name]
			Expect(ok).To(BeTrue(), name)
			Expect(tier.Default.ID).ToNot(BeEmpty(), name)
			Expect(tier.Alternatives).To(HaveLen(2), name)
			Expect(tier.Models()).To(HaveLen(3), name)
		}
	})

	It("names only models that exist in the gallery index", func() {
		for name, tier := range list.Tiers {
			for _, m := range tier.Models() {
				Expect(entries).To(HaveKey(m.ID), "tier %s", name)
			}
		}
	})

	It("keeps to chat models with tool calling, a permissive licence and a usable window", func() {
		for name, tier := range list.Tiers {
			for _, m := range tier.Models() {
				e := entries[m.ID]
				Expect(e.License).To(BeElementOf("apache-2.0", "mit"), "%s in %s", m.ID, name)
				Expect(e.Overrides["known_usecases"]).To(ContainElement("chat"), m.ID)
				Expect(e.Overrides["function"]).ToNot(BeNil(), "%s has no function-calling config", m.ID)
				// An explicit window in the entry is what the loader keeps; it
				// must not be one the chat cannot run in.
				if ctx, ok := e.Overrides["context_size"].(int); ok {
					Expect(ctx).To(BeNumerically(">=", harnessctx.MinContext), m.ID)
				}
				Expect(gallery.RecommendedWeightURI(&e)).To(HavePrefix("https://"), m.ID)
			}
		}
	})

	It("rejects a list that is not complete", func() {
		_, err := gallery.ParseRecommendedModels([]byte("version: 1\ntiers: {}\n"))
		Expect(err).To(HaveOccurred())

		broken := strings.Replace(string(recommendedYAMLForTest()), "      - id: qwen3.8-2b-q4\n        name: Qwen3.8 2B\n        reason: The lightest choice, for machines with little memory or a slow processor.\n", "", 1)
		_, err = gallery.ParseRecommendedModels([]byte(broken))
		Expect(err).To(MatchError(ContainSubstring("alternatives")))
	})
})

var _ = Describe("hardware tiers", func() {
	It("puts a host in the tier of its per-device memory", func() {
		const gb = 1_000_000_000
		Expect(gallery.TierForMemory(0)).To(Equal(gallery.TierCPU))
		Expect(gallery.TierForMemory(4 * gb)).To(Equal(gallery.TierGPUSmall))
		Expect(gallery.TierForMemory(8 * gb)).To(Equal(gallery.TierGPUMid))
		Expect(gallery.TierForMemory(16 * gb)).To(Equal(gallery.TierGPUMid))
		Expect(gallery.TierForMemory(24 * gb)).To(Equal(gallery.TierGPULarge))
		Expect(gallery.TierForMemory(80 * gb)).To(Equal(gallery.TierGPULarge))
	})
})

func recommendedYAMLForTest() []byte {
	data, err := os.ReadFile("recommended_models.yaml")
	Expect(err).ToNot(HaveOccurred())
	return data
}
