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

// UsageStatsQuery is the input for get_usage_stats. UserID is optional;
// when empty the tool returns the calling user's own usage in auth-on
// mode, or the synthetic local user's usage in single-user no-auth
// mode. Admins (or the local user) may pass UserID to inspect another
// user; the LocalAIClient implementation enforces the role check.
type UsageStatsQuery struct {
	Period string `json:"period,omitempty" jsonschema:"Time window. One of: day, week, month, all. Defaults to month."`
	UserID string `json:"user_id,omitempty" jsonschema:"Optional user id to query. Empty = caller's own usage. Querying another user requires admin role."`
	All    bool   `json:"all,omitempty"     jsonschema:"When true, returns the cluster-wide /api/usage/all view (admin-only when auth is on)."`
}

// UsageStats is the response shape for get_usage_stats. Mirrors what
// /api/usage and /api/usage/all return so the LLM can correlate
// dashboard numbers with what it pulls via MCP.
type UsageStats struct {
	Viewer  UsageViewer   `json:"viewer"`
	Period  string        `json:"period"`
	Totals  UsageTotals   `json:"totals"`
	Buckets []UsageBucket `json:"buckets"`
}

type UsageViewer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

type UsageTotals struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	RequestCount     int64 `json:"request_count"`
}

type UsageBucket struct {
	Bucket           string `json:"bucket"`
	Model            string `json:"model"`
	UserID           string `json:"user_id,omitempty"`
	UserName         string `json:"user_name,omitempty"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	RequestCount     int64  `json:"request_count"`
}

// ---- PII / sensitive data tools ----

// PIIPattern is one row in the list_pii_patterns response.
type PIIPattern struct {
	ID             string `json:"id"`
	Description    string `json:"description"`
	Action         string `json:"action"` // mask | block | allow
	MaxMatchLength int    `json:"max_match_length"`
}

// PIIEventsQuery filters get_pii_events.
type PIIEventsQuery struct {
	CorrelationID string `json:"correlation_id,omitempty" jsonschema:"Optional X-Correlation-ID join key (binds events to the request and usage record)."`
	UserID        string `json:"user_id,omitempty"        jsonschema:"Optional user id to scope the query."`
	PatternID     string `json:"pattern_id,omitempty"     jsonschema:"Optional pattern id (e.g. email, ssn)."`
	Limit         int    `json:"limit,omitempty"          jsonschema:"Maximum events. Defaults to 100."`
}

// PIIEvent is the LLM-facing view of one redaction record. The matched
// value is never exposed; admins audit by hash_prefix.
type PIIEvent struct {
	ID            string `json:"id"`
	CorrelationID string `json:"correlation_id"`
	UserID        string `json:"user_id"`
	Direction     string `json:"direction"`
	PatternID     string `json:"pattern_id"`
	ByteOffset    int    `json:"byte_offset"`
	Length        int    `json:"length"`
	HashPrefix    string `json:"hash_prefix"`
	Action        string `json:"action"`
	CreatedAt     string `json:"created_at"`
}

// PIIRedactTestRequest is the input for test_pii_redaction.
type PIIRedactTestRequest struct {
	Text string `json:"text" jsonschema:"The candidate text. Will be run through the redactor without recording an event."`
}

// PIIRedactTestResult is the output for test_pii_redaction. spans
// describes where the redactor matched; redacted is the text after
// applying mask actions; blocked / masked flag what was done.
type PIIRedactTestResult struct {
	Redacted string         `json:"redacted"`
	Spans    []PIIEventSpan `json:"spans"`
	Blocked  bool           `json:"blocked"`
	Masked   bool           `json:"masked"`
}

type PIIEventSpan struct {
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Pattern    string `json:"pattern"`
	HashPrefix string `json:"hash_prefix"`
}

// PIIPatternActionUpdate is the input for set_pii_pattern_action.
// At least one of Action or Disabled must be set. Mutations are
// transient by default — call persist_pii_patterns to flush them
// to runtime_settings.json so the next start re-applies them.
type PIIPatternActionUpdate struct {
	ID       string `json:"id" jsonschema:"Pattern id to mutate (e.g. email, ssn, credit_card, api_key_prefix)."`
	Action   string `json:"action,omitempty" jsonschema:"New action: mask, block, or allow. Optional — omit to leave the action unchanged."`
	Disabled *bool  `json:"disabled,omitempty" jsonschema:"Set true to skip this pattern entirely; false to re-enable. Optional — omit to leave enabled-state unchanged."`
}

// MiddlewareStatus is the aggregated /api/middleware/status payload —
// the React Middleware page renders this in one go. Routing is a
// placeholder until subsystem 2 lands.
type MiddlewareStatus struct {
	PII    MiddlewarePIIStatus    `json:"pii"`
	Router MiddlewareRouterStatus `json:"router"`
}

// MiddlewarePIIStatus shows what the redactor is doing right now and
// which models opt in. enabled_globally=false means --disable-pii.
type MiddlewarePIIStatus struct {
	EnabledGlobally           bool                  `json:"enabled_globally"`
	Reason                    string                `json:"reason,omitempty"`
	DefaultEnabledForBackends []string              `json:"default_enabled_for_backends,omitempty"`
	Patterns                  []PIIPattern          `json:"patterns"`
	Models                    []MiddlewarePIIModel  `json:"models"`
	RecentEventCount          int                   `json:"recent_event_count"`
}

// MiddlewarePIIModel is one model row in the per-model PII table.
type MiddlewarePIIModel struct {
	Name              string            `json:"name"`
	Backend           string            `json:"backend"`
	Enabled           bool              `json:"enabled"`
	Explicit          bool              `json:"explicit"`             // Did YAML set Enabled, or did the backend prefix decide?
	DefaultForBackend bool              `json:"default_for_backend"`  // Backend matches the auto-on rule (proxy-*).
	Overrides         map[string]string `json:"overrides,omitempty"`
}

// MiddlewareRouterStatus is the placeholder shape the Routing tab
// reads. Subsystem 2 fills in Models with real RouterDecision rows.
type MiddlewareRouterStatus struct {
	Configured bool     `json:"configured"`
	Models     []string `json:"models"`
	Note       string   `json:"note,omitempty"`
}

// RouterDecisionsQuery filters get_router_decisions.
type RouterDecisionsQuery struct {
	CorrelationID string `json:"correlation_id,omitempty" jsonschema:"Optional X-Correlation-ID join key (binds decisions to the request and usage record)."`
	UserID        string `json:"user_id,omitempty"        jsonschema:"Optional user id to scope the query."`
	RouterModel   string `json:"router_model,omitempty"   jsonschema:"Optional router model name to filter by (e.g. smart-router)."`
	Limit         int    `json:"limit,omitempty"          jsonschema:"Maximum decisions. Defaults to 100."`
}

// RouterDecision is the LLM-facing view of one routing decision. The
// prompt is NEVER stored; admins audit by hash if they need to dedupe
// recurring routing patterns.
type RouterDecision struct {
	ID             string  `json:"id"`
	CorrelationID  string  `json:"correlation_id"`
	UserID         string  `json:"user_id"`
	RouterModel    string  `json:"router_model"`
	RequestedModel string  `json:"requested_model"`
	ServedModel    string  `json:"served_model"`
	Classifier     string  `json:"classifier"`
	Label          string  `json:"label"`
	Score          float64 `json:"score"`
	LatencyMs      int64   `json:"latency_ms"`
	Cached         bool    `json:"cached"`
	CreatedAt      string  `json:"created_at"`
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
