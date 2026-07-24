package localaitools

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/pkg/vram"
)

// LocalAIClient is the surface tools depend on. It has two implementations:
//
//   - inproc.Client (in-process; calls LocalAI services directly)
//   - httpapi.Client (out-of-process; calls the LocalAI REST API)
//
// Tool handlers and the embedded skill prompts are agnostic to which
// implementation backs the client.
//
// Where the same shape already exists elsewhere in the codebase
// (config.Gallery, gallery.Metadata, schema.KnownBackend, vram.EstimateResult,
// modeladmin.Action/Capability) we surface it directly rather than maintain
// a parallel DTO — keeping the LLM-visible wire format aligned with the
// rest of LocalAI by construction.
type LocalAIClient interface {
	// ---- Models / gallery (read) ----
	GallerySearch(ctx context.Context, q GallerySearchQuery) ([]gallery.Metadata, error)
	ListInstalledModels(ctx context.Context, capability Capability) ([]InstalledModel, error)
	ListGalleries(ctx context.Context) ([]config.Gallery, error)
	GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error)
	GetModelConfig(ctx context.Context, name string) (*ModelConfigView, error)

	// ---- Models / gallery (write) ----
	InstallModel(ctx context.Context, req InstallModelRequest) (jobID string, err error)
	DeleteModel(ctx context.Context, name string) error
	EditModelConfig(ctx context.Context, name string, patch map[string]any) error
	ReloadModels(ctx context.Context) error
	// LoadModel pre-loads a model into memory by name (the inverse of shutting
	// it down). For a realtime pipeline model every configured sub-model is
	// loaded; it returns the model names that became resident.
	LoadModel(ctx context.Context, model string) ([]string, error)
	ImportModelURI(ctx context.Context, req ImportModelURIRequest) (*ImportModelURIResponse, error)

	// ---- Model aliases ----
	// SetAlias creates the alias `name` pointing at `target`, or swaps an
	// existing alias's target. The server validates that `target` is an
	// existing, non-alias, enabled model. Deletion reuses DeleteModel.
	SetAlias(ctx context.Context, name, target string) error
	// ListAliases returns every configured alias and its target.
	ListAliases(ctx context.Context) ([]AliasInfo, error)

	// ---- Backends ----
	// ListBackends returns installed backends. The shape stays a thin
	// localaitools.Backend rather than gallery.SystemBackend because the
	// latter carries filesystem paths (RunFile, Metadata) the LLM
	// shouldn't see.
	ListBackends(ctx context.Context) ([]Backend, error)
	// ListKnownBackends returns the same shape as REST /backends/known.
	ListKnownBackends(ctx context.Context) ([]schema.KnownBackend, error)
	InstallBackend(ctx context.Context, req InstallBackendRequest) (jobID string, err error)
	UpgradeBackend(ctx context.Context, name string) (jobID string, err error)

	// ---- System ----
	SystemInfo(ctx context.Context) (*SystemInfo, error)
	ListNodes(ctx context.Context) ([]Node, error)
	// SetNodeVRAMBudget sets (or, with an empty budget, clears) a federated
	// node's VRAM allocation cap as a sticky admin override. Only meaningful
	// in distributed mode; single-process clients report it as unavailable.
	SetNodeVRAMBudget(ctx context.Context, nodeID, budget string) error
	VRAMEstimate(ctx context.Context, req VRAMEstimateRequest) (*vram.EstimateResult, error)

	// ---- State ----
	// ToggleModelState accepts modeladmin.ActionEnable / ActionDisable.
	ToggleModelState(ctx context.Context, name string, action modeladmin.Action) error
	// ToggleModelPinned accepts modeladmin.ActionPin / ActionUnpin.
	ToggleModelPinned(ctx context.Context, name string, action modeladmin.Action) error

	// ---- Branding / whitelabeling ----
	// GetBranding returns the configured instance branding (name, tagline,
	// asset URLs).
	GetBranding(ctx context.Context) (*Branding, error)
	// SetBranding updates the text branding fields. Asset uploads are not
	// exposed over MCP — admins use the Settings UI for binary files.
	SetBranding(ctx context.Context, req SetBrandingRequest) (*Branding, error)

	// ---- Voice profile library ----
	ListVoiceProfiles(ctx context.Context) ([]VoiceProfile, error)
	CreateVoiceProfile(ctx context.Context, req CreateVoiceProfileRequest) (*VoiceProfile, error)
	DeleteVoiceProfile(ctx context.Context, id string) error

	// ---- Usage / billing ----

	// GetUsageStats returns aggregated token usage. In single-user
	// no-auth mode this reports the synthetic local user's usage. The
	// implementation enforces "admin required to query other users".
	GetUsageStats(ctx context.Context, q UsageStatsQuery) (*UsageStats, error)

	// ---- PII filter ----
	// GetPIIEvents returns recent redaction events. Implementation
	// enforces "admin required" when auth is on. The regex pattern tools
	// were removed — detection policy lives on each detector model's
	// pii_detection block, managed via the model-config tools.
	GetPIIEvents(ctx context.Context, q PIIEventsQuery) ([]PIIEvent, error)

	// ---- Middleware admin ----
	// GetMiddlewareStatus returns the aggregated state surfaced on the
	// /app/middleware page: active PII patterns, per-model resolved
	// enabled state, recent event count, router placeholder.
	GetMiddlewareStatus(ctx context.Context) (*MiddlewareStatus, error)

	// ---- Router (intelligent routing) ----
	// GetRouterDecisions returns recent routing decisions for the
	// /app/middleware Routing tab and for agent-driven introspection.
	// Admin-required when auth is on.
	GetRouterDecisions(ctx context.Context, q RouterDecisionsQuery) ([]RouterDecision, error)

	// GetRouterCorpusStats reports a knn router's corpus size and
	// per-label counts — counts only, texts are never exposed.
	GetRouterCorpusStats(ctx context.Context, routerModel string) (*RouterCorpusStats, error)

	// SeedRouterCorpus adds labelled exemplars to a knn router's
	// corpus (embedded server-side, persisted, indexed immediately).
	SeedRouterCorpus(ctx context.Context, req RouterCorpusSeedRequest) (*RouterCorpusSeedResult, error)

	// ClearRouterCorpus wipes a knn router's corpus — file and live
	// index.
	ClearRouterCorpus(ctx context.Context, routerModel string) (*RouterCorpusClearResult, error)
}
