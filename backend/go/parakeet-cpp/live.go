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
//     FinalResult carries the full transcript, segments, and whether the
//     decode ended on an utterance boundary.
//
// Engine access is serialized per C call (streamBegin/streamFeed*/streamFree
// take engineMu internally), never for the session lifetime — unary
// transcription keeps flowing between feeds.
func (p *ParakeetCpp) AudioTranscriptionLive(in <-chan *pb.TranscriptLiveRequest, out chan<- *pb.TranscriptLiveResponse) error {
	defer close(out)

	if p.ctxPtr == 0 {
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

	out <- &pb.TranscriptLiveResponse{Ready: true}

	var (
		full     strings.Builder
		seg      streamSegmenter
		finalEou bool
		fedSecs  float64

		// behindSec accumulates how far decode wall time has fallen behind
		// the audio it was fed. A live caller feeds in real time, so a
		// persistent positive backlog means every downstream signal —
		// including the <EOU> the turn detector waits on — arrives that many
		// seconds late. Warned once per session; reset by a Config reset.
		behindSec    float64
		behindWarned bool
	)

	// process forwards one feed document and folds it into the final-result
	// accumulators. finalEou tracks whether the decode ENDED on an utterance
	// boundary: an <EOU> re-arms it; later output or a backchannel <EOB>
	// clears it.
	process := func(doc streamFeedJSON) {
		if doc.Text != "" {
			full.WriteString(doc.Text)
		}
		seg.add(doc)
		if doc.Eou != 0 {
			finalEou = true
		} else if doc.Eob != 0 || doc.Text != "" || len(doc.Words) > 0 {
			finalEou = false
		}
		if doc.Text != "" || doc.Eou != 0 || doc.Eob != 0 || len(doc.Words) > 0 {
			out <- &pb.TranscriptLiveResponse{
				Delta: doc.Text,
				Eou:   doc.Eou != 0,
				Eob:   doc.Eob != 0,
				Words: liveWordsToProto(doc.Words),
			}
		}
	}

	feed := func(pcm []float32, finalize bool) error {
		if CppStreamFeedJSON != nil {
			doc, err := p.streamFeedDoc(stream, pcm, finalize)
			if err != nil {
				return err
			}
			process(doc)
			return nil
		}
		delta, eou, eob, err := p.streamFeedText(stream, pcm, finalize)
		if err != nil {
			return err
		}
		doc := streamFeedJSON{Text: delta}
		if eou {
			doc.Eou = 1
		}
		if eob {
			doc.Eob = 1
		}
		process(doc)
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
			seg = streamSegmenter{}
			finalEou = false
			fedSecs = 0
		case *pb.TranscriptLiveRequest_Audio:
			pcm := payload.Audio.GetPcm()
			audioSec := float64(len(pcm)) / liveSampleRate
			fedSecs += audioSec
			start := time.Now()
			// Slice large feeds so each engineMu hold stays short and unary
			// requests interleave fairly; the C session buffers internally.
			for off := 0; off < len(pcm); off += streamChunkSamples {
				end := min(off+streamChunkSamples, len(pcm))
				if err := feed(pcm[off:end], false); err != nil {
					return err
				}
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

	// Send side closed: flush the streaming tail and emit the final result.
	if err := feed(nil, true); err != nil {
		return err
	}
	seg.flush() // close a trailing utterance that never saw an EOU

	text := strings.TrimSpace(full.String())
	segments := seg.segments()
	if len(segments) == 0 && text != "" {
		segments = append(segments, &pb.TranscriptSegment{Id: 0, Text: text})
	}
	out <- &pb.TranscriptLiveResponse{
		FinalResult: &pb.TranscriptResult{
			Text:     text,
			Segments: segments,
			Duration: float32(fedSecs),
			Eou:      finalEou,
		},
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
