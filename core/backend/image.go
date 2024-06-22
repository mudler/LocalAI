package backend

import (
	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

func ImageGeneration(height, width, mode, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, backendConfig config.BackendConfig, appConfig *config.ApplicationConfig) (func() error, error) {
	threads := backendConfig.Threads
	if *threads == 0 && appConfig.Threads != 0 {
		threads = &appConfig.Threads
	}
	gRPCOpts := gRPCModelOpts(backendConfig)
	opts := modelOpts(backendConfig, appConfig, []model.Option{
		model.WithBackendString(backendConfig.Backend),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithThreads(uint32(*threads)),
		model.WithContext(appConfig.Context),
		model.WithModel(backendConfig.Model),
		model.WithLoadGRPCLoadModelOpts(gRPCOpts),
	})

	inferenceModel, err := loader.BackendLoader(
		opts...,
	)
	if err != nil {
		return nil, err
	}

	fn := func() error {
		_, err := inferenceModel.GenerateImage(
			appConfig.Context,
			&proto.GenerateImageRequest{
				Height:           int32(height),
				Width:            int32(width),
				Mode:             int32(mode),
				Step:             int32(step),
				Seed:             int32(seed),
				CLIPSkip:         int32(backendConfig.Diffusers.ClipSkip),
				PositivePrompt:   positive_prompt,
				NegativePrompt:   negative_prompt,
				Dst:              dst,
				Src:              src,
				EnableParameters: backendConfig.Diffusers.EnableParameters,
			})
		return err
	}

	return fn, nil
}
