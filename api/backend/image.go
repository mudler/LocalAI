package backend

import (
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func ImageGeneration(height, width, mode, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, c config.Config, o *options.Option) (func() error, error) {

	opts := modelOpts(c, o, []model.Option{
		model.WithBackendString(c.Backend),
		model.WithAssetDir(o.AssetsDestination),
		model.WithThreads(uint32(c.Threads)),
		model.WithContext(o.Context),
		model.WithModel(c.Model),
		model.WithLoadGRPCLoadModelOpts(&proto.ModelOptions{
			CUDA:          c.Diffusers.CUDA,
			SchedulerType: c.Diffusers.SchedulerType,
			PipelineType:  c.Diffusers.PipelineType,
			CFGScale:      c.Diffusers.CFGScale,
			LoraAdapter:   c.LoraAdapter,
			LoraBase:      c.LoraBase,
			IMG2IMG:       c.Diffusers.IMG2IMG,
			CLIPModel:     c.Diffusers.ClipModel,
			CLIPSubfolder: c.Diffusers.ClipSubFolder,
			CLIPSkip:      int32(c.Diffusers.ClipSkip),
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
			o.Context,
			&proto.GenerateImageRequest{
				Height:           int32(height),
				Width:            int32(width),
				Mode:             int32(mode),
				Step:             int32(step),
				Seed:             int32(seed),
				CLIPSkip:         int32(c.Diffusers.ClipSkip),
				PositivePrompt:   positive_prompt,
				NegativePrompt:   negative_prompt,
				Dst:              dst,
				Src:              src,
				EnableParameters: c.Diffusers.EnableParameters,
			})
		return err
	}

	return fn, nil
}
