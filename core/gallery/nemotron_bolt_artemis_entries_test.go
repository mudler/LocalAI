// SPDX-License-Identifier: MIT
package gallery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

// One entry per weight file of the Artemis repo. IQ1_M is the size the card
// keeps for text, so it ships without the projector. The DFlash drafter stays
// out: a dflash tag would outrank the plain builds in variant selection.
var _ = Describe("gallery/index.yaml Nemotron 3.5 Bolt E4B Artemis entries", func() {
	const (
		resolveMain = "https://huggingface.co/turintech/Nemotron-3.5-Bolt-E4B-Artemis-GGUF/resolve/main/"
		modelDir    = "llama-cpp/models/Nemotron-3.5-Bolt-E4B-Artemis-GGUF/"
		mmproj      = "llama-cpp/mmproj/Nemotron-3.5-Bolt-E4B-Artemis-GGUF/mmproj-Q8_0.gguf"
		mmprojSHA   = "75138529ded13b6b3ff0844005f99aecc6f37342a12f97903abae1fa9d98b45b"
		declaring   = "nemotron-3.5-bolt-e4b-artemis-q3-k-xl"
		textOnly    = "nemotron-3.5-bolt-e4b-artemis-iq1-m"
	)
	builds := []struct{ name, weight, sha string }{
		{"nemotron-3.5-bolt-e4b-artemis-iq1-m", "Nemotron-3.5-Bolt-E4B-Artemis-IQ1_M.gguf", "28927e836abbd46efc8815e93f680da4a7194600bf2c5087532a38a65c3a60fd"},
		{"nemotron-3.5-bolt-e4b-artemis-iq2-xxs", "Nemotron-3.5-Bolt-E4B-Artemis-IQ2_XXS.gguf", "be19850c42ade881e1899a561ca26de80dbe46bc44962652adab8b8c66ff3109"},
		{"nemotron-3.5-bolt-e4b-artemis-iq2-m", "Nemotron-3.5-Bolt-E4B-Artemis-IQ2_M.gguf", "6afb7f5a2b1c9f00a9c04de930ccebfbcfb6458fdea989741696ab5474bd8a3c"},
		{"nemotron-3.5-bolt-e4b-artemis-iq3-xxs", "Nemotron-3.5-Bolt-E4B-Artemis-IQ3_XXS.gguf", "a92891d51c8c3c0118abb950f4bc5baa8950b6de0bb363561508dea1d3127851"},
		{"nemotron-3.5-bolt-e4b-artemis-q3-k-xl", "Nemotron-3.5-Bolt-E4B-Artemis-Q3_K_XL.gguf", "2f6b97cae43c795d95b2047f1963f536cba332bf17a3ff384c994206aa0b4bd7"},
		{"nemotron-3.5-bolt-e4b-artemis-q4-k-xl", "Nemotron-3.5-Bolt-E4B-Artemis-Q4_K_XL.gguf", "55ab544324a4958a18e66cf6f7414adfceeab22bddbd07ac48d7c7229d813313"},
		{"nemotron-3.5-bolt-e4b-artemis-q5-k-xl", "Nemotron-3.5-Bolt-E4B-Artemis-Q5_K_XL.gguf", "4001b00f642eb6e44e565d04290990129e55a86b8093d7a953dfb8fa4ea419e5"},
		{"nemotron-3.5-bolt-e4b-artemis-q6-k-xl", "Nemotron-3.5-Bolt-E4B-Artemis-Q6_K_XL.gguf", "3e8c514e26db6ee8543d62a3102366da796dc3549e9f301e3ae4e3818305193f"},
		{"nemotron-3.5-bolt-e4b-artemis-q8-k-xl", "Nemotron-3.5-Bolt-E4B-Artemis-Q8_K_XL.gguf", "7bec0fb40a7b441f089a158441351c7e7e4747ba9c15dc90aa4def65e978d8f8"},
	}

	byName := func() map[string]gallery.GalleryModel {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		return indexEntriesByName(entries)
	}

	It("declares the other eight builds as variants of Q3_K_XL", func() {
		entries := byName()
		others := []string{}
		for _, b := range builds {
			e, ok := entries[b.name]
			Expect(ok).To(BeTrue(), b.name)
			if b.name != declaring {
				others = append(others, b.name)
				Expect(e.Variants).To(BeEmpty(), b.name)
			}
		}

		variants := []string{}
		for _, v := range entries[declaring].Variants {
			variants = append(variants, v.Model)
		}
		Expect(variants).To(ConsistOf(others))
	})

	It("pins each build's files to resolve/main and the published sha256", func() {
		entries := byName()
		for _, b := range builds {
			e := entries[b.name]
			want := []gallery.File{{Filename: modelDir + b.weight, SHA256: b.sha, URI: resolveMain + b.weight}}
			if b.name == textOnly {
				Expect(e.Overrides).ToNot(HaveKey("mmproj"), b.name)
			} else {
				want = append(want, gallery.File{Filename: mmproj, SHA256: mmprojSHA, URI: resolveMain + "mmproj-Q8_0.gguf"})
				Expect(e.Overrides).To(HaveKeyWithValue("mmproj", mmproj), b.name)
			}
			Expect(e.AdditionalFiles).To(ConsistOf(want), b.name)
			Expect(e.Overrides["parameters"]).To(HaveKeyWithValue("model", modelDir+b.weight), b.name)
		}
	})

	// reasoning_effort none keeps thinking off unless a request asks for it.
	It("keeps thinking off by default and configures no drafter", func() {
		entries := byName()
		for _, b := range builds {
			e := entries[b.name]
			Expect(e.Overrides["options"]).To(ConsistOf("use_jinja:true"), b.name)
			Expect(e.Overrides).To(HaveKeyWithValue("reasoning_effort", "none"), b.name)
			Expect(e.Overrides).ToNot(HaveKey("reasoning"), b.name)
			Expect(e.Tags).ToNot(ContainElement(BeElementOf("dflash", "mtp")), b.name)
		}
	})
})
