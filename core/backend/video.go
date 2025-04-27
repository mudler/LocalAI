package backend

import (
	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

func VideoGeneration(height, width int32, prompt, startImage, endImage, dst string, loader *model.ModelLoader, backendConfig config.BackendConfig, appConfig *config.ApplicationConfig) (func() error, error) {

	opts := ModelOptions(backendConfig, appConfig)
	inferenceModel, err := loader.Load(
		opts...,
	)
	if err != nil {
		return nil, err
	}
	defer loader.Close()

	fn := func() error {
		_, err := inferenceModel.GenerateVideo(
			appConfig.Context,
			&proto.GenerateVideoRequest{
				Height:     height,
				Width:      width,
				Prompt:     prompt,
				StartImage: startImage,
				EndImage:   endImage,
				Dst:        dst,
			})
		return err
	}

	return fn, nil
}
