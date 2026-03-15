package localai

// ModelResponse is the response structure for model operations
type ModelResponse struct {
	Success  bool        `json:"success"`
	Message  string      `json:"message"`
	Filename string      `json:"filename,omitempty"`
	Config   interface{} `json:"config,omitempty"`
	Error    string      `json:"error,omitempty"`
	Details  []string    `json:"details,omitempty"`
	NewConfig bool       `json:"new_config,omitempty"`
}
