// SPDX-License-Identifier: MIT
package backend

import (
	"math"

	"github.com/mudler/LocalAI/core/config"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

func corruptEmbeddingResult(reason string) error {
	return multimodalError(502, "multimodal embedding backend returned an invalid result: "+reason, nil)
}

// FinishMultimodalEmbedding turns the typed result of one input item into the
// final vector of that item. It reuses the legacy pooling and layout rules and
// adds the checks structured input requires: a non-empty actual dimension,
// finite values and a non-zero vector. A result that disagrees with its own
// declared shape is a corrupt backend result (502). A pooling scheme that
// cannot apply to the declared layout keeps the legacy classification, which
// callers map to 400, because the client selected that scheme.
func FinishMultimodalEmbedding(res *pb.MultimodalEmbeddingResponse, cfg config.ModelConfig) ([]float32, error) {
	r := res.GetResult()
	if r == nil {
		return nil, corruptEmbeddingResult("missing result")
	}
	n := int64(len(r.Embeddings))
	if n == 0 {
		return nil, corruptEmbeddingResult("empty vector")
	}
	tokens, dim := int64(r.Tokens), int64(r.Dim)
	if tokens < 0 || dim < 0 {
		return nil, corruptEmbeddingResult("negative shape")
	}
	switch r.Layout {
	case pb.EmbeddingLayout_EMBEDDING_LAYOUT_PER_TOKEN:
		// int64 holds the product of two int32 values without wrapping.
		if tokens == 0 || dim == 0 || tokens*dim != n {
			return nil, corruptEmbeddingResult("per-token matrix does not match its shape")
		}
	default:
		if tokens > 1 || (dim > 0 && dim != n) {
			return nil, corruptEmbeddingResult("vector does not match its shape")
		}
	}
	vec, err := finishEmbeddingResult(r, cfg)
	if err != nil {
		if IsEmbeddingPoolingCompatibilityError(err) {
			return nil, err
		}
		return nil, corruptEmbeddingResult("unusable embedding layout")
	}
	if len(vec) == 0 {
		return nil, corruptEmbeddingResult("empty vector")
	}
	nonZero := false
	for _, v := range vec {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, corruptEmbeddingResult("nonfinite value")
		}
		nonZero = nonZero || v != 0
	}
	if !nonZero {
		return nil, corruptEmbeddingResult("zero vector")
	}
	return vec, nil
}

// CheckEmbeddingModalities refuses parts the selected instance cannot consume.
// Metadata is the capability snapshot of the loaded instance. Media bytes are
// not inspected, so callers can run this before fetching anything. A video is
// accepted when audiovisual video is declared; whether a given clip carries a
// soundtrack is resolved by the backend after it probes the tracks.
//
// A video sent to an instance that consumes images but declares no video
// modality is a missing backend decoder, not a model limit, and answers 501.
// An instance that cannot consume images either gets the usual 422.
func CheckEmbeddingModalities(meta *pb.ModelMetadataResponse, parts []*pb.EmbeddingPart) error {
	if meta == nil {
		return multimodalError(501, "multimodal embedding protocol unavailable", nil)
	}
	return checkModalities(meta.EmbeddingModalities, parts)
}

// CheckRerankModalities applies the same rules to the rerank capability fields.
// Rerank publishes TEXT and IMAGE only, so any other part is refused.
func CheckRerankModalities(meta *pb.ModelMetadataResponse, parts []*pb.EmbeddingPart) error {
	if meta == nil {
		return multimodalError(501, "multimodal rerank protocol unavailable", nil)
	}
	return checkModalities(meta.RerankModalities, parts)
}

func checkModalities(published []pb.EmbeddingModality, parts []*pb.EmbeddingPart) error {
	supported := map[pb.EmbeddingModality]bool{}
	for _, v := range published {
		supported[v] = true
	}
	for _, part := range parts {
		var modality pb.EmbeddingModality
		switch p := part.GetPart().(type) {
		case *pb.EmbeddingPart_Text:
			modality = pb.EmbeddingModality_TEXT
		case *pb.EmbeddingPart_Media:
			switch p.Media.GetKind() {
			case pb.EmbeddingMedia_IMAGE:
				modality = pb.EmbeddingModality_IMAGE
			case pb.EmbeddingMedia_AUDIO:
				modality = pb.EmbeddingModality_AUDIO
			case pb.EmbeddingMedia_VIDEO:
				if supported[pb.EmbeddingModality_AUDIOVISUAL_VIDEO] {
					continue
				}
				modality = pb.EmbeddingModality_SILENT_VIDEO
				if !supported[modality] && supported[pb.EmbeddingModality_IMAGE] {
					return multimodalError(501, "video decoding unavailable in the backend", nil)
				}
			}
		}
		if modality == pb.EmbeddingModality_EMBEDDING_MODALITY_UNSPECIFIED {
			return multimodalError(400, "invalid embedding part", nil)
		}
		if !supported[modality] {
			return multimodalError(422, "model does not support embedding modality", nil)
		}
	}
	return nil
}
