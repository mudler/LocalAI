package backend

import (
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

func VideoGeneration(height, width int32, prompt, negativePrompt, startImage, endImage, dst string, numFrames, fps, seed int32, cfgScale float32, step int32, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func() error, error) {

	opts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err := loader.Load(
		opts...,
	)
	if err != nil {
		return nil, err
	}

	fn := func() error {
		_, err := inferenceModel.GenerateVideo(
			appConfig.Context,
			&proto.GenerateVideoRequest{
				Height:         height,
				Width:          width,
				Prompt:         prompt,
				NegativePrompt: negativePrompt,
				StartImage:     startImage,
				EndImage:       endImage,
				NumFrames:      numFrames,
				Fps:            fps,
				Seed:           seed,
				CfgScale:       cfgScale,
				Step:           step,
				Dst:            dst,
			})
		return err
	}

	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)

		traceData := map[string]any{
			"prompt":          prompt,
			"negative_prompt": negativePrompt,
			"height":          height,
			"width":           width,
			"num_frames":      numFrames,
			"fps":             fps,
			"seed":            seed,
			"cfg_scale":       cfgScale,
			"step":            step,
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
				Type:      trace.BackendTraceVideoGeneration,
				ModelName: modelConfig.Name,
				Backend:   modelConfig.Backend,
				Summary:   trace.TruncateString(prompt, 200),
				Error:     errStr,
				Data:      traceData,
			})

			return err
		}
	}

	return fn, nil
}
