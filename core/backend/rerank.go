package backend

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

func Rerank(request *proto.RerankRequest, loader *model.ModelLoader, appConfig *config.ApplicationConfig, modelConfig config.ModelConfig) (*proto.RerankResult, error) {
	opts := ModelOptions(modelConfig, appConfig)
	rerankModel, err := loader.Load(opts...)
	if err != nil {
		return nil, err
	}
	defer loader.Close()

	if rerankModel == nil {
		return nil, fmt.Errorf("could not load rerank model")
	}

	res, err := rerankModel.Rerank(context.Background(), request)

	return res, err
}
