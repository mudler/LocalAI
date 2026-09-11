package schema

// ModelLoadingStatus describes a cold load that is still in progress. In
// distributed mode a model can take tens of minutes to stage onto a worker,
// which is far longer than a request may be held; a caller that runs out of
// wait budget gets this instead of an anonymous hang or a misleading error.
type ModelLoadingStatus struct {
	Model      string  `json:"model"`
	State      string  `json:"state"`
	Node       string  `json:"node,omitempty"`
	Progress   float64 `json:"progress"`
	BytesSent  int64   `json:"bytes_sent"`
	TotalBytes int64   `json:"total_bytes"`
	FileIndex  int     `json:"file_index"`
	TotalFiles int     `json:"total_files"`
	// ETASeconds is omitted rather than guessed until enough bytes have moved
	// for the observed rate to mean anything. A confidently wrong ETA on a
	// twenty-minute wait is worse than none.
	ETASeconds int `json:"eta_seconds,omitempty"`
}

// ModelLoadingResponse is the 503 body served while a model is still loading.
// The `error` envelope keeps OpenAI-client compatibility; `loading` is additive,
// so existing clients ignore it and load-aware ones can render real progress.
type ModelLoadingResponse struct {
	Error   *APIError           `json:"error,omitempty"`
	Loading *ModelLoadingStatus `json:"loading,omitempty"`
}
