package main

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/.github/ci/galleryedit"
)

func mustIndex(text string) *IndexText {
	ix, err := ParseIndexText(text)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return ix
}

// buildOf renders a realistic child so the specs exercise the payload a hub
// actually inherits rather than a bare name.
func buildOf(name, repo, file string, rank int) childBuild {
	return childBuild{
		rank: rank,
		entry: RenderChild(ChildInput{
			Name:     name,
			Repo:     repo,
			Template: entryTemplate,
			Weights:  []GGUFFile{{Name: file, SHA256: "aa"}},
			BaseTags: baseTags,
		}),
	}
}

var _ = Describe("ResolveHub", func() {
	It("picks the base model name over the APEX name, even when both are in the gallery", func() {
		// The hub is the entry a user searches for. If the *-apex entry were
		// chosen the family would be gathered under a name nobody looks up, and
		// the base entry would go on advertising only its own build.
		ix := mustIndex("- name: qwen3.6-35b-a3b\n  url: u\n- name: qwen3.6-35b-a3b-apex\n  url: u\n")

		name, exists := ResolveHub(ix, "Qwen3.6-35B-A3B-APEX", "Qwen3.6-35B-A3B-APEX")

		Expect(name).To(Equal("qwen3.6-35b-a3b"))
		Expect(exists).To(BeTrue())
	})

	It("falls back to the stem-derived candidate when the repo-derived one is absent", func() {
		// gemma's repo says "-it" and its published files do not, so only one of
		// the two candidates can match whatever the base entry was named after.
		ix := mustIndex("- name: gemma-4-26b-a4b\n  url: u\n")

		name, exists := ResolveHub(ix, "gemma-4-26B-A4B-it-APEX", "gemma-4-26B-A4B-APEX")

		Expect(name).To(Equal("gemma-4-26b-a4b"))
		Expect(exists).To(BeTrue())
	})

	It("reports the base name as absent rather than settling for the APEX entry", func() {
		ix := mustIndex("- name: qwen3.5-35b-a3b-apex\n  url: u\n")

		name, exists := ResolveHub(ix, "Qwen3.5-35B-A3B-APEX", "Qwen3.5-35B-A3B-APEX")

		Expect(name).To(Equal("qwen3.5-35b-a3b"))
		Expect(exists).To(BeFalse())
	})

	It("strips the MTP and TQ markers as well as APEX", func() {
		ix := mustIndex("- name: qwen3.6-35b-a3b\n  url: u\n")

		name, exists := ResolveHub(ix, "Qwen3.6-35B-A3B-APEX-MTP", "Qwen3.6-35B-A3B-APEX-MTP")

		Expect(name).To(Equal("qwen3.6-35b-a3b"))
		Expect(exists).To(BeTrue())
	})
})

var _ = Describe("planHubs", func() {
	noReuse := map[string]string{}
	allAdded := func(names ...string) map[string]bool {
		out := map[string]bool{}
		for _, n := range names {
			out[n] = true
		}
		return out
	}

	It("splices into the existing base entry instead of emitting an *-apex parent", func() {
		ix := mustIndex("- name: step-3.7-flash\n  url: u\n- name: other\n  url: u\n")
		fams := []family{{
			repo:     "mudler/Step-3.7-Flash-APEX-GGUF",
			repoBase: "Step-3.7-Flash-APEX",
			stem:     "Step-3.7-Flash-APEX",
			children: []childBuild{buildOf("step-3.7-flash-apex-i-quality", "mudler/Step-3.7-Flash-APEX-GGUF", "a.gguf", 0)},
		}}

		inserts, newHubs, err := planHubs(fams, ix, noReuse, allAdded("step-3.7-flash-apex-i-quality"))

		Expect(err).ToNot(HaveOccurred())
		Expect(newHubs).To(BeEmpty())
		Expect(inserts).To(HaveLen(1))
		Expect(inserts[0].Entry.Name).To(Equal("step-3.7-flash"))
		Expect(inserts[0].Variants).To(Equal([]string{"step-3.7-flash-apex-i-quality"}))
	})

	It("merges into an entry that already declares variants, without repeating one", func() {
		// The gallery's qwen3.6-35b-a3b already lists its APEX build. Re-adding it
		// would put a duplicate key's worth of noise in the diff and a duplicate
		// reference in the entry.
		ix := mustIndex("- name: qwen3.6-35b-a3b\n  variants:\n    - model: qwen3.6-35b-a3b-apex\n  url: u\n" +
			"- name: qwen3.6-35b-a3b-apex\n  url: u\n")
		fams := []family{{
			repo:     "mudler/Qwen3.6-35B-A3B-APEX-GGUF",
			repoBase: "Qwen3.6-35B-A3B-APEX",
			stem:     "Qwen3.6-35B-A3B-APEX",
			children: []childBuild{buildOf("qwen3.6-35b-a3b-apex-i-quality", "mudler/Qwen3.6-35B-A3B-APEX-GGUF", "a.gguf", 0)},
		}}

		inserts, newHubs, err := planHubs(fams, ix, noReuse, allAdded("qwen3.6-35b-a3b-apex-i-quality"))

		Expect(err).ToNot(HaveOccurred())
		Expect(newHubs).To(BeEmpty())
		Expect(inserts[0].Variants).To(Equal([]string{"qwen3.6-35b-a3b-apex-i-quality"}))

		out, err := galleryedit.Apply(ix.Lines, inserts)
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.Count(strings.Join(out, "\n"), "variants:")).To(Equal(1))
		Expect(out).To(HaveLen(len(ix.Lines) + 1))
	})

	It("never lets the hub reference itself", func() {
		// An unsloth rung whose weights the gallery already ships under the base
		// name resolves, through Merge, straight back to the hub. The verifier
		// reads a self reference as a variant that declares variants of its own.
		ix := mustIndex("- name: step-3.7-flash\n  url: u\n")
		fams := []family{{
			repo:     "mudler/Step-3.7-Flash-APEX-GGUF",
			repoBase: "Step-3.7-Flash-APEX",
			stem:     "Step-3.7-Flash-APEX",
			children: []childBuild{buildOf("step-3.7-flash-ud-q4-k-m", "unsloth/Step-3.7-Flash-GGUF", "a.gguf", 100)},
		}}

		inserts, _, err := planHubs(fams, ix, map[string]string{"step-3.7-flash-ud-q4-k-m": "step-3.7-flash"}, map[string]bool{})

		Expect(err).ToNot(HaveOccurred())
		Expect(inserts).To(BeEmpty())
	})

	It("emits a hub named for the base model when the gallery has none", func() {
		ix := mustIndex("- name: qwen3.5-35b-a3b-apex\n  url: u\n")
		fams := []family{{
			repo:      "mudler/Qwen3.5-35B-A3B-APEX-GGUF",
			repoBase:  "Qwen3.5-35B-A3B-APEX",
			stem:      "Qwen3.5-35B-A3B-APEX",
			hasMMProj: true,
			children: []childBuild{
				buildOf("qwen3.5-35b-a3b-apex-i-quality", "mudler/Qwen3.5-35B-A3B-APEX-GGUF", "a.gguf", 0),
				buildOf("qwen3.5-35b-a3b-ud-q6-k", "unsloth/Qwen3.5-35B-A3B-GGUF", "b.gguf", 102),
			},
		}}

		inserts, newHubs, err := planHubs(fams, ix, noReuse,
			allAdded("qwen3.5-35b-a3b-apex-i-quality", "qwen3.5-35b-a3b-ud-q6-k"))

		Expect(err).ToNot(HaveOccurred())
		Expect(inserts).To(BeEmpty())
		Expect(newHubs).To(HaveLen(1))

		hub := newHubs[0]
		Expect(hub.Name).To(Equal("qwen3.5-35b-a3b"))
		Expect(hub.Name).ToNot(HaveSuffix("-apex"))

		// A hand-written *-apex entry is an ordinary build, referenced like any
		// other rung and never deleted or renamed.
		Expect(hub.Variants).To(Equal([]VariantRef{
			{Model: "qwen3.5-35b-a3b-apex"},
			{Model: "qwen3.5-35b-a3b-apex-i-quality"},
			{Model: "qwen3.5-35b-a3b-ud-q6-k"},
		}))

		// The verifier skips entries with no declared backend, so a hub without
		// one would escape the tagging check in silence.
		Expect(hub.Overrides).To(HaveKeyWithValue("backend", "llama-cpp"))
		Expect(hub.Files).ToNot(BeEmpty())
		Expect(hub.Tags).To(ContainElement("vision"))
	})

	It("gathers two APEX repos that share one base model under a single hub", func() {
		ix := mustIndex("- name: unrelated\n  url: u\n")
		fams := []family{
			{
				repo:     "mudler/Solo-APEX-GGUF",
				repoBase: "Solo-APEX",
				stem:     "Solo-APEX",
				children: []childBuild{buildOf("solo-apex-i-quality", "mudler/Solo-APEX-GGUF", "a.gguf", 0)},
			},
			{
				repo:     "mudler/Solo-APEX-MTP-GGUF",
				repoBase: "Solo-APEX-MTP",
				stem:     "Solo-APEX-MTP",
				children: []childBuild{buildOf("solo-apex-mtp-i-quality", "mudler/Solo-APEX-MTP-GGUF", "b.gguf", 0)},
			},
		}

		_, newHubs, err := planHubs(fams, ix, noReuse, allAdded("solo-apex-i-quality", "solo-apex-mtp-i-quality"))

		Expect(err).ToNot(HaveOccurred())
		Expect(newHubs).To(HaveLen(1))
		Expect(newHubs[0].Name).To(Equal("solo"))
		Expect(newHubs[0].Variants).To(Equal([]VariantRef{
			{Model: "solo-apex-i-quality"},
			{Model: "solo-apex-mtp-i-quality"},
		}))
	})
})

var _ = Describe("hubVariants", func() {
	It("orders builds by quality rung rather than discovery order", func() {
		// DiscoverAPEXTiers preserves input order and the HF API returns siblings
		// alphabetically, so an unsorted list reads I-Balanced, I-Compact, I-Mini,
		// I-Nano, I-Quality. Selection ignores authored order; this is for the
		// human reading the file.
		f := family{repoBase: "X-APEX", stem: "X-APEX", children: []childBuild{
			{rank: rungRank["I-Nano"], entry: GalleryEntry{Name: "x-i-nano"}},
			{rank: 100, entry: GalleryEntry{Name: "x-ud-q4-k-m"}},
			{rank: rungRank["I-Quality"], entry: GalleryEntry{Name: "x-i-quality"}},
			{rank: rungRank["I-Compact"], entry: GalleryEntry{Name: "x-i-compact"}},
		}}

		got := hubVariants(&f, mustIndex("- name: x\n  url: u\n"), map[string]string{}, map[string]bool{})

		Expect(got).To(Equal([]string{"x-i-quality", "x-i-compact", "x-i-nano", "x-ud-q4-k-m"}))
	})
})

var _ = Describe("ParseIndexText", func() {
	It("refuses to edit by line number when the two views of the file disagree", func() {
		_, err := ParseIndexText("- name: one\n  url: u\n-\n")
		Expect(err).To(MatchError(ContainSubstring("empty")))
	})

	It("records the line range of each entry", func() {
		ix := mustIndex("- name: first\n  url: u\n- name: second\n  url: u\n")

		Expect(ix.Find("FIRST").Pos.StartLine).To(Equal(0))
		Expect(ix.Find("first").Pos.EndLine).To(Equal(2))
		Expect(ix.Find("second").Pos.StartLine).To(Equal(2))
	})
})

var _ = Describe("resolveVariant", func() {
	It("keeps an entry that was emitted even when it is also in reused", func() {
		// A within-batch name collision records reused[name] = name while the
		// FIRST entry of that name is still in add. Treating presence in reused as
		// "dropped" would emit nothing for it.
		added := map[string]bool{"dup": true}
		reused := map[string]string{"dup": "dup"}

		Expect(resolveVariant("dup", reused, added)).To(Equal("dup"))
	})

	It("redirects a reused name at the entry that stands in for it", func() {
		added := map[string]bool{}
		reused := map[string]string{"generated": "already-in-gallery"}

		Expect(resolveVariant("generated", reused, added)).To(Equal("already-in-gallery"))
	})
})

var _ = Describe("slug", func() {
	It("lowercases and turns quant underscores into hyphens", func() {
		Expect(slug("UD-Q4_K_M")).To(Equal("ud-q4-k-m"))
		Expect(slug("gemma-4-26B-A4B-it-APEX")).To(Equal("gemma-4-26b-a4b-it-apex"))
		Expect(slug("I-Nano")).To(Equal("i-nano"))
	})
})

var _ = Describe("sortTiers", func() {
	It("puts the imatrix ladder in descending quality order", func() {
		tiers := []Tier{
			{Label: "I-Balanced"}, {Label: "I-Compact"}, {Label: "I-Mini"},
			{Label: "I-Nano"}, {Label: "I-Quality"},
		}
		sortTiers(tiers)
		Expect(tierLabels(tiers)).To(Equal("I-Quality,I-Balanced,I-Compact,I-Mini,I-Nano"))
	})
})

var _ = Describe("restrict", func() {
	It("keeps only the named repos", func() {
		got := restrict([]string{"mudler/A-APEX-GGUF", "mudler/B-APEX-GGUF"}, "mudler/B-APEX-GGUF")
		Expect(got).To(Equal([]string{"mudler/B-APEX-GGUF"}))
	})

	It("returns nothing when the filter matches nothing", func() {
		Expect(restrict([]string{"mudler/A-APEX-GGUF"}, "mudler/typo")).To(BeEmpty())
	})
})

var _ = Describe("reportUnclassified", func() {
	// One real imatrix rung is always present so the specs measure how the
	// remaining files are bucketed, not an empty-repo edge case.
	tier := Tier{Label: "I-Quality", File: GGUFFile{Name: "Model-APEX-I-Quality.gguf"}}

	censusOf := func(names ...string) fileCensus {
		files := []GGUFFile{tier.File}
		for _, n := range names {
			files = append(files, GGUFFile{Name: n})
		}
		return reportUnclassified("mudler/Model-APEX-GGUF", files, []Tier{tier}, nil)
	}

	It("counts a flat full-precision source as excluded, not unclassified", func() {
		got := censusOf("Carnice-MoE-35B-A3B-F16.gguf")
		Expect(got.fullPrecision).To(Equal(1))
		Expect(got.unclassified).To(Equal(0))
	})

	It("counts every shard of a sharded full-precision source as excluded", func() {
		got := censusOf(
			"MiniMax-M2.7-APEX-F16-00001-of-00003.gguf",
			"MiniMax-M2.7-APEX-F16-00002-of-00003.gguf",
			"MiniMax-M2.7-APEX-F16-00003-of-00003.gguf",
		)
		Expect(got.fullPrecision).To(Equal(3))
		Expect(got.unclassified).To(Equal(0))
	})

	It("treats bf16 the same as f16, in either case", func() {
		got := censusOf("Model-APEX-BF16.gguf", "Model-APEX-bf16-00001-of-00002.gguf", "Model-APEX-f16.gguf")
		Expect(got.fullPrecision).To(Equal(3))
		Expect(got.unclassified).To(Equal(0))
	})

	It("still reports a genuinely unknown filename as unclassified", func() {
		got := censusOf("Model-APEX-Turbo.gguf")
		Expect(got.unclassified).To(Equal(1))
		Expect(got.fullPrecision).To(Equal(0))
	})

	It("separates the two kinds when a repo publishes both", func() {
		got := censusOf("Model-APEX-F16.gguf", "Model-APEX-Turbo.gguf")
		Expect(got.fullPrecision).To(Equal(1))
		Expect(got.unclassified).To(Equal(1))
	})
})
