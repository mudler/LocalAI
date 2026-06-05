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

	// Cache-aware streaming (RNN-T) entry points. stream_begin returns 0 for
	// non-streaming models. feed/finalize return a malloc'd char* (uintptr,
	// freed via CppFreeString); feed writes 1 to *eouOut on an <EOU>/<EOB>.
	CppStreamBegin    func(ctx uintptr) uintptr
	CppStreamFeed     func(s uintptr, pcm []float32, nSamples int32, eouOut unsafe.Pointer) uintptr
	CppStreamFinalize func(s uintptr) uintptr
	CppStreamFree     func(s uintptr)
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
	Text   string            `json:"text"`
	Words  []transcriptWord  `json:"words"`
	Tokens []transcriptToken `json:"tokens"`
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
	p.engineMu.Lock()
	cstr := CppTranscribePcmBatchJSON(p.ctxPtr, concat, nSamples, int32(len(reqs)), 16000, dec)
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
// translate/diarize/prompt/temperature/language/threads are not applicable to
// parakeet and are ignored; streaming is handled by AudioTranscriptionStream
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
		return transcriptResultFromDoc(doc, opts), nil
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
	case p.bat.submit <- &batchRequest{pcm: pcm, decoder: 0, reply: rep}:
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
	return transcriptResultFromDoc(doc, opts), nil
}

// transcriptResultFromDoc maps a decoded transcriptJSON to a TranscriptResult,
// synthesising a single whole-clip segment and attaching word timings only when
// the caller requested word granularity. Shared by the batched and direct paths.
func transcriptResultFromDoc(doc transcriptJSON, opts *pb.TranscriptRequest) pb.TranscriptResult {
	text := strings.TrimSpace(doc.Text)
	words := make([]*pb.TranscriptWord, 0, len(doc.Words))
	for _, w := range doc.Words {
		words = append(words, &pb.TranscriptWord{Start: secondsToNanos(w.Start), End: secondsToNanos(w.End), Text: w.W})
	}
	tokens := make([]int32, 0, len(doc.Tokens))
	for _, t := range doc.Tokens {
		tokens = append(tokens, t.ID)
	}
	var segStart, segEnd int64
	if len(words) > 0 {
		segStart = words[0].Start
		segEnd = words[len(words)-1].End
	}
	seg := &pb.TranscriptSegment{Id: 0, Start: segStart, End: segEnd, Text: text, Tokens: tokens}
	if wordsRequested(opts.TimestampGranularities) {
		seg.Words = words
	}
	return pb.TranscriptResult{Text: text, Segments: []*pb.TranscriptSegment{seg}}
}

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

	stream := CppStreamBegin(p.ctxPtr)
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
