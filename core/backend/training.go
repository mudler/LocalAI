package backend

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

// TrainModel initiates a training job on a backend and streams progress via the callback.
func TrainModel(
	ctx context.Context,
	trainReq *proto.TrainRequest,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
	progressCallback func(*proto.TrainResponse),
) error {
	opts := ModelOptions(modelConfig, appConfig)
	backend, err := loader.Load(opts...)
	if err != nil {
		return fmt.Errorf("loading backend for training: %w", err)
	}

	if backend == nil {
		return fmt.Errorf("could not load backend %q for training", modelConfig.Backend)
	}

	return backend.TrainStream(ctx, trainReq, progressCallback)
}
