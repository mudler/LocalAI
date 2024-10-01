package backend

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

func TokenMetrics(
	backend,
	modelFile string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	backendConfig config.BackendConfig) (*proto.MetricsResponse, error) {
	bb := backend
	if bb == "" {
		return nil, fmt.Errorf("backend is required")
	}

	grpcOpts := GRPCModelOpts(backendConfig)

	opts := modelOpts(config.BackendConfig{}, appConfig, []model.Option{
		model.WithBackendString(bb),
		model.WithModel(modelFile),
		model.WithContext(appConfig.Context),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
	})
	model, err := loader.BackendLoader(opts...)
	if err != nil {
		return nil, err
	}

	if model == nil {
		return nil, fmt.Errorf("could not loadmodel model")
	}

	res, err := model.GetTokenMetrics(context.Background(), &proto.MetricsRequest{})

	return res, err
}
