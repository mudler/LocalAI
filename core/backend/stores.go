package backend

import (
	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
)

func StoreBackend(sl *model.ModelLoader, appConfig *config.ApplicationConfig, storeName string, backend string) (grpc.Backend, error) {
	if backend == "" {
		backend = model.LocalStoreBackend
	}
	// ModelLoader caches backend processes by `modelID`, not by the `model`
	// passed via WithModel. Without a distinct modelID, every StoreBackend
	// call collapses to the same `modelID=""` cache slot — face (512-D) and
	// voice (192-D) biometrics would then share the same local-store process
	// and the second enrollment would fail with
	//   Try to add key with length N when existing length is M
	// Use the store namespace as modelID so each namespace gets its own
	// process instance and its own in-memory Store{}.
	sc := []model.Option{
		model.WithBackendString(backend),
		model.WithModelID(storeName),
		model.WithModel(storeName),
	}

	return sl.Load(sc...)
}
