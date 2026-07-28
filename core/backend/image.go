package backend

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

func ImageGeneration(ctx context.Context, height, width, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig, refImages []string) (func() error, error) {

	// model.WithContext(ctx) overrides the app-context default set in
	// ModelOptions so distributed routing decisions reach the request's
	// X-LocalAI-Node holder via distributedhdr.Stamp.
	opts := ModelOptions(modelConfig, appConfig, model.WithContext(ctx))
	inferenceModel, err := loader.Load(
		opts...,
	)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	fn := func() error {
		_, err := inferenceModel.GenerateImage(
			ctx,
			&proto.GenerateImageRequest{
				// ModelIdentity is ModelConfig.Model — the SAME expression
				// ModelOptions feeds to model.WithModel above, which the
				// backend receives as ModelOptions.Model at LoadModel. Equal
				// by construction, so the check cannot false-reject (#10952).
				ModelIdentity:    modelConfig.Model,
				Height:           int32(height),
				Width:            int32(width),
				Step:             int32(step),
				Seed:             int32(seed),
				CLIPSkip:         int32(modelConfig.Diffusers.ClipSkip),
				PositivePrompt:   positive_prompt,
				NegativePrompt:   negative_prompt,
				Dst:              dst,
				Src:              src,
				EnableParameters: modelConfig.Diffusers.EnableParameters,
				RefImages:        refImages,
			})
		return err
	}

	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)

		traceData := map[string]any{
			"positive_prompt": positive_prompt,
			"negative_prompt": negative_prompt,
			"height":          height,
			"width":           width,
			"step":            step,
			"seed":            seed,
			"source_image":    src,
			"destination":     dst,
		}

		startTime := time.Now()
		originalFn := fn
		fn = func() error {
			err := originalFn()
			duration := time.Since(startTime)

			errStr := ""
			if err != nil {
				errStr = err.Error()
			}

			trace.RecordBackendTrace(trace.BackendTrace{
				Timestamp: startTime,
				Duration:  duration,
				Type:      trace.BackendTraceImageGeneration,
				ModelName: modelConfig.Name,
				Backend:   modelConfig.Backend,
				Summary:   trace.TruncateString(positive_prompt, 200),
				Error:     errStr,
				Data:      traceData,
			})

			return err
		}
	}

	return fn, nil
}

// ImageGenerationFunc is a test-friendly indirection to call image generation logic.
// Tests can override this variable to provide a stub implementation.
var ImageGenerationFunc = ImageGeneration
