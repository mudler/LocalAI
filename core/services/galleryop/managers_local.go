package galleryop

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// LocalModelManager handles model install/delete on the local instance.
type LocalModelManager struct {
	systemState                 *system.SystemState
	modelLoader                 *model.ModelLoader
	enforcePredownloadScans     bool
	automaticallyInstallBackend bool
}

// NewLocalModelManager creates a LocalModelManager from the application config.
func NewLocalModelManager(appConfig *config.ApplicationConfig, ml *model.ModelLoader) *LocalModelManager {
	return &LocalModelManager{
		systemState:                 appConfig.SystemState,
		modelLoader:                 ml,
		enforcePredownloadScans:     appConfig.EnforcePredownloadScans,
		automaticallyInstallBackend: appConfig.AutoloadBackendGalleries,
	}
}

// SetAutoInstallBackend controls whether backend binaries are automatically
// installed when a model is installed. In distributed mode the frontend node
// disables this because backends only run on workers.
func (m *LocalModelManager) SetAutoInstallBackend(v bool) {
	m.automaticallyInstallBackend = v
}

func (m *LocalModelManager) DeleteModel(name string) error {
	if err := m.modelLoader.ShutdownModel(name); err != nil {
		xlog.Warn("Failed to unload model during deletion", "model", name, "error", err)
	}
	return gallery.DeleteModelFromSystem(m.systemState, name)
}

func (m *LocalModelManager) InstallModel(ctx context.Context, op *ManagementOp[gallery.GalleryModel, gallery.ModelConfig], progressCb ProgressCallback) error {
	switch {
	case op.GalleryElement != nil:
		installedModel, err := gallery.InstallModel(ctx, m.systemState, op.GalleryElement.Name,
			op.GalleryElement, op.Req.Overrides, progressCb, m.enforcePredownloadScans)
		if err != nil {
			return err
		}
		if m.automaticallyInstallBackend && installedModel.Backend != "" {
			xlog.Debug("Installing backend", "backend", installedModel.Backend)
			return gallery.InstallBackendFromGallery(ctx, op.BackendGalleries, m.systemState,
				m.modelLoader, installedModel.Backend, progressCb, false)
		}
		return nil
	case op.GalleryElementName != "":
		return gallery.InstallModelFromGallery(ctx, op.Galleries, op.BackendGalleries,
			m.systemState, m.modelLoader, op.GalleryElementName, op.Req, progressCb,
			m.enforcePredownloadScans, m.automaticallyInstallBackend)
	default:
		return installModelFromRemoteConfig(ctx, m.systemState, m.modelLoader, op.Req,
			progressCb, m.enforcePredownloadScans, m.automaticallyInstallBackend, op.BackendGalleries)
	}
}

// LocalBackendManager handles backend install/delete on the local instance.
type LocalBackendManager struct {
	systemState      *system.SystemState
	modelLoader      *model.ModelLoader
	backendGalleries []config.Gallery
}

// NewLocalBackendManager creates a LocalBackendManager from the application config.
func NewLocalBackendManager(appConfig *config.ApplicationConfig, ml *model.ModelLoader) *LocalBackendManager {
	return &LocalBackendManager{
		systemState:      appConfig.SystemState,
		modelLoader:      ml,
		backendGalleries: appConfig.BackendGalleries,
	}
}

func (b *LocalBackendManager) DeleteBackend(name string) error {
	err := gallery.DeleteBackendFromSystem(b.systemState, name)
	b.modelLoader.DeleteExternalBackend(name)
	return err
}

func (b *LocalBackendManager) ListBackends() (gallery.SystemBackends, error) {
	return gallery.ListSystemBackends(b.systemState)
}

func (b *LocalBackendManager) InstallBackend(ctx context.Context, op *ManagementOp[gallery.GalleryBackend, any], progressCb ProgressCallback) error {
	if op.ExternalURI != "" {
		return InstallExternalBackend(ctx, b.backendGalleries, b.systemState, b.modelLoader,
			progressCb, op.ExternalURI, op.ExternalName, op.ExternalAlias)
	}
	return gallery.InstallBackendFromGallery(ctx, b.backendGalleries, b.systemState,
		b.modelLoader, op.GalleryElementName, progressCb, true)
}
