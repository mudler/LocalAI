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

// tokenizeTokenCount returns the number of tokens in a backend response,
// treating a nil response as zero. The gRPC client returns (nil, err) on
// failure, and the tracing block below runs before that error is returned —
// so the count must be read nil-safely here. Reading resp.Tokens on a nil
// resp previously panicked the whole HTTP handler when tracing was enabled
// (e.g. a transient tokenize failure during router probe-budget sizing).
func tokenizeTokenCount(resp *pb.TokenizationResponse) int {
	if resp == nil {
		return 0
	}
	return len(resp.Tokens)
}

func ModelTokenize(s string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (schema.TokenizeResponse, error) {

	var inferenceModel grpc.Backend
	var err error

	opts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err = loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return schema.TokenizeResponse{}, err
	}

	predictOptions := gRPCPredictOpts(modelConfig, loader.ModelPath)
	predictOptions.Prompt = s

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
	}

	// tokenize the string
	resp, err := inferenceModel.TokenizeString(appConfig.Context, predictOptions)

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}

		tokenCount := tokenizeTokenCount(resp)

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

	if resp == nil || resp.Tokens == nil {
		return schema.TokenizeResponse{Tokens: make([]int32, 0)}, nil
	}

	return schema.TokenizeResponse{
		Tokens: resp.Tokens,
	}, nil

}
