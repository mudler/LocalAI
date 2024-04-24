package backend

import (
	"context"
	"fmt"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func Rerank(backend, modelFile string, request *proto.RerankRequest, loader *model.ModelLoader, appConfig *config.ApplicationConfig, backendConfig config.BackendConfig) (*proto.RerankResult, error) {
	bb := backend
	if bb == "" {
		return nil, fmt.Errorf("backend is required")
	}

	grpcOpts := gRPCModelOpts(backendConfig)

	opts := modelOpts(config.BackendConfig{}, appConfig, []model.Option{
		model.WithBackendString(bb),
		model.WithModel(modelFile),
		model.WithContext(appConfig.Context),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
	})
	rerankModel, err := loader.BackendLoader(opts...)
	if err != nil {
		return nil, err
	}

	if rerankModel == nil {
		return nil, fmt.Errorf("could not load rerank model")
	}

	res, err := rerankModel.Rerank(context.Background(), request)

	return res, err
}
