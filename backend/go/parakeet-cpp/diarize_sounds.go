package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// checkSoundEventsAvailable reports why include_sounds cannot be served, or
// nil when it can. It is checked before any audio work so a model without a
// sound companion fails fast and never returns an empty list that a client
// would read as "nothing was heard".
func (p *ParakeetCpp) checkSoundEventsAvailable() error {
	if p.tagCtx == 0 {
		return grpcerrors.SoundEventsUnsupported("parakeet-cpp",
			"the model has no sound model; add a sound_model: companion option"+p.roleHint(componentSound, "sound_component"))
	}
	if CppSceneOptsDefault == nil || CppSceneStreamBegin == nil || CppSceneStreamFeedJSON == nil || CppSceneStreamFree == nil {
		return grpcerrors.SoundEventsUnsupported("parakeet-cpp",
			"the loaded libparakeet.so has no scene stream support (parakeet_capi_scene_stream_* missing)")
	}
	return nil
}

// diarizeSoundEvents runs pcm through a tagger-only scene stream and returns
// the closed sound events. It is the same stream, with the same on/off
// thresholds and minimum duration, the live path opens beside an ASR session
// (see sceneBegin), so an offline request and a live session report the same
// events for the same audio. The clip is fed in 10 s pieces with the last one
// flushing events still open at the end of the audio.
//
// Every C call runs under engineMu, held for the whole clip like
// soundStreamDrain does; the stream is freed even when a feed fails or ctx is
// cancelled.
func (p *ParakeetCpp) diarizeSoundEvents(ctx context.Context, pcm []float32) ([]sceneSoundJSON, error) {
	p.engineMu.Lock()
	defer p.engineMu.Unlock()

	// The caller's check ran before this lock was taken; a Free() racing in
	// between zeroes tagCtx under the same lock, so check again.
	if p.tagCtx == 0 {
		return nil, grpcerrors.ModelNotLoaded("parakeet-cpp")
	}

	var opts cSceneOpts
	CppSceneOptsDefault(&opts)
	// Only closed events are used, never per-class scores, so keep none (see
	// sceneBegin for why a non-zero top_k would grow a queue).
	opts.Sound.TopK = 0

	stream := CppSceneStreamBegin(0, 0, p.tagCtx, &opts)
	if stream == 0 {
		return nil, fmt.Errorf("parakeet-cpp: sound scene stream begin failed: %s", soundLastError(p.tagCtx))
	}
	defer CppSceneStreamFree(stream)

	var events []sceneSoundJSON
	for off := 0; ; {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, status.Error(codes.Canceled, "parakeet-cpp: sound detection cancelled")
			}
		}
		end := off + soundFeedChunkSamples
		last := end >= len(pcm)
		if last {
			end = len(pcm)
		}
		doc, err := sceneFeedLocked(stream, pcm[off:end], last)
		if err != nil {
			return nil, err
		}
		events = append(events, doc.Sounds...)
		if last {
			break
		}
		off = end
	}
	return events, nil
}

// diarizeSoundsToProto maps closed scene sound events to DiarizeSound, sorted
// by start (then end, then label) so the order does not depend on how the
// stream closed them. Confidence is the event's peak score. The result is
// never nil: an empty clip of sound still serialises as an empty list next to
// sounds_included=true.
func diarizeSoundsToProto(sounds []sceneSoundJSON) []*pb.DiarizeSound {
	out := make([]*pb.DiarizeSound, 0, len(sounds))
	for _, s := range sounds {
		out = append(out, &pb.DiarizeSound{
			Start:      float32(s.Start),
			End:        float32(s.End),
			Label:      s.Label,
			Confidence: s.Peak,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End < b.End
		}
		return a.Label < b.Label
	})
	return out
}
