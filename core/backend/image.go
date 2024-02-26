package backend

import (
	"github.com/go-skynet/LocalAI/core/config"

	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func ImageGeneration(height, width, mode, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, backendConfig config.BackendConfig, appConfig *config.ApplicationConfig) (func() error, error) {

	opts := modelOpts(backendConfig, appConfig, []model.Option{
		model.WithBackendString(backendConfig.Backend),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithThreads(uint32(backendConfig.Threads)),
		model.WithContext(appConfig.Context),
		model.WithModel(backendConfig.Model),
		model.WithLoadGRPCLoadModelOpts(&proto.ModelOptions{
			CUDA:          backendConfig.CUDA || backendConfig.Diffusers.CUDA,
			SchedulerType: backendConfig.Diffusers.SchedulerType,
			PipelineType:  backendConfig.Diffusers.PipelineType,
			CFGScale:      backendConfig.Diffusers.CFGScale,
			LoraAdapter:   backendConfig.LoraAdapter,
			LoraScale:     backendConfig.LoraScale,
			LoraBase:      backendConfig.LoraBase,
			IMG2IMG:       backendConfig.Diffusers.IMG2IMG,
			CLIPModel:     backendConfig.Diffusers.ClipModel,
			CLIPSubfolder: backendConfig.Diffusers.ClipSubFolder,
			CLIPSkip:      int32(backendConfig.Diffusers.ClipSkip),
			ControlNet:    backendConfig.Diffusers.ControlNet,
		}),
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
