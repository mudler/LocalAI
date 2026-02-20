package backend

import (
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
)

func ModelTokenize(s string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (schema.TokenizeResponse, error) {

	var inferenceModel grpc.Backend
	var err error

	opts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err = loader.Load(opts...)
	if err != nil {
		return schema.TokenizeResponse{}, err
	}

	predictOptions := gRPCPredictOpts(modelConfig, loader.ModelPath)
	predictOptions.Prompt = s

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)
		startTime = time.Now()
	}

	// tokenize the string
	resp, err := inferenceModel.TokenizeString(appConfig.Context, predictOptions)

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}

		tokenCount := 0
		if resp.Tokens != nil {
			tokenCount = len(resp.Tokens)
		}

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceTokenize,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   trace.TruncateString(s, 200),
			Error:     errStr,
			Data: map[string]any{
				"input_text":  trace.TruncateString(s, 1000),
				"token_count": tokenCount,
			},
		})
	}

	if err != nil {
		return schema.TokenizeResponse{}, err
	}

	if resp.Tokens == nil {
		resp.Tokens = make([]int32, 0)
	}

	return schema.TokenizeResponse{
		Tokens: resp.Tokens,
	}, nil

}
