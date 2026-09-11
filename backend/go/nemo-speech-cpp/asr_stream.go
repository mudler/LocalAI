package main

import (
	"context"
	"strings"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// streamChunkSamples is one push into a streaming session. At 16 kHz mono 1600
// samples is 100 ms, short enough that the decoder is polled often enough to
// see an endpoint promptly and short enough that a cancelled request stops
// within one push.
const streamChunkSamples = 1600

// The rates nemo_speech_asr_stream_push_f32 will resample from (asr.h). Outside
// this range the runtime has nothing to do with the audio, and 0 is NOT
// "unknown": it means "these samples are already at the model rate".
const (
	minStreamSampleRate = 8000
	maxStreamSampleRate = 96000
	// TranscriptLiveConfig.sample_rate documents 0 as 16 kHz, which is a
	// different meaning from the C API's 0, so it is resolved before the push.
	defaultLiveSampleRate = 16000
)

// streamResult is one result lifted out of C memory. Everything is copied
// before nemo_speech_asr_result_destroy runs, so a streamResult outlives the
// handle it came from.
type streamResult struct {
	Text  string
	Final bool
	Words []asrWord
}

// asrSession is the streaming half of the ASR C API, narrowed to the four
// entry points the two streaming RPCs use.
//
// It is an interface because there is no NeMo GGUF small enough to keep in the
// tree, so the loops on top of it (chunking, the need-more-audio drain, the
// live config/reset protocol) would otherwise have no test at all. The seam is
// at the ABI, not at the model: a fake session scripts what the C API returns,
// it does not pretend to transcribe anything.
type asrSession interface {
	// push buffers audio. It does not decode; next drives that.
	push(pcm []float32, sampleRate int32) error
	// finish flushes the decoder tail. The end-of-stream final then comes back
	// from next.
	finish() error
	// next pulls one result. ok=false means the decoder needs more audio,
	// which is a pause in the stream and not an error or an end.
	next() (result streamResult, ok bool, err error)
	close()
}

// sessionOpener creates a session for one language. n.openSession is the
// C-backed implementation.
type sessionOpener func(language string) (asrSession, error)

// cSession is the real asrSession, over one nemo_speech_asr_stream.
type cSession struct {
	handle uintptr
}

func (s *cSession) push(pcm []float32, sampleRate int32) error {
	// &pcm[0] panics on an empty slice, and an empty frame is ordinary input
	// from a live caller: it is a keepalive, not audio.
	if len(pcm) == 0 {
		return nil
	}
	if st := ASRStreamPushF32(s.handle, &pcm[0], uint64(len(pcm)), sampleRate); st != 0 {
		return statusErrorf(st, "nemo-speech-cpp: stream push: %s", ASRLastError())
	}
	return nil
}

func (s *cSession) finish() error {
	if st := ASRStreamFinish(s.handle); st != 0 {
		return statusErrorf(st, "nemo-speech-cpp: stream finish: %s", ASRLastError())
	}
	return nil
}

func (s *cSession) next() (streamResult, bool, error) {
	var handle uintptr
	if st := ASRStreamNext(s.handle, &handle); st != 0 {
		return streamResult{}, false, statusErrorf(st, "nemo-speech-cpp: stream next: %s", ASRLastError())
	}
	// OK with a NULL handle is the documented "need more audio". Reading it as
	// an error aborts every stream at the first gap; reading it as "keep
	// pulling" spins forever.
	if handle == 0 {
		return streamResult{}, false, nil
	}
	// Destroyed here rather than by the caller: everything below is copied out
	// of C memory into Go values, so nothing survives that would need it, and
	// a caller that returned early would otherwise leak the result.
	defer ASRResultDestroy(handle)

	return streamResult{
		Text:  ASRResultTranscript(handle, 0),
		Final: ASRResultIsFinal(handle),
		Words: extractWords(handle),
	}, true, nil
}

func (s *cSession) close() { ASRStreamClose(s.handle) }

// openSession starts a streaming recognition on the loaded recognizer.
//
// The caller must hold engineMu.
//
// nemo_speech_asr_streaming_recognize copies the options (src/asr/c_api.cpp
// to_options) and keeps no pointer into them, so the language buffer only has
// to stay pinned across this call, exactly as in loadASR.
func (n *NemoSpeech) openSession(language string) (asrSession, error) {
	// A per-request language wins over the model-level default; both may be
	// empty, which the runtime reads as auto/model default.
	if language == "" {
		language = n.opts.languageCode
	}
	langP, freeLang := cstr(language)
	defer freeLang()

	opts := ASRRecognitionOptionsDef()
	opts.LanguageCode = langP
	// Segments and the live word list are built out of word offsets, so they
	// are always asked for.
	opts.EnableWordTimeOffsets = true
	// Keyed on the recognizer owning a diar model rather than on the request:
	// asr.h documents that asking a recognizer created without one for
	// diarization fails with INVALID_ARGUMENT.
	opts.EnableSpeakerDiarization = n.opts.diarModel != ""
	// interim_results is left off deliberately. The runtime emits interims from
	// next() regardless of it, and they are filtered here rather than
	// forwarded: see streamPCM's emit for why the wire contract cannot carry
	// them.

	var handle uintptr
	// #nosec G103 -- opts is a local POD struct borrowed for this call only, and
	// its one uintptr member (LanguageCode) is the cstr allocation pinned by the
	// deferred freeLang above. to_options copies the struct, so nothing here
	// outlives the call.
	if st := ASRStreamingRecognize(n.recognizer, unsafe.Pointer(&opts), &handle); st != 0 {
		return nil, statusErrorf(st, "nemo-speech-cpp: streaming recognize: %s", ASRLastError())
	}
	xlog.Debug("nemo-speech-cpp: streaming session open", "language", language)
	return &cSession{handle: handle}, nil
}

// chunkPCM slices pcm into fixed-size chunks, leaving the final chunk short
// rather than padding it: silence padding would push audio the caller never
// sent through the encoder and shift the tail word timings.
func chunkPCM(pcm []float32, size int) [][]float32 {
	if len(pcm) == 0 {
		return nil
	}
	out := make([][]float32, 0, (len(pcm)+size-1)/size)
	for off := 0; off < len(pcm); off += size {
		out = append(out, pcm[off:min(off+size, len(pcm))])
	}
	return out
}

// drain pulls every result the session currently has, handing each to emit.
// It returns when the session reports it needs more audio, which is the loop's
// only terminating condition.
func drain(sess asrSession, emit func(streamResult) error) error {
	for {
		r, ok, err := sess.next()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := emit(r); err != nil {
			return err
		}
	}
}

// streamPCM drives one whole clip through an open session, emitting each
// finalized utterance as a delta and closing with the assembled result.
//
// Only finals become deltas, and the reason is the wire contract:
// TranscriptStreamResponse.delta is newly-FINALIZED text that consumers
// CONCATENATE (core/http/endpoints/openai/transcription.go, and the realtime
// semantic-VAD path). An interim is the decoder's running hypothesis for the
// utterance in flight, so forwarding "he", "hell", "hello", "Hello." would
// assemble to "hehellhelloHello." rather than to the transcript. That the
// runtime also postprocesses finals only (build_result_ in
// src/asr/recognizer.cpp runs ITN and strip_formatting on the final, so it
// rewrites rather than extends the interim) means there is no diffing trick
// that would rescue them either.
//
// The cost is that the first delta of an utterance arrives at its endpoint
// rather than mid-word.
func streamPCM(ctx context.Context, sess asrSession, pcm []float32, sampleRate int32, wantWords bool, results chan<- *pb.TranscriptStreamResponse) error {
	if len(pcm) == 0 {
		return status.Error(codes.InvalidArgument, "nemo-speech-cpp: empty audio")
	}

	var (
		full     strings.Builder
		segments []*pb.TranscriptSegment
		// sawEndpoint records a final that arrived before the tail flush, i.e.
		// a real endpoint rather than the end of the file.
		sawEndpoint bool
		flushing    bool
		tailText    string
	)

	emit := func(r streamResult) error {
		if !r.Final {
			return nil
		}
		if flushing {
			tailText += r.Text
		} else {
			sawEndpoint = true
		}
		if r.Text == "" {
			return nil
		}

		// The separator is part of the delta, not added when assembling the
		// final text, so concatenating the deltas reproduces FinalResult.Text
		// exactly. Utterance transcripts carry no leading or trailing space of
		// their own (the runner clears its buffer at each endpoint).
		delta := r.Text
		if full.Len() > 0 {
			delta = " " + delta
		}
		full.WriteString(delta)

		// One segment run per utterance, renumbered into the running sequence.
		// wordsToSegments splits a run further on a speaker change, so a
		// diarized utterance contributes one segment per turn.
		segs := wordsToSegments(r.Words, wantWords)
		if len(segs) == 0 {
			// Word offsets were requested but a decoder head may still return
			// none; a segment carrying just the text beats dropping it.
			segs = []*pb.TranscriptSegment{{Text: r.Text}}
		}
		for _, s := range segs {
			// #nosec G115 -- TranscriptSegment.Id is int32 on the wire, and
			// segments holds one entry per speaker run per finalized utterance of
			// a single request, which exhausts memory long before it reaches 2^31.
			s.Id = int32(len(segments))
			segments = append(segments, s)
		}

		results <- &pb.TranscriptStreamResponse{Delta: delta}
		return nil
	}

	for _, chunk := range chunkPCM(pcm, streamChunkSamples) {
		// The RPC body holds engineMu for the whole stream, so Free waits on
		// it. Without this check a client that disconnected mid-file would pin
		// the model against unload until the whole clip had been pushed.
		if err := ctx.Err(); err != nil {
			return status.Error(codes.Canceled, "nemo-speech-cpp: transcription cancelled")
		}
		if err := sess.push(chunk, sampleRate); err != nil {
			return err
		}
		if err := drain(sess, emit); err != nil {
			return err
		}
	}

	flushing = true
	if err := sess.finish(); err != nil {
		return err
	}
	if err := drain(sess, emit); err != nil {
		return err
	}

	final := &pb.TranscriptResult{
		Text:     full.String(),
		Segments: segments,
		// The tail flush returns whatever the decoder was still holding.
		// Nothing held back after at least one endpoint means the last
		// endpoint consumed the audio, which is what "the clip ended on an
		// utterance boundary" means here. Text coming back means it ended
		// mid-utterance.
		Eou: sawEndpoint && tailText == "",
	}
	if sampleRate > 0 {
		final.Duration = float32(len(pcm)) / float32(sampleRate)
	}
	results <- &pb.TranscriptStreamResponse{FinalResult: final}
	return nil
}

// runLive drives one bidirectional live session. The protocol is the one
// documented on the RPC in backend.proto: a Config first, a ready ack once the
// session is open, deltas as utterances finalize, and a terminal result when
// the caller closes its send side.
//
// There is no context here on purpose. The gRPC host closes `in` when the
// stream context is cancelled (pkg/grpc/server.go's recv pump), so ranging
// over it is what stops this loop, and that is also what releases engineMu for
// a waiting Free.
func runLive(open sessionOpener, in <-chan *pb.TranscriptLiveRequest, out chan<- *pb.TranscriptLiveResponse) error {
	first, ok := <-in
	if !ok {
		// The caller closed without sending anything. Nothing was opened, so
		// there is nothing to report.
		return nil
	}
	cfg := first.GetConfig()
	if cfg == nil {
		return status.Error(codes.InvalidArgument,
			"nemo-speech-cpp: the first live message must carry a config")
	}
	rate, err := liveSampleRate(cfg)
	if err != nil {
		return err
	}

	sess, err := open(cfg.GetLanguage())
	if err != nil {
		return err
	}
	// A mid-stream config replaces sess, so this closes whichever session is
	// current when the RPC unwinds.
	defer func() { sess.close() }()

	// Callers block on the first Recv waiting for this and degrade to
	// non-live transcription when it does not arrive, so it goes out before
	// any audio is read.
	out <- &pb.TranscriptLiveResponse{Ready: true}

	var (
		full     strings.Builder
		flushing bool
	)
	emit := func(r streamResult) error {
		// Finals only, for the same reason as streamPCM: an interim is a
		// hypothesis the final rewrites, and delta is newly-finalized text.
		if !r.Final || (r.Text == "" && len(r.Words) == 0) {
			return nil
		}

		// The separator goes INTO the delta, exactly as in streamPCM, because
		// the live consumer is the one that actually concatenates: the realtime
		// semantic-VAD path joins the accumulated deltas with the empty string
		// and only clears them at a turn reset, never at an endpoint. Adding
		// the space when assembling the terminal text instead would make the
		// running caption read "one.two." while the committed transcript read
		// "one. two.".
		delta := r.Text
		if delta != "" && full.Len() > 0 {
			delta = " " + delta
		}
		full.WriteString(delta)

		out <- &pb.TranscriptLiveResponse{
			Delta: delta,
			// A final that arrives while audio is still coming IS the model's
			// endpoint: the decoder resets its utterance there and the next one
			// starts fresh, which is the turn boundary the realtime detector
			// waits on. The final that comes back from the tail flush is the
			// end of the STREAM, not a user yielding a turn, so it carries no
			// eou even though the send side has already closed.
			Eou:   !flushing,
			Words: wordsToProto(r.Words),
		}
		return nil
	}

	for req := range in {
		switch payload := req.GetPayload().(type) {
		case *pb.TranscriptLiveRequest_Config:
			// A rate cannot change inside a stream (asr.h) and the decoder
			// keeps utterance state, so a reconfigure has to be a fresh
			// session rather than a reconfigured one.
			newRate, err := liveSampleRate(payload.Config)
			if err != nil {
				return err
			}
			// Opened before the old one is closed so a failure here leaves a
			// live session for the deferred close, not a dangling handle.
			next, err := open(payload.Config.GetLanguage())
			if err != nil {
				return err
			}
			sess.close()
			sess, rate = next, newRate
			full.Reset()
		case *pb.TranscriptLiveRequest_Audio:
			pcm := payload.Audio.GetPcm()
			if len(pcm) == 0 {
				continue
			}
			if err := sess.push(pcm, rate); err != nil {
				return err
			}
			if err := drain(sess, emit); err != nil {
				return err
			}
		}
	}

	// Send side closed: flush the tail and emit the terminal result. Like the
	// other backends' live path this carries Text only; per-utterance segments
	// and the duration are the file path's concern.
	flushing = true
	if err := sess.finish(); err != nil {
		return err
	}
	if err := drain(sess, emit); err != nil {
		return err
	}
	// Not trimmed: the terminal text is the verbatim concatenation of the
	// deltas, which is the invariant the concatenating consumers rely on. The
	// first delta never carries the separator, so there is no leading space to
	// trim off in the first place.
	out <- &pb.TranscriptLiveResponse{
		FinalResult: &pb.TranscriptResult{Text: full.String()},
	}
	return nil
}

// liveSampleRate resolves TranscriptLiveConfig.sample_rate to the rate the C
// API is given. The proto's 0 means 16 kHz; the C API's 0 means "already at the
// model rate", so the two cannot be forwarded to each other.
func liveSampleRate(cfg *pb.TranscriptLiveConfig) (int32, error) {
	rate := cfg.GetSampleRate()
	if rate == 0 {
		return defaultLiveSampleRate, nil
	}
	if rate < minStreamSampleRate || rate > maxStreamSampleRate {
		return 0, status.Errorf(codes.InvalidArgument,
			"nemo-speech-cpp: unsupported live sample_rate %d (accepted: 0 or %d-%d Hz)",
			rate, minStreamSampleRate, maxStreamSampleRate)
	}
	return rate, nil
}

// wordsToProto converts decoded words to the wire form. TranscriptWord.start
// and .end are int64 nanoseconds; the runtime reports milliseconds.
func wordsToProto(words []asrWord) []*pb.TranscriptWord {
	if len(words) == 0 {
		return nil
	}
	out := make([]*pb.TranscriptWord, len(words))
	for i, w := range words {
		out[i] = &pb.TranscriptWord{
			Text:  w.Text,
			Start: msToNanos(w.Start),
			End:   msToNanos(w.End),
		}
	}
	return out
}

// AudioTranscriptionStream decodes the audio at req.Dst through the streaming
// recognizer, emitting each finalized utterance as it lands.
//
// The body runs inside withEngine for the reason documented on withEngine, and
// that holds engineMu for the whole stream: Free waits rather than destroying
// the recognizer under a half-finished stream. streamPCM honours ctx so the
// wait is bounded by the client's disconnect rather than by its silence.
func (n *NemoSpeech) AudioTranscriptionStream(ctx context.Context, req *pb.TranscriptRequest, results chan *pb.TranscriptStreamResponse) error {
	// The host ranges over this channel and only returns once it closes, so
	// every path out of here, rejection included, has to close it.
	defer close(results)

	return n.withEngine(familyASR, func() error {
		return n.transcribeStream(ctx, req, results)
	})
}

// transcribeStream is AudioTranscriptionStream's body. The caller must hold
// engineMu.
func (n *NemoSpeech) transcribeStream(ctx context.Context, req *pb.TranscriptRequest, results chan<- *pb.TranscriptStreamResponse) error {
	if req.GetDst() == "" {
		return status.Error(codes.InvalidArgument,
			"nemo-speech-cpp: TranscriptRequest.dst (audio path) is required")
	}
	// Checked before the decode so a client that has already gone away does
	// not pay for an ffmpeg run, and so a cancellation is never reported as a
	// broken file.
	if err := ctx.Err(); err != nil {
		return status.Error(codes.Canceled, "nemo-speech-cpp: transcription cancelled")
	}

	pcm, sampleRate, err := decodeAudioMono16k(req.GetDst())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "nemo-speech-cpp: read audio: %v", err)
	}
	// Before the session is opened, for the same reason as the offline path:
	// there is no transcript to be had from zero samples, and opening a stream
	// only to close it again asks the runtime to allocate decoder state for
	// nothing.
	if len(pcm) == 0 {
		return status.Error(codes.InvalidArgument, "nemo-speech-cpp: empty audio")
	}

	sess, err := n.openSession(req.GetLanguage())
	if err != nil {
		return err
	}
	defer sess.close()

	return streamPCM(ctx, sess, pcm, sampleRate,
		wordsRequested(req.GetTimestampGranularities()), results)
}

// AudioTranscriptionLive serves the bidirectional live RPC over one streaming
// session. See runLive for the protocol and withEngine for the locking.
func (n *NemoSpeech) AudioTranscriptionLive(in <-chan *pb.TranscriptLiveRequest, out chan<- *pb.TranscriptLiveResponse) error {
	defer close(out)

	return n.withEngine(familyASR, func() error {
		return runLive(n.openSession, in, out)
	})
}
