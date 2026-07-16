package main

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// streamFeedResult is one decode increment from a cache-aware streaming session:
// the newly-finalized text plus the model's own per-feed boundary tokens
// (<EOU>/<EOB>) and word timings. It is the single event type both the live
// (bidi) and file (server-stream) paths fold over, hiding the ABI v4 JSON vs
// older text-only entry-point split behind one shape.
type streamFeedResult struct {
	Delta string
	Eou   bool
	Eob   bool
	Words []transcriptWord
}

// feedChunk feeds one PCM chunk to the streaming session (or finalizes it, when
// finalize is true) and returns the unified decode increment. It prefers the
// ABI v4 JSON entry points (which also carry per-word timestamps) and falls
// back to the older text-only entry points against an older libparakeet.so.
//
// This is the one place the JSON-vs-text choice is made; every consumer works
// in terms of streamFeedResult.
func (p *ParakeetCpp) feedChunk(stream uintptr, pcm []float32, finalize bool) (streamFeedResult, error) {
	if CppStreamFeedJSON != nil {
		doc, err := p.streamFeedDoc(stream, pcm, finalize)
		if err != nil {
			return streamFeedResult{}, err
		}
		return streamFeedResult{Delta: doc.Text, Eou: doc.Eou != 0, Eob: doc.Eob != 0, Words: doc.Words}, nil
	}
	delta, eou, eob, err := p.streamFeedText(stream, pcm, finalize)
	if err != nil {
		return streamFeedResult{}, err
	}
	return streamFeedResult{Delta: delta, Eou: eou, Eob: eob}, nil
}

// feedSlices feeds pcm through the session in streamChunkSamples slices,
// invoking onFeed for each decode increment. It does NOT finalize: callers
// decide when the send side is done. The file path finalizes after the whole
// file; the live path finalizes only when its request channel closes, never
// between audio messages. Slicing keeps each per-call engineMu hold short so
// concurrent unary transcription interleaves fairly (the C session buffers
// internally).
//
// If ctx is non-nil it is checked before each slice so a cancelled file
// transcription stops promptly; the live path passes nil (it is bounded by its
// request channel instead of a ctx).
func (p *ParakeetCpp) feedSlices(ctx context.Context, stream uintptr, pcm []float32, onFeed func(streamFeedResult) error) error {
	for off := 0; off < len(pcm); off += streamChunkSamples {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return status.Error(codes.Canceled, "transcription cancelled")
			}
		}
		end := min(off+streamChunkSamples, len(pcm))
		res, err := p.feedChunk(stream, pcm[off:end], false)
		if err != nil {
			return err
		}
		if err := onFeed(res); err != nil {
			return err
		}
	}
	return nil
}

// flushTail finalizes the session once and folds the flushed tail (the last
// ~2 encoder frames of text, which only appear on finalize) through onFeed.
func (p *ParakeetCpp) flushTail(stream uintptr, onFeed func(streamFeedResult) error) error {
	res, err := p.feedChunk(stream, nil, true)
	if err != nil {
		return err
	}
	return onFeed(res)
}
