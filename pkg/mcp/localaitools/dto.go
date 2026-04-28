package localaitools

// DTOs for the LocalAIClient interface. These are stripped, JSON-friendly
// representations of LocalAI's internal types — never the raw service types,
// so that inproc and httpapi clients serialize the same payloads.

// GallerySearchQuery is the input for gallery_search.
type GallerySearchQuery struct {
	Query    string `json:"query"           jsonschema:"Free-text query matched against model name, gallery and tags. Empty returns the first Limit models."`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return. Defaults to 20 when zero or negative."`
	Tag      string `json:"tag,omitempty"   jsonschema:"Optional tag filter (e.g. chat, embed, image)."`
	Gallery  string `json:"gallery,omitempty" jsonschema:"Restrict results to a specific gallery name."`
}

// GalleryModelHit is a single result from gallery_search.
type GalleryModelHit struct {
	Name        string   `json:"name"`
	Gallery     string   `json:"gallery"`
	URL         string   `json:"url,omitempty"`
	Description string   `json:"description,omitempty"`
	License     string   `json:"license,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Installed   bool     `json:"installed"`
}

// InstalledModel is one entry in list_installed_models.
type InstalledModel struct {
	Name         string   `json:"name"`
	Backend      string   `json:"backend,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Pinned       bool     `json:"pinned,omitempty"`
	Disabled     bool     `json:"disabled,omitempty"`
}

// Gallery is one entry in list_galleries.
type Gallery struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// JobStatus mirrors core/services/galleryop.OpStatus, with only the fields the
// LLM actually needs to drive its loop.
type JobStatus struct {
	ID                 string  `json:"id"`
	Processed          bool    `json:"processed"`
	Cancelled          bool    `json:"cancelled,omitempty"`
	Progress           float64 `json:"progress"`
	TotalFileSize      string  `json:"total_file_size,omitempty"`
	DownloadedFileSize string  `json:"downloaded_file_size,omitempty"`
	Message            string  `json:"message,omitempty"`
	ErrorMessage       string  `json:"error,omitempty"`
}

// ModelConfigView is a JSON view of a model config file.
type ModelConfigView struct {
	Name string         `json:"name"`
	YAML string         `json:"yaml,omitempty"  jsonschema:"Full YAML serialization of the model config."`
	JSON map[string]any `json:"json,omitempty"  jsonschema:"Parsed JSON view of the same config (convenience for diffing)."`
}

// InstallModelRequest is the input for install_model.
type InstallModelRequest struct {
	GalleryName string         `json:"gallery_name,omitempty" jsonschema:"The gallery the model lives in (from gallery_search). Optional when ModelName is unique across galleries."`
	ModelName   string         `json:"model_name"             jsonschema:"The canonical model name as returned by gallery_search."`
	Overrides   map[string]any `json:"overrides,omitempty"    jsonschema:"Optional config overrides to merge into the installed model's YAML."`
}

// InstallBackendRequest is the input for install_backend.
type InstallBackendRequest struct {
	GalleryName string `json:"gallery_name,omitempty" jsonschema:"Source backend gallery."`
	BackendName string `json:"backend_name"           jsonschema:"Backend identifier (e.g. llama-cpp)."`
}

// Backend is one entry in list_backends / list_known_backends.
type Backend struct {
	Name        string   `json:"name"`
	Gallery     string   `json:"gallery,omitempty"`
	Installed   bool     `json:"installed"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// SystemInfo summarises the LocalAI deployment.
type SystemInfo struct {
	Version          string   `json:"version"`
	Distributed      bool     `json:"distributed"`
	BackendsPath     string   `json:"backends_path,omitempty"`
	ModelsPath       string   `json:"models_path,omitempty"`
	LoadedModels     []string `json:"loaded_models,omitempty"`
	InstalledBackends []string `json:"installed_backends,omitempty"`
}

// Node is one entry in list_nodes.
type Node struct {
	ID          string `json:"id"`
	Address     string `json:"address,omitempty"`
	HTTPAddress string `json:"http_address,omitempty"`
	TotalVRAM   uint64 `json:"total_vram,omitempty"`
	Healthy     bool   `json:"healthy"`
	LastSeen    string `json:"last_seen,omitempty"`
}

// ImportModelURIRequest is the input for import_model_uri. It mirrors the
// REST surface (`/models/import-uri`) closely so both clients can produce
// identical responses; the BackendPreference is a flat field rather than the
// REST `preferences` JSON blob since the LLM only needs to specify a backend
// name when it disambiguates a multi-backend match.
type ImportModelURIRequest struct {
	URI               string         `json:"uri"                          jsonschema:"The model source. Accepts HuggingFace URLs (https://huggingface.co/...), OCI image references, http(s) URLs to a manifest, file:// paths, or a bare HF repo (e.g. Qwen/Qwen3-4B-GGUF)."`
	BackendPreference string         `json:"backend_preference,omitempty" jsonschema:"Optional backend name (e.g. llama-cpp). Required as the second-step retry when a previous import_model_uri call returned ambiguous_backend=true."`
	Overrides         map[string]any `json:"overrides,omitempty"          jsonschema:"Optional config overrides applied to the discovered model (e.g. context_size)."`
}

// ImportModelURIResponse is what import_model_uri returns. When
// AmbiguousBackend is true the LLM must surface the candidates to the user
// and call again with BackendPreference set; the JobID is empty in that case.
type ImportModelURIResponse struct {
	JobID               string   `json:"job_id,omitempty"`
	DiscoveredModelName string   `json:"discovered_model_name,omitempty"`
	AmbiguousBackend    bool     `json:"ambiguous_backend,omitempty"`
	Modality            string   `json:"modality,omitempty"`
	BackendCandidates   []string `json:"backend_candidates,omitempty"`
	Hint                string   `json:"hint,omitempty"`
}

// VRAMEstimateRequest is the input for vram_estimate.
type VRAMEstimateRequest struct {
	ModelName    string `json:"model_name"             jsonschema:"Installed model name."`
	ContextSize  int    `json:"context_size,omitempty" jsonschema:"Context size in tokens."`
	GPULayers    int    `json:"gpu_layers,omitempty"   jsonschema:"Number of layers to offload to GPU. -1 for all."`
	KVQuantBits  int    `json:"kv_quant_bits,omitempty" jsonschema:"KV cache quantization bits (e.g. 4, 8, 16)."`
}

// VRAMEstimate is the output of vram_estimate.
type VRAMEstimate struct {
	ModelName       string `json:"model_name"`
	EstimatedVRAMMB uint64 `json:"estimated_vram_mb"`
	WeightsMB       uint64 `json:"weights_mb,omitempty"`
	KVCacheMB       uint64 `json:"kv_cache_mb,omitempty"`
	OverheadMB      uint64 `json:"overhead_mb,omitempty"`
}
