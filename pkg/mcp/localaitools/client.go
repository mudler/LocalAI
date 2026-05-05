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
	ImportModelURI(ctx context.Context, req ImportModelURIRequest) (*ImportModelURIResponse, error)

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
}
