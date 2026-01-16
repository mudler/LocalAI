package schema

import (
	"context"
)

// OpenResponsesRequest represents a request to the Open Responses API
// https://www.openresponses.org/specification
type OpenResponsesRequest struct {
	Model              string            `json:"model"`
	Input              interface{}       `json:"input"` // string or []ORItemParam
	Tools              []ORFunctionTool  `json:"tools,omitempty"`
	ToolChoice         interface{}       `json:"tool_choice,omitempty"` // "auto"|"required"|"none"|{type:"function",name:"..."}
	Stream             bool              `json:"stream,omitempty"`
	MaxOutputTokens    *int              `json:"max_output_tokens,omitempty"`
	Temperature        *float64          `json:"temperature,omitempty"`
	TopP               *float64          `json:"top_p,omitempty"`
	Truncation         string            `json:"truncation,omitempty"` // "auto"|"disabled"
	Instructions       string            `json:"instructions,omitempty"`
	Reasoning          *ORReasoningParam `json:"reasoning,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	PreviousResponseID string            `json:"previous_response_id,omitempty"`

	// Additional parameters from spec
	TextFormat        interface{} `json:"text_format,omitempty"`         // TextResponseFormat or JsonSchemaResponseFormatParam
	ServiceTier       string      `json:"service_tier,omitempty"`        // "auto"|"default"|priority hint
	AllowedTools      []string    `json:"allowed_tools,omitempty"`       // Restrict which tools can be invoked
	Store             *bool       `json:"store,omitempty"`               // Whether to store the response
	Include           []string    `json:"include,omitempty"`             // What to include in response
	ParallelToolCalls *bool       `json:"parallel_tool_calls,omitempty"` // Allow parallel tool calls
	PresencePenalty   *float64    `json:"presence_penalty,omitempty"`    // Presence penalty (-2.0 to 2.0)
	FrequencyPenalty  *float64    `json:"frequency_penalty,omitempty"`   // Frequency penalty (-2.0 to 2.0)
	TopLogprobs       *int        `json:"top_logprobs,omitempty"`        // Number of top logprobs to return
	Background        *bool       `json:"background,omitempty"`          // Run request in background
	MaxToolCalls      *int        `json:"max_tool_calls,omitempty"`      // Maximum number of tool calls

	// OpenAI-compatible extensions (not in Open Responses spec)
	LogitBias map[string]float64 `json:"logit_bias,omitempty"` // Map of token IDs to bias values (-100 to 100)

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
	Strict      bool                   `json:"strict"` // Always include in response
}

// ORReasoningParam represents reasoning configuration
type ORReasoningParam struct {
	Effort  string `json:"effort,omitempty"`  // "none"|"low"|"medium"|"high"|"xhigh"
	Summary string `json:"summary,omitempty"` // "auto"|"concise"|"detailed"
}

// ORItemParam represents an input/output item (discriminated union by type)
type ORItemParam struct {
	Type   string `json:"type"`             // message|function_call|function_call_output|reasoning|item_reference
	ID     string `json:"id,omitempty"`     // Present for all output items
	Status string `json:"status,omitempty"` // in_progress|completed|incomplete

	// Message fields
	Role    string      `json:"role,omitempty"`    // user|assistant|system|developer
	Content interface{} `json:"content,omitempty"` // string or []ORContentPart for messages

	// Function call fields
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// Function call output fields
	Output interface{} `json:"output,omitempty"` // string or []ORContentPart

	// Note: For item_reference type, use the ID field above to reference the item
}

// ORContentPart represents a content block (discriminated union by type)
// For output_text: type, text, annotations, logprobs are ALL REQUIRED per Open Responses spec
type ORContentPart struct {
	Type        string         `json:"type"`        // input_text|input_image|input_file|output_text|refusal
	Text        string         `json:"text"`        // REQUIRED for output_text - must always be present (even if empty)
	Annotations []ORAnnotation `json:"annotations"` // REQUIRED for output_text - must always be present (use [])
	Logprobs    []ORLogProb    `json:"logprobs"`    // REQUIRED for output_text - must always be present (use [])
	ImageURL    string         `json:"image_url,omitempty"`
	FileURL     string         `json:"file_url,omitempty"`
	Filename    string         `json:"filename,omitempty"`
	FileData    string         `json:"file_data,omitempty"`
	Refusal     string         `json:"refusal,omitempty"`
	Detail      string         `json:"detail,omitempty"` // low|high|auto for images
}

// OROutputTextContentPart is an alias for ORContentPart used specifically for output_text
type OROutputTextContentPart = ORContentPart

// ORItemField represents an output item (same structure as ORItemParam)
type ORItemField = ORItemParam

// ORResponseResource represents the main response object
type ORResponseResource struct {
	ID                 string               `json:"id"`
	Object             string               `json:"object"` // always "response"
	CreatedAt          int64                `json:"created_at"`
	CompletedAt        *int64               `json:"completed_at"` // Required: present as number or null
	Status             string               `json:"status"`       // in_progress|completed|failed|incomplete
	Model              string               `json:"model"`
	Output             []ORItemField        `json:"output"`
	Error              *ORError             `json:"error"`              // Always present, null if no error
	IncompleteDetails  *ORIncompleteDetails `json:"incomplete_details"` // Always present, null if complete
	PreviousResponseID *string              `json:"previous_response_id"`
	Instructions       *string              `json:"instructions"`

	// Tool-related fields
	Tools             []ORFunctionTool `json:"tools"` // Always present, empty array if no tools
	ToolChoice        interface{}      `json:"tool_choice"`
	ParallelToolCalls bool             `json:"parallel_tool_calls"`
	MaxToolCalls      *int             `json:"max_tool_calls"` // nullable

	// Sampling parameters (always required)
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	PresencePenalty  float64 `json:"presence_penalty"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	TopLogprobs      int     `json:"top_logprobs"` // Default to 0
	MaxOutputTokens  *int    `json:"max_output_tokens"`

	// Text format configuration
	Text *ORTextConfig `json:"text"`

	// Truncation and reasoning
	Truncation string       `json:"truncation"`
	Reasoning  *ORReasoning `json:"reasoning"` // nullable

	// Usage statistics
	Usage *ORUsage `json:"usage"` // nullable

	// Metadata and operational flags
	Metadata    map[string]string `json:"metadata"`
	Store       bool              `json:"store"`
	Background  bool              `json:"background"`
	ServiceTier string            `json:"service_tier"`

	// Safety and caching
	SafetyIdentifier *string `json:"safety_identifier"` // nullable
	PromptCacheKey   *string `json:"prompt_cache_key"`  // nullable
}

// ORTextConfig represents text format configuration
type ORTextConfig struct {
	Format *ORTextFormat `json:"format,omitempty"`
}

// ORTextFormat represents the text format type
type ORTextFormat struct {
	Type string `json:"type"` // "text" or "json_schema"
}

// ORError represents an error in the response
type ORError struct {
	Type    string `json:"type"` // invalid_request|not_found|server_error|model_error|too_many_requests
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
}

// ORUsage represents token usage statistics
type ORUsage struct {
	InputTokens         int                    `json:"input_tokens"`
	OutputTokens        int                    `json:"output_tokens"`
	TotalTokens         int                    `json:"total_tokens"`
	InputTokensDetails  *ORInputTokensDetails  `json:"input_tokens_details"`  // Always present
	OutputTokensDetails *OROutputTokensDetails `json:"output_tokens_details"` // Always present
}

// ORInputTokensDetails represents input token breakdown
type ORInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"` // Always include, even if 0
}

// OROutputTokensDetails represents output token breakdown
type OROutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"` // Always include, even if 0
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
// Note: Fields like delta, text, logprobs should be set explicitly for events that require them
// The sendSSEEvent function uses a custom serializer to handle conditional field inclusion
type ORStreamEvent struct {
	Type            string              `json:"type"`
	SequenceNumber  int                 `json:"sequence_number"`
	Response        *ORResponseResource `json:"response,omitempty"`
	OutputIndex     *int                `json:"output_index,omitempty"`
	ContentIndex    *int                `json:"content_index,omitempty"`
	SummaryIndex    *int                `json:"summary_index,omitempty"`
	ItemID          string              `json:"item_id,omitempty"`
	Item            *ORItemField        `json:"item,omitempty"`
	Part            *ORContentPart      `json:"part,omitempty"`
	Delta           *string             `json:"delta,omitempty"`     // Pointer to distinguish unset from empty
	Text            *string             `json:"text,omitempty"`      // Pointer to distinguish unset from empty
	Arguments       *string             `json:"arguments,omitempty"` // Pointer to distinguish unset from empty
	Refusal         string              `json:"refusal,omitempty"`
	Error           *ORErrorPayload     `json:"error,omitempty"`
	Logprobs        *[]ORLogProb        `json:"logprobs,omitempty"` // Pointer to distinguish unset from empty
	Obfuscation     string              `json:"obfuscation,omitempty"`
	Annotation      *ORAnnotation       `json:"annotation,omitempty"`
	AnnotationIndex *int                `json:"annotation_index,omitempty"`
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
	Token       string         `json:"token"`
	Logprob     float64        `json:"logprob"`
	Bytes       []int          `json:"bytes"`
	TopLogprobs []ORTopLogProb `json:"top_logprobs,omitempty"`
}

// ORTopLogProb represents a top log probability
type ORTopLogProb struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
	Bytes   []int   `json:"bytes"`
}

// ORAnnotation represents an annotation (e.g., URL citation)
type ORAnnotation struct {
	Type       string `json:"type"` // url_citation
	StartIndex int    `json:"start_index"`
	EndIndex   int    `json:"end_index"`
	URL        string `json:"url"`
	Title      string `json:"title"`
}

// ORContentPartWithLogprobs creates an output_text content part with logprobs converted from OpenAI format
func ORContentPartWithLogprobs(text string, logprobs *Logprobs) ORContentPart {
	orLogprobs := []ORLogProb{}

	// Convert OpenAI-style logprobs to Open Responses format
	if logprobs != nil && len(logprobs.Content) > 0 {
		for _, lp := range logprobs.Content {
			// Convert top logprobs
			topLPs := []ORTopLogProb{}
			for _, tlp := range lp.TopLogprobs {
				topLPs = append(topLPs, ORTopLogProb{
					Token:   tlp.Token,
					Logprob: tlp.Logprob,
					Bytes:   tlp.Bytes,
				})
			}

			orLogprobs = append(orLogprobs, ORLogProb{
				Token:       lp.Token,
				Logprob:     lp.Logprob,
				Bytes:       lp.Bytes,
				TopLogprobs: topLPs,
			})
		}
	}

	return ORContentPart{
		Type:        "output_text",
		Text:        text,
		Annotations: []ORAnnotation{}, // REQUIRED - must always be present as array (empty if none)
		Logprobs:    orLogprobs,       // REQUIRED - must always be present as array (empty if none)
	}
}
