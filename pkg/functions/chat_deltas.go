package functions

import (
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// ToolCallsFromChatDeltas extracts tool calls from C++ autoparser chat deltas.
// Returns nil if no tool calls are present in the deltas.
func ToolCallsFromChatDeltas(deltas []*pb.ChatDelta) []FuncCallResults {
	if len(deltas) == 0 {
		xlog.Debug("[ChatDeltas] no chat deltas received from backend")
		return nil
	}

	// Count what's in the deltas for logging
	totalContentChunks := 0
	totalReasoningChunks := 0
	totalToolCallChunks := 0
	for _, d := range deltas {
		if d.Content != "" {
			totalContentChunks++
		}
		if d.ReasoningContent != "" {
			totalReasoningChunks++
		}
		totalToolCallChunks += len(d.ToolCalls)
	}
	xlog.Debug("[ChatDeltas] received deltas from backend",
		"total_deltas", len(deltas),
		"content_chunks", totalContentChunks,
		"reasoning_chunks", totalReasoningChunks,
		"tool_call_chunks", totalToolCallChunks,
	)

	type toolCallAccum struct {
		Name      string
		Arguments string
		ID        string
	}
	byIndex := map[int32]*toolCallAccum{}
	var maxIndex int32 = -1

	for _, d := range deltas {
		for _, tc := range d.ToolCalls {
			acc, ok := byIndex[tc.Index]
			if !ok {
				acc = &toolCallAccum{}
				byIndex[tc.Index] = acc
			}
			if tc.Name != "" {
				acc.Name = tc.Name
			}
			if tc.Id != "" {
				acc.ID = tc.Id
			}
			acc.Arguments += tc.Arguments
			if tc.Index > maxIndex {
				maxIndex = tc.Index
			}
		}
	}

	if len(byIndex) == 0 {
		xlog.Debug("[ChatDeltas] deltas present but no tool calls found, falling back to text parsing")
		return nil
	}

	results := make([]FuncCallResults, 0, len(byIndex))
	for i := int32(0); i <= maxIndex; i++ {
		if acc, ok := byIndex[i]; ok {
			xlog.Debug("[ChatDeltas] extracted tool call",
				"index", i,
				"name", acc.Name,
				"id", acc.ID,
				"args_length", len(acc.Arguments),
			)
			results = append(results, FuncCallResults{
				Name:      acc.Name,
				Arguments: acc.Arguments,
				ID:        acc.ID,
			})
		}
	}
	xlog.Debug("[ChatDeltas] using C++ autoparser tool calls, skipping Go-side parsing", "count", len(results))
	return results
}

// ContentFromChatDeltas extracts accumulated content text from chat deltas.
func ContentFromChatDeltas(deltas []*pb.ChatDelta) string {
	var sb strings.Builder
	for _, d := range deltas {
		sb.WriteString(d.Content)
	}
	return sb.String()
}

// ReasoningFromChatDeltas extracts accumulated reasoning text from chat deltas.
func ReasoningFromChatDeltas(deltas []*pb.ChatDelta) string {
	var sb strings.Builder
	for _, d := range deltas {
		sb.WriteString(d.ReasoningContent)
	}
	return sb.String()
}
