package localai

// ModelRequest represents the common request structure for importing and editing models
type ModelRequest struct {
	Name               string `form:"name"`
	Backend            string `form:"backend"`
	Description        string `form:"description"`
	Usage              string `form:"usage"`
	Model              string `form:"model"`
	MMProj             string `form:"mmproj"`
	ChatTemplate       string `form:"chat_template"`
	CompletionTemplate string `form:"completion_template"`
	SystemPrompt       string `form:"system_prompt"`
	Temperature        string `form:"temperature"`
	TopP               string `form:"top_p"`
	TopK               string `form:"top_k"`
	ContextSize        string `form:"context_size"`
	Threads            string `form:"threads"`
	Seed               string `form:"seed"`
	LoraAdapter        string `form:"lora_adapter"`
	Grammar            string `form:"grammar"`
	PipelineType       string `form:"pipeline_type"`
	SchedulerType      string `form:"scheduler_type"`
	AudioPath          string `form:"audio_path"`
	Voice              string `form:"voice"`
	F16                bool   `form:"f16"`
	CUDA               bool   `form:"cuda"`
	Embeddings         bool   `form:"embeddings"`
	Debug              bool   `form:"debug"`
	MMap               bool   `form:"mmap"`
	MMlock             bool   `form:"mmlock"`
}

// ModelResponse represents the common response structure for model operations
type ModelResponse struct {
	Success  bool        `json:"success"`
	Message  string      `json:"message"`
	Filename string      `json:"filename,omitempty"`
	Config   interface{} `json:"config,omitempty"`
	Error    string      `json:"error,omitempty"`
	Details  []string    `json:"details,omitempty"`
}
