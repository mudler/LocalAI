package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

const (
	defaultMaxSummaryTokens = 512
)

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
