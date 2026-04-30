package backend

import (
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

func ModelDetokenize(tokens []int32, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (schema.DetokenizeResponse, error) {

	var inferenceModel grpc.Backend
	var err error

	opts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err = loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return schema.DetokenizeResponse{}, err
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
	}

	resp, err := inferenceModel.Detokenize(appConfig.Context, &pb.DetokenizeRequest{Tokens: tokens})

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}

		content := ""
		if resp != nil {
			content = resp.Content
		}

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceTokenize,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   trace.TruncateString(content, 200),
			Error:     errStr,
			Data: map[string]any{
				"token_count": len(tokens),
				"output_text": trace.TruncateString(content, 1000),
			},
		})
	}

	if err != nil {
		return schema.DetokenizeResponse{}, err
	}

	return schema.DetokenizeResponse{
		Content: resp.Content,
	}, nil
}
