package trace

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/emirpasic/gods/v2/queues/circularbuffer"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

type BackendTraceType string

const (
	BackendTraceLLM              BackendTraceType = "llm"
	BackendTraceEmbedding        BackendTraceType = "embedding"
	BackendTraceTranscription    BackendTraceType = "transcription"
	BackendTraceImageGeneration  BackendTraceType = "image_generation"
	BackendTraceVideoGeneration  BackendTraceType = "video_generation"
	BackendTraceTTS              BackendTraceType = "tts"
	BackendTraceSoundGeneration  BackendTraceType = "sound_generation"
	BackendTraceRerank           BackendTraceType = "rerank"
	BackendTraceTokenize         BackendTraceType = "tokenize"
)

type BackendTrace struct {
	Timestamp time.Time        `json:"timestamp"`
	Duration  time.Duration    `json:"duration"`
	Type      BackendTraceType `json:"type"`
	ModelName string           `json:"model_name"`
	Backend   string           `json:"backend"`
	Summary   string           `json:"summary"`
	Error     string           `json:"error,omitempty"`
	Data      map[string]any   `json:"data"`
}

var backendTraceBuffer *circularbuffer.Queue[*BackendTrace]
var backendMu sync.Mutex
var backendLogChan = make(chan *BackendTrace, 100)
var backendInitOnce sync.Once

func InitBackendTracingIfEnabled(maxItems int) {
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
}

func RecordBackendTrace(t BackendTrace) {
	select {
	case backendLogChan <- &t:
	default:
		xlog.Warn("Backend trace channel full, dropping trace")
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

	sort.Slice(traces, func(i, j int) bool {
		return traces[i].Timestamp.Before(traces[j].Timestamp)
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
