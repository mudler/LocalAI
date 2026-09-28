package main

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sceneSpeakerJSON mirrors one element of a scene feed document's "speakers"
// array: {"speaker":0,"start":0.0,"end":0.6}.
type sceneSpeakerJSON struct {
	Speaker int     `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

// sceneSoundJSON mirrors one element of a scene feed document's "sounds"
// array: {"index":365,"label":"Chicken, rooster","start":24.0,"end":30.0,"peak":0.86}.
type sceneSoundJSON struct {
	Index int     `json:"index"`
	Label string  `json:"label"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Peak  float32 `json:"peak"`
}

// sceneFeedJSON mirrors the subset of the document
// parakeet_capi_scene_stream_feed_json returns (docs/sound.md) that the live
// path consumes: the closed "speakers" and "sounds" arrays. "t",
// "utterances", "words" and "active" belong to an offline scene/SAS
// consumer, not the live path, and are not decoded here.
type sceneFeedJSON struct {
	Speakers []sceneSpeakerJSON `json:"speakers"`
	Sounds   []sceneSoundJSON   `json:"sounds"`
}

// sceneWanted reports whether AudioTranscriptionLive should run a companion
// scene stream beside the ASR streaming session: at least one of the
// diarization/sound companions must be loaded, and the scene C-API symbols
// must be present. In practice the nil checks are defensive rather than
// live: loadRoles only ever sets diarCtx/tagCtx when parakeet_capi_model_kind
// (ABI v8) is present, and main.go registers every scene symbol in the same
// Dlsym-gated block as model_kind, so a companion being loaded already
// guarantees the scene symbols exist.
func (p *ParakeetCpp) sceneWanted() bool {
	return (p.diarCtx != 0 || p.tagCtx != 0) &&
		CppSceneOptsDefault != nil && CppSceneStreamBegin != nil &&
		CppSceneStreamFeedJSON != nil && CppSceneStreamFree != nil
}

// sceneBegin opens a no-ASR scene stream (diarization and/or sound events
// only; the live path's own ASR session already covers transcription) under
// engineMu. Call only when sceneWanted() is true. A 0 return means the C
// call itself failed; the caller logs a warning and continues the live
// session without speaker/sound events.
func (p *ParakeetCpp) sceneBegin() uintptr {
	p.engineMu.Lock()
	defer p.engineMu.Unlock()
	var opts cSceneOpts
	CppSceneOptsDefault(&opts)
	opts.DiarLatency = p.diarLatency
	return CppSceneStreamBegin(0, p.diarCtx, p.tagCtx, &opts)
}

// sceneFree releases a scene stream opened by sceneBegin. A 0 stream (scene
// events disabled or never began) is a no-op.
func (p *ParakeetCpp) sceneFree(s uintptr) {
	if s == 0 {
		return
	}
	p.engineMu.Lock()
	defer p.engineMu.Unlock()
	CppSceneStreamFree(s)
}

// sceneFeed runs one scene-stream feed (or the is_last flush) under
// engineMu and returns the parsed document. last_error is stream-scoped
// (parakeet_capi_scene_stream_last_error), so it is read under the same
// lock as the failing call.
func (p *ParakeetCpp) sceneFeed(s uintptr, pcm []float32, isLast bool) (sceneFeedJSON, error) {
	p.engineMu.Lock()
	defer p.engineMu.Unlock()

	var last int32
	if isLast {
		last = 1
	}
	var ptr *float32
	if len(pcm) > 0 {
		ptr = &pcm[0]
	}
	ret := CppSceneStreamFeedJSON(s, ptr, int32(len(pcm)), last)
	if ret == 0 {
		msg := ""
		if CppSceneStreamLastError != nil {
			msg = CppSceneStreamLastError(s)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return sceneFeedJSON{}, fmt.Errorf("parakeet-cpp: scene stream feed failed: %s", msg)
	}
	raw := goStringFromCPtr(ret)
	CppFreeString(ret)
	var doc sceneFeedJSON
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return sceneFeedJSON{}, fmt.Errorf("parakeet-cpp: decode scene json: %w", err)
	}
	return doc, nil
}

// feedSlicesScene mirrors driver.go's feedSlices but also feeds the same pcm
// slice to an optional companion scene stream right after each ASR slice, so
// the live path's speaker/sound events stay time-aligned with the ASR
// decode increments. sceneStream == 0 disables scene feeding for this call
// (no companions, or a previous scene feed already disabled it this
// session).
//
// A scene feed failure degrades gracefully rather than aborting live
// transcription over a secondary feature: it frees the broken stream, warns
// once, and returns 0 so the caller carries the ASR-only session forward.
// It returns the (possibly now-zeroed) scene stream for the caller to keep
// across the next call.
func (p *ParakeetCpp) feedSlicesScene(ctx context.Context, stream, sceneStream uintptr, pcm []float32, onFeed func(streamFeedResult, sceneFeedJSON) error) (uintptr, error) {
	for off := 0; off < len(pcm); off += streamChunkSamples {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return sceneStream, status.Error(codes.Canceled, "transcription cancelled")
			}
		}
		end := min(off+streamChunkSamples, len(pcm))
		chunk := pcm[off:end]

		res, err := p.feedChunk(stream, chunk, false)
		if err != nil {
			return sceneStream, err
		}

		var sceneDoc sceneFeedJSON
		if sceneStream != 0 {
			sceneDoc, err = p.sceneFeed(sceneStream, chunk, false)
			if err != nil {
				xlog.Warn("parakeet-cpp: live scene feed failed; disabling speaker/sound events for this session",
					"err", err)
				p.sceneFree(sceneStream)
				sceneStream = 0
				sceneDoc = sceneFeedJSON{}
			}
		}

		if err := onFeed(res, sceneDoc); err != nil {
			return sceneStream, err
		}
	}
	return sceneStream, nil
}

// liveSpeakersToProto maps a scene feed document's closed "speakers" into
// TranscriptLiveResponse.speakers (stream-relative nanoseconds). Reuses
// diarize.go's speakerLabel so the live path renders speaker indices the
// same way the offline Diarize RPC does.
func liveSpeakersToProto(speakers []sceneSpeakerJSON) []*pb.LiveSpeakerSegment {
	if len(speakers) == 0 {
		return nil
	}
	out := make([]*pb.LiveSpeakerSegment, len(speakers))
	for i, s := range speakers {
		out[i] = &pb.LiveSpeakerSegment{
			Speaker: speakerLabel(s.Speaker),
			Start:   secondsToNanos(s.Start),
			End:     secondsToNanos(s.End),
		}
	}
	return out
}

// liveSoundsToProto maps a scene feed document's closed "sounds" into
// TranscriptLiveResponse.sounds (stream-relative nanoseconds).
func liveSoundsToProto(sounds []sceneSoundJSON) []*pb.LiveSoundEvent {
	if len(sounds) == 0 {
		return nil
	}
	out := make([]*pb.LiveSoundEvent, len(sounds))
	for i, s := range sounds {
		out[i] = &pb.LiveSoundEvent{
			Label: s.Label,
			Index: int32(s.Index),
			Peak:  s.Peak,
			Start: secondsToNanos(s.Start),
			End:   secondsToNanos(s.End),
		}
	}
	return out
}
