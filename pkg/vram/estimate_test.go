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

var _ = Describe("EstimateMultiContext", func() {
	ctx := context.Background()
	defaultCtx := []uint32{8192}

	Describe("empty or non-GGUF inputs", func() {
		It("returns zero size and vram for nil files", func() {
			res, err := EstimateMultiContext(ctx, nil, defaultCtx, EstimateOptions{}, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(0)))
			Expect(res.Estimates["8192"].VRAMBytes).To(Equal(uint64(0)))
			Expect(res.SizeDisplay).To(Equal("0 B"))
		})

		It("counts only weight files and ignores other extensions", func() {
			files := []FileInput{
				{URI: "http://a/model.gguf", Size: 1_000_000_000},
				{URI: "http://a/readme.txt", Size: 100},
			}
			res, err := EstimateMultiContext(ctx, files, defaultCtx, EstimateOptions{}, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(1_000_000_000)))
		})

		It("sums size for multiple non-GGUF weight files (e.g. safetensors)", func() {
			files := []FileInput{
				{URI: "http://hf.co/model/model.safetensors", Size: 2_000_000_000},
				{URI: "http://hf.co/model/model2.safetensors", Size: 3_000_000_000},
			}
			res, err := EstimateMultiContext(ctx, files, defaultCtx, EstimateOptions{}, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(5_000_000_000)))
		})
	})

	Describe("GGUF size and resolver", func() {
		It("uses size resolver when file size is not set", func() {
			sizes := fakeSizeResolver{"http://example.com/model.gguf": 1_500_000_000}
			files := []FileInput{{URI: "http://example.com/model.gguf"}}

			res, err := EstimateMultiContext(ctx, files, defaultCtx, EstimateOptions{}, sizes, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(1_500_000_000)))
			Expect(res.Estimates["8192"].VRAMBytes).To(BeNumerically(">=", res.SizeBytes))
			Expect(res.SizeDisplay).To(Equal("1.5 GB"))
		})

		It("uses size-only VRAM formula when metadata is missing and size is large", func() {
			sizes := fakeSizeResolver{"http://a/model.gguf": 10_000_000_000}
			files := []FileInput{{URI: "http://a/model.gguf"}}

			res, err := EstimateMultiContext(ctx, files, defaultCtx, EstimateOptions{}, sizes, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Estimates["8192"].VRAMBytes).To(BeNumerically(">", 10_000_000_000))
		})

		It("sums size for multiple GGUF shards", func() {
			files := []FileInput{
				{URI: "http://a/shard1.gguf", Size: 10_000_000_000},
				{URI: "http://a/shard2.gguf", Size: 5_000_000_000},
			}

			res, err := EstimateMultiContext(ctx, files, defaultCtx, EstimateOptions{}, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(15_000_000_000)))
		})

		It("formats size display correctly", func() {
			files := []FileInput{{URI: "http://a/model.gguf", Size: 2_500_000_000}}

			res, err := EstimateMultiContext(ctx, files, defaultCtx, EstimateOptions{}, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeDisplay).To(Equal("2.5 GB"))
		})
	})

	Describe("GGUF with metadata reader", func() {
		It("uses metadata for VRAM when reader returns meta and partial offload", func() {
			meta := &GGUFMeta{BlockCount: 32, EmbeddingLength: 4096}
			reader := fakeGGUFReader{"http://a/model.gguf": meta}
			opts := EstimateOptions{GPULayers: 20}
			files := []FileInput{{URI: "http://a/model.gguf", Size: 8_000_000_000}}

			res, err := EstimateMultiContext(ctx, files, defaultCtx, opts, nil, reader)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Estimates["8192"].VRAMBytes).To(BeNumerically(">", 0))
		})

		It("uses metadata head counts for KV and yields vram > size", func() {
			files := []FileInput{{URI: "http://a/model.gguf", Size: 15_000_000_000}}
			meta := &GGUFMeta{BlockCount: 32, EmbeddingLength: 4096, HeadCount: 32, HeadCountKV: 8}
			reader := fakeGGUFReader{"http://a/model.gguf": meta}

			res, err := EstimateMultiContext(ctx, files, defaultCtx, EstimateOptions{}, nil, reader)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(15_000_000_000)))
			Expect(res.Estimates["8192"].VRAMBytes).To(BeNumerically(">", res.SizeBytes))
		})

		It("populates ModelMaxContext from GGUF metadata", func() {
			meta := &GGUFMeta{BlockCount: 32, EmbeddingLength: 4096, MaximumContextLength: 131072}
			reader := fakeGGUFReader{"http://a/model.gguf": meta}
			files := []FileInput{{URI: "http://a/model.gguf", Size: 8_000_000_000}}

			res, err := EstimateMultiContext(ctx, files, defaultCtx, EstimateOptions{}, nil, reader)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.ModelMaxContext).To(Equal(uint64(131072)))
		})
	})

	Describe("multi-context behavior", func() {
		It("returns estimates for all requested context sizes", func() {
			files := []FileInput{{URI: "http://a/model.gguf", Size: 4_000_000_000}}
			sizes := []uint32{8192, 32768, 131072}

			res, err := EstimateMultiContext(ctx, files, sizes, EstimateOptions{}, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Estimates).To(HaveLen(3))
			Expect(res.Estimates).To(HaveKey("8192"))
			Expect(res.Estimates).To(HaveKey("32768"))
			Expect(res.Estimates).To(HaveKey("131072"))
		})

		It("VRAM increases monotonically with context size", func() {
			files := []FileInput{{URI: "http://a/model.gguf", Size: 4_000_000_000}}
			meta := &GGUFMeta{BlockCount: 32, EmbeddingLength: 4096, HeadCount: 32, HeadCountKV: 8}
			reader := fakeGGUFReader{"http://a/model.gguf": meta}
			sizes := []uint32{8192, 16384, 32768, 65536, 131072, 262144}

			res, err := EstimateMultiContext(ctx, files, sizes, EstimateOptions{}, nil, reader)
			Expect(err).ToNot(HaveOccurred())

			prev := uint64(0)
			for _, sz := range sizes {
				v := res.VRAMForContext(sz)
				Expect(v).To(BeNumerically(">", prev), "VRAM should increase at context %d", sz)
				prev = v
			}
		})

		It("size is constant across context sizes", func() {
			files := []FileInput{{URI: "http://a/model.gguf", Size: 4_000_000_000}}
			sizes := []uint32{8192, 32768}

			res, err := EstimateMultiContext(ctx, files, sizes, EstimateOptions{}, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.SizeBytes).To(Equal(uint64(4_000_000_000)))
		})

		It("defaults to [8192] when contextSizes is empty", func() {
			files := []FileInput{{URI: "http://a/model.gguf", Size: 4_000_000_000}}

			res, err := EstimateMultiContext(ctx, files, nil, EstimateOptions{}, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Estimates).To(HaveLen(1))
			Expect(res.Estimates).To(HaveKey("8192"))
		})
	})

	Describe("VRAMForContext helper", func() {
		It("returns 0 for missing context size", func() {
			res := MultiContextEstimate{
				Estimates: map[string]VRAMAt{
					"8192": {VRAMBytes: 5000},
				},
			}
			Expect(res.VRAMForContext(99999)).To(Equal(uint64(0)))
			Expect(res.VRAMForContext(8192)).To(Equal(uint64(5000)))
		})
	})
})

var _ = Describe("FormatBytes", func() {
	It("formats 2.5e9 as 2.5 GB", func() {
		Expect(FormatBytes(2_500_000_000)).To(Equal("2.5 GB"))
	})
})
