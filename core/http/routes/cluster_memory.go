package routes

import (
	"context"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// ClusterMemoryProvider reports the memory budget a model actually gets: the
// largest single healthy node. It is nil in single-node mode, where the local
// host is the only thing worth sizing against.
type ClusterMemoryProvider func(ctx context.Context) (*nodes.ClusterMemory, error)

// ClusterMemoryProviderFor returns the memory source backing every surface that
// answers "will this model fit", or nil in single-node mode.
//
// This is the sibling of ClusterCapabilityProviderFor and exists for the same
// reason. Backend discovery already unions worker capabilities because the
// controller is usually a GPU-less pod; the model gallery asks a second
// question about the same hardware, "how big a model can run here", and
// answering it from the controller's own RAM told admins that a cluster of
// A100s could only run the smallest CPU build.
func ClusterMemoryProviderFor(app *application.Application) ClusterMemoryProvider {
	if app == nil || !app.IsDistributed() || app.Distributed().Registry == nil {
		return nil
	}
	return app.Distributed().Registry.HealthyNodeMemory
}

// resolveClusterMemory reads the cluster's memory budget, degrading to no
// reading on error.
//
// Every caller treats a nil reading as "size against the local host exactly as
// before", so a registry hiccup narrows the catalog back to single-node
// behavior rather than marking every model as too large.
func resolveClusterMemory(ctx context.Context, provider ClusterMemoryProvider) *nodes.ClusterMemory {
	if provider == nil {
		return nil
	}
	memory, err := provider(ctx)
	if err != nil {
		xlog.Warn("Could not read cluster memory, sizing models against the local system only", "error", err)
		return nil
	}
	return memory
}

// clusterResourceBlock renders a cluster reading for the API surfaces that
// carry one, or nil when there is nothing to report.
//
// It is an ADDITIONAL field rather than a rewrite of the local aggregate. The
// resource monitor shows the controller's genuine own usage and must keep
// doing so; only the model-sizing surfaces switch to this block, and a client
// that has never heard of it behaves exactly as it did before.
func clusterResourceBlock(memory *nodes.ClusterMemory) map[string]any {
	if memory == nil {
		return nil
	}
	return map[string]any{
		"enabled":      true,
		"node_id":      memory.NodeID,
		"node_name":    memory.NodeName,
		"total_memory": memory.TotalMemory,
		"is_gpu":       memory.IsGPU,
		"node_count":   memory.NodeCount,
	}
}

// hostModelEnv describes the local host to variant selection.
func hostModelEnv(ctx context.Context, systemState *system.SystemState) gallery.ResolveEnv {
	return gallery.HostResolveEnv(ctx, systemState)
}

// clusterModelEnv describes whichever machine actually runs models to variant
// selection: the cluster's best node in distributed mode, the local host
// otherwise.
//
// The two providers are read independently on purpose. A cluster that can
// report its hardware but not a usable memory figure still deserves the
// hardware verdict, so each half falls back on its own.
func clusterModelEnv(ctx context.Context, systemState *system.SystemState, memory ClusterMemoryProvider, capabilities localai.ClusterCapabilityProvider) gallery.ResolveEnv {
	reading := resolveClusterMemory(ctx, memory)
	caps := localai.ResolveClusterCapabilities(ctx, capabilities)

	if reading == nil && len(caps) == 0 {
		return hostModelEnv(ctx, systemState)
	}

	var budget uint64
	if reading != nil {
		budget = reading.TotalMemory
	}
	return gallery.ClusterResolveEnv(ctx, systemState, budget, caps)
}
