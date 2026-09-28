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
		return grpcerrors.ModelNotLoaded("parakeet-cpp")
	}
	if p.isDiarModel {
		return errDiarModelNoASR
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

	// Speaker labels (diarize.go): a low-latency diarization stream runs next
	// to the ASR stream when a diar_model is attached, unless the session
	// opts out with params diarize=false.
	var diar *liveDiarizer
	var allWords []transcriptWord
	startDiar := func(c *pb.TranscriptLiveConfig) error {
		diar.free()
		diar, allWords = nil, nil
		if p.diarCtx == 0 || !liveDiarizationAvailable() || c.GetParams()["diarize"] == "false" {
			return nil
		}
		var err error
		diar, err = p.newLiveDiarizer(p.liveDiarLatency)
		return err
	}
	if err := startDiar(cfg); err != nil {
		return err
	}
	defer func() { diar.free() }()

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

	// emit forwards one decode increment: it streams the per-feed tokens the
	// realtime turn detector consumes (delta/eou/eob/words) and accumulates the
	// running transcript for the closing FinalResult. No segmentation or
	// boundary latch here — the live consumer reads only the streamed tokens
	// and the final Text; per-utterance segments and the terminal <EOU> flag
	// are an offline-path concern (see AudioTranscriptionStream / boundary.go).
	emit := func(r streamFeedResult) error {
		if r.Delta != "" {
			full.WriteString(r.Delta)
		}
		if r.Delta != "" || r.Eou || r.Eob || len(r.Words) > 0 {
			words := liveWordsToProto(r.Words)
			if diar != nil && len(r.Words) > 0 {
				allWords = append(allWords, r.Words...)
				for i, spk := range diar.speakersFor(r.Words) {
					words[i].Speaker = speakerLabel(spk)
				}
			}
			out <- &pb.TranscriptLiveResponse{
				Delta: r.Delta,
				Eou:   r.Eou,
				Eob:   r.Eob,
				Words: words,
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
			full.Reset()
			fedSecs = 0
			if err := startDiar(payload.Config); err != nil {
				return err
			}
		case *pb.TranscriptLiveRequest_Audio:
			pcm := payload.Audio.GetPcm()
			audioSec := float64(len(pcm)) / liveSampleRate
			fedSecs += audioSec
			start := time.Now()
			// Diarize first so the words this feed finalizes can be labelled.
			if diar != nil {
				if err := diar.feed(pcm, false); err != nil {
					return err
				}
			}
			// nil ctx: a live session is bounded by this request channel, not a
			// context — cancellation is the caller closing the stream.
			if err := p.feedSlices(nil, stream, pcm, emit); err != nil {
				return err
			}
			wallSec := time.Since(start).Seconds()
			behindSec += wallSec - audioSec
			if behindSec < 0 {
				behindSec = 0
			}
			xlog.Debug("parakeet-cpp: live feed",
				"audio_ms", int(audioSec*1000), "wall_ms", int(wallSec*1000),
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
	// The live FinalResult carries Text — the authoritative full-turn
	// transcript the realtime core commits — plus, when diarizing, one segment
	// per speaker turn. Duration and the terminal <EOU> flag are not produced
	// on the live path.
	if err := p.flushTail(stream, emit); err != nil {
		return err
	}
	final := &pb.TranscriptResult{Text: strings.TrimSpace(full.String())}
	// With speakers, the final result also carries one segment per speaker
	// turn, relabelled now that diarization has seen the whole session.
	if diar != nil && len(allWords) > 0 {
		if err := diar.feed(nil, true); err != nil {
			return err
		}
		final.Segments = speakerTurns(allWords, assignSpeakers(allWords, diar.closed))
	}
	out <- &pb.TranscriptLiveResponse{FinalResult: final}
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
