package localai

// ModelResponse represents the common response structure for model operations
type ModelResponse struct {
	Success  bool        `json:"success"`
	Message  string      `json:"message"`
	Filename string      `json:"filename,omitempty"`
	Config   interface{} `json:"config,omitempty"`
	Error    string      `json:"error,omitempty"`
	Details  []string    `json:"details,omitempty"`
}
