package openai

// Finish reason constants for OpenAI API responses
const (
	FinishReasonStop         = "stop"
	FinishReasonToolCalls    = "tool_calls"
	FinishReasonFunctionCall = "function_call"
	// FinishReasonLength is reported when generation stopped because it
	// reached the max_tokens budget rather than a natural stop (issue #9716).
	FinishReasonLength = "length"
)
