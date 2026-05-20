package openai

import (
	"encoding/base64"
	"encoding/binary"
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("applyEmbeddingDimensions", func() {
	It("truncates embeddings locally when dimensions are configured", func() {
		embedding := []float32{1, 2, 3, 4}

		truncated, err := applyEmbeddingDimensions(embedding, 2)

		Expect(err).ToNot(HaveOccurred())
		Expect(truncated).To(Equal([]float32{1, 2}))
		Expect(embedding).To(Equal([]float32{1, 2, 3, 4}))
	})

	It("keeps embeddings unchanged when dimensions are not configured", func() {
		embedding := []float32{1, 2, 3}

		result, err := applyEmbeddingDimensions(embedding, 0)

		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(embedding))
	})

	It("fails clearly when the backend embedding is shorter than requested", func() {
		_, err := applyEmbeddingDimensions([]float32{1, 2}, 4)

		Expect(err).To(MatchError("embedding dimensions requested 4 but backend returned 2"))
	})

	It("rejects negative dimensions", func() {
		_, err := applyEmbeddingDimensions([]float32{1, 2}, -1)

		Expect(err).To(MatchError("embedding dimensions must be non-negative, got -1"))
	})
})

var _ = Describe("embeddingItem", func() {
	It("base64-encodes the locally truncated embedding", func() {
		embedding, err := applyEmbeddingDimensions([]float32{1, 2, 3}, 2)
		Expect(err).ToNot(HaveOccurred())

		item := embeddingItem(embedding, 0, "base64")
		raw, err := base64.StdEncoding.DecodeString(item.EmbeddingBase64)

		Expect(err).ToNot(HaveOccurred())
		Expect(raw).To(HaveLen(8))
		Expect(math.Float32frombits(binary.LittleEndian.Uint32(raw[0:4]))).To(Equal(float32(1)))
		Expect(math.Float32frombits(binary.LittleEndian.Uint32(raw[4:8]))).To(Equal(float32(2)))
	})
})
