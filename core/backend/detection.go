package backend

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

func Detection(
	sourceFile string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	backendConfig config.BackendConfig,
) (*proto.DetectResponse, error) {
	opts := ModelOptions(backendConfig, appConfig)
	detectionModel, err := loader.Load(opts...)
	if err != nil {
		return nil, err
	}
	defer loader.Close()

	if detectionModel == nil {
		return nil, fmt.Errorf("could not load detection model")
	}

	res, err := detectionModel.Detect(context.Background(), &proto.DetectOptions{
		Src: sourceFile,
	})

	return res, err
}
