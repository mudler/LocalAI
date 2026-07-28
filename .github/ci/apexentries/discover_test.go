package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DiscoverAPEXTiers", func() {
	It("finds tiers regardless of how the stem relates to the repo name", func() {
		// This repo is mudler/gemma-4-26B-A4B-it-APEX-GGUF but its files drop "-it".
		files := []GGUFFile{
			{Name: "gemma-4-26B-A4B-APEX-I-Quality.gguf", SHA256: "a"},
			{Name: "gemma-4-26B-A4B-APEX-I-Nano.gguf", SHA256: "b"},
			{Name: "gemma-4-26B-A4B-APEX-Quality.gguf", SHA256: "c"},
			{Name: "mmproj-F16.gguf", SHA256: "d"},
		}

		imatrix, plain := DiscoverAPEXTiers(files)

		Expect(labels(imatrix)).To(ConsistOf("I-Quality", "I-Nano"))
		Expect(labels(plain)).To(ConsistOf("Quality"))
	})

	It("excludes mmproj from the tier list", func() {
		files := []GGUFFile{{Name: "mmproj.gguf", SHA256: "d"}}

		imatrix, plain := DiscoverAPEXTiers(files)

		Expect(imatrix).To(BeEmpty())
		Expect(plain).To(BeEmpty())
	})
})

var _ = Describe("DiscoverMMProj", func() {
	It("finds an mmproj whatever its suffix", func() {
		files := []GGUFFile{
			{Name: "Model-APEX-I-Mini.gguf", SHA256: "a"},
			{Name: "mmproj-step3.7-flash-f16.gguf", SHA256: "b"},
		}

		got, ok := DiscoverMMProj(files)

		Expect(ok).To(BeTrue())
		Expect(got.Name).To(Equal("mmproj-step3.7-flash-f16.gguf"))
	})

	It("reports absence when the repo ships none", func() {
		_, ok := DiscoverMMProj([]GGUFFile{{Name: "Model-APEX-Quality.gguf", SHA256: "a"}})

		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("FileStem", func() {
	It("strips the tier suffix", func() {
		t := Tier{Label: "I-Quality", File: GGUFFile{Name: "gemma-4-26B-A4B-APEX-I-Quality.gguf"}}

		Expect(FileStem(t)).To(Equal("gemma-4-26B-A4B-APEX"))
	})
})

func labels(ts []Tier) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Label)
	}
	return out
}
