package compression

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
)

const summaryPrefix = "[COMPRESSED: "

type Counter interface {
	CountMessages([]schema.Message) (int, error)
}

type CounterFunc func([]schema.Message) (int, error)

func (f CounterFunc) CountMessages(messages []schema.Message) (int, error) { return f(messages) }

type Summarizer interface {
	Summarize(context.Context, string, []schema.Message, int) (string, int, error)
}

type SummarizerFunc func(context.Context, string, []schema.Message, int) (string, int, error)

func (f SummarizerFunc) Summarize(ctx context.Context, model string, messages []schema.Message, maxTokens int) (string, int, error) {
	return f(ctx, model, messages, maxTokens)
}

type Metadata struct {
	OriginalTokens     int    `json:"original_tokens"`
	CompressedTokens   int    `json:"compressed_tokens"`
	DroppedTurns       int    `json:"dropped_turns"`
	Compressor         string `json:"compressor"`
	SummaryTokens      int    `json:"summary_tokens"`
	OverflowRecoveries int    `json:"overflow_recoveries"`
}

type overflowError struct{ tokens, limit int }

func (e *overflowError) Error() string {
	return fmt.Sprintf("compressed request (%d tokens) exceeds context size (%d)", e.tokens, e.limit)
}

func IsOverflow(err error) bool {
	var target *overflowError
	return errors.As(err, &target)
}

type Service struct {
	counter    Counter
	summarizer Summarizer
}

func New(counter Counter, summarizer Summarizer) *Service {
	return &Service{counter: counter, summarizer: summarizer}
}

func (s *Service) Transform(ctx context.Context, policy config.CompressionConfig, contextSize int, primaryModel string, messages []schema.Message) ([]schema.Message, *Metadata, error) {
	if !policy.Enabled || len(messages) == 0 {
		record(primaryModel, "skipped", time.Time{}, 0, 0)
		return messages, nil, nil
	}
	originalTokens, err := s.counter.CountMessages(messages)
	if err != nil {
		record(primaryModel, "error", time.Now(), 0, 0)
		return nil, nil, err
	}
	if contextSize <= 0 {
		record(primaryModel, "error", time.Now(), originalTokens, originalTokens)
		return nil, nil, &overflowError{tokens: originalTokens, limit: contextSize}
	}
	ratio := policy.TriggerAtRatio
	if ratio == 0 {
		ratio = .75
	}
	if originalTokens < int(float64(contextSize)*ratio) {
		record(primaryModel, "skipped", time.Time{}, originalTokens, originalTokens)
		return messages, nil, nil
	}
	started := time.Now()
	if len(messages) < 2 {
		if originalTokens <= contextSize {
			record(primaryModel, "skipped", started, originalTokens, originalTokens)
			return messages, nil, nil
		}
		record(primaryModel, "error", started, originalTokens, originalTokens)
		return nil, nil, &overflowError{tokens: originalTokens, limit: contextSize}
	}

	prefixEnd := 0
	for prefixEnd < len(messages) && isPreservedPrefix(messages[prefixEnd]) {
		prefixEnd++
	}
	prefix := messages[:prefixEnd]
	head, tail := partition(messages[prefixEnd:], policy.KeepTailTokens, s.counter)
	if len(head) == 0 {
		if originalTokens <= contextSize {
			record(primaryModel, "skipped", started, originalTokens, originalTokens)
			return messages, nil, nil
		}
		record(primaryModel, "error", started, originalTokens, originalTokens)
		return nil, nil, &overflowError{tokens: originalTokens, limit: contextSize}
	}
	compressor := policy.CompressorModel
	if compressor == "" {
		compressor = primaryModel
	}
	maxSummaryTokens := policy.MaxSummaryTokens
	if maxSummaryTokens == 0 {
		maxSummaryTokens = 512
	}
	summary, summaryTokens, err := s.summarizer.Summarize(ctx, compressor, head, maxSummaryTokens)
	if err != nil {
		record(primaryModel, "error", started, originalTokens, 0)
		return nil, nil, fmt.Errorf("compress chat history: %w", err)
	}
	content := summaryPrefix + strings.TrimSpace(summary) + "]"
	result := append([]schema.Message(nil), prefix...)
	result = append(result, schema.Message{Role: "system", Content: content, StringContent: content})
	result = append(result, tail...)
	compressedTokens, err := s.counter.CountMessages(result)
	if err != nil {
		record(primaryModel, "error", started, originalTokens, 0)
		return nil, nil, err
	}
	meta := &Metadata{
		OriginalTokens: originalTokens, CompressedTokens: compressedTokens,
		DroppedTurns: len(head), Compressor: compressor, SummaryTokens: summaryTokens,
	}
	if compressedTokens <= contextSize {
		record(primaryModel, "success", started, originalTokens, compressedTokens)
		return result, meta, nil
	}
	if policy.OnPostCompressionOverflow != "drop_oldest_summary" {
		record(primaryModel, "error", started, originalTokens, compressedTokens)
		return nil, nil, &overflowError{tokens: compressedTokens, limit: contextSize}
	}

	for recoveries := 0; recoveries < 2; recoveries++ {
		idx := oldestSummary(result)
		if idx < 0 {
			break
		}
		result = append(result[:idx], result[idx+1:]...)
		meta.OverflowRecoveries++
		compressedTokens, err = s.counter.CountMessages(result)
		if err != nil {
			return nil, nil, err
		}
		meta.CompressedTokens = compressedTokens
		if compressedTokens <= contextSize {
			record(primaryModel, "success", started, originalTokens, compressedTokens)
			return result, meta, nil
		}
	}
	record(primaryModel, "error", started, originalTokens, compressedTokens)
	return nil, nil, &overflowError{tokens: compressedTokens, limit: contextSize}
}

func isPreservedPrefix(message schema.Message) bool {
	return message.Role == "system" || message.Role == "developer"
}

func partition(messages []schema.Message, keepTokens int, counter Counter) ([]schema.Message, []schema.Message) {
	if keepTokens <= 0 {
		keepTokens = 2048
	}
	cut := len(messages)
	for cut > 1 {
		start := cut - 1
		if messages[start].Role == "tool" {
			for start > 0 && messages[start-1].Role == "tool" {
				start--
			}
			if start > 0 && len(messages[start-1].ToolCalls) > 0 {
				start--
			}
		}
		candidate := messages[start:]
		tokens, err := counter.CountMessages(candidate)
		// The newest complete unit is mandatory even when it exceeds the
		// preferred tail budget. Summarizing the active user request changes
		// its meaning; the post-compression fit check returns 413 instead.
		if cut != len(messages) && (err != nil || tokens > keepTokens) {
			break
		}
		cut = start
	}
	return messages[:cut], messages[cut:]
}

func oldestSummary(messages []schema.Message) int {
	for i, message := range messages {
		if message.Role == "system" && strings.HasPrefix(message.StringContent, summaryPrefix) {
			return i
		}
	}
	return -1
}
