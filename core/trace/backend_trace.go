package trace

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emirpasic/gods/v2/queues/circularbuffer"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

type BackendTraceType string

const (
	BackendTraceLLM             BackendTraceType = "llm"
	BackendTraceEmbedding       BackendTraceType = "embedding"
	BackendTraceTranscription   BackendTraceType = "transcription"
	BackendTraceImageGeneration BackendTraceType = "image_generation"
	BackendTraceVideoGeneration BackendTraceType = "video_generation"
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

type BackendTrace struct {
	// ID identifies this trace for the lifetime of the process, so the list
	// endpoint can return trimmed entries and clients can fetch the full
	// record back from /api/backend-traces/:id.
	ID        string           `json:"id"`
	Timestamp time.Time        `json:"timestamp"`
	Duration  time.Duration    `json:"duration"`
	Type      BackendTraceType `json:"type"`
	ModelName string           `json:"model_name"`
	Backend   string           `json:"backend"`
	Summary   string           `json:"summary"`
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
	backendTraceBuffer *circularbuffer.Queue[*BackendTrace]
	backendMu          sync.Mutex
	backendLogChan     = make(chan *BackendTrace, 100)
	backendInitOnce    sync.Once
	backendTraceIDSeq  atomic.Uint64
)

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
	backendInitOnce.Do(func() {
		if maxItems <= 0 {
			maxItems = 100
		}
		backendMu.Lock()
		backendTraceBuffer = circularbuffer.New[*BackendTrace](maxItems)
		backendMu.Unlock()

		go func() {
			for t := range backendLogChan {
				backendMu.Lock()
				if backendTraceBuffer != nil {
					backendTraceBuffer.Enqueue(t)
				}
				backendMu.Unlock()
			}
		}()
	})

	// The body cap tracks the LATEST call, not the first: tracing_max_body_bytes
	// is runtime-mutable via the settings API (ApplyRuntimeSettings), and every
	// recording path calls this right before RecordBackendTrace with the current
	// appConfig value. Freezing the cap on first init meant a raised setting let
	// producers (e.g. trace.AudioSnippet, which reads the live value) embed
	// payloads that this recorder then stomped with the "<truncated: N bytes>"
	// marker — corrupting audio_wav_base64 into an unplayable string. maxItems
	// keeps first-call semantics: resizing the ring buffer would drop entries.
	backendMu.Lock()
	backendMaxBodyBytes = maxBodyBytes
	backendMu.Unlock()
}

func RecordBackendTrace(t BackendTrace) {
	backendMu.Lock()
	maxBody := backendMaxBodyBytes
	backendMu.Unlock()
	// Always walk Data, even with no body cap configured: besides capping
	// oversized strings (maxBody > 0), the walk replaces non-finite floats
	// (Inf/NaN) that encoding/json cannot marshal. A single such value — e.g. a
	// -Inf dBFS audio metric from a silent clip — would otherwise fail the whole
	// /api/backend-traces response and blank the Traces UI.
	if t.Data != nil {
		t.Data = sanitizeData(t.Data, maxBody)
	}
	if t.ID == "" {
		t.ID = strconv.FormatUint(backendTraceIDSeq.Add(1), 10)
	}
	select {
	case backendLogChan <- &t:
	default:
		xlog.Warn("Backend trace channel full, dropping trace")
	}
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
	if backendTraceBuffer == nil {
		backendMu.Unlock()
		return []BackendTrace{}
	}
	ptrs := backendTraceBuffer.Values()
	backendMu.Unlock()

	traces := make([]BackendTrace, len(ptrs))
	for i, p := range ptrs {
		traces[i] = *p
	}

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
