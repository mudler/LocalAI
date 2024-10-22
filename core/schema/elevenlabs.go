package schema

type ElevenLabsTTSRequest struct {
	Text    string `json:"text" yaml:"text"`
	ModelID string `json:"model_id" yaml:"model_id"`
}

type ElevenLabsSoundGenerationRequest struct {
	Text        string   `json:"text" yaml:"text"`
	ModelID     string   `json:"model_id" yaml:"model_id"`
	Duration    *float32 `json:"duration_seconds,omitempty" yaml:"duration_seconds,omitempty"`
	Temperature *float32 `json:"prompt_influence,omitempty" yaml:"prompt_influence,omitempty"`
	DoSample    *bool    `json:"do_sample,omitempty" yaml:"do_sample,omitempty"`
}
