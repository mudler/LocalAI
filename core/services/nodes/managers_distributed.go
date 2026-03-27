package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// DistributedModelManager wraps a local ModelManager and adds NATS fan-out
// for model deletion so worker nodes clean up stale files.
type DistributedModelManager struct {
	local   galleryop.ModelManager
	adapter *RemoteUnloaderAdapter
}

// NewDistributedModelManager creates a DistributedModelManager.
// Backend auto-install is disabled because the frontend node delegates
// inference to workers and never runs backends locally.
func NewDistributedModelManager(appConfig *config.ApplicationConfig, ml *model.ModelLoader, adapter *RemoteUnloaderAdapter) *DistributedModelManager {
	local := galleryop.NewLocalModelManager(appConfig, ml)
	local.SetAutoInstallBackend(false)
	return &DistributedModelManager{
		local:   local,
		adapter: adapter,
	}
}

func (d *DistributedModelManager) DeleteModel(name string) error {
	err := d.local.DeleteModel(name)
	// Best-effort: fan out model.delete to worker nodes
	if rcErr := d.adapter.DeleteModelFiles(name); rcErr != nil {
		xlog.Warn("Failed to propagate model file deletion to workers", "model", name, "error", rcErr)
	}
	return err
}

func (d *DistributedModelManager) InstallModel(ctx context.Context, op *galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig], progressCb galleryop.ProgressCallback) error {
	return d.local.InstallModel(ctx, op, progressCb)
}

// DistributedBackendManager wraps a local BackendManager and adds NATS fan-out
// for backend deletion so worker nodes clean up stale files.
type DistributedBackendManager struct {
	local    galleryop.BackendManager
	adapter  *RemoteUnloaderAdapter
	registry *NodeRegistry
}

// NewDistributedBackendManager creates a DistributedBackendManager.
func NewDistributedBackendManager(appConfig *config.ApplicationConfig, ml *model.ModelLoader, adapter *RemoteUnloaderAdapter, registry *NodeRegistry) *DistributedBackendManager {
	return &DistributedBackendManager{
		local:    galleryop.NewLocalBackendManager(appConfig, ml),
		adapter:  adapter,
		registry: registry,
	}
}

func (d *DistributedBackendManager) DeleteBackend(name string) error {
	// Try local deletion but ignore "not found" errors — in distributed mode
	// the frontend node typically doesn't have backends installed locally;
	// they only exist on worker nodes.
	if err := d.local.DeleteBackend(name); err != nil {
		if !errors.Is(err, gallery.ErrBackendNotFound) {
			return err
		}
		xlog.Debug("Backend not found locally, will attempt deletion on workers", "backend", name)
	}
	// Fan out backend.delete to all healthy nodes
	allNodes, listErr := d.registry.List()
	if listErr != nil {
		xlog.Warn("Failed to list nodes for backend deletion fan-out", "error", listErr)
		return listErr
	}
	var errs []error
	for _, node := range allNodes {
		if node.Status != StatusHealthy {
			continue
		}
		if _, delErr := d.adapter.DeleteBackend(node.ID, name); delErr != nil {
			xlog.Warn("Failed to propagate backend deletion to worker", "node", node.Name, "backend", name, "error", delErr)
			errs = append(errs, fmt.Errorf("node %s: %w", node.Name, delErr))
		}
	}
	return errors.Join(errs...)
}

// ListBackends aggregates installed backends from all healthy worker nodes.
func (d *DistributedBackendManager) ListBackends() (gallery.SystemBackends, error) {
	result := make(gallery.SystemBackends)
	allNodes, err := d.registry.List()
	if err != nil {
		return result, err
	}

	for _, node := range allNodes {
		if node.Status != StatusHealthy {
			continue
		}
		reply, err := d.adapter.ListBackends(node.ID)
		if err != nil {
			xlog.Warn("Failed to list backends on worker", "node", node.Name, "error", err)
			continue
		}
		if reply.Error != "" {
			xlog.Warn("Worker returned error listing backends", "node", node.Name, "error", reply.Error)
			continue
		}
		for _, b := range reply.Backends {
			if _, exists := result[b.Name]; !exists {
				result[b.Name] = gallery.SystemBackend{
					Name:     b.Name,
					IsSystem: b.IsSystem,
					IsMeta:   b.IsMeta,
					Metadata: &gallery.BackendMetadata{
						InstalledAt: b.InstalledAt,
						GalleryURL:  b.GalleryURL,
					},
				}
			}
		}
	}
	return result, nil
}

// InstallBackend fans out backend installation to all healthy worker nodes.
func (d *DistributedBackendManager) InstallBackend(ctx context.Context, op *galleryop.ManagementOp[gallery.GalleryBackend, any], progressCb galleryop.ProgressCallback) error {
	allNodes, err := d.registry.List()
	if err != nil {
		return err
	}

	galleriesJSON, _ := json.Marshal(op.Galleries)
	backendName := op.GalleryElementName

	for _, node := range allNodes {
		if node.Status != StatusHealthy {
			continue
		}
		reply, err := d.adapter.InstallBackend(node.ID, backendName, "", string(galleriesJSON))
		if err != nil {
			xlog.Warn("Failed to install backend on worker", "node", node.Name, "backend", backendName, "error", err)
			continue
		}
		if !reply.Success {
			xlog.Warn("Backend install failed on worker", "node", node.Name, "backend", backendName, "error", reply.Error)
		}
	}
	return nil
}
