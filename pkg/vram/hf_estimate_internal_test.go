package vram

import (
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func file(path string, size int64) hfapi.FileInfo {
	return hfapi.FileInfo{Type: "file", Path: path, Size: size}
}

var _ = Describe("sumWeightFileBytes", func() {
	It("reports only the largest quantization for a multi-GGUF repo", func() {
		// A single repo shipping several mutually-exclusive quantizations.
		files := []hfapi.FileInfo{
			file("model-Q4_K_M.gguf", 5_000),
			file("model-Q5_K_M.gguf", 6_000),
			file("model-Q8_0.gguf", 9_000),
			file("README.md", 100),
			file(".gitattributes", 10),
		}
		Expect(sumWeightFileBytes(files)).To(Equal(uint64(9_000)))
	})

	It("sums shards that belong to the same GGUF variant", func() {
		files := []hfapi.FileInfo{
			file("model-Q8_0-00001-of-00003.gguf", 4_000),
			file("model-Q8_0-00002-of-00003.gguf", 4_000),
			file("model-Q8_0-00003-of-00003.gguf", 4_000),
			file("model-Q4_K_M.gguf", 5_000),
		}
		// Q8_0 variant totals 12_000, larger than the single Q4_K_M file.
		Expect(sumWeightFileBytes(files)).To(Equal(uint64(12_000)))
	})

	It("still sums all shards for a safetensors-only repo", func() {
		files := []hfapi.FileInfo{
			file("model-00001-of-00003.safetensors", 3_000),
			file("model-00002-of-00003.safetensors", 3_000),
			file("model-00003-of-00003.safetensors", 3_000),
		}
		Expect(sumWeightFileBytes(files)).To(Equal(uint64(9_000)))
	})

	It("prefers LFS size when present", func() {
		files := []hfapi.FileInfo{
			{Type: "file", Path: "model-Q4_K_M.gguf", Size: 133, LFS: &hfapi.LFSInfo{Size: 7_000}},
		}
		Expect(sumWeightFileBytes(files)).To(Equal(uint64(7_000)))
	})
})
