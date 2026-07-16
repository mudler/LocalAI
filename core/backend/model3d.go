package backend

import (
	"maps"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

// Model3DGenerationOptions is the backend-neutral request passed to 3D
// generators. Image contains a staged local path by the time it reaches
// this layer.
type Model3DGenerationOptions struct {
	Image        string
	Destination  string
	Seed         int32
	Step         int32
	CFGScale     float32
	TextureSteps int32
	Quality      string
	Background   string
	Params       map[string]string
}

func Model3DGeneration(options Model3DGenerationOptions, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func() error, error) {
	opts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	fn := func() error {
		_, err := inferenceModel.Generate3D(
			appConfig.Context,
			&proto.Generate3DRequest{
				Src:          options.Image,
				Dst:          options.Destination,
				Seed:         options.Seed,
				Step:         options.Step,
				CfgScale:     options.CFGScale,
				TextureSteps: options.TextureSteps,
				Quality:      options.Quality,
				Background:   options.Background,
				Params:       maps.Clone(options.Params),
			},
		)
		return err
	}

	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)

		traceData := map[string]any{
			"seed":          options.Seed,
			"step":          options.Step,
			"cfg_scale":     options.CFGScale,
			"texture_steps": options.TextureSteps,
			"quality":       options.Quality,
			"background":    options.Background,
			"has_image":     options.Image != "",
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

			// 3D generation has no prompt; the pipeline quality is the most
			// useful one-line summary of the request.
			trace.RecordBackendTrace(trace.BackendTrace{
				Timestamp: startTime,
				Duration:  duration,
				Type:      trace.BackendTrace3DGeneration,
				ModelName: modelConfig.Name,
				Backend:   modelConfig.Backend,
				Summary:   trace.TruncateString("3d: "+options.Quality, 200),
				Error:     errStr,
				Data:      traceData,
			})

			return err
		}
	}

	return fn, nil
}
