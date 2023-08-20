package schema

import (
	"context"

	config "github.com/go-skynet/LocalAI/api/config"

	"github.com/go-skynet/LocalAI/pkg/grammar"
)

// APIError provides error information returned by the OpenAI API.
type APIError struct {
	Code    any     `json:"code,omitempty"`
	Message string  `json:"message"`
	Param   *string `json:"param,omitempty"`
	Type    string  `json:"type"`
}

type ErrorResponse struct {
	Error *APIError `json:"error,omitempty"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Item struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
	Object    string    `json:"object,omitempty"`

	// Images
	URL     string `json:"url,omitempty"`
	B64JSON string `json:"b64_json,omitempty"`
}

type OpenAIResponse struct {
	Created int      `json:"created,omitempty"`
	Object  string   `json:"object,omitempty"`
	ID      string   `json:"id,omitempty"`
	Model   string   `json:"model,omitempty"`
	Choices []Choice `json:"choices,omitempty"`
	Data    []Item   `json:"data,omitempty"`

	Usage OpenAIUsage `json:"usage"`
}

type Choice struct {
	Index        int      `json:"index"`
	FinishReason string   `json:"finish_reason,omitempty"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Message `json:"delta,omitempty"`
	Text         string   `json:"text,omitempty"`
}

type Message struct {
	// The message role
	Role string `json:"role,omitempty" yaml:"role"`
	// The message content
	Content *string `json:"content" yaml:"content"`
	// A result of a function call
	FunctionCall interface{} `json:"function_call,omitempty" yaml:"function_call,omitempty"`
}

type OpenAIModel struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type OpenAIRequest struct {
	config.PredictionOptions

	Context context.Context
	Cancel  context.CancelFunc

	// whisper
	File string `json:"file" validate:"required"`
	//whisper/image
	ResponseFormat string `json:"response_format"`
	// image
	Size string `json:"size"`
	// Prompt is read only by completion/image API calls
	Prompt interface{} `json:"prompt" yaml:"prompt"`

	// Edit endpoint
	Instruction string      `json:"instruction" yaml:"instruction"`
	Input       interface{} `json:"input" yaml:"input"`

	Stop interface{} `json:"stop" yaml:"stop"`

	// Messages is read only by chat/completion API calls
	Messages []Message `json:"messages" yaml:"messages"`

	// A list of available functions to call
	Functions    []grammar.Function `json:"functions" yaml:"functions"`
	FunctionCall interface{}        `json:"function_call" yaml:"function_call"` // might be a string or an object

	Stream bool `json:"stream"`

	// Image (not supported by OpenAI)
	Mode int `json:"mode"`
	Step int `json:"step"`

	// A grammar to constrain the LLM output
	Grammar string `json:"grammar" yaml:"grammar"`

	JSONFunctionGrammarObject *grammar.JSONFunctionStructure `json:"grammar_json_functions" yaml:"grammar_json_functions"`

	Backend string `json:"backend" yaml:"backend"`

	// AutoGPTQ
	ModelBaseName string `json:"model_base_name" yaml:"model_base_name"`
}
