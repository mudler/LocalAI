package backend

import (
	"math"

	"github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Go-side embedding pooling", func() {
	Describe("reshapeEmbeddings", func() {
		It("views a flat payload as tokens x dim rows", func() {
			vecs, err := reshapeEmbeddings([]float32{1, 2, 3, 4, 5, 6}, 2, 3)
			Expect(err).ToNot(HaveOccurred())
			Expect(vecs).To(Equal([][]float32{{1, 2, 3}, {4, 5, 6}}))
		})

		It("rejects a payload that does not match the reported shape", func() {
			_, err := reshapeEmbeddings([]float32{1, 2, 3, 4, 5}, 2, 3)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not match reported shape"))
		})

		It("rejects a non-positive shape", func() {
			_, err := reshapeEmbeddings(nil, 0, 3)
			Expect(err).To(HaveOccurred())
			_, err = reshapeEmbeddings(nil, 3, 0)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("poolMean", func() {
		It("averages the per-token vectors", func() {
			Expect(poolMean([][]float32{{1, 2}, {3, 4}})).To(Equal([]float32{2, 3}))
		})
	})

	Describe("poolLast", func() {
		It("returns the last token's vector", func() {
			Expect(poolLast([][]float32{{1, 2}, {3, 4}})).To(Equal([]float32{3, 4}))
		})
	})

	Describe("poolDecayedMean", func() {
		It("weights tokens by 2^(-(T-1-i)/H): H=1, T=3 gives [0.25 0.5 1]/1.75", func() {
			vecs := [][]float32{{1, 0}, {0, 1}, {1, 1}}
			got := poolDecayedMean(vecs, 1)
			Expect(got[0]).To(BeNumerically("~", 1.25/1.75, 1e-6))
			Expect(got[1]).To(BeNumerically("~", 1.5/1.75, 1e-6))
		})

		It("approaches the plain mean as the half-life grows", func() {
			vecs := [][]float32{{1, 0}, {0, 1}, {1, 1}}
			got := poolDecayedMean(vecs, 1e12)
			want := poolMean(vecs)
			Expect(got[0]).To(BeNumerically("~", want[0], 1e-6))
			Expect(got[1]).To(BeNumerically("~", want[1], 1e-6))
		})
	})

	Describe("single-token conversations", func() {
		It("agree across mean, last and decayed_mean", func() {
			vecs := [][]float32{{3, 4}}
			Expect(poolMean(vecs)).To(Equal([]float32{3, 4}))
			Expect(poolLast(vecs)).To(Equal([]float32{3, 4}))
			got := poolDecayedMean(vecs, 256)
			Expect(got[0]).To(BeNumerically("~", 3, 1e-6))
			Expect(got[1]).To(BeNumerically("~", 4, 1e-6))
		})
	})

	Describe("normalizeEmbedding (common_embd_normalize port)", func() {
		v := []float32{3, -4}

		It("passes through untouched for negative embd_norm", func() {
			Expect(normalizeEmbedding(v, -1)).To(Equal([]float32{3, -4}))
		})

		It("scales to the int16 range for embd_norm 0 (max-abs)", func() {
			// max-abs = 4, sum = 4/32760, norm = 32760/4 = 8190
			got := normalizeEmbedding(v, 0)
			Expect(got[0]).To(BeNumerically("~", 3*8190.0, 1e-2))
			Expect(got[1]).To(BeNumerically("~", -4*8190.0, 1e-2))
		})

		It("applies the taxicab norm for embd_norm 1", func() {
			got := normalizeEmbedding(v, 1)
			Expect(got[0]).To(BeNumerically("~", 3.0/7.0, 1e-6))
			Expect(got[1]).To(BeNumerically("~", -4.0/7.0, 1e-6))
		})

		It("applies the L2 norm for embd_norm 2", func() {
			got := normalizeEmbedding(v, 2)
			Expect(got[0]).To(BeNumerically("~", 0.6, 1e-6))
			Expect(got[1]).To(BeNumerically("~", -0.8, 1e-6))
		})

		It("applies a p-norm for embd_norm > 2", func() {
			p3 := math.Cbrt(27 + 64) // (|3|^3 + |-4|^3)^(1/3)
			got := normalizeEmbedding(v, 3)
			Expect(got[0]).To(BeNumerically("~", 3.0/p3, 1e-5))
			Expect(got[1]).To(BeNumerically("~", -4.0/p3, 1e-5))
		})

		It("maps the all-zero vector to all zeros instead of dividing by zero", func() {
			Expect(normalizeEmbedding([]float32{0, 0, 0}, 2)).To(Equal([]float32{0, 0, 0}))
		})
	})

	Describe("embdNormalizeFromOptions", func() {
		It("defaults to 2 (L2) like llama.cpp", func() {
			Expect(embdNormalizeFromOptions(nil)).To(Equal(2))
			Expect(embdNormalizeFromOptions([]string{"pooling:none", "gpu"})).To(Equal(2))
		})

		It("parses embd_normalize and its embedding_normalize alias", func() {
			Expect(embdNormalizeFromOptions([]string{"embd_normalize:0"})).To(Equal(0))
			Expect(embdNormalizeFromOptions([]string{"embedding_normalize:-1"})).To(Equal(-1))
			Expect(embdNormalizeFromOptions([]string{"embd_normalize: 3"})).To(Equal(3))
		})

		It("keeps the default when the value does not parse", func() {
			Expect(embdNormalizeFromOptions([]string{"embd_normalize:junk"})).To(Equal(2))
			Expect(embdNormalizeFromOptions([]string{"embd_normalize"})).To(Equal(2))
		})
	})

	Describe("PoolEmbeddingResult", func() {
		res := &proto.EmbeddingResult{
			Embeddings: []float32{1, 2, 3, 4},
			Tokens:     2,
			Dim:        2,
		}

		It("reshapes, pools and normalizes", func() {
			got, err := PoolEmbeddingResult(res, PoolingMean, 0, -1)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal([]float32{2, 3}))
		})

		It("L2-normalizes by default norm 2", func() {
			got, err := PoolEmbeddingResult(res, PoolingLast, 0, 2)
			Expect(err).ToNot(HaveOccurred())
			Expect(got[0]).To(BeNumerically("~", 0.6, 1e-6))
			Expect(got[1]).To(BeNumerically("~", 0.8, 1e-6))
		})

		It("rejects unknown pooling schemes", func() {
			_, err := PoolEmbeddingResult(res, "sideways", 0, 2)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown Go-side pooling scheme"))
		})

		It("propagates shape mismatches", func() {
			bad := &proto.EmbeddingResult{Embeddings: []float32{1, 2, 3}, Tokens: 2, Dim: 2}
			_, err := PoolEmbeddingResult(bad, PoolingMean, 0, 2)
			Expect(err).To(HaveOccurred())
		})
	})
})
