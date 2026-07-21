package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CounterpartCandidates", func() {
	It("offers both the repo-derived and stem-derived names", func() {
		// mudler/gemma-4-26B-A4B-it-APEX-GGUF ships gemma-4-26B-A4B-APEX-*.gguf,
		// and only the repo-derived name finds unsloth/gemma-4-26B-A4B-it-GGUF.
		got := CounterpartCandidates("gemma-4-26B-A4B-it-APEX-GGUF", "gemma-4-26B-A4B-APEX")

		Expect(got).To(Equal([]string{"gemma-4-26B-A4B-it", "gemma-4-26B-A4B"}))
	})

	It("strips MTP and TQ markers", func() {
		got := CounterpartCandidates("Qwopus3.6-35B-A3B-v1-APEX-MTP-GGUF", "Qwopus3.6-35B-A3B-v1-APEX-MTP")

		Expect(got[0]).To(Equal("Qwopus3.6-35B-A3B-v1"))
	})

	It("does not repeat a candidate when both derivations agree", func() {
		got := CounterpartCandidates("Qwen3.6-35B-A3B-APEX-GGUF", "Qwen3.6-35B-A3B-APEX")

		Expect(got).To(Equal([]string{"Qwen3.6-35B-A3B"}))
	})
})

var _ = Describe("DiscoverUnslothQuants", func() {
	It("finds flat single-file quants", func() {
		files := []GGUFFile{
			{Name: "Qwen3.6-35B-A3B-UD-Q4_K_M.gguf", SHA256: "a"},
			{Name: "Qwen3.6-35B-A3B-UD-IQ1_M.gguf", SHA256: "b"},
		}

		got := DiscoverUnslothQuants(files)

		Expect(got).To(HaveLen(1))
		Expect(got[0].Quant).To(Equal("UD-Q4_K_M"))
		Expect(got[0].Sharded).To(BeFalse())
		Expect(got[0].Files).To(HaveLen(1))
	})

	It("collects a sharded quant from its subdirectory in shard order", func() {
		files := []GGUFFile{
			{Name: "UD-Q4_K_M/Step-3.7-Flash-UD-Q4_K_M-00002-of-00002.gguf", SHA256: "b"},
			{Name: "UD-Q4_K_M/Step-3.7-Flash-UD-Q4_K_M-00001-of-00002.gguf", SHA256: "a"},
		}

		got := DiscoverUnslothQuants(files)

		Expect(got).To(HaveLen(1))
		Expect(got[0].Quant).To(Equal("UD-Q4_K_M"))
		Expect(got[0].Sharded).To(BeTrue())
		Expect(got[0].Files).To(HaveLen(2))
		Expect(got[0].Files[0].Name).To(HaveSuffix("00001-of-00002.gguf"))
	})

	It("ignores quants outside the wanted subset", func() {
		files := []GGUFFile{{Name: "Model-UD-IQ2_XXS.gguf", SHA256: "a"}}

		Expect(DiscoverUnslothQuants(files)).To(BeEmpty())
	})
})
