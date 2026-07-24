package backend

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

// ImageUpscale loads the model specified in modelConfig and calls UpscaleImage
// on the backend, writing the result to dst.
func ImageUpscale(ctx context.Context, src, dst string, scale int, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func() error, error) {
	opts := ModelOptions(modelConfig, appConfig, model.WithContext(ctx))
	inferenceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	fn := func() error {
		_, err := inferenceModel.UpscaleImage(
			ctx,
			&proto.UpscaleImageRequest{
				Src:   src,
				Dst:   dst,
				Scale: int32(scale),
			},
		)
		return err
	}

	return fn, nil
}

// ImageUpscaleFunc is a test-friendly indirection.
var ImageUpscaleFunc = ImageUpscale
