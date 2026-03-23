package nodes

import (
	"context"
	"fmt"
	"sync"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// ModelRouterAdapter wraps SmartRouter to provide a model.ModelRouter callback
// for the ModelLoader. When the ModelLoader needs to start a gRPC backend,
// it calls this adapter instead of starting a local process.
//
// The adapter:
// 1. Calls SmartRouter.Route() to find/load the model on a remote node
//    (SmartRouter pre-stages model files via FileStager in Route())
// 2. Returns a Model with a FileStagingClient-wrapped gRPC client
// 3. Tracks Release() functions for cleanup on model unload
type ModelRouterAdapter struct {
	router  *SmartRouter
	mu      sync.Mutex
	release map[string]func() // modelID -> Release() callback
}

// NewModelRouterAdapter creates a new adapter.
func NewModelRouterAdapter(router *SmartRouter) *ModelRouterAdapter {
	return &ModelRouterAdapter{
		router:  router,
		release: make(map[string]func()),
	}
}

// Route implements the model.ModelRouter callback signature.
// It delegates to SmartRouter.Route() and returns a Model that wraps the
// remote gRPC client with file staging if configured.
func (a *ModelRouterAdapter) Route(ctx context.Context, backend, modelID, modelName, modelFile string,
	opts *pb.ModelOptions, parallel bool) (*model.Model, error) {

	backendType := backend

	// Set model file and name on opts — these are passed as function args
	// (like the local path in initializers.go) but not pre-set in gRPCOptions.
	if opts != nil {
		opts.Model = modelName
		opts.ModelFile = modelFile
	}

	// Route to a remote node (SmartRouter handles model pre-staging via FileStager)
	// Pass modelID so the DB tracks models by their logical ID, not the file path
	result, err := a.router.Route(ctx, modelID, modelName, backendType, opts, parallel)
	if err != nil {
		return nil, fmt.Errorf("routing model %s: %w", modelName, err)
	}

	// Store release function for cleanup on unload
	a.mu.Lock()
	a.release[modelID] = result.Release
	a.mu.Unlock()

	// The client from Route is already a grpc.Backend.
	// If file staging is configured, it's already wrapped with FileStagingClient
	// by SmartRouter. Use NewModelWithClient so the wrapper is preserved when
	// the ModelLoader returns this model on subsequent requests.
	m := model.NewModelWithClient(modelID, result.Node.Address, result.Client)

	xlog.Info("Model routed to remote node", "model", modelName, "node", result.Node.Name, "address", result.Node.Address)
	return m, nil
}

// ReleaseModel releases the in-flight counter for a model.
// Called when the model is unloaded.
func (a *ModelRouterAdapter) ReleaseModel(modelID string) {
	a.mu.Lock()
	release, ok := a.release[modelID]
	if ok {
		delete(a.release, modelID)
	}
	a.mu.Unlock()

	if ok && release != nil {
		release()
	}
}

// AsModelRouter returns a model.ModelRouter function suitable for ModelLoader.SetModelRouter().
func (a *ModelRouterAdapter) AsModelRouter() model.ModelRouter {
	return a.Route
}
