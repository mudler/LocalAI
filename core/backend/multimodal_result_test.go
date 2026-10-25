// SPDX-License-Identifier: MIT
package backend_test

import (
	"errors"
	"math"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Multimodal HTTP", func() {
	final := func(v ...float32) *pb.MultimodalEmbeddingResponse {
		return &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: v, Tokens: 1, Dim: int32(len(v)), Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}}
	}
	code := func(err error) int {
		var typed *backend.MultimodalEmbeddingError
		Expect(errors.As(err, &typed)).To(BeTrue(), "error %v is not classified", err)
		return typed.StatusCode()
	}
	It("returns a final vector unchanged", func() {
		v, err := backend.FinishMultimodalEmbedding(final(3, 4), config.ModelConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(Equal([]float32{3, 4}))
	})
	It("accepts a final vector whose shape was not reported", func() {
		res := &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: []float32{1, 2}, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}}
		v, err := backend.FinishMultimodalEmbedding(res, config.ModelConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(HaveLen(2))
	})
	It("pools per-token results with the configured scheme exactly once", func() {
		res := &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: []float32{2, 0, 0, 2}, Tokens: 2, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_PER_TOKEN}}
		cfg := config.ModelConfig{}
		cfg.Pooling = backend.PoolingMean
		v, err := backend.FinishMultimodalEmbedding(res, cfg)
		Expect(err).NotTo(HaveOccurred())
		want, err := backend.PoolEmbeddingResult(res.Result, backend.PoolingMean, 0, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(Equal(want))
	})
	It("classifies pooling and layout incompatibility as a pooling compatibility error", func() {
		cfg := config.ModelConfig{}
		cfg.Pooling = backend.PoolingMean
		_, err := backend.FinishMultimodalEmbedding(final(1, 2), cfg)
		Expect(backend.IsEmbeddingPoolingCompatibilityError(err)).To(BeTrue())
	})
	DescribeTable("rejects corrupt results as 502",
		func(res *pb.MultimodalEmbeddingResponse) {
			v, err := backend.FinishMultimodalEmbedding(res, config.ModelConfig{})
			Expect(v).To(BeNil())
			Expect(code(err)).To(Equal(502))
		},
		Entry("nil response", nil),
		Entry("missing result", &pb.MultimodalEmbeddingResponse{}),
		Entry("empty vector", final()),
		Entry("NaN", final(1, float32(math.NaN()))),
		Entry("negative infinity", final(1, float32(math.Inf(-1)))),
		Entry("zero vector", final(0, 0)),
		Entry("declared dimension disagrees", &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: []float32{1, 2, 3}, Tokens: 1, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}}),
		Entry("final layout with several vectors", &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: []float32{1, 2, 3, 4}, Tokens: 2, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}}),
		Entry("per-token matrix of the wrong size", &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: []float32{1, 2, 3}, Tokens: 2, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_PER_TOKEN}}),
	)
	It("rejects pooled zero and nonfinite vectors", func() {
		cfg := config.ModelConfig{}
		cfg.Pooling = backend.PoolingMean
		res := &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: []float32{0, 0, 0, 0}, Tokens: 2, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_PER_TOKEN}}
		_, err := backend.FinishMultimodalEmbedding(res, cfg)
		Expect(code(err)).To(Equal(502))
	})
	It("checks modalities against metadata the way the session does", func() {
		meta := &pb.ModelMetadataResponse{EmbeddingModalities: []pb.EmbeddingModality{pb.EmbeddingModality_TEXT, pb.EmbeddingModality_SILENT_VIDEO}}
		text := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Text{Text: "x"}}
		video := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_VIDEO}}}
		image := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_IMAGE}}}
		Expect(backend.CheckEmbeddingModalities(meta, []*pb.EmbeddingPart{text, video})).To(Succeed())
		Expect(code(backend.CheckEmbeddingModalities(meta, []*pb.EmbeddingPart{text, image}))).To(Equal(422))
		Expect(code(backend.CheckEmbeddingModalities(nil, []*pb.EmbeddingPart{text}))).To(Equal(501))
	})
	It("answers 501 for video when the instance reads images but publishes no video decoder", func() {
		meta := &pb.ModelMetadataResponse{EmbeddingModalities: []pb.EmbeddingModality{pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE, pb.EmbeddingModality_AUDIO}}
		text := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Text{Text: "x"}}
		video := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_VIDEO}}}
		image := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_IMAGE}}}
		Expect(backend.CheckEmbeddingModalities(meta, []*pb.EmbeddingPart{text, image})).To(Succeed())
		Expect(code(backend.CheckEmbeddingModalities(meta, []*pb.EmbeddingPart{text, image, video}))).To(Equal(501))
		// A text-only model has no image input either, so video is a model limit.
		textOnly := &pb.ModelMetadataResponse{EmbeddingModalities: []pb.EmbeddingModality{pb.EmbeddingModality_TEXT}}
		Expect(code(backend.CheckEmbeddingModalities(textOnly, []*pb.EmbeddingPart{video}))).To(Equal(422))
	})
	It("checks rerank parts against the rerank capability only", func() {
		meta := &pb.ModelMetadataResponse{
			EmbeddingModalities: []pb.EmbeddingModality{pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE, pb.EmbeddingModality_AUDIO},
			RerankModalities:    []pb.EmbeddingModality{pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE},
		}
		text := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Text{Text: "x"}}
		image := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_IMAGE}}}
		audio := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_AUDIO}}}
		Expect(backend.CheckRerankModalities(meta, []*pb.EmbeddingPart{text, image})).To(Succeed())
		Expect(code(backend.CheckRerankModalities(meta, []*pb.EmbeddingPart{audio}))).To(Equal(422))
		Expect(code(backend.CheckRerankModalities(nil, []*pb.EmbeddingPart{text}))).To(Equal(501))
		// A text-only reranker refuses an image with 422, never a text-only score.
		textOnly := &pb.ModelMetadataResponse{RerankModalities: []pb.EmbeddingModality{pb.EmbeddingModality_TEXT}}
		Expect(code(backend.CheckRerankModalities(textOnly, []*pb.EmbeddingPart{text, image}))).To(Equal(422))
	})
})
