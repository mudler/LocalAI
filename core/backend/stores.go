package backend

import (
	"github.com/go-skynet/LocalAI/core/config"

	"github.com/go-skynet/LocalAI/pkg/grpc"
	"github.com/go-skynet/LocalAI/pkg/model"
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

    return sl.BackendLoader(sc...)
}

