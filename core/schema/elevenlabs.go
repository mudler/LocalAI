package schema

type ElevenLabsTTSRequest struct {
	Text    string `json:"text" yaml:"text"`
	ModelID string `json:"model_id" yaml:"model_id"`
}
