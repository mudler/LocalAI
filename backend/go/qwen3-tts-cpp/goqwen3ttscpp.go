package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var (
	// qt3_load(talker_path, codec_path, use_fa, clamp_fp16, max_batch) int
	CppLoad func(talkerPath, codecPath string, useFA, clampFP16, maxBatch int) int
	// qt3_tts(text, lang, instruct, speaker, ref_samples, ref_n, ref_text,
	//         seed, temperature, top_k, top_p, rep_pen, max_new, out_n) -> float*
	CppTTS func(text, lang, instruct, speaker string, refSamples unsafe.Pointer,
		refN int, refText string, seed int64, temperature float32, topK int,
		topP, repPen float32, maxNew int, outN unsafe.Pointer) uintptr
	// qt3_tts_stream(..., cb, user) int
	CppTTSStream func(text, lang, instruct, speaker string, refSamples unsafe.Pointer,
		refN int, refText string, seed int64, temperature float32, topK int,
		topP, repPen float32, maxNew int, cb uintptr, user uintptr) int
	CppPCMFree func(ptr uintptr)
	CppUnload  func()
)

type Qwen3TtsCpp struct {
	base.SingleThread
	opts loadOptions
	// audioPath is the model-config reference voice (tts.audio_path), the
	// default clone reference when a request omits an audio Voice.
	audioPath string
	// maxBatch mirrors opts.maxBatch for Locking(), which the gRPC server
	// reads on every request (atomic: Load writes it once, requests read it).
	maxBatch atomic.Int32
}

// Locking keeps LocalAI's per-backend request lock only for single-sequence
// synthesis. With max_batch > 1 the native compute worker owns every backend
// compute on the handle and batches concurrent qt_synthesize calls, so
// TTS/TTSStream requests must reach it concurrently instead of one at a time.
// Before Load (maxBatch 0) the lock stays on, so loading is serialized.
func (q *Qwen3TtsCpp) Locking() bool {
	return q.maxBatch.Load() <= 1
}

func (q *Qwen3TtsCpp) Load(opts *pb.ModelOptions) error {
	model := opts.ModelFile
	if model == "" {
		model = opts.ModelPath
	}
	if !filepath.IsAbs(model) && opts.ModelPath != "" {
		model = filepath.Join(opts.ModelPath, model)
	}

	q.opts = parseOptions(opts.Options)

	// Resolve the codec/tokenizer GGUF: explicit option, else auto-discover a
	// *tokenizer*.gguf sibling of the talker model.
	codec := q.opts.codecPath
	if codec != "" && !filepath.IsAbs(codec) {
		codec = filepath.Join(filepath.Dir(model), codec)
	}
	if codec == "" {
		codec = discoverTokenizer(filepath.Dir(model))
	}
	if codec == "" {
		return fmt.Errorf("qwen3-tts: no codec/tokenizer GGUF found; set option 'tokenizer:<file>'")
	}
	q.opts.codecPath = codec

	q.audioPath = opts.AudioPath
	if q.audioPath != "" && !filepath.IsAbs(q.audioPath) {
		q.audioPath = filepath.Join(filepath.Dir(model), q.audioPath)
	}

	useFA := boolToInt(q.opts.useFA)
	clamp := boolToInt(q.opts.clampFP16)

	fmt.Fprintf(os.Stderr, "[qwen3-tts-cpp] Load talker=%s codec=%s use_fa=%d clamp_fp16=%d max_batch=%d\n",
		model, codec, useFA, clamp, q.opts.maxBatch)

	if rc := CppLoad(model, codec, useFA, clamp, q.opts.maxBatch); rc != 0 {
		return fmt.Errorf("qwen3-tts: failed to load model (rc=%d)", rc)
	}
	q.maxBatch.Store(int32(q.opts.maxBatch))
	if q.opts.maxBatch > 1 && q.opts.batchWarmup {
		warmupBatch(q.opts.maxBatch)
	}
	return nil
}

// warmupBatch synthesizes n short utterances of increasing length
// concurrently through the streaming path, discarding the audio. qt builds
// its code-predictor and codec-stream graphs lazily per batch width; the
// first request that hits a new width (e.g. two requests finishing in the
// same step) otherwise stalls the whole batch for seconds (~5 s per width on
// an RTX 5060 Ti). The staggered lengths make the slots finish one by one, so
// every width from n down to 1 is built here, at load.
func warmupBatch(n int) {
	streamCbOnce.Do(func() {
		streamCbPtr = purego.NewCallback(streamCallback)
	})
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		text := strings.TrimSpace(strings.Repeat("Hello there. ", i+1))
		wg.Add(1)
		go func() {
			defer wg.Done()
			// user_data 0 is never a registered route: chunks are dropped.
			CppTTSStream(text, "", "", "", nil, 0, "", 0, 0, 0, 0, 0, 96, streamCbPtr, 0)
		}()
	}
	wg.Wait()
	fmt.Fprintf(os.Stderr, "[qwen3-tts-cpp] batch warmup (%d concurrent) done in %s\n", n, time.Since(start).Round(time.Millisecond))
}

// discoverTokenizer returns the first *tokenizer*.gguf in dir, or "".
func discoverTokenizer(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if strings.Contains(name, "tokenizer") && strings.HasSuffix(name, ".gguf") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func optStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// resolveRequest derives the synthesis inputs from a TTSRequest:
// language, instruct, speaker, ref-audio samples, ref-text and sampling.
func (q *Qwen3TtsCpp) resolveRequest(req *pb.TTSRequest) (lang, instruct, speaker, refText string, ref []float32, s sampling, err error) {
	lang = normalizeLanguage(optStr(req.Language))
	instruct = optStr(req.Instructions)

	var refPath string
	speaker, refPath = resolveVoice(req.Voice)
	if refPath == "" && speaker == "" && q.audioPath != "" {
		// No per-request voice: fall back to the config clone reference.
		refPath = q.audioPath
	}
	if refPath != "" {
		ref, err = readWAVAsFloat(refPath)
		if err != nil {
			return
		}
	}

	if req.Params != nil {
		refText = req.Params["ref_text"]
	}
	s = parseSampling(req.Params, q.opts.seed)
	return
}

func (q *Qwen3TtsCpp) TTS(req *pb.TTSRequest) error {
	if req.Dst == "" {
		return fmt.Errorf("qwen3-tts: TTS requires a destination path")
	}
	if req.Text == "" {
		return fmt.Errorf("qwen3-tts: TTS requires text")
	}
	lang, instruct, speaker, refText, ref, s, err := q.resolveRequest(req)
	if err != nil {
		return err
	}
	var refPtr unsafe.Pointer
	if len(ref) > 0 {
		refPtr = unsafe.Pointer(&ref[0])
	}

	var n int32
	ptr := CppTTS(req.Text, lang, instruct, speaker, refPtr, len(ref), refText,
		s.seed, s.temperature, s.topK, s.topP, s.repPen, s.maxNew, unsafe.Pointer(&n))
	runtimeKeepAlive(ref)
	if ptr == 0 {
		return fmt.Errorf("qwen3-tts: synthesis failed")
	}
	// Register the free as soon as we own a non-null buffer, so the n<=0 guard
	// below cannot leak it (defensive: the C contract returns NULL on failure).
	defer CppPCMFree(ptr)
	if n <= 0 {
		return fmt.Errorf("qwen3-tts: synthesis produced no samples")
	}
	src := unsafe.Slice((*float32)(unsafe.Pointer(ptr)), int(n)) //nolint:govet // C-allocated PCM, copied out before free
	out := make([]float32, int(n))
	copy(out, src)
	return writeWAV24k(req.Dst, out)
}

// One purego callback is shared by every TTSStream request (purego callbacks
// cannot be freed and are capped). Each request registers a streamRoute under
// a fresh id that travels as the C callback's user_data, so concurrent
// requests (max_batch > 1) each get their own chunks. streamMu serializes the
// incremental (TTSLive) path, whose state is global (see live.go).
var (
	streamMu     sync.Mutex
	streamCbOnce sync.Once
	streamCbPtr  uintptr

	streamRoutesMu sync.Mutex
	streamRoutes   = map[uintptr]*streamRoute{}
	streamNextID   uintptr
)

// streamRoute queues one request's PCM chunks. With max_batch > 1 the native
// worker invokes every request's callback from its single compute thread, so
// push never blocks on the consumer: a slow client must not stall the other
// requests of the batch. forward drains the queue into the request's channel.
type streamRoute struct {
	mu     sync.Mutex
	cond   *sync.Cond
	chunks [][]byte
	closed bool
}

func newStreamRoute() *streamRoute {
	r := &streamRoute{}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *streamRoute) push(b []byte) {
	r.mu.Lock()
	r.chunks = append(r.chunks, b)
	r.mu.Unlock()
	r.cond.Signal()
}

func (r *streamRoute) close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cond.Signal()
}

// forward sends queued chunks to out until close() and the queue is empty.
func (r *streamRoute) forward(out chan<- []byte) {
	for {
		r.mu.Lock()
		for len(r.chunks) == 0 && !r.closed {
			r.cond.Wait()
		}
		batch := r.chunks
		r.chunks = nil
		done := r.closed
		r.mu.Unlock()
		for _, b := range batch {
			out <- b
		}
		if done && len(batch) == 0 {
			return
		}
	}
}

func registerStreamRoute() (uintptr, *streamRoute) {
	r := newStreamRoute()
	streamRoutesMu.Lock()
	streamNextID++
	id := streamNextID
	streamRoutes[id] = r
	streamRoutesMu.Unlock()
	return id, r
}

func unregisterStreamRoute(id uintptr) {
	streamRoutesMu.Lock()
	delete(streamRoutes, id)
	streamRoutesMu.Unlock()
}

// streamCallback is registered once and routes each PCM chunk to the request
// identified by user_data.
func streamCallback(samples *float32, nSamples int32, user uintptr) uintptr {
	if nSamples <= 0 || samples == nil {
		return 1 // continue
	}
	streamRoutesMu.Lock()
	r := streamRoutes[user]
	streamRoutesMu.Unlock()
	if r == nil {
		return 1 // continue
	}
	src := unsafe.Slice(samples, int(nSamples))
	cp := make([]float32, int(nSamples)) // copy out of C memory before returning
	copy(cp, src)
	r.push(floatToPCM16LE(cp))
	return 1 // continue
}

func (q *Qwen3TtsCpp) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)
	if req.Text == "" {
		return fmt.Errorf("qwen3-tts: TTSStream requires text")
	}

	streamCbOnce.Do(func() {
		streamCbPtr = purego.NewCallback(streamCallback)
	})

	lang, instruct, speaker, refText, ref, s, err := q.resolveRequest(req)
	if err != nil {
		return err
	}
	var refPtr unsafe.Pointer
	if len(ref) > 0 {
		refPtr = unsafe.Pointer(&ref[0])
	}

	// Emit the WAV header first so the HTTP layer gets a self-describing stream.
	results <- wavHeader24k()

	id, route := registerStreamRoute()
	forwarded := make(chan struct{})
	go func() {
		route.forward(results)
		close(forwarded)
	}()
	rc := CppTTSStream(req.Text, lang, instruct, speaker, refPtr, len(ref), refText,
		s.seed, s.temperature, s.topK, s.topP, s.repPen, s.maxNew, streamCbPtr, id)
	unregisterStreamRoute(id)
	route.close()
	<-forwarded // every chunk is in results before the deferred close
	runtimeKeepAlive(ref)

	if rc != 0 {
		return fmt.Errorf("qwen3-tts: streaming synthesis failed (rc=%d)", rc)
	}
	return nil
}
