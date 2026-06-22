package openai

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

const (
	defaultMaxSummaryTokens = 512
	memoryPrefix            = "Summary of earlier conversation:\n"
	// compactionTimeout bounds the summarizer call so a stuck model can't pin the
	// compacting flag (and thus block all further compaction) forever.
	compactionTimeout = 60 * time.Second
)

// thinkTagRe matches a <think>…</think> span (dotall so it spans newlines).
var thinkTagRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// withMemory inserts the rolling summary as a system message after the existing
// (instructions) history. No-op when memory is empty.
func withMemory(history schema.Messages, memory string) schema.Messages {
	if memory == "" {
		return history
	}
	content := memoryPrefix + memory
	return append(history, schema.Message{
		Role:          string(types.MessageRoleSystem),
		StringContent: content,
		Content:       content,
	})
}

// renderItemsTranscript renders conversation items as a plain "role: text"
// transcript for summarization. Non-text items (bare tool calls) are labelled
// so the summarizer keeps track of actions taken.
func renderItemsTranscript(items []*types.MessageItemUnion) string {
	var b strings.Builder
	for _, item := range items {
		switch {
		case item.User != nil:
			b.WriteString("user: ")
			for _, c := range item.User.Content {
				if c.Text != "" {
					b.WriteString(c.Text)
				}
				if c.Transcript != "" {
					b.WriteString(c.Transcript)
				}
			}
			b.WriteString("\n")
		case item.Assistant != nil:
			b.WriteString("assistant: ")
			// Realtime assistant *audio* turns store the spoken words in
			// .Transcript (not .Text), so emit both or spoken turns are dropped.
			for _, c := range item.Assistant.Content {
				if c.Text != "" {
					b.WriteString(c.Text)
				}
				if c.Transcript != "" {
					b.WriteString(c.Transcript)
				}
			}
			b.WriteString("\n")
		case item.FunctionCall != nil:
			b.WriteString(fmt.Sprintf("assistant called tool %s(%s)\n", item.FunctionCall.Name, item.FunctionCall.Arguments))
		case item.FunctionCallOutput != nil:
			b.WriteString(fmt.Sprintf("tool result: %s\n", item.FunctionCallOutput.Output))
		}
	}
	return strings.TrimSpace(b.String())
}

// buildSummaryMessages builds the chat messages for the summarizer LLM: a system
// instruction plus prior memory and the new transcript to fold in. maxTokens is
// advisory (fed to the prompt; not hard-enforced in v1).
func buildSummaryMessages(priorMemory, transcript string, maxTokens int) schema.Messages {
	system := fmt.Sprintf("You maintain a running memory of a live voice conversation. "+
		"Merge the prior memory with the new exchanges into an updated memory. "+
		"Keep names, decisions, facts, preferences, and open threads. Be concise "+
		"(under ~%d tokens). Output only the updated memory, with no reasoning or tags.", maxTokens)
	var user strings.Builder
	if priorMemory != "" {
		user.WriteString("Prior memory:\n")
		user.WriteString(priorMemory)
		user.WriteString("\n\n")
	}
	user.WriteString("New exchanges to fold in:\n")
	user.WriteString(transcript)
	return schema.Messages{
		{Role: string(types.MessageRoleSystem), StringContent: system, Content: system},
		{Role: string(types.MessageRoleUser), StringContent: user.String(), Content: user.String()},
	}
}

// clearInputAudio resets the session's pending input audio buffer (the raw
// PCM and any buffered Opus frames). Used by the input_audio_buffer.clear
// realtime event so a client can discard a partially-captured utterance.
func clearInputAudio(s *Session) {
	s.AudioBufferLock.Lock()
	s.InputAudioBuffer = nil
	s.AudioBufferLock.Unlock()
	s.OpusFramesLock.Lock()
	s.OpusFrames = nil
	s.OpusFramesLock.Unlock()
}

// itemID extracts the id from any MessageItemUnion variant ("" if none).
func itemID(item *types.MessageItemUnion) string {
	switch {
	case item == nil:
		return ""
	case item.System != nil:
		return item.System.ID
	case item.User != nil:
		return item.User.ID
	case item.Assistant != nil:
		return item.Assistant.ID
	case item.FunctionCall != nil:
		return item.FunctionCall.ID
	case item.FunctionCallOutput != nil:
		return item.FunctionCallOutput.ID
	default:
		return ""
	}
}

// deleteItem removes the item with id from items, returning the new slice and
// whether it was found.
func deleteItem(items []*types.MessageItemUnion, id string) ([]*types.MessageItemUnion, bool) {
	for i, item := range items {
		if itemID(item) == id {
			return append(items[:i:i], items[i+1:]...), true
		}
	}
	return items, false
}

// truncateAssistantText clears the text of the assistant item's content part at
// contentIndex. Minimal truncate: used to discard an interrupted/barge-in
// response tail. Both .Text and .Transcript are cleared because realtime audio
// turns store the spoken words in .Transcript (clearing only .Text would no-op).
func truncateAssistantText(items []*types.MessageItemUnion, id string, contentIndex int) bool {
	for _, item := range items {
		if itemID(item) != id || item.Assistant == nil {
			continue
		}
		if contentIndex >= 0 && contentIndex < len(item.Assistant.Content) {
			item.Assistant.Content[contentIndex].Text = ""
			item.Assistant.Content[contentIndex].Transcript = ""
		}
		return true
	}
	return false
}

// compactionCut returns the index splitting items into overflow (items[:cut],
// to be summarized+evicted) and the kept live tail (items[cut:]), keeping the
// last `keep` items. It mirrors trimRealtimeItems' pair-safety: the cut is
// pulled left so a function_call and its function_call_output are never split
// across the boundary (the whole pair lands in the kept tail). Returns 0 when
// there is nothing to cut.
func compactionCut(items []*types.MessageItemUnion, keep int) int {
	// keep <= 0 means no live-window cap (the "unlimited history" sentinel, as
	// in trimRealtimeItems): there is nothing to evict, so cut nothing. This
	// also avoids indexing items[len(items)] in the pair-safety loop below.
	if keep <= 0 {
		return 0
	}
	cut := len(items) - keep
	if cut <= 0 {
		return 0
	}
	for cut > 0 && items[cut] != nil && items[cut].FunctionCallOutput != nil {
		cut--
	}
	return cut
}

// resolveCompaction reads the pipeline.compaction block, applying defaults and
// the trigger>max_history invariant. maxHistory is the already-resolved live
// window size. Returns enabled=false (and zero values) when compaction is off.
func resolveCompaction(cfg *config.ModelConfig, maxHistory int) (enabled bool, trigger, maxSummaryTokens int, summaryModel string) {
	if cfg == nil || cfg.Pipeline.Compaction == nil || !cfg.Pipeline.Compaction.Enabled {
		return false, 0, 0, ""
	}
	c := cfg.Pipeline.Compaction
	trigger = c.TriggerItems
	if trigger <= 0 {
		trigger = maxHistory * 2
	}
	if trigger <= maxHistory {
		trigger = maxHistory + 1
	}
	maxSummaryTokens = c.MaxSummaryTokens
	if maxSummaryTokens <= 0 {
		maxSummaryTokens = defaultMaxSummaryTokens
	}
	return true, trigger, maxSummaryTokens, c.SummaryModel
}

// stripThinkTags removes any leaked <think>…</think> spans from a summary.
func stripThinkTags(s string) string {
	return strings.TrimSpace(thinkTagRe.ReplaceAllString(s, ""))
}

// prefixMatches reports whether items begins with the same ids, in order, as
// snapshot — i.e. the overflow we summarized is still at the head (no concurrent
// client delete reshuffled it).
func prefixMatches(items, snapshot []*types.MessageItemUnion) bool {
	if len(items) < len(snapshot) {
		return false
	}
	for i := range snapshot {
		if itemID(items[i]) != itemID(snapshot[i]) {
			return false
		}
	}
	return true
}

// compact folds overflow items into conv.Memory and evicts them. It never holds
// conv.Lock across the summarizer call: snapshot under lock, summarize unlocked,
// commit under lock (re-validating the head is unchanged). On any error it
// leaves the conversation untouched — items are never dropped without a summary.
func (s *Session) compact(conv *Conversation, model Model) {
	if model == nil {
		return
	}
	// Snapshot.
	conv.Lock.Lock()
	if len(conv.Items) <= s.CompactionTrigger {
		conv.Lock.Unlock()
		return
	}
	cut := compactionCut(conv.Items, s.MaxHistoryItems)
	if cut <= 0 {
		conv.Lock.Unlock()
		return
	}
	overflow := append([]*types.MessageItemUnion(nil), conv.Items[:cut]...)
	prior := conv.Memory
	conv.Lock.Unlock()

	// Summarize (unlocked).
	msgs := buildSummaryMessages(prior, renderItemsTranscript(overflow), s.MaxSummaryTokens)
	ctx, cancel := context.WithTimeout(context.Background(), compactionTimeout)
	defer cancel()
	predFunc, err := model.Predict(ctx, msgs, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		xlog.Warn("realtime compaction: summarizer predict failed", "error", err)
		return
	}
	pred, err := predFunc()
	if err != nil {
		xlog.Warn("realtime compaction: summarizer inference failed", "error", err)
		return
	}
	summary := stripThinkTags(pred.Response)
	if summary == "" {
		xlog.Warn("realtime compaction: empty summary, skipping eviction")
		return
	}

	// Commit.
	conv.Lock.Lock()
	defer conv.Lock.Unlock()
	if !prefixMatches(conv.Items, overflow) {
		xlog.Debug("realtime compaction: head changed during summary, skipping")
		return
	}
	conv.Memory = summary
	conv.Items = conv.Items[len(overflow):]
	xlog.Debug("realtime compaction: evicted items into memory", "evicted", len(overflow), "remaining", len(conv.Items))
}

// summarizerModel resolves the model used to produce compaction summaries.
// Without a configured summary_model (or factory) it reuses the pipeline LLM.
func (s *Session) summarizerModel() Model {
	if s.SummaryModel == "" || s.summarizerFactory == nil {
		return s.ModelInterface
	}
	s.summarizerOnce.Do(func() {
		m, err := s.summarizerFactory()
		if err != nil {
			xlog.Warn("realtime compaction: summary_model load failed, falling back to pipeline LLM", "model", s.SummaryModel, "error", err)
			m = s.ModelInterface
		}
		s.summarizerCached = m
	})
	return s.summarizerCached
}

// maybeCompact schedules a background compaction when the live buffer has grown
// past the trigger and none is already running. Returns immediately.
func (s *Session) maybeCompact(conv *Conversation, model Model) {
	if !s.CompactionEnabled || model == nil {
		return
	}
	conv.Lock.Lock()
	over := len(conv.Items) > s.CompactionTrigger
	conv.Lock.Unlock()
	if !over {
		return
	}
	if !conv.compacting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer conv.compacting.Store(false)
		s.compact(conv, model)
	}()
}
