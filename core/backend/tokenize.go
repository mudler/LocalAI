package backend

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc"
	model "github.com/mudler/LocalAI/pkg/model"
)

func ModelTokenize(s string, loader *model.ModelLoader, backendConfig config.BackendConfig, appConfig *config.ApplicationConfig) (schema.TokenizeResponse, error) {

	modelFile := backendConfig.Model

	grpcOpts := GRPCModelOpts(backendConfig)

	var inferenceModel grpc.Backend
	var err error

	opts := modelOpts(backendConfig, appConfig, []model.Option{
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
		model.WithThreads(uint32(*backendConfig.Threads)),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithModel(modelFile),
		model.WithContext(appConfig.Context),
	})

	if backendConfig.Backend == "" {
		inferenceModel, err = loader.GreedyLoader(opts...)
	} else {
		opts = append(opts, model.WithBackendString(backendConfig.Backend))
		inferenceModel, err = loader.BackendLoader(opts...)
	}
	if err != nil {
		return schema.TokenizeResponse{}, err
	}

	predictOptions := gRPCPredictOpts(backendConfig, loader.ModelPath)
	predictOptions.Prompt = s

	// tokenize the string
	resp, err := inferenceModel.TokenizeString(appConfig.Context, predictOptions)
	if err != nil {
		return schema.TokenizeResponse{}, err
	}

	return schema.TokenizeResponse{
		Tokens: resp.Tokens,
	}, nil

}
