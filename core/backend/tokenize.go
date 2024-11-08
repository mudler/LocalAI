package backend

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc"
	model "github.com/mudler/LocalAI/pkg/model"
)

func ModelTokenize(s string, loader *model.ModelLoader, backendConfig config.BackendConfig, appConfig *config.ApplicationConfig) (schema.TokenizeResponse, error) {

	modelFile := backendConfig.Model

	var inferenceModel grpc.Backend
	var err error

	opts := ModelOptions(backendConfig, appConfig, model.WithModel(modelFile))

	if backendConfig.Backend == "" {
		inferenceModel, err = loader.Load(opts...)
	} else {
		opts = append(opts, model.WithBackendString(backendConfig.Backend))
		inferenceModel, err = loader.Load(opts...)
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
