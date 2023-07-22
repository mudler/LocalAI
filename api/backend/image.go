package backend

import (
	"fmt"
	"sync"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func ImageGeneration(height, width, mode, step, seed int, positive_prompt, negative_prompt, dst string, loader *model.ModelLoader, c config.Config, o *options.Option) (func() error, error) {
	if c.Backend != model.StableDiffusionBackend {
		return nil, fmt.Errorf("endpoint only working with stablediffusion models")
	}

	opts := []model.Option{
		model.WithBackendString(c.Backend),
		model.WithAssetDir(o.AssetsDestination),
		model.WithThreads(uint32(c.Threads)),
		model.WithContext(o.Context),
		model.WithModelFile(c.ImageGenerationAssets),
	}

	for k, v := range o.ExternalGRPCBackends {
		opts = append(opts, model.WithExternalBackend(k, v))
	}

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
				Height:         int32(height),
				Width:          int32(width),
				Mode:           int32(mode),
				Step:           int32(step),
				Seed:           int32(seed),
				PositivePrompt: positive_prompt,
				NegativePrompt: negative_prompt,
				Dst:            dst,
			})
		return err
	}

	return func() error {
		// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
		mutexMap.Lock()
		l, ok := mutexes[c.Backend]
		if !ok {
			m := &sync.Mutex{}
			mutexes[c.Backend] = m
			l = m
		}
		mutexMap.Unlock()
		l.Lock()
		defer l.Unlock()

		return fn()
	}, nil
}
