package gallery_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/config/harnessctx"
	"github.com/mudler/LocalAI/core/gallery"
)

// acceptedLicenceExceptions lists the models on the recommended list whose
// licence is not Apache-2.0 or MIT. A maintainer has read the licence and
// accepted it for the list; the value says which licence that is. Adding an id
// here is that decision, so do it in a reviewed change with the licence named.
var acceptedLicenceExceptions = map[string]string{
	// Liquid AI's LFM Open License, listed as "other" in the gallery index.
	"lfm2.5-24b-a2b-ud-q4": "LFM Open License",
}

var _ = Describe("curated recommended models", func() {
	var (
		list    gallery.RecommendedList
		entries map[string]gallery.GalleryModel
	)

	BeforeEach(func() {
		var err error
		list, err = gallery.BuiltinRecommendedModels()
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

	It("keeps to chat models with tool calling, an accepted licence and a usable window", func() {
		for name, tier := range list.Tiers {
			for _, m := range tier.Models() {
				e := entries[m.ID]
				if reason, ok := acceptedLicenceExceptions[m.ID]; ok {
					Expect(reason).ToNot(BeEmpty(), m.ID)
					Expect(e.License).To(Equal("other"), "%s is on the exceptions list, so the index must say so", m.ID)
				} else {
					Expect(e.License).To(BeElementOf("apache-2.0", "mit"), "%s in %s needs an entry in acceptedLicenceExceptions", m.ID, name)
				}
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

	It("serves the same list as the file in the gallery, so the fallback does not drift", func() {
		served, err := os.ReadFile("../../gallery/recommended.yaml")
		Expect(err).ToNot(HaveOccurred())
		fromGallery, err := gallery.ParseRecommendedModels(served)
		Expect(err).ToNot(HaveOccurred())
		// The two may diverge later on purpose (that is what serving the file
		// is for). Until then they are one list, and a divergence should be a
		// decision, not an accident.
		Expect(fromGallery).To(Equal(list))
	})

	It("names only models that exist in the gallery index, in the served file too", func() {
		served, err := os.ReadFile("../../gallery/recommended.yaml")
		Expect(err).ToNot(HaveOccurred())
		fromGallery, err := gallery.ParseRecommendedModels(served)
		Expect(err).ToNot(HaveOccurred())
		for name, tier := range fromGallery.Tiers {
			for _, m := range tier.Models() {
				Expect(entries).To(HaveKey(m.ID), "tier %s", name)
			}
		}
	})

	DescribeTable("rejects a file that is not valid",
		func(mutate func(string) string, want string) {
			_, err := gallery.ParseRecommendedModels([]byte(mutate(string(recommendedYAMLForTest()))))
			Expect(err).To(MatchError(ContainSubstring(want)))
		},
		Entry("a schema version it does not know", func(s string) string { return strings.Replace(s, "version: 1", "version: 2", 1) }, "schema version"),
		Entry("no version", func(s string) string { return strings.Replace(s, "version: 1\n", "", 1) }, "schema version"),
		Entry("a tier with a missing alternative", func(s string) string {
			return strings.Replace(s, "    - rank: alternative\n      id: lfm2.5-24b-a2b-ud-q4\n      name: LFM2.5 24B A2B\n      reason: A 24B mixture-of-experts model with about 2.45B active parameters, fast on a CPU when the host has 16 GiB or more of RAM.\n", "", 1)
		}, "1 and 2"),
		Entry("a rank it does not know", func(s string) string { return strings.Replace(s, "rank: default", "rank: best", 1) }, "rank"),
		Entry("an unknown tier", func(s string) string { return s + "  gpu-huge: []\n" }, "unknown tier"),
		Entry("an entry without a reason", func(s string) string {
			return strings.Replace(s, "      reason: Google's smallest Gemma 4, built for devices without a GPU; it fits an 8 GB machine and reads images.\n", "", 1)
		}, "without id, name or reason"),
		Entry("not YAML at all", func(string) string { return "<html>captive portal</html>" }, "recommended models"),
	)
})

var _ = Describe("loading the recommended models", func() {
	var dir string
	BeforeEach(func() { dir = GinkgoT().TempDir() })

	galleryAt := func(url string, mirrors ...string) []config.Gallery {
		return []config.Gallery{{Name: "test", URL: url, Mirrors: mirrors}}
	}
	served := func() []byte {
		data, err := os.ReadFile("../../gallery/recommended.yaml")
		Expect(err).ToNot(HaveOccurred())
		return data
	}

	It("finds the file next to the index of a gallery", func() {
		changed := strings.Replace(string(served()), "name: Gemma 4 E2B", "name: Served Name", 1)
		Expect(os.WriteFile(filepath.Join(dir, "recommended.yaml"), []byte(changed), 0o600)).To(Succeed())

		list, source := gallery.LoadRecommendedModels(context.Background(), galleryAt("file://"+filepath.Join(dir, "index.yaml")), dir, false)
		Expect(source).To(Equal("test"))
		Expect(list.Tiers[gallery.TierCPU].Default.DisplayName).To(Equal("Served Name"))
	})

	It("takes it from a mirror when the primary has no sibling file", func() {
		Expect(os.WriteFile(filepath.Join(dir, "recommended.yaml"), served(), 0o600)).To(Succeed())
		_, source := gallery.LoadRecommendedModels(context.Background(),
			galleryAt("https://example.invalid/models", "file://"+filepath.Join(dir, "index.yaml")), dir, false)
		Expect(source).To(Equal("test"))
	})

	DescribeTable("falls back to the built-in list",
		func(prepare func(dir string) []config.Gallery, strict bool) {
			_, source := gallery.LoadRecommendedModels(context.Background(), prepare(dir), dir, strict)
			Expect(source).To(Equal(gallery.RecommendedSourceBuiltin))
		},
		Entry("when the file is missing", func(dir string) []config.Gallery {
			return galleryAt("file://" + filepath.Join(dir, "index.yaml"))
		}, false),
		Entry("when the file is not valid", func(dir string) []config.Gallery {
			Expect(os.WriteFile(filepath.Join(dir, "recommended.yaml"), []byte("version: 9\ntiers: {}\n"), 0o600)).To(Succeed())
			return galleryAt("file://" + filepath.Join(dir, "index.yaml"))
		}, false),
		Entry("when no gallery has an index file", func(dir string) []config.Gallery {
			return galleryAt("https://example.invalid/models")
		}, false),
		Entry("when there is no gallery", func(string) []config.Gallery { return nil }, false),
		Entry("when strict integrity is on, as the file is not signed", func(dir string) []config.Gallery {
			Expect(os.WriteFile(filepath.Join(dir, "recommended.yaml"), served(), 0o600)).To(Succeed())
			return galleryAt("file://" + filepath.Join(dir, "index.yaml"))
		}, true),
	)
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
