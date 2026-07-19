package routes

import (
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
