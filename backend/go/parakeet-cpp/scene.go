package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
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
// array: {"index":99,"label":"Chicken, rooster","start":24.0,"end":30.0,"peak":0.86}.
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
	// Names is the CURRENT name of each speaker slot (keyed by the slot
	// index as a string) at the time of this feed. Each closed segment takes
	// its slot's current name, so a segment that closes before its slot is
	// identified carries an empty name. Absent without a speaker model.
	Names map[string]speakerNameJSON `json:"names"`
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

// sceneStreamHandle bundles the C scene_stream pointer with the diar/tag
// contexts it was begun with. sceneFeed re-checks those against p.diarCtx/
// p.tagCtx under engineMu before every call, so a Free() racing between the
// begin and a later feed (freeing the very contexts the stream borrows) is
// caught instead of handed to the C side — mirroring streamFeedDoc's re-check
// of p.ctxPtr (see the "Per-C-call engine serialization" comment in
// goparakeetcpp.go). The zero value (s == 0) means "no scene stream".
//
// spk and reg are the speaker model and the known-voice registry of a
// speaker-named stream (0 for a plain one). The stream borrows both: sceneFree
// frees the stream first and then the registry, which this handle owns.
type sceneStreamHandle struct {
	names map[string]string
	s     uintptr
	diar  uintptr
	tag   uintptr
	spk   uintptr
	reg   uintptr
}

// sceneBegin opens a no-ASR scene stream (diarization and/or sound events
// only; the live path's own ASR session already covers transcription) under
// engineMu. Call only when sceneWanted() is true. Refuses to begin with both
// contexts 0 (defensive: sceneWanted() already guards this). A zero handle
// means the C call itself failed; the caller logs a warning and continues
// the live session without speaker/sound events.
func (p *ParakeetCpp) sceneBegin(voices []*pb.KnownVoice) sceneStreamHandle {
	p.engineMu.Lock()
	defer p.engineMu.Unlock()
	diar, tag := p.diarCtx, p.tagCtx
	if diar == 0 && tag == 0 {
		return sceneStreamHandle{}
	}
	var opts cSceneOpts
	CppSceneOptsDefault(&opts)
	opts.DiarLatency = p.diarLatency
	// The live scene path never drains sound scores (unlike the offline
	// SoundDetection RPC, see sound.go), so the default top_k of 5 would
	// leave the C side's per-window score queue growing for the session's
	// whole lifetime. 0 disables per-class score retention; sound EVENTS
	// (onset/offset, what the live path actually consumes) are unaffected.
	opts.Sound.TopK = 0

	// With a speaker model and at least one usable known voice the stream
	// names speakers through a registry it borrows. engineMu is already
	// held, so use the Locked builder. Any failure keeps the plain stream.
	var reg uintptr
	if diar != 0 && p.spkCtx != 0 && CppSceneStreamBeginSpeaker != nil {
		r, err := p.buildSpeakerRegistryLocked(voices)
		if err != nil {
			xlog.Warn("parakeet-cpp: could not build the speaker registry for a live session; speakers stay unnamed", "err", err)
		} else {
			reg = r
		}
	}
	if reg != 0 {
		opts.SpeakerAcceptThreshold = p.speakerAccept
		opts.SpeakerMargin = p.speakerMargin
		s := CppSceneStreamBeginSpeaker(0, diar, tag, p.spkCtx, reg, &opts)
		if s == 0 {
			p.freeSpeakerRegistry(reg)
			return sceneStreamHandle{}
		}
		return sceneStreamHandle{s: s, diar: diar, tag: tag, spk: p.spkCtx, reg: reg, names: voiceNames(voices)}
	}
	s := CppSceneStreamBegin(0, diar, tag, &opts)
	if s == 0 {
		return sceneStreamHandle{}
	}
	return sceneStreamHandle{s: s, diar: diar, tag: tag}
}

// sceneFree releases a scene stream opened by sceneBegin. A zero handle
// (scene events disabled or never began) is a no-op. Safe to call even after
// the contexts the stream borrowed have been freed: parakeet_scene_stream's
// destructor only releases its own buffers and never dereferences the
// borrowed asr/diar/tagger pointers (verified against
// parakeet.cpp's parakeet_capi_scene_stream_free / SceneStream::~SceneStream
// / DiarPcmStream::~DiarPcmStream, all `= default`), unlike a feed call.
func (p *ParakeetCpp) sceneFree(h sceneStreamHandle) {
	if h.s == 0 {
		return
	}
	p.engineMu.Lock()
	defer p.engineMu.Unlock()
	CppSceneStreamFree(h.s)
	// The stream borrowed the registry: free it only after the stream.
	p.freeSpeakerRegistry(h.reg)
}

// sceneFeed runs one scene-stream feed (or the is_last flush) under
// engineMu and returns the parsed document. Before touching the C side it
// re-checks that p.diarCtx/p.tagCtx still match what the stream was begun
// with: Free() can run between the caller's ASR feed and this call (both
// take engineMu individually, never for a session's lifetime, so nothing
// blocks a concurrent Free()) and free the very model the stream borrows.
// A mismatch returns ModelNotLoaded without making the C call; last_error is
// otherwise stream-scoped (parakeet_capi_scene_stream_last_error), read
// under the same lock as the failing call.
func (p *ParakeetCpp) sceneFeed(h sceneStreamHandle, pcm []float32, isLast bool) (sceneFeedJSON, error) {
	p.engineMu.Lock()
	defer p.engineMu.Unlock()

	if p.diarCtx != h.diar || p.tagCtx != h.tag || (h.spk != 0 && p.spkCtx != h.spk) {
		// A plain stream (h.spk == 0) never borrows the speaker model, so a
		// speaker model loaded or freed meanwhile does not concern it.
		return sceneFeedJSON{}, grpcerrors.ModelNotLoaded("parakeet-cpp")
	}

	var last int32
	if isLast {
		last = 1
	}
	var ptr *float32
	if len(pcm) > 0 {
		ptr = &pcm[0]
	}
	ret := CppSceneStreamFeedJSON(h.s, ptr, int32(len(pcm)), last)
	if ret == 0 {
		msg := ""
		if CppSceneStreamLastError != nil {
			msg = CppSceneStreamLastError(h.s)
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
	translateNames(doc.Names, h.names)
	return doc, nil
}

// feedSlicesScene mirrors driver.go's feedSlices but also feeds the same pcm
// slice to an optional companion scene stream right after each ASR slice, so
// the live path's speaker/sound events stay time-aligned with the ASR decode
// increments. scene.s == 0 disables scene feeding for this call (no
// companions, or a previous scene feed already disabled it this session).
//
// The ASR result is emitted immediately after the ASR feed — the same
// response contents/timing a no-companion session would produce — before the
// scene feed for that slice runs, so a companion model never adds scene
// compute latency in front of the ASR delta/<EOU> that drives realtime turn
// detection. Any closed speakers/sounds from the scene feed are emitted
// afterward as their own response, so a slice with both produces two
// responses, ASR first.
//
// A scene feed failure degrades gracefully rather than aborting live
// transcription over a secondary feature: it frees the broken stream, warns
// once, and zeroes the handle so the caller carries the ASR-only session
// forward. Returns the (possibly now-zeroed) scene handle plus the
// cumulative ASR and scene wall time this call spent in feedChunk/sceneFeed,
// for the caller's lag log line.
func (p *ParakeetCpp) feedSlicesScene(ctx context.Context, stream uintptr, scene sceneStreamHandle, pcm []float32, onFeed func(streamFeedResult, sceneFeedJSON) error) (sceneStreamHandle, time.Duration, time.Duration, error) {
	var asrWall, sceneWall time.Duration
	for off := 0; off < len(pcm); off += streamChunkSamples {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return scene, asrWall, sceneWall, status.Error(codes.Canceled, "transcription cancelled")
			}
		}
		end := min(off+streamChunkSamples, len(pcm))
		chunk := pcm[off:end]

		asrStart := time.Now()
		res, err := p.feedChunk(stream, chunk, false)
		asrWall += time.Since(asrStart)
		if err != nil {
			return scene, asrWall, sceneWall, err
		}
		if err := onFeed(res, sceneFeedJSON{}); err != nil {
			return scene, asrWall, sceneWall, err
		}

		if scene.s == 0 {
			continue
		}
		sceneStart := time.Now()
		sceneDoc, serr := p.sceneFeed(scene, chunk, false)
		sceneWall += time.Since(sceneStart)
		if serr != nil {
			xlog.Warn("parakeet-cpp: live scene feed failed; disabling speaker/sound events for this session",
				"err", serr)
			p.sceneFree(scene)
			scene = sceneStreamHandle{}
			continue
		}
		if err := onFeed(streamFeedResult{}, sceneDoc); err != nil {
			return scene, asrWall, sceneWall, err
		}
	}
	return scene, asrWall, sceneWall, nil
}

// liveSpeakersToProto maps a scene feed document's closed "speakers" into
// TranscriptLiveResponse.speakers (stream-relative nanoseconds). Reuses
// diarize.go's speakerLabel so the live path renders speaker indices the
// same way the offline Diarize RPC does.
//
// names is the feed document's "names" map; each segment takes its slot's
// current name, empty if the slot was not yet identified when it closed.
func liveSpeakersToProto(speakers []sceneSpeakerJSON, names map[string]speakerNameJSON) []*pb.LiveSpeakerSegment {
	if len(speakers) == 0 {
		return nil
	}
	out := make([]*pb.LiveSpeakerSegment, len(speakers))
	for i, s := range speakers {
		name, _ := nameFor(names, s.Speaker)
		out[i] = &pb.LiveSpeakerSegment{
			Speaker: speakerLabel(s.Speaker),
			Name:    name,
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
