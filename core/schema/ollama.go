package schema

import (
	"context"
	"time"
)

// OllamaOptions represents runtime parameters for Ollama generation
type OllamaOptions struct {
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	TopK          *int     `json:"top_k,omitempty"`
	NumPredict    *int     `json:"num_predict,omitempty"`
	RepeatPenalty float64  `json:"repeat_penalty,omitempty"`
	RepeatLastN   int      `json:"repeat_last_n,omitempty"`
	Seed          *int     `json:"seed,omitempty"`
	Stop          []string `json:"stop,omitempty"`
	NumCtx        int      `json:"num_ctx,omitempty"`
}

// OllamaMessage represents a message in Ollama chat format
type OllamaMessage struct {
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	Images    []string `json:"images,omitempty"`
	ToolCalls []any    `json:"tool_calls,omitempty"`
}

// OllamaChatRequest represents a request to the Ollama Chat API
type OllamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []OllamaMessage `json:"messages"`
	Stream   *bool           `json:"stream,omitempty"`
	Format   any             `json:"format,omitempty"`
	Options  *OllamaOptions  `json:"options,omitempty"`
	Tools    []any           `json:"tools,omitempty"`

	// Internal fields
	Context context.Context    `json:"-"`
	Cancel  context.CancelFunc `json:"-"`
}

// ModelName implements the LocalAIRequest interface
func (r *OllamaChatRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// IsStream returns whether streaming is enabled (defaults to true for Ollama)
func (r *OllamaChatRequest) IsStream() bool {
	if r.Stream == nil {
		return true
	}
	return *r.Stream
}

// OllamaChatResponse represents a response from the Ollama Chat API
type OllamaChatResponse struct {
	Model              string        `json:"model"`
	CreatedAt          time.Time     `json:"created_at"`
	Message            OllamaMessage `json:"message"`
	Done               bool          `json:"done"`
	DoneReason         string        `json:"done_reason,omitempty"`
	TotalDuration      int64         `json:"total_duration,omitempty"`
	LoadDuration       int64         `json:"load_duration,omitempty"`
	PromptEvalCount    int           `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64         `json:"prompt_eval_duration,omitempty"`
	EvalCount          int           `json:"eval_count,omitempty"`
	EvalDuration       int64         `json:"eval_duration,omitempty"`
}

// OllamaGenerateRequest represents a request to the Ollama Generate API
type OllamaGenerateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	System  string         `json:"system,omitempty"`
	Stream  *bool          `json:"stream,omitempty"`
	Raw     bool           `json:"raw,omitempty"`
	Format  any            `json:"format,omitempty"`
	Options *OllamaOptions `json:"options,omitempty"`
	// Context from a previous generate call for continuation
	Context []int `json:"context,omitempty"`

	// Internal fields
	Ctx    context.Context    `json:"-"`
	Cancel context.CancelFunc `json:"-"`
}

// ModelName implements the LocalAIRequest interface
func (r *OllamaGenerateRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// IsStream returns whether streaming is enabled (defaults to true for Ollama)
func (r *OllamaGenerateRequest) IsStream() bool {
	if r.Stream == nil {
		return true
	}
	return *r.Stream
}

// OllamaGenerateResponse represents a response from the Ollama Generate API
type OllamaGenerateResponse struct {
	Model              string    `json:"model"`
	CreatedAt          time.Time `json:"created_at"`
	Response           string    `json:"response"`
	Done               bool      `json:"done"`
	DoneReason         string    `json:"done_reason,omitempty"`
	Context            []int     `json:"context,omitempty"`
	TotalDuration      int64     `json:"total_duration,omitempty"`
	LoadDuration       int64     `json:"load_duration,omitempty"`
	PromptEvalCount    int       `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64     `json:"prompt_eval_duration,omitempty"`
	EvalCount          int       `json:"eval_count,omitempty"`
	EvalDuration       int64     `json:"eval_duration,omitempty"`
}

// OllamaEmbedRequest represents a request to the Ollama Embed API
type OllamaEmbedRequest struct {
	Model   string `json:"model"`
	Input   any    `json:"input"` // string or []string
	Options *OllamaOptions `json:"options,omitempty"`
}

// ModelName implements the LocalAIRequest interface
func (r *OllamaEmbedRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// GetInputStrings normalizes the Input field to a string slice
func (r *OllamaEmbedRequest) GetInputStrings() []string {
	switch v := r.Input.(type) {
	case string:
		return []string{v}
	case []any:
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return v
	}
	return nil
}

// OllamaEmbedResponse represents a response from the Ollama Embed API
type OllamaEmbedResponse struct {
	Model           string      `json:"model"`
	Embeddings      [][]float32 `json:"embeddings"`
	TotalDuration   int64       `json:"total_duration,omitempty"`
	LoadDuration    int64       `json:"load_duration,omitempty"`
	PromptEvalCount int         `json:"prompt_eval_count,omitempty"`
}

// OllamaShowRequest represents a request to the Ollama Show API
type OllamaShowRequest struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	Verbose bool   `json:"verbose,omitempty"`
}

// ModelName implements the LocalAIRequest interface
func (r *OllamaShowRequest) ModelName(s *string) string {
	name := r.Name
	if name == "" {
		name = r.Model
	}
	if s != nil {
		r.Name = *s
	}
	return name
}

// OllamaShowResponse represents a response from the Ollama Show API
type OllamaShowResponse struct {
	Modelfile  string             `json:"modelfile"`
	Parameters string             `json:"parameters"`
	Template   string             `json:"template"`
	License    string             `json:"license,omitempty"`
	Details    OllamaModelDetails `json:"details"`
}

// OllamaModelDetails contains model metadata
type OllamaModelDetails struct {
	ParentModel       string   `json:"parent_model,omitempty"`
	Format            string   `json:"format,omitempty"`
	Family            string   `json:"family,omitempty"`
	Families          []string `json:"families,omitempty"`
	ParameterSize     string   `json:"parameter_size,omitempty"`
	QuantizationLevel string   `json:"quantization_level,omitempty"`
}

// OllamaModelEntry represents a model in the list response
type OllamaModelEntry struct {
	Name       string             `json:"name"`
	Model      string             `json:"model"`
	ModifiedAt time.Time          `json:"modified_at"`
	Size       int64              `json:"size"`
	Digest     string             `json:"digest"`
	Details    OllamaModelDetails `json:"details"`
}

// OllamaListResponse represents a response from the Ollama Tags API
type OllamaListResponse struct {
	Models []OllamaModelEntry `json:"models"`
}

// OllamaPsEntry represents a running model in the ps response
type OllamaPsEntry struct {
	Name       string             `json:"name"`
	Model      string             `json:"model"`
	Size       int64              `json:"size"`
	Digest     string             `json:"digest"`
	Details    OllamaModelDetails `json:"details"`
	ExpiresAt  time.Time          `json:"expires_at"`
	SizeVRAM   int64              `json:"size_vram"`
}

// OllamaPsResponse represents a response from the Ollama Ps API
type OllamaPsResponse struct {
	Models []OllamaPsEntry `json:"models"`
}

// OllamaVersionResponse represents a response from the Ollama Version API
type OllamaVersionResponse struct {
	Version string `json:"version"`
}

// OllamaPullRequest represents a request to pull a model
type OllamaPullRequest struct {
	Name     string `json:"name"`
	Insecure bool   `json:"insecure,omitempty"`
	Stream   *bool  `json:"stream,omitempty"`
}

// OllamaDeleteRequest represents a request to delete a model
type OllamaDeleteRequest struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

// OllamaCopyRequest represents a request to copy a model
type OllamaCopyRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}
