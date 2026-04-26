package schema

// QuantizationJobRequest is the REST API request to start a quantization job.
type QuantizationJobRequest struct {
	Model            string            `json:"model"`                       // HF model name or local path
	Backend          string            `json:"backend"`                     // "llama-cpp-quantization"
	QuantizationType string            `json:"quantization_type,omitempty"` // q4_k_m, q5_k_m, q8_0, f16, etc.
	ExtraOptions     map[string]string `json:"extra_options,omitempty"`
}

// QuantizationJob represents a quantization job with its current state.
type QuantizationJob struct {
	ID               string            `json:"id"`
	UserID           string            `json:"user_id,omitempty"`
	Model            string            `json:"model"`
	Backend          string            `json:"backend"`
	ModelID          string            `json:"model_id,omitempty"`
	QuantizationType string            `json:"quantization_type"`
	Status           string            `json:"status"` // queued, downloading, converting, quantizing, completed, failed, stopped
	Message          string            `json:"message,omitempty"`
	OutputDir        string            `json:"output_dir"`
	OutputFile       string            `json:"output_file,omitempty"` // path to final GGUF
	ExtraOptions     map[string]string `json:"extra_options,omitempty"`
	CreatedAt        string            `json:"created_at"`

	// Import state (tracked separately from quantization status)
	ImportStatus    string `json:"import_status,omitempty"` // "", "importing", "completed", "failed"
	ImportMessage   string `json:"import_message,omitempty"`
	ImportModelName string `json:"import_model_name,omitempty"` // registered model name after import

	// Full config for reuse
	Config *QuantizationJobRequest `json:"config,omitempty"`
}

// QuantizationJobResponse is the REST API response when creating a job.
type QuantizationJobResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// QuantizationProgressEvent is an SSE event for quantization progress.
type QuantizationProgressEvent struct {
	JobID           string             `json:"job_id"`
	ProgressPercent float32            `json:"progress_percent"`
	Status          string             `json:"status"`
	Message         string             `json:"message,omitempty"`
	OutputFile      string             `json:"output_file,omitempty"`
	ExtraMetrics    map[string]float32 `json:"extra_metrics,omitempty"`
}

// QuantizationImportRequest is the REST API request to import a quantized model.
type QuantizationImportRequest struct {
	Name string `json:"name,omitempty"` // model name for LocalAI (auto-generated if empty)
}
