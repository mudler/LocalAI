package routes

import (
	"context"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
)

// ClusterCapabilityProviderFor returns the capability source backing every
// capability-filtered backend discovery endpoint, or nil in single-node mode.
//
// A nil provider makes those endpoints filter against the local system exactly
// as they always have. In distributed mode the controller is typically a
// GPU-less pod, so discovery instead unions the capabilities of the healthy
// worker nodes that would actually run the backend.
func ClusterCapabilityProviderFor(app *application.Application) localai.ClusterCapabilityProvider {
	if app == nil || !app.IsDistributed() || app.Distributed().Registry == nil {
		return nil
	}
	return app.Distributed().Registry.HealthyBackendCapabilities
}

// ClusterInstalledProviderFor returns the install-state source backing every
// backend discovery endpoint that filters on installed backends, or nil in
// single-node mode.
//
// A nil provider leaves those endpoints reading the local filesystem exactly as
// they always have. In distributed mode a backend lives on the worker that runs
// it, so the controller's own disk cannot answer the question; the active
// BackendManager already aggregates the per-node view that GET /backends
// renders, and discovery reuses it rather than growing a second path.
func ClusterInstalledProviderFor(app *application.Application) localai.ClusterInstalledProvider {
	if app == nil || !app.IsDistributed() || app.GalleryService() == nil {
		return nil
	}
	return func(ctx context.Context) ([]string, error) {
		manager := app.GalleryService().BackendManager()
		if manager == nil {
			return nil, nil
		}
		backends, err := manager.ListBackends()
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(backends))
		for name := range backends {
			names = append(names, name)
		}
		return names, nil
	}
}
