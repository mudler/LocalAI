package backend

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

func TokenMetrics(
	modelFile string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig) (*proto.MetricsResponse, error) {

	opts := ModelOptions(modelConfig, appConfig, model.WithModel(modelFile))
	model, err := loader.Load(opts...)
	if err != nil {
		return nil, err
	}
	defer loader.Close()

	if model == nil {
		return nil, fmt.Errorf("could not loadmodel model")
	}

	res, err := model.GetTokenMetrics(context.Background(), &proto.MetricsRequest{})

	return res, err
}
