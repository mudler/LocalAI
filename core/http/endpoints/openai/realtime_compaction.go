package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

const (
	defaultMaxSummaryTokens = 512
)

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
	if keep < 0 {
		keep = 0
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
