package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("renderParent", func() {
	It("orders variants by quality rung rather than discovery order", func() {
		// DiscoverAPEXTiers preserves input order and the HF API returns siblings
		// alphabetically, so an unsorted list reads I-Balanced, I-Compact, I-Mini,
		// I-Nano, I-Quality. Selection ignores authored order; this is for the
		// human reading the file.
		children := []childBuild{
			{rank: rungRank["I-Nano"], entry: GalleryEntry{Name: "x-i-nano"}},
			{rank: 100, entry: GalleryEntry{Name: "x-ud-q4-k-m"}},
			{rank: rungRank["I-Quality"], entry: GalleryEntry{Name: "x-i-quality"}},
			{rank: rungRank["I-Compact"], entry: GalleryEntry{Name: "x-i-compact"}},
		}

		p := renderParent("mudler/X-APEX-GGUF", "X-APEX", children, true)

		var names []string
		for _, v := range p.Variants {
			names = append(names, v.Model)
		}
		Expect(names).To(Equal([]string{"x-i-quality", "x-i-compact", "x-i-nano", "x-ud-q4-k-m"}))
	})

	It("carries no dflash or mtp tag, since it declares no backend", func() {
		// The verifier skips entries with no declared backend, so a feature tag on
		// a parent would escape the tagging check entirely.
		p := renderParent("mudler/X-APEX-MTP-GGUF", "X-APEX-MTP", nil, false)

		Expect(p.Overrides).To(BeEmpty())
		Expect(p.Tags).ToNot(ContainElement("mtp"))
		Expect(p.Tags).ToNot(ContainElement("dflash"))
	})

	It("tags a multimodal family vision", func() {
		p := renderParent("mudler/X-APEX-GGUF", "X-APEX", nil, true)
		Expect(p.Tags).To(ContainElement("vision"))
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
