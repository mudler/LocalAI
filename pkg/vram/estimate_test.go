package vram_test

import (
	"context"

	. "github.com/mudler/LocalAI/pkg/vram"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeSizeResolver map[string]int64

func (f fakeSizeResolver) ContentLength(ctx context.Context, uri string) (int64, error) {
	if n, ok := f[uri]; ok {
		return int64(n), nil
	}
	return 0, nil
}

type fakeGGUFReader map[string]*GGUFMeta

func (f fakeGGUFReader) ReadMetadata(ctx context.Context, uri string) (*GGUFMeta, error) {
	return f[uri], nil
}

var _ = Describe("Estimate", func() {
	ctx := context.Background()

	Describe("empty or non-GGUF inputs", func() {
		It("returns zero size and vram for nil files", func() {
			opts := EstimateOptions{ContextLength: 8192}
			res, err := Estimate(ctx, nil, opts, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(0)))
			Expect(res.VRAMBytes).To(Equal(uint64(0)))
			Expect(res.SizeDisplay).To(Equal("0 B"))
		})

		It("counts only .gguf files and ignores other extensions", func() {
			files := []FileInput{
				{URI: "http://a/model.gguf", Size: 1_000_000_000},
				{URI: "http://a/readme.txt", Size: 100},
			}
			opts := EstimateOptions{ContextLength: 8192}
			res, err := Estimate(ctx, files, opts, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(1_000_000_000)))
		})

		It("sums size for multiple non-GGUF weight files (e.g. safetensors)", func() {
			files := []FileInput{
				{URI: "http://hf.co/model/model.safetensors", Size: 2_000_000_000},
				{URI: "http://hf.co/model/model2.safetensors", Size: 3_000_000_000},
			}
			opts := EstimateOptions{ContextLength: 8192}
			res, err := Estimate(ctx, files, opts, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(5_000_000_000)))
		})
	})

	Describe("GGUF size and resolver", func() {
		It("uses size resolver when file size is not set", func() {
			sizes := fakeSizeResolver{"http://example.com/model.gguf": 1_500_000_000}
			opts := EstimateOptions{ContextLength: 8192}
			files := []FileInput{{URI: "http://example.com/model.gguf"}}

			res, err := Estimate(ctx, files, opts, sizes, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(1_500_000_000)))
			Expect(res.VRAMBytes).To(BeNumerically(">=", res.SizeBytes))
			Expect(res.SizeDisplay).To(Equal("1.5 GB"))
		})

		It("uses size-only VRAM formula when metadata is missing and size is large", func() {
			sizes := fakeSizeResolver{"http://a/model.gguf": 10_000_000_000}
			opts := EstimateOptions{ContextLength: 8192}
			files := []FileInput{{URI: "http://a/model.gguf"}}

			res, err := Estimate(ctx, files, opts, sizes, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.VRAMBytes).To(BeNumerically(">", 10_000_000_000))
		})

		It("sums size for multiple GGUF shards", func() {
			files := []FileInput{
				{URI: "http://a/shard1.gguf", Size: 10_000_000_000},
				{URI: "http://a/shard2.gguf", Size: 5_000_000_000},
			}
			opts := EstimateOptions{ContextLength: 8192}

			res, err := Estimate(ctx, files, opts, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(15_000_000_000)))
		})

		It("formats size display correctly", func() {
			files := []FileInput{{URI: "http://a/model.gguf", Size: 2_500_000_000}}
			opts := EstimateOptions{ContextLength: 8192}

			res, err := Estimate(ctx, files, opts, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeDisplay).To(Equal("2.5 GB"))
		})
	})

	Describe("GGUF with metadata reader", func() {
		It("uses metadata for VRAM when reader returns meta and partial offload", func() {
			meta := &GGUFMeta{BlockCount: 32, EmbeddingLength: 4096}
			reader := fakeGGUFReader{"http://a/model.gguf": meta}
			opts := EstimateOptions{ContextLength: 8192, GPULayers: 20}
			files := []FileInput{{URI: "http://a/model.gguf", Size: 8_000_000_000}}

			res, err := Estimate(ctx, files, opts, nil, reader)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.VRAMBytes).To(BeNumerically(">", 0))
		})

		It("uses metadata head counts for KV and yields vram > size", func() {
			files := []FileInput{{URI: "http://a/model.gguf", Size: 15_000_000_000}}
			meta := &GGUFMeta{BlockCount: 32, EmbeddingLength: 4096, HeadCount: 32, HeadCountKV: 8}
			reader := fakeGGUFReader{"http://a/model.gguf": meta}
			opts := EstimateOptions{ContextLength: 8192}

			res, err := Estimate(ctx, files, opts, nil, reader)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(15_000_000_000)))
			Expect(res.VRAMBytes).To(BeNumerically(">", res.SizeBytes))
		})
	})
})

var _ = Describe("FormatBytes", func() {
	It("formats 2.5e9 as 2.5 GB", func() {
		Expect(FormatBytes(2_500_000_000)).To(Equal("2.5 GB"))
	})
})
