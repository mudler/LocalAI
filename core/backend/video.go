package backend

import (
	"maps"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

// VideoGenerationOptions is the backend-neutral request passed to video generators.
// Media fields contain staged local paths by the time they reach this layer.
type VideoGenerationOptions struct {
	Height         int32
	Width          int32
	Prompt         string
	NegativePrompt string
	StartImage     string
	EndImage       string
	Audio          string
	Destination    string
	NumFrames      int32
	FPS            int32
	Seed           int32
	CFGScale       float32
	Step           int32
	Params         map[string]string
}

func VideoGeneration(options VideoGenerationOptions, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func() error, error) {
	opts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	fn := func() error {
		_, err := inferenceModel.GenerateVideo(
			appConfig.Context,
			&proto.GenerateVideoRequest{
				// See the ModelIdentity note in image.go: this is the value
				// ModelOptions passed to LoadModel a few lines above.
				ModelIdentity:  modelConfig.Model,
				Height:         options.Height,
				Width:          options.Width,
				Prompt:         options.Prompt,
				NegativePrompt: options.NegativePrompt,
				StartImage:     options.StartImage,
				EndImage:       options.EndImage,
				Audio:          options.Audio,
				NumFrames:      options.NumFrames,
				Fps:            options.FPS,
				Seed:           options.Seed,
				CfgScale:       options.CFGScale,
				Step:           options.Step,
				Dst:            options.Destination,
				Params:         maps.Clone(options.Params),
			},
		)
		return err
	}

	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)

		traceData := map[string]any{
			"prompt":          options.Prompt,
			"negative_prompt": options.NegativePrompt,
			"height":          options.Height,
			"width":           options.Width,
			"num_frames":      options.NumFrames,
			"fps":             options.FPS,
			"seed":            options.Seed,
			"cfg_scale":       options.CFGScale,
			"step":            options.Step,
			"has_start_image": options.StartImage != "",
			"has_end_image":   options.EndImage != "",
			"has_audio":       options.Audio != "",
		}

		originalFn := fn
		fn = func() error {
			startTime := time.Now()
			traceID := trace.BeginBackendTrace(trace.BackendTrace{Timestamp: startTime, Type: trace.BackendTraceVideoGeneration, ModelName: modelConfig.Name, Backend: modelConfig.Backend, Summary: trace.TruncateString(options.Prompt, 200)})
			defer trace.CancelBackendTrace(traceID)
			err := originalFn()
			duration := time.Since(startTime)

			errStr := ""
			if err != nil {
				errStr = err.Error()
			}

			trace.RecordBackendTrace(trace.BackendTrace{
				ID:        traceID,
				Timestamp: startTime,
				Duration:  duration,
				Type:      trace.BackendTraceVideoGeneration,
				ModelName: modelConfig.Name,
				Backend:   modelConfig.Backend,
				Summary:   trace.TruncateString(options.Prompt, 200),
				Error:     errStr,
				Data:      traceData,
			})

			return err
		}
	}
	originalFn := fn
	fn = func() error {
		release, err := AcquireGlobalBackendSlot()
		if err != nil {
			return err
		}
		defer release()
		return originalFn()
	}

	return fn, nil
}
