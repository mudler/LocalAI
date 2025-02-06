package backend

import (
	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
)

func StoreBackend(sl *model.ModelLoader, appConfig *config.ApplicationConfig, storeName string) (grpc.Backend, error) {
	if storeName == "" {
		storeName = "default"
	}

	sc := []model.Option{
		model.WithBackendString(model.LocalStoreBackend),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithModel(storeName),
	}

	return sl.Load(sc...)
}
