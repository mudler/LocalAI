package localaitools

import (
	"context"

	"github.com/mudler/LocalAI/core/services/modeladmin"
)

// LocalAIClient is the surface tools depend on. It has two implementations:
//
//   - inproc.Client (in-process; calls LocalAI services directly)
//   - httpapi.Client (out-of-process; calls the LocalAI REST API)
//
// Tool handlers and the embedded skill prompts are agnostic to which
// implementation backs the client.
type LocalAIClient interface {
	// ---- Models / gallery (read) ----
	GallerySearch(ctx context.Context, q GallerySearchQuery) ([]GalleryModelHit, error)
	ListInstalledModels(ctx context.Context, capability Capability) ([]InstalledModel, error)
	ListGalleries(ctx context.Context) ([]Gallery, error)
	GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error)
	GetModelConfig(ctx context.Context, name string) (*ModelConfigView, error)

	// ---- Models / gallery (write) ----
	InstallModel(ctx context.Context, req InstallModelRequest) (jobID string, err error)
	DeleteModel(ctx context.Context, name string) error
	EditModelConfig(ctx context.Context, name string, patch map[string]any) error
	ReloadModels(ctx context.Context) error
	ImportModelURI(ctx context.Context, req ImportModelURIRequest) (*ImportModelURIResponse, error)

	// ---- Backends ----
	ListBackends(ctx context.Context) ([]Backend, error)
	ListKnownBackends(ctx context.Context) ([]Backend, error)
	InstallBackend(ctx context.Context, req InstallBackendRequest) (jobID string, err error)
	UpgradeBackend(ctx context.Context, name string) (jobID string, err error)

	// ---- System ----
	SystemInfo(ctx context.Context) (*SystemInfo, error)
	ListNodes(ctx context.Context) ([]Node, error)
	VRAMEstimate(ctx context.Context, req VRAMEstimateRequest) (*VRAMEstimate, error)

	// ---- State ----
	// ToggleModelState accepts modeladmin.ActionEnable / ActionDisable.
	ToggleModelState(ctx context.Context, name string, action modeladmin.Action) error
	// ToggleModelPinned accepts modeladmin.ActionPin / ActionUnpin.
	ToggleModelPinned(ctx context.Context, name string, action modeladmin.Action) error
}
