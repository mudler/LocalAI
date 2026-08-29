package trace

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emirpasic/gods/v2/queues/circularbuffer"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace/tracepersist"
	"github.com/mudler/xlog"
)

type BackendTraceType string

type BackendTraceStatus string

const (
	BackendTraceLLM             BackendTraceType = "llm"
	BackendTraceEmbedding       BackendTraceType = "embedding"
	BackendTraceTranscription   BackendTraceType = "transcription"
	BackendTraceImageGeneration BackendTraceType = "image_generation"
	BackendTraceVideoGeneration BackendTraceType = "video_generation"
	BackendTrace3DGeneration    BackendTraceType = "3d_generation"
	BackendTrace3DRemesh        BackendTraceType = "3d_remesh"
	BackendTraceTTS             BackendTraceType = "tts"
	BackendTraceSoundGeneration BackendTraceType = "sound_generation"
	BackendTraceRerank          BackendTraceType = "rerank"
	BackendTraceTokenize        BackendTraceType = "tokenize"
	BackendTraceDetection       BackendTraceType = "detection"
	BackendTraceDepth           BackendTraceType = "depth"
	BackendTraceFaceVerify      BackendTraceType = "face_verify"
	BackendTraceFaceAnalyze     BackendTraceType = "face_analyze"
	BackendTraceVoiceVerify     BackendTraceType = "voice_verify"
	BackendTraceVoiceAnalyze    BackendTraceType = "voice_analyze"
	BackendTraceVoiceEmbed      BackendTraceType = "voice_embed"
	BackendTraceAudioTransform  BackendTraceType = "audio_transform"
	BackendTraceModelLoad       BackendTraceType = "model_load"
	BackendTraceScore           BackendTraceType = "score"
	BackendTraceTokenClassify   BackendTraceType = "token_classify"
	BackendTracePatternPII      BackendTraceType = "pattern_pii"
	BackendTraceVectorStore     BackendTraceType = "vector_store"
)

const (
	BackendTraceRunning   BackendTraceStatus = "running"
	BackendTraceCompleted BackendTraceStatus = "completed"
	BackendTraceFailed    BackendTraceStatus = "failed"
)

type BackendTrace struct {
	// ID identifies this trace for the lifetime of the process, so the list
	// endpoint can return trimmed entries and clients can fetch the full
	// record back from /api/backend-traces/:id.
	ID        string             `json:"id"`
	Timestamp time.Time          `json:"timestamp"`
	Duration  time.Duration      `json:"duration"`
	Status    BackendTraceStatus `json:"status,omitempty"`
	Type      BackendTraceType   `json:"type"`
	ModelName string             `json:"model_name"`
	Backend   string             `json:"backend"`
	Summary   string             `json:"summary"`
	// Body is the full request payload sent to the backend, when one
	// applies (currently: cloud-proxy passthrough forwards). Summary
	// is a short preview for the trace list; Body is the full
	// payload shown when the row is expanded. Capped by the recorder
	// to keep the in-memory ring buffer bounded.
	Body  string         `json:"body,omitempty"`
	Error string         `json:"error,omitempty"`
	Data  map[string]any `json:"data"`
}

// MaxTraceBodyBytes caps the per-trace stored request body. Roomy
// enough to keep typical chat histories intact while preventing a
// runaway buffer when a caller streams MB-scale payloads.
const MaxTraceBodyBytes = 1 << 20

var (
	backendTraceBuffer   *circularbuffer.Queue[*BackendTrace]
	backendMu            sync.Mutex
	backendLogChan       = make(chan backendTraceCommand, 100)
	backendConsumerOnce  sync.Once
	backendTraceIDSeq    atomic.Uint64
	backendTraceDataPath string
	backendTraceStore    *tracepersist.Store[BackendTrace]
	backendTraceStoreKey string
	backendInFlight      = make(map[string]BackendTrace)
	backendMaxInFlight   = 1024
)

type backendTraceCommand struct {
	trace *BackendTrace
	store *tracepersist.Store[BackendTrace]
	clear chan error
}

// backendMaxBodyBytes caps each captured string value in a BackendTrace.Data
// field to keep the /api/backend-traces JSON small enough for the admin UI to
// load on every 5s auto-refresh. Mirrors the API-trace body cap added in
// commit 61bf34ea: without it a chatty LLM workload (full message history per
// trace) or any TTS run (~1.3 MiB of audio_wav_base64 per trace) blows the
// payload past tens of MiB and locks the Traces page in a loading state.
//
// 0 disables the cap. Guarded by backendMu; refreshed on EVERY
// InitBackendTracingIfEnabled call — see below.
var backendMaxBodyBytes int

func InitBackendTracingIfEnabled(maxItems, maxBodyBytes int) {
	if maxItems <= 0 {
		maxItems = 100
	}
	backendMu.Lock()
	key := filepath.Join(backendTraceDataPath, strconv.Itoa(maxItems))
	if backendTraceBuffer == nil || backendTraceStoreKey != key {
		var store *tracepersist.Store[BackendTrace]
		var restored []BackendTrace
		if backendTraceDataPath != "" {
			var err error
			store, err = tracepersist.New[BackendTrace](filepath.Join(backendTraceDataPath, "traces", "backend"), maxItems)
			if err != nil {
				xlog.Warn("Failed to initialize backend trace persistence", "error", err)
			} else if restored, err = store.Load(); err != nil {
				xlog.Warn("Failed to restore backend traces", "error", err)
				store = nil
			}
		}
		backendTraceBuffer = circularbuffer.New[*BackendTrace](maxItems)
		for i := range restored {
			t := restored[i]
			backendTraceBuffer.Enqueue(&t)
			advanceBackendTraceID(t.ID)
		}
		backendTraceStore = store
		backendTraceStoreKey = key
	}
	backendMaxBodyBytes = maxBodyBytes
	backendMu.Unlock()

	backendConsumerOnce.Do(func() {
		go func() {
			for command := range backendLogChan {
				if command.clear != nil {
					backendMu.Lock()
					if backendTraceBuffer != nil {
						backendTraceBuffer.Clear()
					}
					backendMu.Unlock()
					var err error
					if command.store != nil {
						err = command.store.Clear()
					}
					command.clear <- err
					continue
				}
				if command.store != nil {
					if err := command.store.Append(command.trace.ID, *command.trace); err != nil {
						xlog.Warn("Failed to persist backend trace", "error", err)
					}
				}
				backendMu.Lock()
				if backendTraceBuffer != nil {
					backendTraceBuffer.Enqueue(command.trace)
				}
				backendMu.Unlock()
			}
		}()
	})
}

func ConfigureBackendTracePersistence(dataPath string) {
	backendMu.Lock()
	backendTraceDataPath = dataPath
	backendMu.Unlock()
}

// ConfigureBackendTraceMaxInFlight applies the process-wide admission ceiling
// to backend work that can outlive the HTTP request which started it.
func ConfigureBackendTraceMaxInFlight(max int) {
	if max <= 0 {
		max = 1024
	}
	backendMu.Lock()
	backendMaxInFlight = max
	backendMu.Unlock()
}

func advanceBackendTraceID(id string) {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return
	}
	for current := backendTraceIDSeq.Load(); n > current && !backendTraceIDSeq.CompareAndSwap(current, n); current = backendTraceIDSeq.Load() {
	}
}

func RecordBackendTrace(trace BackendTrace) {
	backendMu.Lock()
	maxBody := backendMaxBodyBytes
	store := backendTraceStore
	backendMu.Unlock()
	// Always walk Data, even with no body cap configured: besides capping
	// oversized strings (maxBody > 0), the walk replaces non-finite floats
	// (Inf/NaN) that encoding/json cannot marshal. A single such value — e.g. a
	// -Inf dBFS audio metric from a silent clip — would otherwise fail the whole
	// /api/backend-traces response and blank the Traces UI.
	if trace.Data != nil {
		trace.Data = sanitizeData(trace.Data, maxBody)
	}
	if trace.ID == "" {
		trace.ID = strconv.FormatUint(backendTraceIDSeq.Add(1), 10)
	}
	if trace.Error != "" {
		trace.Status = BackendTraceFailed
	} else {
		trace.Status = BackendTraceCompleted
	}
	backendMu.Lock()
	delete(backendInFlight, trace.ID)
	backendMu.Unlock()
	select {
	case backendLogChan <- backendTraceCommand{trace: &trace, store: store}:
	default:
		xlog.Warn("Backend trace channel full, dropping trace")
	}
}

// BeginBackendTrace makes an operation visible before its backend call starts.
// RecordBackendTrace completes the same record when passed the trace again.
func BeginBackendTrace(t BackendTrace) string {
	if t.Timestamp.IsZero() {
		t.Timestamp = time.Now()
	}
	if t.ID == "" {
		t.ID = strconv.FormatUint(backendTraceIDSeq.Add(1), 10)
	}
	t.Status = BackendTraceRunning

	backendMu.Lock()
	defer backendMu.Unlock()
	if backendMaxInFlight > 0 && len(backendInFlight) >= backendMaxInFlight {
		return ""
	}
	running := t
	// Completion-only payloads may be mutated by producers while the request is
	// running. The list and backend-log link only need this stable metadata.
	running.Body = ""
	running.Data = nil
	backendInFlight[running.ID] = running
	return running.ID
}

// CancelBackendTrace removes an operation that is represented by a different
// completion trace, such as a model-load failure enriched by its caller.
func CancelBackendTrace(id string) {
	if id == "" {
		return
	}
	backendMu.Lock()
	delete(backendInFlight, id)
	backendMu.Unlock()
}

// sanitizeData walks a trace Data map (recursing into nested maps and slices)
// and makes every value safe for the /api/backend-traces JSON response:
//
//   - When maxBytes > 0, any string longer than maxBytes is replaced with a
//     fixed-size marker that names the original byte count. The replacement is
//     intentionally short and not valid base64/JSON: it flags "this was dropped"
//     cheaply rather than keeping a partial value the UI might try to render.
//   - Non-finite floats (Inf/NaN) are replaced with nil regardless of maxBytes,
//     because encoding/json refuses to marshal them and one bad value would fail
//     the entire response.
//
// Other scalars (ints, bools, finite floats) pass through untouched so
// structural fields like total_deltas or audio_sample_rate remain useful.
//
// The walk is copy-on-write: it runs on every RecordBackendTrace call, and in
// the common case nothing needs rewriting, so containers are only re-allocated
// on the paths that actually changed and untouched values keep their original
// interface boxes instead of paying a per-value re-boxing allocation.
func sanitizeData(data map[string]any, maxBytes int) map[string]any {
	out, _ := sanitizeMap(data, maxBytes)
	return out
}

func sanitizeMap(m map[string]any, maxBytes int) (map[string]any, bool) {
	var out map[string]any
	for k, v := range m {
		nv, changed := sanitizeValue(v, maxBytes)
		if changed && out == nil {
			// First change: fork the map. Entries already visited were
			// unchanged, so a full copy then overwriting as we go is exact.
			out = make(map[string]any, len(m))
			maps.Copy(out, m)
		}
		if out != nil {
			out[k] = nv
		}
	}
	if out == nil {
		return m, false
	}
	return out, true
}

func sanitizeSlice(s []any, maxBytes int) ([]any, bool) {
	var out []any
	for i, v := range s {
		nv, changed := sanitizeValue(v, maxBytes)
		if changed && out == nil {
			out = make([]any, len(s))
			copy(out, s)
		}
		if out != nil {
			out[i] = nv
		}
	}
	if out == nil {
		return s, false
	}
	return out, true
}

func sanitizeValue(v any, maxBytes int) (any, bool) {
	switch val := v.(type) {
	case string:
		if maxBytes > 0 && len(val) > maxBytes {
			return fmt.Sprintf("<truncated: %d bytes>", len(val)), true
		}
		return v, false
	case float64:
		if math.IsInf(val, 0) || math.IsNaN(val) {
			return nil, true
		}
		return v, false
	case float32:
		if f := float64(val); math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, true
		}
		return v, false
	case map[string]any:
		return sanitizeMap(val, maxBytes)
	case []any:
		return sanitizeSlice(val, maxBytes)
	default:
		return v, false
	}
}

func GetBackendTraces() []BackendTrace {
	backendMu.Lock()
	var ptrs []*BackendTrace
	if backendTraceBuffer != nil {
		ptrs = backendTraceBuffer.Values()
	}
	running := make([]BackendTrace, 0, len(backendInFlight))
	for _, t := range backendInFlight {
		t.Duration = time.Since(t.Timestamp)
		running = append(running, t)
	}
	backendMu.Unlock()

	traces := make([]BackendTrace, 0, len(ptrs)+len(running))
	for _, p := range ptrs {
		traces = append(traces, *p)
	}
	traces = append(traces, running...)

	slices.SortFunc(traces, func(a, b BackendTrace) int {
		return b.Timestamp.Compare(a.Timestamp)
	})

	return traces
}

// GetBackendTracesPage returns the newest-first window
// [offset, offset+limit) of the backend trace buffer plus the total number
// of buffered traces. A limit <= 0 means "no bound".
func GetBackendTracesPage(offset, limit int) ([]BackendTrace, int) {
	all := GetBackendTraces()
	total := len(all)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []BackendTrace{}, total
	}
	page := all[offset:]
	if limit > 0 && limit < len(page) {
		page = page[:limit]
	}
	out := make([]BackendTrace, len(page))
	copy(out, page)
	return out, total
}

// GetBackendTrace returns the buffered trace with the given ID.
func GetBackendTrace(id string) (BackendTrace, bool) {
	for _, t := range GetBackendTraces() {
		if t.ID == id {
			return t, true
		}
	}
	return BackendTrace{}, false
}

// SummarizeBackendTrace drops the heavy fields (the full request Body and the
// Data map, which carries things like base64 audio snippets and complete
// input_text payloads) while keeping everything the trace list renders. The
// full record stays reachable by ID. Without this the list response grew to
// tens of megabytes and the UI re-fetched it every few seconds.
func SummarizeBackendTrace(t BackendTrace) BackendTrace {
	t.Body = ""
	t.Data = nil
	return t
}

func ClearBackendTraces() {
	backendMu.Lock()
	store := backendTraceStore
	initialized := backendTraceBuffer != nil
	dataPath := backendTraceDataPath
	backendMu.Unlock()
	if initialized {
		done := make(chan error, 1)
		backendLogChan <- backendTraceCommand{store: store, clear: done}
		if err := <-done; err != nil {
			xlog.Warn("Failed to clear persisted backend traces", "error", err)
		}
		return
	}
	if store == nil && dataPath != "" {
		var err error
		store, err = tracepersist.New[BackendTrace](filepath.Join(dataPath, "traces", "backend"), 100)
		if err != nil {
			xlog.Warn("Failed to initialize backend trace persistence for clear", "error", err)
		} else if err := store.Clear(); err != nil {
			xlog.Warn("Failed to clear persisted backend traces", "error", err)
		}
	}
	backendMu.Lock()
	if backendTraceBuffer != nil {
		backendTraceBuffer.Clear()
	}
	backendMu.Unlock()
}

func GenerateLLMSummary(messages schema.Messages, prompt string) string {
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		text := ""
		switch content := last.Content.(type) {
		case string:
			text = content
		default:
			b, err := json.Marshal(content)
			if err == nil {
				text = string(b)
			}
		}
		if text != "" {
			return TruncateString(text, 200)
		}
	}
	if prompt != "" {
		return TruncateString(prompt, 200)
	}
	return ""
}

func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// TruncateToBytes caps a string at exactly maxBytes, preserving the leading
// content and appending a marker so the UI knows the value was clipped.
// Unlike TruncateString it guarantees output <= maxBytes, which matters for
// fields that feed back into the trace pipeline: capDataStrings in
// RecordBackendTrace re-checks size and would otherwise replace a producer's
// head-preserving truncation with the bare marker, losing the prefix.
//
// maxBytes <= 0 disables the cap, matching backendMaxBodyBytes semantics.
func TruncateToBytes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	suffix := fmt.Sprintf("...[truncated, %d bytes]", len(s))
	if len(suffix) >= maxBytes {
		// Pathologically small caps can't fit the marker; fall back to a
		// hard cut so the contract (output <= maxBytes) still holds.
		return s[:maxBytes]
	}
	return s[:maxBytes-len(suffix)] + suffix
}

// TruncateBytes is the []byte counterpart of TruncateString — it copies
// at most maxLen bytes, avoiding a full string([]byte) allocation when
// the input is a large request body.
func TruncateBytes(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "..."
}
