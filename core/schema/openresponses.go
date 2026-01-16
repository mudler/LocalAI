package schema

import (
	"context"
)

// OpenResponsesRequest represents a request to the Open Responses API
// https://www.openresponses.org/specification
type OpenResponsesRequest struct {
	Model           string              `json:"model"`
	Input           interface{}         `json:"input"`           // string or []ORItemParam
	Tools           []ORFunctionTool    `json:"tools,omitempty"`
	ToolChoice      interface{}         `json:"tool_choice,omitempty"` // "auto"|"required"|"none"|{type:"function",name:"..."}
	Stream          bool                `json:"stream,omitempty"`
	MaxOutputTokens *int                `json:"max_output_tokens,omitempty"`
	Temperature     *float64            `json:"temperature,omitempty"`
	TopP            *float64            `json:"top_p,omitempty"`
	Truncation      string              `json:"truncation,omitempty"` // "auto"|"disabled"
	Instructions    string              `json:"instructions,omitempty"`
	Reasoning       *ORReasoningParam   `json:"reasoning,omitempty"`
	Metadata        map[string]string   `json:"metadata,omitempty"`
	PreviousResponseID string           `json:"previous_response_id,omitempty"`

	// Internal fields (like OpenAIRequest)
	Context context.Context    `json:"-"`
	Cancel  context.CancelFunc `json:"-"`
}

// ModelName implements the LocalAIRequest interface
func (r *OpenResponsesRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// ORFunctionTool represents a function tool definition
type ORFunctionTool struct {
	Type        string                 `json:"type"` // always "function"
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Strict      bool                   `json:"strict,omitempty"`
}

// ORReasoningParam represents reasoning configuration
type ORReasoningParam struct {
	Effort  string `json:"effort,omitempty"`  // "none"|"low"|"medium"|"high"|"xhigh"
	Summary string `json:"summary,omitempty"` // "auto"|"concise"|"detailed"
}

// ORItemParam represents an input item (discriminated union by type)
type ORItemParam struct {
	Type   string      `json:"type"` // message|function_call|function_call_output|reasoning|item_reference
	ID     string      `json:"id,omitempty"`
	Status string      `json:"status,omitempty"` // in_progress|completed|incomplete

	// Message fields
	Role    string      `json:"role,omitempty"`    // user|assistant|system|developer
	Content interface{} `json:"content,omitempty"` // string or []ORContentPart

	// Function call fields
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// Function call output fields
	Output interface{} `json:"output,omitempty"` // string or []ORContentPart

	// Item reference fields
	ItemID string `json:"item_id,omitempty"` // for item_reference type
}

// ORContentPart represents a content block (discriminated union by type)
type ORContentPart struct {
	Type     string `json:"type"` // input_text|input_image|input_file|output_text|refusal
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
	Refusal  string `json:"refusal,omitempty"`
	Detail   string `json:"detail,omitempty"` // low|high|auto for images
}

// ORItemField represents an output item (same structure as ORItemParam)
type ORItemField = ORItemParam

// ORResponseResource represents the main response object
type ORResponseResource struct {
	ID              string            `json:"id"`
	Object          string            `json:"object"` // always "response"
	CreatedAt       int64             `json:"created_at"`
	CompletedAt     *int64            `json:"completed_at,omitempty"`
	Status          string            `json:"status"` // in_progress|completed|failed|incomplete
	Model           string            `json:"model"`
	Output          []ORItemField     `json:"output"`
	Error           *ORError          `json:"error,omitempty"`
	Usage           *ORUsage          `json:"usage,omitempty"`
	Tools           []ORFunctionTool  `json:"tools,omitempty"`
	ToolChoice      interface{}       `json:"tool_choice,omitempty"`
	Truncation      string            `json:"truncation,omitempty"`
	Temperature     float64           `json:"temperature,omitempty"`
	TopP            float64           `json:"top_p,omitempty"`
	MaxOutputTokens *int              `json:"max_output_tokens,omitempty"`
	Reasoning       *ORReasoning      `json:"reasoning,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Instructions    string            `json:"instructions,omitempty"`
	IncompleteDetails *ORIncompleteDetails `json:"incomplete_details,omitempty"`
}

// ORError represents an error in the response
type ORError struct {
	Type    string `json:"type"`    // invalid_request|not_found|server_error|model_error|too_many_requests
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
}

// ORUsage represents token usage statistics
type ORUsage struct {
	InputTokens        int                  `json:"input_tokens"`
	OutputTokens       int                  `json:"output_tokens"`
	TotalTokens        int                  `json:"total_tokens"`
	InputTokensDetails *ORInputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *OROutputTokensDetails `json:"output_tokens_details,omitempty"`
}

// ORInputTokensDetails represents input token breakdown
type ORInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// OROutputTokensDetails represents output token breakdown
type OROutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// ORReasoning represents reasoning configuration and metadata
type ORReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// ORIncompleteDetails represents details about why a response was incomplete
type ORIncompleteDetails struct {
	Reason string `json:"reason"`
}

// ORStreamEvent represents a streaming event
type ORStreamEvent struct {
	Type           string              `json:"type"`
	SequenceNumber int                 `json:"sequence_number"`
	Response       *ORResponseResource `json:"response,omitempty"`
	OutputIndex    *int                `json:"output_index,omitempty"`
	ContentIndex   *int                `json:"content_index,omitempty"`
	SummaryIndex   *int                `json:"summary_index,omitempty"`
	ItemID         string              `json:"item_id,omitempty"`
	Item           *ORItemField        `json:"item,omitempty"`
	Part           *ORContentPart      `json:"part,omitempty"`
	Delta          string              `json:"delta,omitempty"`
	Text           string              `json:"text,omitempty"`
	Arguments      string              `json:"arguments,omitempty"`
	Refusal        string              `json:"refusal,omitempty"`
	Error          *ORErrorPayload     `json:"error,omitempty"`
	Logprobs       []ORLogProb         `json:"logprobs,omitempty"`
	Obfuscation    string              `json:"obfuscation,omitempty"`
	Annotation     *ORAnnotation       `json:"annotation,omitempty"`
	AnnotationIndex *int               `json:"annotation_index,omitempty"`
}

// ORErrorPayload represents an error payload in streaming events
type ORErrorPayload struct {
	Type    string            `json:"type"`
	Code    string            `json:"code,omitempty"`
	Message string            `json:"message"`
	Param   string            `json:"param,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ORLogProb represents log probability information
type ORLogProb struct {
	Token       string       `json:"token"`
	Logprob     float64      `json:"logprob"`
	Bytes       []int        `json:"bytes"`
	TopLogprobs []ORTopLogProb `json:"top_logprobs,omitempty"`
}

// ORTopLogProb represents a top log probability
type ORTopLogProb struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
	Bytes   []int    `json:"bytes"`
}

// ORAnnotation represents an annotation (e.g., URL citation)
type ORAnnotation struct {
	Type        string `json:"type"` // url_citation
	StartIndex  int    `json:"start_index"`
	EndIndex    int    `json:"end_index"`
	URL         string `json:"url"`
	Title       string `json:"title"`
}
