package main

import (
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// liveSampleRate is the only PCM rate the parakeet C streaming API accepts.
const liveSampleRate = 16000

// AudioTranscriptionLive drives one cache-aware streaming session over audio
// fed incrementally by the caller (the realtime API's semantic_vad turn
// detection). Contract:
//
//   - the first request must carry a Config; a Config mid-stream resets the
//     decode session (free + begin) and drops accumulated transcript state;
//   - a Ready ack is sent right after a successful stream_begin so callers
//     can degrade synchronously when the model has no streaming support
//     (LiveTranscriptionUnsupported, codes.Unimplemented);
//   - every feed that produced output is forwarded as {delta, eou, words};
//     the <EOU>/<EOB> flag is the model's own utterance boundary and the
//     decoder auto-resets after it, so one session spans many utterances;
//   - closing the send side finalizes: the held-back tail chunk is flushed
//     (the last ~2 encoder frames of words only appear here) and a terminal
//     FinalResult carries the full transcript Text only. Per-utterance
//     segments, duration, and the terminal <EOU> flag are NOT produced here —
//     the realtime core consumes the streamed per-feed tokens and the final
//     Text; those batch fields are the file path's concern (see
//     AudioTranscriptionStream).
//
// Engine access is serialized per C call (streamBegin/streamFeed*/streamFree
// take engineMu internally), never for the session lifetime — unary
// transcription keeps flowing between feeds.
func (p *ParakeetCpp) AudioTranscriptionLive(in <-chan *pb.TranscriptLiveRequest, out chan<- *pb.TranscriptLiveResponse) error {
	defer close(out)

	if p.ctxPtr == 0 {
		if err := p.notASRError(); err != nil {
			return err
		}
		return grpcerrors.ModelNotLoaded("parakeet-cpp")
	}

	first, ok := <-in
	if !ok {
		return nil // caller closed without sending anything
	}
	cfg := first.GetConfig()
	if cfg == nil {
		return status.Error(codes.InvalidArgument, "parakeet-cpp: first live message must carry a config")
	}
	if err := validateLiveConfig(cfg); err != nil {
		return err
	}

	stream, err := p.streamBegin(cfg.GetLanguage())
	if err != nil {
		return err
	}
	if stream == 0 {
		return grpcerrors.LiveTranscriptionUnsupported("parakeet-cpp",
			"loaded model is not a cache-aware streaming model")
	}
	// stream is reassigned on a mid-stream Config reset; free whatever is
	// current when the RPC unwinds.
	defer func() { p.streamFree(stream) }()

	// scene runs a no-ASR scene stream (diarization/sound only) beside the
	// ASR session when a diarization_model:/sound_model: companion is loaded
	// (see scene.go). A zero handle means scene events are disabled: no
	// companions, or the begin/a later feed call failed (logged below / in
	// feedSlicesScene), in which case live transcription continues ASR-only.
	// Reassigned on a mid-stream Config reset alongside stream, which also
	// brings back a scene stream the session had disabled after an earlier
	// scene error.
	var scene sceneStreamHandle
	if p.sceneWanted() {
		scene = p.sceneBegin()
		if scene.s == 0 {
			xlog.Warn("parakeet-cpp: scene stream begin failed; live continues without speaker/sound events")
		}
	}
	defer func() { p.sceneFree(scene) }()

	out <- &pb.TranscriptLiveResponse{Ready: true}

	var (
		full    strings.Builder
		fedSecs float64

		// behindSec accumulates how far decode wall time has fallen behind
		// the audio it was fed. A live caller feeds in real time, so a
		// persistent positive backlog means every downstream signal —
		// including the <EOU> the turn detector waits on — arrives that many
		// seconds late. Warned once per session; reset by a Config reset.
		behindSec    float64
		behindWarned bool
	)

	// emit sends one decode increment as its own response when it carries
	// anything: either the ASR side (delta/eou/eob/words, accumulated into
	// the running transcript for the closing FinalResult) or the scene
	// side (closed speaker/sound events), never both at once — the live
	// audio loop below calls it once for the ASR result right after the ASR
	// feed and, separately, once more for the scene document after the
	// scene feed (see feedSlicesScene), so a slice with both produces two
	// responses, ASR first. No segmentation or boundary latch here — the
	// live consumer reads only the streamed tokens and the final Text;
	// per-utterance segments and the terminal <EOU> flag are an
	// offline-path concern (see AudioTranscriptionStream / boundary.go).
	emit := func(r streamFeedResult, sceneDoc sceneFeedJSON) error {
		if r.Delta != "" {
			full.WriteString(r.Delta)
		}
		speakers := liveSpeakersToProto(sceneDoc.Speakers)
		sounds := liveSoundsToProto(sceneDoc.Sounds)
		if r.Delta != "" || r.Eou || r.Eob || len(r.Words) > 0 || len(speakers) > 0 || len(sounds) > 0 {
			out <- &pb.TranscriptLiveResponse{
				Delta:    r.Delta,
				Eou:      r.Eou,
				Eob:      r.Eob,
				Words:    liveWordsToProto(r.Words),
				Speakers: speakers,
				Sounds:   sounds,
			}
		}
		return nil
	}

	for req := range in {
		switch payload := req.GetPayload().(type) {
		case *pb.TranscriptLiveRequest_Config:
			if err := validateLiveConfig(payload.Config); err != nil {
				return err
			}
			// Reset: a fresh decode session, dropping accumulated state.
			p.streamFree(stream)
			stream, err = p.streamBegin(payload.Config.GetLanguage())
			if err != nil {
				return err
			}
			if stream == 0 {
				return grpcerrors.LiveTranscriptionUnsupported("parakeet-cpp",
					"loaded model is not a cache-aware streaming model")
			}
			// The scene stream is freed and begun again alongside the ASR
			// session, mirroring the reset above.
			p.sceneFree(scene)
			scene = sceneStreamHandle{}
			if p.sceneWanted() {
				scene = p.sceneBegin()
				if scene.s == 0 {
					xlog.Warn("parakeet-cpp: scene stream begin failed; live continues without speaker/sound events")
				}
			}
			full.Reset()
			fedSecs = 0
			behindSec = 0
			behindWarned = false
		case *pb.TranscriptLiveRequest_Audio:
			pcm := payload.Audio.GetPcm()
			audioSec := float64(len(pcm)) / liveSampleRate
			fedSecs += audioSec
			start := time.Now()
			// nil ctx: a live session is bounded by this request channel, not a
			// context — cancellation is the caller closing the stream.
			var asrWall, sceneWall time.Duration
			scene, asrWall, sceneWall, err = p.feedSlicesScene(nil, stream, scene, pcm, emit)
			if err != nil {
				return err
			}
			wallSec := time.Since(start).Seconds()
			behindSec += wallSec - audioSec
			if behindSec < 0 {
				behindSec = 0
			}
			xlog.Debug("parakeet-cpp: live feed",
				"audio_ms", int(audioSec*1000), "wall_ms", int(wallSec*1000),
				"asr_wall_ms", int(asrWall.Seconds()*1000), "scene_wall_ms", int(sceneWall.Seconds()*1000),
				"behind_ms", int(behindSec*1000), "fed_s", fedSecs)
			if behindSec > 1 && !behindWarned {
				behindWarned = true
				xlog.Warn("parakeet-cpp: live decode is falling behind real time; "+
					"end-of-utterance signals will arrive late",
					"behind_s", behindSec, "fed_s", fedSecs)
			}
		}
	}

	// Send side closed: flush the streaming tail and emit the final transcript.
	// The live FinalResult carries only Text — the authoritative full-turn
	// transcript the realtime core commits. Per-utterance segments, duration,
	// and the terminal <EOU> flag are not produced on the live path.
	if err := p.flushTail(stream, func(r streamFeedResult) error {
		return emit(r, sceneFeedJSON{})
	}); err != nil {
		return err
	}
	// The scene stream gets its own is_last flush (it consumes no new audio
	// here, so it is not part of flushTail above); its remaining events go
	// out before the terminal FinalResult, then the stream is released by the
	// deferred sceneFree above.
	if scene.s != 0 {
		doc, err := p.sceneFeed(scene, nil, true)
		if err != nil {
			xlog.Warn("parakeet-cpp: live scene finalize failed", "err", err)
		} else if err := emit(streamFeedResult{}, doc); err != nil {
			return err
		}
	}
	out <- &pb.TranscriptLiveResponse{
		FinalResult: &pb.TranscriptResult{Text: strings.TrimSpace(full.String())},
	}
	return nil
}

func validateLiveConfig(cfg *pb.TranscriptLiveConfig) error {
	if sr := cfg.GetSampleRate(); sr != 0 && sr != liveSampleRate {
		return status.Errorf(codes.InvalidArgument,
			"parakeet-cpp: unsupported live sample_rate %d (only %d)", sr, liveSampleRate)
	}
	return nil
}

func liveWordsToProto(words []transcriptWord) []*pb.TranscriptWord {
	if len(words) == 0 {
		return nil
	}
	out := make([]*pb.TranscriptWord, len(words))
	for i, w := range words {
		out[i] = &pb.TranscriptWord{
			Start: secondsToNanos(w.Start),
			End:   secondsToNanos(w.End),
			Text:  w.W,
		}
	}
	return out
}
