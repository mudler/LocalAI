package schema

import (
	"context"
	"encoding/json"
)

// AnthropicRequest represents a request to the Anthropic Messages API
// https://docs.anthropic.com/claude/reference/messages_post
type AnthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []AnthropicMessage `json:"messages"`
	MaxTokens     int                `json:"max_tokens"`
	Metadata      map[string]string  `json:"metadata,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
	System        string             `json:"system,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice    interface{}        `json:"tool_choice,omitempty"`

	// Internal fields for request handling
	Context context.Context    `json:"-"`
	Cancel  context.CancelFunc `json:"-"`
}

// ModelName implements the LocalAIRequest interface
func (ar *AnthropicRequest) ModelName(s *string) string {
	if s != nil {
		ar.Model = *s
	}
	return ar.Model
}

// AnthropicTool represents a tool definition in the Anthropic format
type AnthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// AnthropicMessage represents a message in the Anthropic format
type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// AnthropicContentBlock represents a content block in an Anthropic message
type AnthropicContentBlock struct {
	Type       string                 `json:"type"`
	Text       string                 `json:"text,omitempty"`
	Source     *AnthropicImageSource  `json:"source,omitempty"`
	ID         string                 `json:"id,omitempty"`
	Name       string                 `json:"name,omitempty"`
	Input      map[string]interface{} `json:"input,omitempty"`
	ToolUseID  string                 `json:"tool_use_id,omitempty"`
	Content    interface{}            `json:"content,omitempty"`
	IsError    *bool                  `json:"is_error,omitempty"`
}

// AnthropicImageSource represents an image source in Anthropic format
type AnthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// AnthropicResponse represents a response from the Anthropic Messages API
type AnthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   *string                 `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage          `json:"usage"`
}

// AnthropicUsage represents token usage in Anthropic format
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// AnthropicStreamEvent represents a streaming event from the Anthropic API
type AnthropicStreamEvent struct {
	Type         string                  `json:"type"`
	Index        int                     `json:"index,omitempty"`
	ContentBlock *AnthropicContentBlock  `json:"content_block,omitempty"`
	Delta        *AnthropicStreamDelta   `json:"delta,omitempty"`
	Message      *AnthropicStreamMessage `json:"message,omitempty"`
	Usage        *AnthropicUsage         `json:"usage,omitempty"`
}

// AnthropicStreamDelta represents the delta in a streaming response
type AnthropicStreamDelta struct {
	Type         string  `json:"type,omitempty"`
	Text         string  `json:"text,omitempty"`
	PartialJSON  string  `json:"partial_json,omitempty"`
	StopReason   *string `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}

// AnthropicStreamMessage represents the message object in streaming events
type AnthropicStreamMessage struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   *string                 `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage          `json:"usage"`
}

// AnthropicErrorResponse represents an error response from the Anthropic API
type AnthropicErrorResponse struct {
	Type  string         `json:"type"`
	Error AnthropicError `json:"error"`
}

// AnthropicError represents an error in the Anthropic format
type AnthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// GetStringContent extracts the string content from an AnthropicMessage
// Content can be either a string or an array of content blocks
func (m *AnthropicMessage) GetStringContent() string {
	switch content := m.Content.(type) {
	case string:
		return content
	case []interface{}:
		var result string
		for _, block := range content {
			if blockMap, ok := block.(map[string]interface{}); ok {
				if blockMap["type"] == "text" {
					if text, ok := blockMap["text"].(string); ok {
						result += text
					}
				}
			}
		}
		return result
	}
	return ""
}

// GetContentBlocks extracts content blocks from an AnthropicMessage
func (m *AnthropicMessage) GetContentBlocks() []AnthropicContentBlock {
	switch content := m.Content.(type) {
	case string:
		return []AnthropicContentBlock{{Type: "text", Text: content}}
	case []interface{}:
		var blocks []AnthropicContentBlock
		for _, block := range content {
			if blockMap, ok := block.(map[string]interface{}); ok {
				cb := AnthropicContentBlock{}
				data, err := json.Marshal(blockMap)
				if err != nil {
					continue
				}
				if err := json.Unmarshal(data, &cb); err != nil {
					continue
				}
				blocks = append(blocks, cb)
			}
		}
		return blocks
	}
	return nil
}
