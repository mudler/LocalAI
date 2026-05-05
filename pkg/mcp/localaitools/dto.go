package localaitools

// DTOs for the LocalAIClient interface. Where the same shape already exists
// elsewhere (config.Gallery, gallery.Metadata, schema.KnownBackend,
// vram.EstimateResult) we surface that type directly via the interface
// instead of maintaining a parallel DTO. The remaining types in this file
// are LLM-shaped views of internal state where the source struct carries
// fields the LLM shouldn't see (auth tokens, filesystem paths) or
// non-JSON-friendly fields (e.g. galleryop.OpStatus.Error which marshals
// to "{}" because it's an interface).

// GallerySearchQuery is the input for gallery_search.
type GallerySearchQuery struct {
	Query   string `json:"query"             jsonschema:"Free-text query matched against model name, gallery and tags. Empty returns the first Limit models."`
	Limit   int    `json:"limit,omitempty"   jsonschema:"Maximum number of results to return. Defaults to 20 when zero or negative."`
	Tag     string `json:"tag,omitempty"     jsonschema:"Optional tag filter (e.g. chat, embed, image)."`
	Gallery string `json:"gallery,omitempty" jsonschema:"Restrict results to a specific gallery name."`
}

// InstalledModel is one entry in list_installed_models. Distinct from
// config.ModelConfig (which is the full on-disk YAML — far too large to
// serialise per request); this is a summary the LLM can scan cheaply.
type InstalledModel struct {
	Name         string   `json:"name"`
	Backend      string   `json:"backend,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Pinned       bool     `json:"pinned,omitempty"`
	Disabled     bool     `json:"disabled,omitempty"`
}

// JobStatus is a JSON-friendly mirror of galleryop.OpStatus. We don't surface
// OpStatus directly because its `Error error` field marshals to `{}` (the
// json.Marshal default for an error interface), and the underlying status
// map keys jobs by UUID rather than carrying the ID on the value, so we
// add the ID here too. Keep field names aligned with OpStatus where they
// overlap so callers comparing the two don't have to translate.
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

// Backend is the LLM-facing summary returned by list_backends. We don't
// expose gallery.SystemBackend directly because it carries filesystem
// paths (RunFile, IsSystem, IsMeta, the full Metadata) the LLM doesn't
// need and the tokens add up. ListKnownBackends returns schema.KnownBackend
// directly — that one is already the canonical wire shape.
type Backend struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
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

// Branding is the LLM-facing view of the instance's whitelabel settings.
// Only the configurable text fields and the resolved asset URLs are
// surfaced — the backing filenames on disk stay an implementation detail.
type Branding struct {
	InstanceName      string `json:"instance_name"`
	InstanceTagline   string `json:"instance_tagline"`
	LogoURL           string `json:"logo_url"`
	LogoHorizontalURL string `json:"logo_horizontal_url"`
	FaviconURL        string `json:"favicon_url"`
}

// SetBrandingRequest is the input for set_branding. Both fields are
// optional; nil leaves the existing value untouched. Asset uploads are
// deliberately excluded from MCP — admins use the Settings UI for that.
type SetBrandingRequest struct {
	InstanceName    *string `json:"instance_name,omitempty"    jsonschema:"New instance display name (replaces \"LocalAI\" in headers, footers, and the browser tab). Pass an empty string to reset to default."`
	InstanceTagline *string `json:"instance_tagline,omitempty" jsonschema:"Optional short subtitle shown beneath the instance name. Pass an empty string to clear."`
}

// VRAMEstimateRequest is the input for vram_estimate. The output type is
// pkg/vram.EstimateResult — used directly via the LocalAIClient interface
// so the LLM sees the same shape (size_bytes/size_display/vram_bytes/
// vram_display) that the REST endpoint returns.
type VRAMEstimateRequest struct {
	ModelName   string `json:"model_name"              jsonschema:"Installed model name."`
	ContextSize int    `json:"context_size,omitempty"  jsonschema:"Context size in tokens."`
	GPULayers   int    `json:"gpu_layers,omitempty"    jsonschema:"Number of layers to offload to GPU. -1 for all."`
	KVQuantBits int    `json:"kv_quant_bits,omitempty" jsonschema:"KV cache quantization bits (e.g. 4, 8, 16)."`
}
