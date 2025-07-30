package schema

import (
	"context"

	functions "github.com/mudler/LocalAI/pkg/functions"
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
	// Extra timing data, disabled by default as is't not a part of OpenAI specification
	TimingPromptProcessing float64 `json:"timing_prompt_processing,omitempty"`
	TimingTokenGeneration  float64 `json:"timing_token_generation,omitempty"`
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
	FinishReason string   `json:"finish_reason"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Message `json:"delta,omitempty"`
	Text         string   `json:"text,omitempty"`
}

type Content struct {
	Type       string     `json:"type" yaml:"type"`
	Text       string     `json:"text" yaml:"text"`
	ImageURL   ContentURL `json:"image_url" yaml:"image_url"`
	AudioURL   ContentURL `json:"audio_url" yaml:"audio_url"`
	VideoURL   ContentURL `json:"video_url" yaml:"video_url"`
	InputAudio InputAudio `json:"input_audio" yaml:"input_audio"`
}

type ContentURL struct {
	URL string `json:"url" yaml:"url"`
}

type InputAudio struct {
	// Format identifies the audio format, e.g. 'wav'.
	Format string `json:"format" yaml:"format"`
	// Data holds the base64-encoded audio data.
	Data string `json:"data" yaml:"data"`
}

type Message struct {
	// The message role
	Role string `json:"role,omitempty" yaml:"role"`

	// The message name (used for tools calls)
	Name string `json:"name,omitempty" yaml:"name"`

	// The message content
	Content interface{} `json:"content" yaml:"content"`

	StringContent string   `json:"string_content,omitempty" yaml:"string_content,omitempty"`
	StringImages  []string `json:"string_images,omitempty" yaml:"string_images,omitempty"`
	StringVideos  []string `json:"string_videos,omitempty" yaml:"string_videos,omitempty"`
	StringAudios  []string `json:"string_audios,omitempty" yaml:"string_audios,omitempty"`

	// A result of a function call
	FunctionCall interface{} `json:"function_call,omitempty" yaml:"function_call,omitempty"`

	ToolCalls []ToolCall `json:"tool_calls,omitempty" yaml:"tool_call,omitempty"`
}

type ToolCall struct {
	Index        int          `json:"index"`
	ID           string       `json:"id"`
	Type         string       `json:"type"`
	FunctionCall FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type OpenAIModel struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type ImageGenerationResponseFormat string

type ChatCompletionResponseFormatType string

type ChatCompletionResponseFormat struct {
	Type ChatCompletionResponseFormatType `json:"type,omitempty"`
}

type JsonSchemaRequest struct {
	Type       string     `json:"type"`
	JsonSchema JsonSchema `json:"json_schema"`
}

type JsonSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema functions.Item `json:"schema"`
}

type OpenAIRequest struct {
	PredictionOptions

	Context context.Context    `json:"-"`
	Cancel  context.CancelFunc `json:"-"`

	// whisper
	File string `json:"file" validate:"required"`
	// Multiple input images for img2img or inpainting
	Files []string `json:"files,omitempty"`
	// Reference images for models that support them (e.g., Flux Kontext)
	RefImages []string `json:"ref_images,omitempty"`
	//whisper/image
	ResponseFormat interface{} `json:"response_format,omitempty"`
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
	Functions    functions.Functions `json:"functions" yaml:"functions"`
	FunctionCall interface{}         `json:"function_call" yaml:"function_call"` // might be a string or an object

	Tools       []functions.Tool `json:"tools,omitempty" yaml:"tools"`
	ToolsChoice interface{}      `json:"tool_choice,omitempty" yaml:"tool_choice"`

	Stream bool `json:"stream"`

	// Image (not supported by OpenAI)
	Mode    int    `json:"mode"`
	Quality string `json:"quality"`
	Step    int    `json:"step"`

	// A grammar to constrain the LLM output
	Grammar string `json:"grammar" yaml:"grammar"`

	JSONFunctionGrammarObject *functions.JSONFunctionStructure `json:"grammar_json_functions" yaml:"grammar_json_functions"`

	Backend string `json:"backend" yaml:"backend"`

	ModelBaseName string `json:"model_base_name" yaml:"model_base_name"`
}

type ModelsDataResponse struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}
