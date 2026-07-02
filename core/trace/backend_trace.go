package trace

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
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
	if t.Data != nil && maxBody > 0 {
		t.Data = capDataStrings(t.Data, maxBody)
	}
	select {
	case backendLogChan <- &t:
	default:
		xlog.Warn("Backend trace channel full, dropping trace")
	}
}

// capDataStrings walks a trace Data map and replaces any string value (at any
// depth) that exceeds maxBytes with a fixed-size marker that names the
// original byte count. The replacement is intentionally short and not valid
// base64/JSON: the goal is to flag "this was dropped" cheaply, not to keep a
// partial value that the UI might try to render. Non-string scalars and
// non-map containers pass through untouched so structural fields like
// total_deltas or audio_sample_rate remain useful.
func capDataStrings(data map[string]any, maxBytes int) map[string]any {
	out := make(map[string]any, len(data))
	for k, v := range data {
		out[k] = capValue(v, maxBytes)
	}
	return out
}

func capValue(v any, maxBytes int) any {
	switch val := v.(type) {
	case string:
		if len(val) > maxBytes {
			return fmt.Sprintf("<truncated: %d bytes>", len(val))
		}
		return val
	case map[string]any:
		return capDataStrings(val, maxBytes)
	default:
		return v
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
