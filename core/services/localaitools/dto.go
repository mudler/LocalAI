package localaitools

import (
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

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

// ModelSearchRequest is the input for search_models.
type ModelSearchRequest struct {
	Query     string `json:"query,omitempty"     jsonschema:"Search text, for example a model family or a repository name. May be empty only with sort latest."`
	Providers string `json:"providers,omitempty" jsonschema:"Comma separated source IDs to ask, for example huggingface. Empty asks the sources the person has enabled."`
	Limit     int    `json:"limit,omitempty"     jsonschema:"Results per provider. Defaults to 20, at most 50."`
	Format    string `json:"format,omitempty"    jsonschema:"File format filter. Defaults to gguf; any turns it off."`
	Sort      string `json:"sort,omitempty"      jsonschema:"downloads (default), likes, updated, trending or latest. latest lists the newest models and is the only sort that works with an empty query; a source that cannot list its newest models reports unsupported."`
}

// ModelSearchResponse is what search_models returns: the merged results and
// one status per provider. It is the wire shape of GET /api/models/search.
type ModelSearchResponse = modelsearch.Response

// RemoteModelRequest is the input for inspect_remote_model.
type RemoteModelRequest struct {
	URI      string `json:"uri"                jsonschema:"Repository uri of a search result, for example huggingface://owner/repo or pirateface://owner/repo."`
	Estimate bool   `json:"estimate,omitempty" jsonschema:"Also estimate the memory the model needs. Reads the GGUF header of the chosen file, so it costs one more upstream call."`
	File     string `json:"file,omitempty"     jsonschema:"First file of the variant to estimate, as listed in variants. Defaults to the recommended variant. Implies estimate."`
	Contexts string `json:"contexts,omitempty" jsonschema:"Comma separated context sizes for the estimate. Defaults to 8192."`
}

// RemoteModelInfo is what inspect_remote_model returns: the wire shape of
// GET /api/models/remote/files, with the wire shape of
// GET /api/models/remote/estimate under "estimate" when it was asked for.
type RemoteModelInfo struct {
	*modelsearch.RepoFiles
	Estimate *modelsearch.EstimateResult `json:"estimate,omitempty"`
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

// AliasInfo is one alias -> target pair, the shape list_aliases returns and
// GET /api/aliases emits. Kept aligned with localai.AliasInfo so the
// MCP wire output matches the REST endpoint by construction.
type AliasInfo struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// InstallModelRequest is the input for install_model.
type InstallModelRequest struct {
	GalleryName string         `json:"gallery_name,omitempty" jsonschema:"The gallery the model lives in (from gallery_search). Optional when ModelName is unique across galleries."`
	ModelName   string         `json:"model_name"             jsonschema:"The canonical model name as returned by gallery_search."`
	Overrides   map[string]any `json:"overrides,omitempty"    jsonschema:"Optional config overrides to merge into the installed model's YAML."`
	Variant     string         `json:"variant,omitempty"      jsonschema:"Optional. Installs one specific build of an entry that offers several (different quantizations or engines), named exactly as it appears in that entry's variant list. Leave empty unless the user explicitly asked for a particular build: by default LocalAI drops builds this machine cannot run or cannot fit, prefers the engine this hardware is best served by, and uses size only to decide between equally preferred builds."`
}

// InstallBackendRequest is the input for install_backend.
type InstallBackendRequest struct {
	GalleryName string `json:"gallery_name,omitempty" jsonschema:"Source backend gallery."`
	BackendName string `json:"backend_name"           jsonschema:"Backend identifier (e.g. llama-cpp)."`
}

// InstallLibraryBackendRequest is the input for install_library_backend.
type InstallLibraryBackendRequest struct {
	SourceID string `json:"source_id" jsonschema:"The library the entry comes from, as shown in the Add-ons library list."`
	Name     string `json:"name"      jsonschema:"The backend name of the library entry."`
	Version  string `json:"version"   jsonschema:"The version or the channel of the library entry."`
}

// InstallLibraryBackendResult is the output of install_library_backend. The
// install runs to the end before the call returns, so there is no job id.
type InstallLibraryBackendResult struct {
	Status   string `json:"status"`
	Name     string `json:"name"`
	SourceID string `json:"source_id"`
}

// BackendLibraryUpdate is one installed library backend with a newer build in
// its library, as list_backend_library_updates returns it. Version is what an
// install_library_backend call names to take the update.
type BackendLibraryUpdate struct {
	Name             string `json:"name"`
	SourceID         string `json:"sourceId"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	InstalledDigest  string `json:"installedDigest,omitempty"`
	Version          string `json:"version"`
	Digest           string `json:"digest"`
	Signed           bool   `json:"signed"`
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
	Version           string   `json:"version"`
	Distributed       bool     `json:"distributed"`
	BackendsPath      string   `json:"backends_path,omitempty"`
	ModelsPath        string   `json:"models_path,omitempty"`
	LoadedModels      []string `json:"loaded_models,omitempty"`
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
	// Route is how the frontends reach the node: nats, websocket (the tunnel
	// over a websocket) or p2p (the tunnel of a worker that joined with an
	// invite). Empty when the node reports nothing.
	Route string `json:"route,omitempty"`
	// PeerID is the P2P peer of a node that joined over P2P.
	PeerID string `json:"peer_id,omitempty"`
	// SplitServer is true when the node may serve model-split workers.
	SplitServer bool `json:"split_server,omitempty"`
}

// NodeSplitServer is the result of set_node_split_server.
type NodeSplitServer struct {
	NodeID      string `json:"node_id"`
	SplitServer bool   `json:"split_server"`
	// Detached is the number of split workers that were attached to the node
	// and are now attached to no node.
	Detached int `json:"detached"`
}

// NodeRoute names the route of a node from what it reported: NATS when it is
// attached to NATS, then its tunnel, over P2P for a node with a peer ID and
// over a websocket otherwise.
func NodeRoute(attached []string, peerID string) string {
	switch {
	case slices.Contains(attached, "nats"):
		return "nats"
	case peerID != "":
		return "p2p"
	case slices.Contains(attached, "tunnel"):
		return "websocket"
	}
	return ""
}

// SetNodeVRAMBudgetRequest is the input for set_node_vram_budget. It PUTs
// the value to /api/nodes/{node_id}/vram-budget, where the server validates
// and resolves the budget against the node's total VRAM. An empty Budget
// clears the admin override so the worker's own default takes over again.
type SetNodeVRAMBudgetRequest struct {
	NodeID string `json:"node_id"          jsonschema:"The federated node id (from list_nodes) whose VRAM budget to set."`
	Budget string `json:"budget,omitempty" jsonschema:"VRAM allocation cap as a percentage (e.g. 80%) or absolute amount (e.g. 12GB). Empty string clears the override."`
}

// ModelSchedulingConfig is the MCP wire shape for one per-model distributed
// scheduling rule. Keep this DTO explicit instead of aliasing the node-registry
// model so the MCP contract only exposes operator-facing scheduling fields.
type ModelSchedulingConfig struct {
	ModelName           string  `json:"model_name"`
	NodeSelector        string  `json:"node_selector,omitempty"`
	MinReplicas         int     `json:"min_replicas"`
	MaxReplicas         int     `json:"max_replicas"`
	SpreadAll           bool    `json:"spread_all,omitempty"`
	RoutePolicy         string  `json:"route_policy,omitempty"`
	BalanceAbsThreshold int     `json:"balance_abs_threshold,omitempty"`
	BalanceRelThreshold float64 `json:"balance_rel_threshold,omitempty"`
	MinPrefixMatch      float64 `json:"min_prefix_match,omitempty"`
	// TargetModel is the model the rule governs. It differs from ModelName when
	// the rule is keyed by an alias, in which case it follows the alias.
	TargetModel string `json:"target_model,omitempty"`
	// Shadowed reports that another rule already governs TargetModel, leaving
	// this one with no effect.
	Shadowed bool `json:"shadowed,omitempty"`
	// UpgradeStrategy is how an upgrade replaces the running replicas:
	// stop_and_start (the default) or canary (stored, not available yet).
	UpgradeStrategy string `json:"upgrade_strategy,omitempty"`
}

// SetSchedulingRequest is the input for set_scheduling. It mirrors
// /api/nodes/scheduling so standalone MCP and REST callers preserve the same
// PATCH-style semantics for the optional prefix-cache routing fields.
type SetSchedulingRequest struct {
	ModelName           string            `json:"model_name"                         jsonschema:"Installed model name, or model alias, whose distributed scheduling rule should be created or updated. A rule keyed by an alias follows that alias to whatever model it currently points at."`
	NodeSelector        map[string]string `json:"node_selector,omitempty"            jsonschema:"Optional node-label selector. Empty means any healthy backend node."`
	MinReplicas         int               `json:"min_replicas"                       jsonschema:"Minimum desired replicas. Mutually exclusive with spread_all."`
	MaxReplicas         int               `json:"max_replicas"                       jsonschema:"Maximum desired replicas. Must be >= min_replicas when non-zero. Mutually exclusive with spread_all."`
	SpreadAll           bool              `json:"spread_all,omitempty"               jsonschema:"When true, keep one replica on every matching node. Mutually exclusive with min_replicas/max_replicas."`
	RoutePolicy         *string           `json:"route_policy,omitempty"             jsonschema:"Optional prefix-cache route policy override. Omit to preserve the existing value on updates."`
	BalanceAbsThreshold *int              `json:"balance_abs_threshold,omitempty"    jsonschema:"Optional absolute imbalance threshold override. Omit to preserve the existing value on updates."`
	BalanceRelThreshold *float64          `json:"balance_rel_threshold,omitempty"    jsonschema:"Optional relative imbalance threshold override. Omit to preserve the existing value on updates."`
	MinPrefixMatch      *float64          `json:"min_prefix_match,omitempty"         jsonschema:"Optional minimum prefix match threshold override. Omit to preserve the existing value on updates."`
	UpgradeStrategy     *string           `json:"upgrade_strategy,omitempty"         jsonschema:"How a model upgrade replaces running replicas: stop_and_start (default) or canary (stored, not available yet: upgrades stop and start). Omit to preserve the existing value."`
}

// DeleteSchedulingRequest identifies the model scheduling rule to remove.
type DeleteSchedulingRequest struct {
	ModelName string `json:"model_name" jsonschema:"Installed model name whose scheduling config should be removed."`
}

// ImportModelURIRequest is the input for import_model_uri. It mirrors the
// REST surface (`/models/import-uri`) closely so both clients can produce
// identical responses; the BackendPreference is a flat field rather than the
// REST `preferences` JSON blob since the LLM only needs to specify a backend
// name when it disambiguates a multi-backend match.
type ImportModelURIRequest struct {
	URI               string         `json:"uri"                          jsonschema:"The model source. Accepts HuggingFace URLs (https://huggingface.co/...), OCI image references, http(s) URLs to a manifest, file:// paths, or a bare HF repo (e.g. Qwen/Qwen3-4B-GGUF)."`
	BackendPreference string         `json:"backend_preference,omitempty" jsonschema:"Optional backend name (e.g. llama-cpp). Required as the second-step retry when a previous import_model_uri call returned ambiguous_backend=true."`
	Quantization      string         `json:"quantization,omitempty"       jsonschema:"Optional quantization to pick from a repository with several GGUF files, such as q4_k_m. Use the preference of a variant from inspect_remote_model."`
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

// VoiceProfile is the same path-free shape returned by the REST library.
// Keeping the service type avoids REST/MCP field drift.
type VoiceProfile = voiceprofile.Profile

// CreateVoiceProfileRequest is the input of the voice_profile tool with the
// action create. Audio is a file reference of the FileStore, never bytes.
type CreateVoiceProfileRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Language    string `json:"language,omitempty"`
	Transcript  string `json:"transcript"`
	Audio       string `json:"audio"`
	// ExtraClips are more clips of the same voice, after Audio. The API takes
	// up to voiceprofile.MaxReferences clips, each with its own transcript.
	ExtraClips []VoiceClip `json:"extra_clips,omitempty"`
}

// VoiceClip is one more clip of a voice profile: a file reference of the
// FileStore and the exact words said in it.
type VoiceClip struct {
	Audio      string `json:"audio"`
	Transcript string `json:"transcript"`
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

// PIIEventsQuery filters get_pii_events.
type PIIEventsQuery struct {
	CorrelationID string `json:"correlation_id,omitempty" jsonschema:"Optional X-Correlation-ID join key (binds events to the request and usage record)."`
	UserID        string `json:"user_id,omitempty"        jsonschema:"Optional user id to scope the query."`
	PatternID     string `json:"pattern_id,omitempty"     jsonschema:"Optional detector group id (e.g. ner:EMAIL)."`
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

// MiddlewareStatus is the aggregated /api/middleware/status payload —
// the React Middleware page renders this in one go. Routing is a
// placeholder until subsystem 2 lands.
type MiddlewareStatus struct {
	PII    MiddlewarePIIStatus    `json:"pii"`
	Router MiddlewareRouterStatus `json:"router"`
}

// MiddlewarePIIStatus shows which models opt in to PII redaction and the
// NER detector models they reference. The detection policy itself lives
// on each detector model's pii_detection block.
type MiddlewarePIIStatus struct {
	EnabledGlobally           bool                 `json:"enabled_globally"`
	Reason                    string               `json:"reason,omitempty"`
	DefaultEnabledForBackends []string             `json:"default_enabled_for_backends,omitempty"`
	Models                    []MiddlewarePIIModel `json:"models"`
	RecentEventCount          int                  `json:"recent_event_count"`
}

// MiddlewarePIIModel is one model row in the per-model PII table.
type MiddlewarePIIModel struct {
	Name              string   `json:"name"`
	Backend           string   `json:"backend"`
	Enabled           bool     `json:"enabled"`
	Explicit          bool     `json:"explicit"`            // Did YAML set Enabled, or did the backend prefix decide?
	DefaultForBackend bool     `json:"default_for_backend"` // Backend matches the auto-on rule (proxy-*).
	Detectors         []string `json:"detectors,omitempty"` // NER detector model names this config references.
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

// RouterCorpusEntry is one labelled exemplar for seed_router_corpus.
type RouterCorpusEntry struct {
	Text   string   `json:"text"   jsonschema:"Example prompt text. Embedded server-side and persisted; NEVER returned by any tool or endpoint."`
	Labels []string `json:"labels" jsonschema:"Policy labels this exemplar activates. Every label must be declared in the router's policies."`
}

// RouterCorpusSeedRequest is the input for seed_router_corpus.
type RouterCorpusSeedRequest struct {
	Router  string              `json:"router"  jsonschema:"Router model name — the ModelConfig with classifier: knn and a router.knn block."`
	Entries []RouterCorpusEntry `json:"entries" jsonschema:"Labelled exemplars to add. Duplicate texts are skipped, not double-weighted."`
}

// RouterCorpusSeedResult reports the outcome of seed_router_corpus.
type RouterCorpusSeedResult struct {
	Router      string         `json:"router"`
	Added       int            `json:"added"`
	Skipped     int            `json:"skipped"`
	Total       int            `json:"total"`
	LabelCounts map[string]int `json:"label_counts"`
}

// RouterCorpusQuery names the router whose corpus to inspect or clear.
type RouterCorpusQuery struct {
	Router string `json:"router" jsonschema:"Router model name."`
}

// RouterCorpusStats is the count-only inspection surface for a
// router's KNN corpus. Entry texts are never exposed.
type RouterCorpusStats struct {
	Router          string         `json:"router"`
	StoreName       string         `json:"store_name"`
	EmbeddingModel  string         `json:"embedding_model"`
	Total           int            `json:"total"`
	LabelCounts     map[string]int `json:"label_counts"`
	EmbeddingModels []string       `json:"embedding_models,omitempty"`
}

// RouterCorpusClearResult reports how many entries clear_router_corpus
// removed.
type RouterCorpusClearResult struct {
	Router  string `json:"router"`
	Cleared int    `json:"cleared"`
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

// ClusterCarrierStatus is the LLM-facing view of the transport of a
// distributed cluster. Distributed is false on a frontend that is not
// distributed, and the other fields are then empty.
type ClusterCarrierStatus struct {
	Distributed bool `json:"distributed"`
	// Active is "nats" or "tunnel".
	Active string `json:"active,omitempty"`
	// State is "stable", "prepare" or "commit". A stable cluster with
	// DrainRemainingSeconds above zero is draining the previous carrier.
	State                 string  `json:"state,omitempty"`
	Target                string  `json:"target,omitempty"`
	Epoch                 int64   `json:"epoch,omitempty"`
	DrainRemainingSeconds float64 `json:"drain_remaining_seconds,omitempty"`
	// Replicas are the live frontends.
	Replicas []ClusterReplicaInfo `json:"replicas,omitempty"`
	// Workers are the registered workers and the carriers they hold.
	Workers []ClusterWorkerInfo `json:"workers,omitempty"`
}

// ClusterReplicaInfo is one live frontend replica.
type ClusterReplicaInfo struct {
	ID          string `json:"id"`
	Version     string `json:"version,omitempty"`
	ReadyEpoch  int64  `json:"ready_epoch"`
	ReadyReason string `json:"ready_reason,omitempty"`
}

// ClusterWorkerInfo is one worker. CanFollow is about a change to the other
// carrier, and Reason says why it is false.
type ClusterWorkerInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Attached    []string `json:"attached,omitempty"`
	CanFollow   bool     `json:"can_follow"`
	Reason      string   `json:"reason,omitempty"`
	FollowError string   `json:"follow_error,omitempty"`
}

// FailoverTargetInfo is the LLM-facing view of one failover chain target's
// health. It mirrors failover.TargetStatus but drops ConsecutiveOK and
// LastProbe — internal probing detail the LLM doesn't need to decide
// whether to pin or unpin a target.
type FailoverTargetInfo struct {
	Model     string `json:"model"`
	Kind      string `json:"kind"`
	Warm      bool   `json:"warm"`
	State     string `json:"state"`
	LastError string `json:"last_error,omitempty"`
}

// FailoverChainInfo is the LLM-facing view of one failover chain: its
// current state, the target serving it now, an optional pin, and every
// target's health.
type FailoverChainInfo struct {
	Name    string               `json:"name"`
	State   string               `json:"state"`
	Active  string               `json:"active"`
	Pinned  string               `json:"pinned,omitempty"`
	Targets []FailoverTargetInfo `json:"targets"`
}

// FederationP2PInfo is how this server reaches an instance over P2P.
type FederationP2PInfo struct {
	PeerID string `json:"peer_id"`
	// State is off, listening, up, down or error.
	State string `json:"state"`
	// Path is direct, hole_punched or relayed; empty before the first
	// request.
	Path    string `json:"path,omitempty"`
	Limited bool   `json:"limited,omitempty"`
}

// FederationModelInfo is a model an instance serves: its model name on
// this server and its id at the instance.
type FederationModelInfo struct {
	Model      string `json:"model"`
	UpstreamID string `json:"upstream_id"`
}

// FederationInstanceInfo is a LocalAI that can serve a group. ID "local" is
// this server; any other ID is a remote.
type FederationInstanceInfo struct {
	ID        string                `json:"id"`
	Name      string                `json:"name,omitempty"`
	Local     bool                  `json:"local"`
	Transport string                `json:"transport,omitempty"`
	Status    string                `json:"status,omitempty"`
	P2P       *FederationP2PInfo    `json:"p2p,omitempty"`
	InFlight  int                   `json:"in_flight"`
	Models    []FederationModelInfo `json:"models"`
}

// FederationMemberInfo is one model of a group with its health.
type FederationMemberInfo struct {
	Model        string `json:"model"`
	Instance     string `json:"instance,omitempty"`
	InstanceName string `json:"instance_name,omitempty"`
	Drained      bool   `json:"drained"`
	State        string `json:"state,omitempty"`
	InFlight     int    `json:"in_flight"`
	Constrained  bool   `json:"constrained,omitempty"`
	LastError    string `json:"last_error,omitempty"`
}

// FederationGroupInfo is a model name served by several instances.
type FederationGroupInfo struct {
	Name    string                 `json:"name"`
	Policy  string                 `json:"policy"`
	State   string                 `json:"state,omitempty"`
	Members []FederationMemberInfo `json:"members"`
}

// FederationInfo is the answer of list_federation.
type FederationInfo struct {
	Instances []FederationInstanceInfo `json:"instances"`
	Groups    []FederationGroupInfo    `json:"groups"`
}

// FederationGroupAction is one change of a group. Action is create, update,
// delete, drain or undrain. Create needs Name and at least two Members;
// update takes Policy and/or Members; drain and undrain take Model.
type FederationGroupAction struct {
	Action  string   `json:"action" jsonschema:"create, update, delete, drain or undrain"`
	Name    string   `json:"name" jsonschema:"the group's model name"`
	Policy  string   `json:"policy,omitempty" jsonschema:"least_busy (default) or round_robin; for create and update"`
	Members []string `json:"members,omitempty" jsonschema:"model names that serve the group (this server's models or entries of remotes, see list_federation); at least two; for create and update"`
	Model   string   `json:"model,omitempty" jsonschema:"the member to drain or undrain"`
}

// Actions of manage_federation_group.
const (
	FederationActionCreate  = "create"
	FederationActionUpdate  = "update"
	FederationActionDelete  = "delete"
	FederationActionDrain   = "drain"
	FederationActionUndrain = "undrain"
)

// LoadCancelResult is what a cancel of a distributed load did. State is
// "stopped" (the worker confirmed), "stopping" (recorded, stop pending; the model
// is released after RetryAfter seconds regardless) or "gone".
type LoadCancelResult struct {
	Model      string `json:"model"`
	JobID      string `json:"job_id"`
	State      string `json:"state"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

// SharedFolder is a shared folder with its members, as the REST endpoint
// /api/chats/admin/shared-folders returns it. A member is a user ID, or
// "@everyone" for every user, with the mode read or write.
type SharedFolder = schema.SharedFolder

// SharedFolderRemoved is a removed shared folder and the undo_rev that
// restore_shared_folder takes.
type SharedFolderRemoved = schema.SharedFolderRemoved

// SharedFolderEvent is one line of the audit of the shared folders.
type SharedFolderEvent = schema.SharedFolderEvent

// CreateSharedFolderRequest is the input for create_shared_folder.
type CreateSharedFolderRequest struct {
	Name string `json:"name" jsonschema:"Folder name: 1 to 64 letters, digits, spaces, dots, dashes or underscores, starting with a letter or a digit. It becomes /shared/<name>."`
}

// RenameSharedFolderRequest is the input for rename_shared_folder.
type RenameSharedFolderRequest struct {
	ID   int64  `json:"id"   jsonschema:"Shared folder id from list_shared_folders."`
	Name string `json:"name" jsonschema:"The new name, with the same rules as create_shared_folder."`
}

// SharedFolderRef names one shared folder.
type SharedFolderRef struct {
	ID int64 `json:"id" jsonschema:"Shared folder id from list_shared_folders."`
}

// RestoreSharedFolderRequest is the input for restore_shared_folder.
type RestoreSharedFolderRequest struct {
	Rev int64 `json:"rev" jsonschema:"The undo_rev that delete_shared_folder returned."`
}

// SetSharedFolderMemberRequest is the input for set_shared_folder_member.
type SetSharedFolderMemberRequest struct {
	ID     int64  `json:"id"     jsonschema:"Shared folder id from list_shared_folders."`
	Member string `json:"member" jsonschema:"A user's ID, email or name, or @everyone for every user."`
	Mode   string `json:"mode"   jsonschema:"read (can read) or write (can edit)."`
}

// RemoveSharedFolderMemberRequest is the input for remove_shared_folder_member.
type RemoveSharedFolderMemberRequest struct {
	ID     int64  `json:"id"     jsonschema:"Shared folder id from list_shared_folders."`
	Member string `json:"member" jsonschema:"The member's principal from list_shared_folders, or @everyone."`
}

// SharedFolderEventsQuery is the input for list_shared_folder_events.
type SharedFolderEventsQuery struct {
	Limit int `json:"limit,omitempty" jsonschema:"How many lines, newest first. Defaults to 50; at most 500."`
}

// ChatBuiltinTool is one built-in chat tool (list_chat_tools).
type ChatBuiltinTool = schema.ChatsBuiltinTool

// ChatBuiltinToolList is the built-in chat tools and the built-in skills that
// installed add-ons replace (list_chat_tools).
type ChatBuiltinToolList = schema.ChatsBuiltinToolList

// HostExecEvent is one command the chats' built-in bash ran on this machine
// (list_host_exec_events).
type HostExecEvent = schema.ChatsHostExecEvent

// HostExecEventsQuery is the input for list_host_exec_events.
type HostExecEventsQuery struct {
	Limit int `json:"limit,omitempty" jsonschema:"How many lines, newest first. Defaults to 50; at most 500."`
}

// ChatEgress is the administrator's view of the Sandbox network
// (get_chat_egress).
type ChatEgress = schema.ChatsEgressAdmin

// EgressEvent is one line of the Sandbox network audit (list_egress_events).
type EgressEvent = schema.ChatsEgressEvent

// EgressEventsQuery is the input for list_egress_events.
type EgressEventsQuery struct {
	Limit   int    `json:"limit,omitempty"   jsonschema:"How many lines, newest first. Defaults to 50; at most 500."`
	User    string `json:"user,omitempty"    jsonschema:"Only the lines of this user ID."`
	Chat    string `json:"chat,omitempty"    jsonschema:"Only the lines of this chat ID."`
	Verdict string `json:"verdict,omitempty" jsonschema:"allow, block, or done (an allowed connection that ended)."`
	Host    string `json:"host,omitempty"    jsonschema:"Only the lines of this host name."`
}

// SetChatEgressCeilingRequest is the input for set_chat_egress_ceiling.
type SetChatEgressCeilingRequest struct {
	Ceiling string `json:"ceiling" jsonschema:"The widest network preset any chat may use: off, registries or all."`
}

// P2PStatusInfo is the LLM-facing view of the P2P layer.
type P2PStatusInfo struct {
	// Mode is off, connect (dial out only) or share (accepts paired devices).
	Mode           string   `json:"mode"`
	Running        bool     `json:"running"`
	Error          string   `json:"error,omitempty"`
	PeerID         string   `json:"peer_id,omitempty"`
	Addrs          []string `json:"addrs,omitempty"`
	Reachability   string   `json:"reachability"`
	RelayService   bool     `json:"relays_for_devices"`
	PendingInvites int      `json:"pending_invites"`
	Devices        int      `json:"devices"`
}

// P2PDeviceInfo is the LLM-facing view of one paired device.
type P2PDeviceInfo struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	PeerID string `json:"peer_id"`
	// Status is active or revoked.
	Status string `json:"status"`
	Online bool   `json:"online"`
	// Path is none, direct, hole_punched or relayed.
	Path       string     `json:"path"`
	RelayLabel string     `json:"relay,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// Models and AllModels say what a remote device may use.
	Models    []string `json:"models,omitempty"`
	AllModels bool     `json:"all_models,omitempty"`
}

// P2PInviteInfo is the LLM-facing view of one invite. The link is never part
// of it: it is shown once, to the person who made it.
type P2PInviteInfo struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Status is pending, used, expired or canceled.
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
	DeviceID  string    `json:"device_id,omitempty"`
	// Phrase is the confirmation phrase of a used invite, which the admin
	// compares with the one the device shows.
	Phrase string `json:"phrase,omitempty"`
	// RelayPass is the relay that holds the pass of the invite; empty when
	// the link carries none.
	RelayPass string `json:"relay_pass,omitempty"`
	// Models and AllModels say what a remote invite grants: the names, or
	// every model.
	Models    []string `json:"models,omitempty"`
	AllModels bool     `json:"all_models,omitempty"`
}

// P2PSplitWorkerInfo is the LLM-facing view of one model-split worker.
type P2PSplitWorkerInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Engine is llama (llama.cpp RPC) or mlx.
	Engine string `json:"engine"`
	// State is online (in use), offline, slow (only a relay reaches it; it
	// waits for allow_slow_path), limited (only a public relay reaches it,
	// which never carries model layers) or refused.
	State         string `json:"state"`
	Error         string `json:"error,omitempty"`
	AllowSlowPath bool   `json:"allow_slow_path"`
	// Path is none, direct, hole_punched or relayed.
	Path       string `json:"path"`
	RelayLabel string `json:"relay,omitempty"`
	// RelayID is the relay of a relayed path, for get_p2p_relay_limits.
	RelayID string `json:"relay_id,omitempty"`
	// OwnRelay is true when that relay is one this server administers. With
	// a data or time limit it ends each connection at the limit, also after
	// Use anyway; set_p2p_relay_limits raises or removes the limits.
	OwnRelay    bool   `json:"own_relay,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes"`
	// NodeID is the backend node the worker is attached to (empty: this
	// server); NodeName is its name. In a cluster a worker attached to no
	// node is not used.
	NodeID   string `json:"node_id,omitempty"`
	NodeName string `json:"node_name,omitempty"`
	// NodeOffline is true when that node is offline: the worker stays
	// attached and waits for it.
	NodeOffline bool `json:"node_offline,omitempty"`
	// NodeNote says why a worker attached to no node was detached.
	NodeNote string `json:"node_note,omitempty"`
	// NodeRelay is whether the user's gated relays admit the node, so the
	// worker can reach it through them: admitted, refused, pending or
	// not_needed. NodeRelayReason says why when it is not admitted.
	NodeRelay       string `json:"relay_admission,omitempty"`
	NodeRelayReason string `json:"relay_admission_reason,omitempty"`
}

// P2PRelayLimitsInfo is the LLM-facing view of the limits of one relay.
// Zero means no limit.
type P2PRelayLimitsInfo struct {
	// CircuitBytes is the data one circuit may carry in each direction.
	CircuitBytes int64 `json:"circuit_bytes"`
	// CircuitSeconds is the time one circuit may stay open.
	CircuitSeconds int64 `json:"circuit_seconds"`
	// RateBPS is the bandwidth of one circuit in each direction, in bytes
	// per second.
	RateBPS int64 `json:"rate_bps"`
	// Ceiling is the most set_p2p_relay_limits may set for each limit; the
	// operator of the relay sets it. Zero means no ceiling. With a ceiling a
	// limit cannot be removed either.
	Ceiling P2PRelayCeiling `json:"ceiling"`
	// CanChange is true when this server is an admin of a gated relay.
	CanChange bool `json:"can_change"`
	// Restarted is true when the change made the relay restart its circuit
	// service.
	Restarted bool `json:"restarted,omitempty"`
}

// P2PRelayCeiling is the highest value an admin may set for each limit of a
// relay. Zero means no ceiling.
type P2PRelayCeiling struct {
	CircuitBytes   int64 `json:"circuit_bytes"`
	CircuitSeconds int64 `json:"circuit_seconds"`
	RateBPS        int64 `json:"rate_bps"`
}

// SetP2PRelayLimitsRequest is the input of set_p2p_relay_limits. A field
// that is left out stays as it is; zero removes that limit.
type SetP2PRelayLimitsRequest struct {
	ID             string `json:"id" jsonschema:"relay ID from list_p2p_relays"`
	CircuitBytes   *int64 `json:"circuit_bytes,omitempty" jsonschema:"data one circuit may carry in each direction, in bytes (0 removes the limit; a value above the relay ceiling is refused)"`
	CircuitSeconds *int64 `json:"circuit_seconds,omitempty" jsonschema:"time one circuit may stay open, in seconds (0 removes the limit)"`
	RateBPS        *int64 `json:"rate_bps,omitempty" jsonschema:"bandwidth of one circuit in each direction, in bytes per second (0 removes the limit)"`
}

// P2PRelayInfo is the LLM-facing view of one relay.
type P2PRelayInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Addr  string `json:"addr"`
	// Mode is gated, open, unclaimed or unknown.
	Mode    string `json:"mode"`
	State   string `json:"state"`
	Health  string `json:"health,omitempty"`
	Members int    `json:"members"`
}

// The actions of upgrade_model.
const (
	UpgradeModelActionUpgrade    = "upgrade"
	UpgradeModelActionConfigOnly = "config_only"
	UpgradeModelActionRetry      = "retry"
	UpgradeModelActionRemoveOld  = "remove_old"
	UpgradeModelActionHold       = "hold"
	UpgradeModelActionUnhold     = "unhold"
	UpgradeModelActionAdopt      = "adopt"
)

// UpgradeModelRequest is the input of upgrade_model. One flat schema for the
// seven actions; the handler checks what each action needs.
type UpgradeModelRequest struct {
	Action  string            `json:"action"            jsonschema:"upgrade, config_only, retry, remove_old, hold, unhold or adopt."`
	Name    string            `json:"name"              jsonschema:"Installed model name (from model_updates)."`
	Choices map[string]string `json:"choices,omitempty" jsonschema:"upgrade and config_only only: mine or gallery for each edited key the update also changes (editedKeys with conflict true in model_updates)."`
	Gallery string            `json:"gallery,omitempty" jsonschema:"adopt only: the gallery the model came from, when more than one lists it."`
	Entry   string            `json:"entry,omitempty"   jsonschema:"adopt only: the gallery entry, when it differs from the model name."`
}

// ModelAdoptResult is what adopt recorded (REST schema.ModelAdoptResponse).
type ModelAdoptResult struct {
	Name       string `json:"name"`
	Gallery    string `json:"gallery"`
	Entry      string `json:"entry"`
	FilesMatch bool   `json:"filesMatch"`
}

// ModelRemoveOldResult is what remove_old deleted (REST
// schema.ModelRemoveOldResponse).
type ModelRemoveOldResult struct {
	Name    string   `json:"name"`
	Removed []string `json:"removed"`
	// Kept and Errors are set when only some files could be removed.
	Kept   []string `json:"kept,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

// ModelHoldResult is the hold of a model after hold or unhold (REST
// schema.ModelHoldResponse).
type ModelHoldResult struct {
	Name string `json:"name"`
	Hold bool   `json:"hold"`
}
