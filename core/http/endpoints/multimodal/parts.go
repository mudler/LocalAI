// SPDX-License-Identifier: MIT
package multimodal

import (
	"context"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// MediaAcquirer fetches and validates the compressed bytes of one media source.
type MediaAcquirer interface {
	Acquire(ctx context.Context, source embeddingmedia.Source, budget *embeddingmedia.Budget) ([]byte, error)
}

// StructuredParts builds the ordered typed parts of one item. Media bytes are
// attached by the caller; before anything is fetched it also serves as the
// capability skeleton used to check the loaded model.
func StructuredParts(item schema.EmbeddingInputItem) []*pb.EmbeddingPart {
	if item.Text != nil || len(item.Content) == 0 {
		s := ""
		if item.Text != nil {
			s = *item.Text
		}
		return []*pb.EmbeddingPart{{Part: &pb.EmbeddingPart_Text{Text: s}}}
	}
	parts := make([]*pb.EmbeddingPart, 0, len(item.Content))
	for _, p := range item.Content {
		switch p.Type {
		case "text":
			parts = append(parts, &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Text{Text: p.Text}})
		default:
			parts = append(parts, &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: mediaKind(p.Type), Format: p.Source.Format}}})
		}
	}
	return parts
}

func mediaKind(partType string) pb.EmbeddingMedia_Kind {
	switch partType {
	case "image_url":
		return pb.EmbeddingMedia_IMAGE
	case "input_audio":
		return pb.EmbeddingMedia_AUDIO
	case "input_video", "video_url":
		return pb.EmbeddingMedia_VIDEO
	}
	return pb.EmbeddingMedia_UNSPECIFIED
}

func sourceKind(partType string) string {
	switch partType {
	case "image_url":
		return embeddingmedia.KindImage
	case "input_audio":
		return embeddingmedia.KindAudio
	}
	return embeddingmedia.KindVideo
}

func ToProtoBudget(b embeddingmedia.Budget) *pb.EmbeddingBudget {
	return &pb.EmbeddingBudget{
		RemainingFrames:                     b.Frames,
		RemainingPixels:                     b.Pixels,
		RemainingAudioAllocationBytes:       b.AudioBytes,
		RemainingSourceDurationMilliseconds: b.DurationMilliseconds,
		RemainingTemporaryBytes:             b.TemporaryBytes,
		RemainingPreparationMilliseconds:    b.PreparationMilliseconds,
		RemainingCompressedBytes:            b.MediaBytes,
		RemainingTextBytes:                  b.TextBytes,
		RemainingSourceParts:                b.SourceParts,
	}
}

// FromProtoConsumption never trusts the backend: the caller compares the
// result with the budget it supplied before any subtraction.
func FromProtoConsumption(c *pb.EmbeddingConsumption) embeddingmedia.Consumption {
	return embeddingmedia.Consumption{
		Frames:                  c.GetFrames(),
		Pixels:                  c.GetPixels(),
		AudioBytes:              c.GetAudioAllocationBytes(),
		DurationMilliseconds:    c.GetSourceDurationMilliseconds(),
		TemporaryBytes:          c.GetTemporaryBytes(),
		PreparationMilliseconds: c.GetPreparationMilliseconds(),
		MediaBytes:              c.GetCompressedBytes(),
		TextBytes:               c.GetTextBytes(),
		SourceParts:             c.GetSourceParts(),
	}
}

func HasMedia(item schema.EmbeddingInputItem) bool {
	for _, p := range item.Content {
		if p.Type != "text" {
			return true
		}
	}
	return false
}

func Exceeds(c embeddingmedia.Consumption, remaining embeddingmedia.Budget) bool {
	return c.Frames > remaining.Frames || c.Pixels > remaining.Pixels || c.AudioBytes > remaining.AudioBytes ||
		c.DurationMilliseconds > remaining.DurationMilliseconds || c.TemporaryBytes > remaining.TemporaryBytes ||
		c.PreparationMilliseconds > remaining.PreparationMilliseconds || c.MediaBytes > remaining.MediaBytes ||
		c.TextBytes > remaining.TextBytes || c.SourceParts > remaining.SourceParts
}

// PrepareItem builds the ordered typed parts of one item and fetches its media
// under work, a scratch copy of the live budget that makes the acquirer enforce
// cumulative limits inside the item. It returns what Go itself counted, which a
// backend cannot report below. Fetches are sequential on purpose: the budget is
// not synchronized.
func PrepareItem(ctx context.Context, acquirer MediaAcquirer, item schema.EmbeddingInputItem, work *embeddingmedia.Budget) ([]*pb.EmbeddingPart, embeddingmedia.Consumption, error) {
	var known embeddingmedia.Consumption
	parts := StructuredParts(item)
	if item.Text != nil || len(item.Content) == 0 {
		n := uint64(len(parts[0].GetText()))
		if err := work.Consume(embeddingmedia.Consumption{TextBytes: n, SourceParts: 1}); err != nil {
			return nil, known, err
		}
		known.TextBytes, known.SourceParts = n, 1
		return parts, known, nil
	}
	for i, p := range item.Content {
		if p.Type == "text" {
			n := uint64(len(p.Text))
			if err := work.Consume(embeddingmedia.Consumption{TextBytes: n, SourceParts: 1}); err != nil {
				return nil, known, err
			}
			known.TextBytes += n
			known.SourceParts++
			continue
		}
		if err := work.Consume(embeddingmedia.Consumption{SourceParts: 1}); err != nil {
			return nil, known, err
		}
		known.SourceParts++
		data, err := acquirer.Acquire(ctx, embeddingmedia.Source{
			Data: p.Source.Data, URL: p.Source.URL, Format: p.Source.Format, Kind: sourceKind(p.Type),
		}, work)
		if err != nil {
			return nil, known, err
		}
		parts[i].GetMedia().Data = data
		known.MediaBytes += uint64(len(data))
	}
	return parts, known, nil
}
