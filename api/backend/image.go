package backend

import (
	"fmt"
	"sync"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/stablediffusion"
)

func ImageGeneration(height, width, mode, step, seed int, positive_prompt, negative_prompt, dst string, loader *model.ModelLoader, c config.Config, o *options.Option) (func() error, error) {
	if c.Backend != model.StableDiffusionBackend {
		return nil, fmt.Errorf("endpoint only working with stablediffusion models")
	}

	inferenceModel, err := loader.BackendLoader(
		model.WithBackendString(c.Backend),
		model.WithAssetDir(o.AssetsDestination),
		model.WithThreads(uint32(c.Threads)),
		model.WithModelFile(c.ImageGenerationAssets),
	)
	if err != nil {
		return nil, err
	}

	var fn func() error
	switch model := inferenceModel.(type) {
	case *stablediffusion.StableDiffusion:
		fn = func() error {
			return model.GenerateImage(height, width, mode, step, seed, positive_prompt, negative_prompt, dst)
		}

	default:
		fn = func() error {
			return fmt.Errorf("creation of images not supported by the backend")
		}
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
