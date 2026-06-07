package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/go-audio/wav"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// purego-bound entry points from libparakeet.so. Names match
// parakeet_capi.h exactly so a `nm libparakeet.so | grep parakeet_capi`
// is enough to spot drift.
//
// Functions that return char* are declared as uintptr so we can call
// parakeet_capi_free_string on the same pointer after copying, the
// C-API contract is "caller owns and must free the returned buffer".
var (
	CppAbiVersion         func() int32
	CppLoad               func(ggufPath string) uintptr
	CppFree               func(ctx uintptr)
	CppTranscribePath     func(ctx uintptr, wavPath string, decoder int32) uintptr
	CppTranscribePathJSON func(ctx uintptr, wavPath string, decoder int32) uintptr
	CppFreeString         func(s uintptr)
	CppLastError          func(ctx uintptr) string

	// Batched JSON transcription: takes a concatenated float buffer of clips
	// plus their per-clip sample counts (sum(nSamples)==len(samplesConcat))
	// and returns a malloc'd char* JSON ARRAY of per-clip {"text","words",
	// "tokens"} objects (uintptr, freed via CppFreeString). purego passes the
	// Go slices as the base pointer of their backing array (kept alive for the
	// call), matching the CppStreamFeed pcm []float32 binding pattern; the C
	// side reads them as const float*/const int*.
	CppTranscribePcmBatchJSON func(ctx uintptr, samplesConcat []float32, nSamples []int32, nClips int32, sampleRate int32, decoder int32) uintptr

	// CppTranscribePcmBatchJSONLang is the multilingual variant of the batched
	// JSON entry point: identical, plus a trailing target_lang. "" (the model
	// default, "auto") is passed for non-prompt models, which ignore it; an
	// unknown locale on a prompt model returns 0 and sets last_error. Present
	// only in newer libparakeet.so; nil falls back to CppTranscribePcmBatchJSON.
	CppTranscribePcmBatchJSONLang func(ctx uintptr, samplesConcat []float32, nSamples []int32, nClips int32, sampleRate int32, decoder int32, targetLang string) uintptr

	// Cache-aware streaming (RNN-T) entry points. stream_begin returns 0 for
	// non-streaming models. feed/finalize return a malloc'd char* (uintptr,
	// freed via CppFreeString); feed writes 1 to *eouOut on an <EOU>/<EOB>.
	CppStreamBegin    func(ctx uintptr) uintptr
	CppStreamFeed     func(s uintptr, pcm []float32, nSamples int32, eouOut unsafe.Pointer) uintptr
	CppStreamFinalize func(s uintptr) uintptr
	CppStreamFree     func(s uintptr)

	// CppStreamBeginLang is the multilingual variant of stream_begin: identical,
	// plus a trailing target_lang ("" means the model default). Present only in
	// newer libparakeet.so; nil falls back to CppStreamBegin.
	CppStreamBeginLang func(ctx uintptr, targetLang string) uintptr

	// Streaming JSON variants (ABI v4): feed/finalize returning a malloc'd char*
	// JSON document {text,eou,frame_sec,words} (uintptr, freed via CppFreeString)
	// so streaming segments can carry per-word timestamps. Present only in newer
	// libparakeet.so; nil falls back to the text-only CppStreamFeed/Finalize path.
	CppStreamFeedJSON     func(s uintptr, pcm []float32, nSamples int32) uintptr
	CppStreamFinalizeJSON func(s uintptr) uintptr
)

// streamChunkSamples is how much 16 kHz mono PCM we hand to stream_feed per
// call (1 s). The session buffers internally and decodes once a full
// cache-aware encoder chunk is available, so this only bounds how often we
// poll for newly-finalized text, not the model's actual chunk size.
const streamChunkSamples = 16000

// transcriptJSON mirrors the document returned by
// parakeet_capi_transcribe_path_json (see parakeet_capi.h):
//
//	{"text":"...",
//	 "words":[{"w":"...","start":0.480,"end":0.640,"conf":0.9100}, ...],
//	 "tokens":[{"id":123,"t":0.480,"conf":0.9100}, ...]}
//
// "start"/"end"/"t" are seconds; "conf" is confidence in (0,1].
type transcriptJSON struct {
	Text     string            `json:"text"`
	FrameSec float64           `json:"frame_sec"`
	Words    []transcriptWord  `json:"words"`
	Tokens   []transcriptToken `json:"tokens"`
}

// streamFeedJSON mirrors the document returned by
// parakeet_capi_stream_feed_json / parakeet_capi_stream_finalize_json (ABI v4):
//
//	{"text":"...","eou":0,"frame_sec":0.080000,
//	 "words":[{"w":"...","start":0.480,"end":0.640,"conf":0.9100}, ...]}
//
// "text" is the newly-finalized text since the last call; "eou" is 1 when an
// <EOU>/<EOB> fired this feed; "words" are the words finalized this call with
// absolute (stream-relative) start/end seconds.
type streamFeedJSON struct {
	Text     string           `json:"text"`
	Eou      int              `json:"eou"`
	FrameSec float64          `json:"frame_sec"`
	Words    []transcriptWord `json:"words"`
}

type transcriptWord struct {
	W     string  `json:"w"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Conf  float64 `json:"conf"`
}

type transcriptToken struct {
	ID   int32   `json:"id"`
	T    float64 `json:"t"`
	Conf float64 `json:"conf"`
}

// ParakeetCpp owns a single loaded parakeet_ctx. The C engine is a
// thread-unsafe singleton (mirrors whisper.cpp / vibevoice.cpp). Rather than
// serialize every call through base.SingleThread, we route unary
// transcription through an in-process batcher (its sole dispatcher goroutine
// is the only caller of the engine on that path) and guard the shared engine
// with engineMu so a streaming session and a batched-unary dispatch never
// touch it concurrently.
type ParakeetCpp struct {
	base.Base
	ctxPtr   uintptr
	engineMu sync.Mutex // sole guard of the one C engine (dispatcher + streaming)
	bat      *batcher
	batStop  chan struct{}
	// segmentGapFrames is NeMo's segment_gap_threshold in ENCODER FRAMES (model
	// YAML option, default 0=off). When >0 it adds NeMo's silence-gap split on
	// top of the punctuation split; converted to seconds via the JSON frame_sec.
	segmentGapFrames int
}

// Load is the LocalAI gRPC entry point for LoadModel: it calls
// parakeet_capi_load with the GGUF path and stashes the resulting
// opaque context pointer for AudioTranscription.
func (p *ParakeetCpp) Load(opts *pb.ModelOptions) error {
	if opts.ModelFile == "" {
		return errors.New("parakeet-cpp: ModelFile is required")
	}

	ctx := CppLoad(opts.ModelFile)
	if ctx == 0 {
		// No ctx to ask for last_error (the C-API's last-error buffer
		// lives on the ctx that was never returned). Surface the path
		// so the operator at least knows which load failed.
		return fmt.Errorf("parakeet-cpp: parakeet_capi_load failed for %q", opts.ModelFile)
	}
	p.ctxPtr = ctx

	// Dynamic batching knobs (model YAML options:, key:value form). Batching is
	// OFF by default (batch_max_size:1): each request runs on its own. On GPU,
	// raising batch_max_size coalesces concurrent requests into one batched
	// engine call and improves throughput under load; leave it at 1 on CPU and
	// for low-concurrency setups, where batching only adds latency.
	maxSize := optInt(opts, "batch_max_size", 1)
	maxWaitMs := optInt(opts, "batch_max_wait_ms", 15)
	if maxWaitMs < 0 {
		maxWaitMs = 0
	}

	// NeMo's segment_gap_threshold (encoder frames, default 0=off). Off by
	// default matches NeMo's default (punctuation-only segments); when set it
	// additionally splits segments on inter-word silence (see transcriptResultFromDoc).
	p.segmentGapFrames = optInt(opts, "segment_gap_threshold", 0)
	if CppTranscribePcmBatchJSON != nil {
		p.batStop = make(chan struct{})
		p.bat = newBatcher(maxSize, time.Duration(maxWaitMs)*time.Millisecond, p.runBatch)
		go p.bat.run(p.batStop) // dispatcher runs until Free closes batStop
		if maxSize > 1 {
			xlog.Info("parakeet-cpp: dynamic batching enabled",
				"batch_max_size", maxSize, "batch_max_wait_ms", maxWaitMs)
		} else {
			xlog.Info("parakeet-cpp: dynamic batching off (batch_max_size=1); " +
				"set batch_max_size>1 to coalesce concurrent requests on GPU")
		}
	} else {
		xlog.Info("parakeet-cpp: batched C-API not present in libparakeet.so; " +
			"batching disabled, using per-request transcription")
	}
	return nil
}

// optInt reads an integer model option (key:value form) from ModelOptions,
// returning def when absent or unparseable. The options array carries the
// model YAML's options: entries (see core/config; siblings such as
// acestep-cpp parse the same key:value form via strings.Cut on ":").
func optInt(opts *pb.ModelOptions, key string, def int) int {
	for _, o := range opts.GetOptions() {
		k, v, ok := strings.Cut(o, ":")
		if ok && strings.TrimSpace(k) == key {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
	}
	return def
}

// runBatch is the dispatcher's batch handler and the ONLY caller of the C
// engine on the unary path. It concatenates the batch PCM, calls the batched
// JSON C-API under engineMu, splits the JSON array, and replies to each request.
func (p *ParakeetCpp) runBatch(reqs []*batchRequest) {
	// Observability: the actual coalesced batch size per engine call. Debug-level
	// so it stays silent in normal operation but lets operators confirm/tune batching.
	xlog.Debug("parakeet-cpp: dispatching batch", "size", len(reqs))
	nSamples := make([]int32, len(reqs))
	total := 0
	for i, r := range reqs {
		nSamples[i] = int32(len(r.pcm))
		total += len(r.pcm)
	}
	concat := make([]float32, 0, total)
	for _, r := range reqs {
		concat = append(concat, r.pcm...)
	}
	var dec int32
	if len(reqs) > 0 {
		dec = reqs[0].decoder
	}
	// All requests in a batch share one language (the batcher coalesces only
	// same-language requests), so any element's language describes the batch.
	lang := ""
	if len(reqs) > 0 {
		lang = reqs[0].language
	}
	p.engineMu.Lock()
	var cstr uintptr
	if CppTranscribePcmBatchJSONLang != nil {
		cstr = CppTranscribePcmBatchJSONLang(p.ctxPtr, concat, nSamples, int32(len(reqs)), 16000, dec, lang)
	} else {
		cstr = CppTranscribePcmBatchJSON(p.ctxPtr, concat, nSamples, int32(len(reqs)), 16000, dec)
	}
	p.engineMu.Unlock()
	if cstr == 0 {
		err := fmt.Errorf("parakeet-cpp: batch transcribe failed: %s", CppLastError(p.ctxPtr))
		for _, r := range reqs {
			r.reply <- batchReply{err: err}
		}
		return
	}
	raw := goStringFromCPtr(cstr)
	CppFreeString(cstr)
	var docs []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &docs); err != nil || len(docs) != len(reqs) {
		e := fmt.Errorf("parakeet-cpp: batch json: got %d results for %d reqs (%v)", len(docs), len(reqs), err)
		for _, r := range reqs {
			r.reply <- batchReply{err: e}
		}
		return
	}
	for i, r := range reqs {
		r.reply <- batchReply{json: string(docs[i])}
	}
}

// AudioTranscription decodes the wav at opts.Dst to 16 kHz mono PCM and
// submits it to the in-process batcher, which coalesces concurrent requests
// into a single batched engine call (parakeet_capi_transcribe_pcm_batch_json)
// with the default decoder (decoder=0, which selects the right head per
// architecture: transducer for tdt/rnnt/hybrid, CTC for ctc) and shapes the
// per-word timestamps into a LocalAI TranscriptResult.
//
// Parakeet emits word- and token-level timestamps but no native segment
// boundaries, so we synthesise a single whole-clip segment spanning the first
// word start to the last word end. Word-level timings are attached only when
// the caller opts in via timestamp_granularities=["word"] (matching the
// OpenAI API, whose default is segment-level); token ids always populate
// Segment.Tokens.
//
// translate/diarize/prompt/temperature/threads are not applicable to parakeet
// and are ignored; language is honored on the batched + streaming paths (see
// opts.GetLanguage() below); streaming is handled by AudioTranscriptionStream
// (L2).
func (p *ParakeetCpp) AudioTranscription(ctx context.Context, opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if p.ctxPtr == 0 {
		return pb.TranscriptResult{}, grpcerrors.ModelNotLoaded("parakeet-cpp")
	}
	if opts.Dst == "" {
		return pb.TranscriptResult{}, errors.New("parakeet-cpp: TranscriptRequest.dst (audio path) is required")
	}

	// Fallback when the batched C-API is unavailable: transcribe from a file
	// path (original behavior, no batching). The C library's audio loader only
	// understands 16 kHz mono WAV/PCM, so convert the input first - otherwise
	// any non-WAV upload (MP3, etc.) fails with "failed to load audio". This
	// mirrors what every other audio backend (whisper, crispasr) does via
	// utils.AudioToWav before handing the file to the engine.
	if p.bat == nil {
		converted, cleanup, err := convertToWavMono16k(opts.Dst)
		if err != nil {
			return pb.TranscriptResult{}, err
		}
		defer cleanup()
		cstr := CppTranscribePathJSON(p.ctxPtr, converted, 0)
		if cstr == 0 {
			return pb.TranscriptResult{}, fmt.Errorf("parakeet-cpp: transcribe_path_json failed: %s", CppLastError(p.ctxPtr))
		}
		raw := goStringFromCPtr(cstr)
		CppFreeString(cstr)
		var doc transcriptJSON
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return pb.TranscriptResult{}, fmt.Errorf("parakeet-cpp: decode transcript json: %w", err)
		}
		return transcriptResultFromDoc(doc, opts, p.segmentGapFrames), nil
	}

	// Batched path: decode to PCM, submit to the batcher, wait for this request's
	// JSON element. The dispatcher is the sole engine caller on this path; both
	// sends honour ctx cancellation.
	pcm, _, err := decodeWavMono16k(opts.Dst)
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	rep := make(chan batchReply, 1)
	select {
	case p.bat.submit <- &batchRequest{pcm: pcm, decoder: 0, language: opts.GetLanguage(), reply: rep}:
	case <-ctx.Done():
		return pb.TranscriptResult{}, status.Error(codes.Canceled, "transcription cancelled")
	}
	var res batchReply
	select {
	case res = <-rep:
	case <-ctx.Done():
		return pb.TranscriptResult{}, status.Error(codes.Canceled, "transcription cancelled")
	}
	if res.err != nil {
		return pb.TranscriptResult{}, res.err
	}
	var doc transcriptJSON
	if err := json.Unmarshal([]byte(res.json), &doc); err != nil {
		return pb.TranscriptResult{}, fmt.Errorf("parakeet-cpp: decode transcript json: %w", err)
	}
	return transcriptResultFromDoc(doc, opts, p.segmentGapFrames), nil
}

// segmentSeparators is NeMo's default segment_seperators (sentence-ending
// punctuation). Splitting on these matches NeMo's default segment timestamps.
var segmentSeparators = []rune{'.', '?', '!'}

// transcriptResultFromDoc maps a decoded transcriptJSON to a TranscriptResult,
// grouping words into NeMo-faithful segments (see splitWordsIntoSegments). The
// optional gapFrames (NeMo's segment_gap_threshold, in encoder FRAMES; 0=off)
// additionally splits on inter-word silence; it is converted to a seconds gap
// with the document's frame_sec. Per-segment word timings are attached only when
// the caller requested word granularity; token ids populate each segment's
// Tokens by time-window membership. Shared by the batched and direct paths.
func transcriptResultFromDoc(doc transcriptJSON, opts *pb.TranscriptRequest, gapFrames int) pb.TranscriptResult {
	text := strings.TrimSpace(doc.Text)

	// Frame-unit gap threshold -> seconds (NeMo segment_gap_threshold). 0 = off.
	gapSeconds := 0.0
	if gapFrames > 0 {
		if doc.FrameSec > 0 {
			gapSeconds = float64(gapFrames) * doc.FrameSec
		} else {
			xlog.Warn("parakeet-cpp: segment_gap_threshold set but libparakeet.so " +
				"did not report frame_sec; falling back to punctuation-only segments")
		}
	}

	groups := splitWordsIntoSegments(doc.Words, segmentSeparators, gapSeconds)
	if len(groups) == 0 {
		// No words (edge case): single whole-clip text segment.
		return pb.TranscriptResult{
			Text:     text,
			Segments: []*pb.TranscriptSegment{{Id: 0, Text: text}},
		}
	}

	wantWords := wordsRequested(opts.TimestampGranularities)
	segments := make([]*pb.TranscriptSegment, 0, len(groups))
	for id, group := range groups {
		parts := make([]string, len(group))
		for i, gw := range group {
			parts[i] = gw.W
		}
		seg := &pb.TranscriptSegment{
			Id:     int32(id),
			Start:  secondsToNanos(group[0].Start),
			End:    secondsToNanos(group[len(group)-1].End),
			Text:   strings.TrimSpace(strings.Join(parts, " ")),
			Tokens: tokensInWindow(doc.Tokens, group[0].Start, group[len(group)-1].End),
		}
		if wantWords {
			ws := make([]*pb.TranscriptWord, len(group))
			for i, gw := range group {
				ws[i] = &pb.TranscriptWord{Start: secondsToNanos(gw.Start), End: secondsToNanos(gw.End), Text: gw.W}
			}
			seg.Words = ws
		}
		segments = append(segments, seg)
	}
	return pb.TranscriptResult{Text: text, Segments: segments}
}

// splitWordsIntoSegments groups words into segments exactly as NeMo's
// get_segment_offsets does (nemo/collections/asr/parts/utils/timestamp_utils.py).
// Walking the words, it closes a segment when (1) the gap rule is enabled
// (gapSeconds > 0) and the segment already has words and the gap from the
// previous word's end to this word's start is >= gapSeconds - the current word
// then STARTS a new segment - or, checked only when the gap rule did not apply
// (NeMo's elif), (2) the word ends with (or is) a separator, which closes the
// segment INCLUDING that word. Trailing words flush into a final segment.
// gapSeconds <= 0 disables the gap rule, matching NeMo's default
// segment_gap_threshold=None (punctuation-only segments).
func splitWordsIntoSegments(words []transcriptWord, separators []rune, gapSeconds float64) [][]transcriptWord {
	var segments [][]transcriptWord
	var cur []transcriptWord
	for i, word := range words {
		gapActive := gapSeconds > 0 && len(cur) > 0
		if gapActive && (word.Start-words[i-1].End) >= gapSeconds {
			segments = append(segments, cur)
			cur = []transcriptWord{word}
			continue
		}
		if !gapActive && endsWithSeparator(word.W, separators) {
			cur = append(cur, word)
			segments = append(segments, cur)
			cur = nil
			continue
		}
		cur = append(cur, word)
	}
	if len(cur) > 0 {
		segments = append(segments, cur)
	}
	return segments
}

// endsWithSeparator reports whether w's last rune is in separators (matching
// NeMo's `word[-1] in delims or word in delims`).
func endsWithSeparator(w string, separators []rune) bool {
	r := []rune(strings.TrimSpace(w))
	if len(r) == 0 {
		return false
	}
	last := r[len(r)-1]
	for _, s := range separators {
		if last == s {
			return true
		}
	}
	return false
}

// tokensInWindow returns the ids of tokens whose timestamp t falls in
// [start, end] (inclusive), assigning each token to the segment that spans its
// time. The last segment's end is the last word end, so the final token is
// included.
func tokensInWindow(tokens []transcriptToken, start, end float64) []int32 {
	var ids []int32
	for _, t := range tokens {
		if t.T >= start && t.T <= end {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// streamSegmenter accumulates streaming words into per-utterance segments. EOU
// is the model's own utterance boundary; each closed segment takes its start/end
// from its first/last accumulated word.
type streamSegmenter struct {
	segs   []*pb.TranscriptSegment
	cur    []transcriptWord
	nextID int32
}

func (s *streamSegmenter) add(doc streamFeedJSON) {
	s.cur = append(s.cur, doc.Words...)
	if doc.Eou != 0 {
		s.flush()
	}
}

func (s *streamSegmenter) flush() {
	if len(s.cur) == 0 {
		return
	}
	parts := make([]string, len(s.cur))
	for i, w := range s.cur {
		parts[i] = w.W
	}
	s.segs = append(s.segs, &pb.TranscriptSegment{
		Id:    s.nextID,
		Start: secondsToNanos(s.cur[0].Start),
		End:   secondsToNanos(s.cur[len(s.cur)-1].End),
		Text:  strings.TrimSpace(strings.Join(parts, " ")),
	})
	s.nextID++
	s.cur = nil
}

func (s *streamSegmenter) segments() []*pb.TranscriptSegment { return s.segs }

// wordsRequested reports whether the caller asked for word-level timestamps.
// The OpenAI transcription API gates word timings behind
// timestamp_granularities[] containing "word" and defaults to segment-level
// otherwise; we follow that contract.
func wordsRequested(granularities []string) bool {
	for _, g := range granularities {
		if strings.EqualFold(strings.TrimSpace(g), "word") {
			return true
		}
	}
	return false
}

// secondsToNanos converts the C-API's fractional-second timestamps into the
// int64 nanoseconds LocalAI carries on TranscriptSegment/TranscriptWord, the
// same nanosecond convention the whisper backend uses.
func secondsToNanos(sec float64) int64 {
	return int64(sec * 1e9)
}

// AudioTranscriptionStream drives the cache-aware streaming RNN-T over the
// audio at opts.Dst: it decodes the file to 16 kHz mono PCM, feeds it in
// chunks to parakeet_capi_stream_feed, and emits each newly-finalized text
// run as a TranscriptStreamResponse delta. <EOU>/<EOB> events close the
// current segment; a closing FinalResult carries the full transcript and the
// per-utterance segments.
//
// stream_begin returns 0 for models that are not cache-aware streaming models
// (only e.g. nvidia/parakeet_realtime_eou_120m-v1 qualifies). For those we fall
// back to a single offline transcription emitted as one delta plus a closing
// FinalResult, matching LocalAI's non-streaming streaming contract (and the
// whisper backend), so the streaming endpoint works for every model.
func (p *ParakeetCpp) AudioTranscriptionStream(ctx context.Context, opts *pb.TranscriptRequest, results chan *pb.TranscriptStreamResponse) error {
	defer close(results)

	if p.ctxPtr == 0 {
		return grpcerrors.ModelNotLoaded("parakeet-cpp")
	}
	if opts.Dst == "" {
		return errors.New("parakeet-cpp: TranscriptRequest.dst (audio path) is required")
	}
	if err := ctx.Err(); err != nil {
		return status.Error(codes.Canceled, "transcription cancelled")
	}

	var stream uintptr
	if CppStreamBeginLang != nil {
		stream = CppStreamBeginLang(p.ctxPtr, opts.GetLanguage())
	} else {
		stream = CppStreamBegin(p.ctxPtr)
	}
	if stream == 0 {
		// Not a cache-aware streaming model: run a normal offline
		// transcription and emit it as one delta + a closing final result.
		res, err := p.AudioTranscription(ctx, opts)
		if err != nil {
			return err
		}
		if t := strings.TrimSpace(res.Text); t != "" {
			results <- &pb.TranscriptStreamResponse{Delta: t}
		}
		results <- &pb.TranscriptStreamResponse{FinalResult: &res}
		return nil
	}
	defer CppStreamFree(stream)
	// The C engine is a single shared context: a streaming session and a batched
	// unary dispatch must never touch it at once, so hold engineMu for the whole
	// stream. This lock is intentionally taken AFTER the non-streaming fallback
	// above returns: that fallback goes through AudioTranscription -> the batcher
	// -> runBatch, which itself acquires engineMu, so locking here first would
	// deadlock. Do not hoist this lock above the fallback.
	p.engineMu.Lock()
	defer p.engineMu.Unlock()

	data, duration, err := decodeWavMono16k(opts.Dst)
	if err != nil {
		return err
	}

	// ABI v4: when the streaming JSON entry points are present, drive them so the
	// per-utterance segments carry per-word start/end timestamps. Falls through to
	// the text-only loop below against an older libparakeet.so. Runs under the
	// engineMu already held above.
	if CppStreamFeedJSON != nil {
		return p.streamJSON(ctx, stream, data, duration, results)
	}

	var (
		full     strings.Builder
		segText  strings.Builder
		segments []*pb.TranscriptSegment
		segID    int32
	)

	flushSegment := func() {
		t := strings.TrimSpace(segText.String())
		segText.Reset()
		if t == "" {
			return
		}
		segments = append(segments, &pb.TranscriptSegment{Id: segID, Text: t})
		segID++
	}

	// emitDelta consumes the malloc'd char* returned by feed/finalize: frees
	// it, accumulates the text, and sends a delta when non-empty. A 0 return
	// is an error (vs the "" empty-but-non-NULL no-new-text case).
	emitDelta := func(ret uintptr) error {
		if ret == 0 {
			msg := CppLastError(p.ctxPtr)
			if msg == "" {
				msg = "unknown error"
			}
			return fmt.Errorf("parakeet-cpp: stream feed/finalize failed: %s", msg)
		}
		delta := goStringFromCPtr(ret)
		CppFreeString(ret)
		if delta == "" {
			return nil
		}
		full.WriteString(delta)
		segText.WriteString(delta)
		results <- &pb.TranscriptStreamResponse{Delta: delta}
		return nil
	}

	for off := 0; off < len(data); off += streamChunkSamples {
		if err := ctx.Err(); err != nil {
			return status.Error(codes.Canceled, "transcription cancelled")
		}
		end := min(off+streamChunkSamples, len(data))
		chunk := data[off:end]

		var eou int32
		ret := CppStreamFeed(stream, chunk, int32(len(chunk)), unsafe.Pointer(&eou))
		if err := emitDelta(ret); err != nil {
			return err
		}
		if eou != 0 {
			flushSegment()
		}
	}

	// Flush the streaming tail (final encoder chunk).
	if err := emitDelta(CppStreamFinalize(stream)); err != nil {
		return err
	}
	flushSegment()

	text := strings.TrimSpace(full.String())
	if len(segments) == 0 && text != "" {
		segments = append(segments, &pb.TranscriptSegment{Id: 0, Text: text})
	}
	results <- &pb.TranscriptStreamResponse{
		FinalResult: &pb.TranscriptResult{
			Text:     text,
			Segments: segments,
			Duration: duration,
		},
	}
	return nil
}

// streamJSON drives the ABI v4 streaming JSON entry points: each feed/finalize
// returns a {text,eou,frame_sec,words} document. The newly-finalized text is
// emitted as a delta (unchanged streaming contract) while words are accumulated
// into per-utterance segments (closed on EOU) so the closing FinalResult carries
// timestamped segments. Runs under engineMu (already held by the caller).
func (p *ParakeetCpp) streamJSON(ctx context.Context, stream uintptr, data []float32,
	duration float32, results chan *pb.TranscriptStreamResponse) error {
	var (
		full strings.Builder
		seg  streamSegmenter
	)
	// consume frees the malloc'd char* (a 0 return is an error), parses the JSON,
	// emits the delta, and routes words through the segmenter.
	consume := func(ret uintptr) error {
		if ret == 0 {
			msg := CppLastError(p.ctxPtr)
			if msg == "" {
				msg = "unknown error"
			}
			return fmt.Errorf("parakeet-cpp: stream feed/finalize failed: %s", msg)
		}
		raw := goStringFromCPtr(ret)
		CppFreeString(ret)
		var doc streamFeedJSON
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return fmt.Errorf("parakeet-cpp: decode stream json: %w", err)
		}
		if doc.Text != "" {
			full.WriteString(doc.Text)
			results <- &pb.TranscriptStreamResponse{Delta: doc.Text}
		}
		seg.add(doc)
		return nil
	}

	for off := 0; off < len(data); off += streamChunkSamples {
		if err := ctx.Err(); err != nil {
			return status.Error(codes.Canceled, "transcription cancelled")
		}
		end := min(off+streamChunkSamples, len(data))
		chunk := data[off:end]
		if err := consume(CppStreamFeedJSON(stream, chunk, int32(len(chunk)))); err != nil {
			return err
		}
	}
	if err := consume(CppStreamFinalizeJSON(stream)); err != nil {
		return err
	}
	seg.flush() // close any trailing utterance that never saw an EOU

	text := strings.TrimSpace(full.String())
	segments := seg.segments()
	if len(segments) == 0 && text != "" {
		segments = append(segments, &pb.TranscriptSegment{Id: 0, Text: text})
	}
	results <- &pb.TranscriptStreamResponse{
		FinalResult: &pb.TranscriptResult{
			Text:     text,
			Segments: segments,
			Duration: duration,
		},
	}
	return nil
}

// decodeWavMono16k converts any input audio to 16 kHz mono PCM and returns the
// float samples plus the clip duration in seconds. Mirrors the whisper
// backend: utils.AudioToWav (ffmpeg) normalises rate/channels, go-audio
// decodes the PCM.
// convertToWavMono16k converts an arbitrary audio file to a 16 kHz mono WAV in
// a fresh temp dir and returns the path together with a cleanup func the caller
// must defer. WAV inputs already at 16 kHz/mono/16-bit are passed through by
// utils.AudioToWav (hardlink/copy), everything else is transcoded via ffmpeg.
// Used by the direct (non-batched) transcription path, which hands a file path
// to the C library's WAV-only audio loader.
func convertToWavMono16k(path string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "parakeet")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	converted := filepath.Join(dir, "converted.wav")
	if err := utils.AudioToWav(path, converted); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return converted, cleanup, nil
}

func decodeWavMono16k(path string) ([]float32, float32, error) {
	converted, cleanup, err := convertToWavMono16k(path)
	if err != nil {
		return nil, 0, err
	}
	defer cleanup()

	fh, err := os.Open(converted)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = fh.Close() }()

	buf, err := wav.NewDecoder(fh).FullPCMBuffer()
	if err != nil {
		return nil, 0, err
	}
	data := buf.AsFloat32Buffer().Data
	var duration float32
	if buf.Format != nil && buf.Format.SampleRate > 0 {
		duration = float32(len(data)) / float32(buf.Format.SampleRate)
	}
	return data, duration, nil
}

// Free releases the underlying parakeet_ctx. Called by LocalAI when the
// model is unloaded.
func (p *ParakeetCpp) Free() error {
	// Stop the dispatcher before releasing the engine so no in-flight runBatch
	// can touch a freed ctx (close leak / use-after-free on reload).
	if p.batStop != nil {
		close(p.batStop)
		p.batStop = nil
	}
	if p.ctxPtr != 0 {
		CppFree(p.ctxPtr)
		p.ctxPtr = 0
	}
	return nil
}

// goStringFromCPtr copies a NUL-terminated C string into Go memory.
// cptr is the raw pointer returned by purego from the C-API (a malloc'd
// buffer the caller owns); callers must free it via CppFreeString after
// the copy lands.
//
// The uintptr->unsafe.Pointer conversion below trips go vet's unsafeptr
// check, which can't distinguish a C-owned heap pointer from Go-managed
// memory. It is safe here: the pointer addresses a malloc'd C buffer the
// Go GC neither tracks nor moves, and we dereference it immediately to
// copy the bytes out, the same pattern (and the same tolerated warning)
// as the whisper backend's unsafe.Slice over segsPtr.
func goStringFromCPtr(cptr uintptr) string {
	if cptr == 0 {
		return ""
	}
	p := unsafe.Pointer(cptr) //nolint:govet // C-owned malloc'd buffer, not Go-GC memory (see doc above)
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}
