package middleware

// Context keys used by routing-module middlewares to communicate with
// the usage recorder. Unlike the legacy CONTEXT_LOCALS_KEY_* constants
// (which exist for backward-compatible callers), these are the
// canonical names for new fields.
const (
	// ContextKeyRequestedModel is set by content-router middleware to
	// the model name the client originally asked for, before any router
	// remapping. UsageMiddleware writes this into UsageRecord.RequestedModel.
	ContextKeyRequestedModel = "routing.requested_model"

	// ContextKeyServedModel is set by content-router middleware to the
	// model that actually handled the request (post-routing). When no
	// router runs, callers may leave this unset and the response-reported
	// model name is used as the served value.
	ContextKeyServedModel = "routing.served_model"

	// ContextKeyPreFilterPromptTokens / ContextKeyPostFilterPromptTokens
	// are set by the PII middleware to record how many prompt tokens
	// the user sent vs how many made it past redaction. When both are
	// zero or unset, UsageMiddleware uses the response-reported prompt
	// token count for both — i.e., no filter ran.
	ContextKeyPreFilterPromptTokens  = "routing.pre_filter_prompt_tokens"
	ContextKeyPostFilterPromptTokens = "routing.post_filter_prompt_tokens"

	// ContextKeyCorrelationID is the join key threaded across PII
	// events, router decisions, admission events, and usage records.
	// trace.go middleware sets X-Correlation-ID on the response; this
	// key mirrors the same value into echo.Context for in-process
	// propagation without re-parsing the header.
	ContextKeyCorrelationID = "routing.correlation_id"

	// ContextKeyPromptTokens / ContextKeyCompletionTokens / ContextKeyTotalTokens
	// are the canonical token counts the request handler measured. Stamping
	// these from the handler is the only reliable path for streaming
	// responses, where the SSE chunks may not include a usage block (OpenAI
	// requires stream_options.include_usage; Anthropic uses a separate
	// message_delta event shape). UsageMiddleware prefers these context
	// values over body-parsing.
	ContextKeyPromptTokens     = "routing.prompt_tokens"
	ContextKeyCompletionTokens = "routing.completion_tokens"
	ContextKeyTotalTokens      = "routing.total_tokens"

	// ContextKeyResponseModel is the model name the handler committed to
	// in its response payload. UsageMiddleware uses it when neither the
	// router nor the body-parse path has produced one. Distinct from
	// ContextKeyServedModel, which is the router's resolved choice.
	ContextKeyResponseModel = "routing.response_model"
)
