package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

func Detection(
	sourceFile string,
	prompt string,
	points []float32,
	boxes []float32,
	threshold float32,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (*proto.DetectResponse, error) {
	opts := ModelOptions(modelConfig, appConfig)
	detectionModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	if detectionModel == nil {
		return nil, fmt.Errorf("could not load detection model")
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)
		startTime = time.Now()
	}

	res, err := detectionModel.Detect(context.Background(), &proto.DetectOptions{
		Src:       sourceFile,
		Prompt:    prompt,
		Points:    points,
		Boxes:     boxes,
		Threshold: threshold,
	})

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceDetection,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   trace.TruncateString(sourceFile, 200),
			Error:     errStr,
			Data: map[string]any{
				"source_file": sourceFile,
			},
		})
	}

	return res, err
}
