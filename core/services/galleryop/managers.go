package galleryop

import (
	"context"

	"github.com/mudler/LocalAI/core/gallery"
)

// ProgressCallback reports download progress for model/backend installations.
type ProgressCallback func(fileName, current, total string, percentage float64)

// ModelManager handles model install and delete lifecycle.
type ModelManager interface {
	InstallModel(ctx context.Context, op *ManagementOp[gallery.GalleryModel, gallery.ModelConfig], progressCb ProgressCallback) error
	DeleteModel(name string) error
}

// BackendManager handles backend install, delete, upgrade, and listing lifecycle.
type BackendManager interface {
	InstallBackend(ctx context.Context, op *ManagementOp[gallery.GalleryBackend, any], progressCb ProgressCallback) error
	DeleteBackend(name string) error
	ListBackends() (gallery.SystemBackends, error)
	UpgradeBackend(ctx context.Context, name string, progressCb ProgressCallback) error
	CheckUpgrades(ctx context.Context) (map[string]gallery.UpgradeInfo, error)
}
