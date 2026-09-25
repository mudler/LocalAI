package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

// ModelSystemOne loads the backend for modelConfig and returns a closure
// that runs the kev/laya decision pipeline via the Score gRPC RPC with
// question_type set to "systemone". requestJSON is the raw /v1/systemone
// POST body; the closure returns the full response JSON from the backend.
func ModelSystemOne(requestJSON string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func(ctx context.Context) (string, error), error) {
	modelOpts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err := loader.Load(modelOpts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	b, ok := inferenceModel.(grpc.Backend)
	if !ok {
		return nil, fmt.Errorf("systemone not supported by backend %q", modelConfig.Backend)
	}
	return func(ctx context.Context) (string, error) {
		release, err := AcquireGlobalBackendSlot()
		if err != nil {
			return "", err
		}
		defer release()
		var startTime time.Time
		var traceID string
		if appConfig.EnableTracing {
			trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
			startTime = time.Now()
			traceID = trace.BeginBackendTrace(trace.BackendTrace{
				Timestamp: startTime,
				Type:      trace.BackendTraceScore,
				ModelName: modelConfig.Name,
				Backend:   modelConfig.Backend,
				Summary:   trace.TruncateString(requestJSON, 200),
			})
		}
		defer trace.CancelBackendTrace(traceID)
		resp, err := b.Score(ctx, &pb.ScoreRequest{
			Prompt:        requestJSON,
			QuestionType:  "systemone",
			ModelIdentity: modelConfig.Model,
		})
		if appConfig.EnableTracing {
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			trace.RecordBackendTrace(trace.BackendTrace{
				ID:        traceID,
				Timestamp: startTime,
				Duration:  time.Since(startTime),
				Type:      trace.BackendTraceScore,
				ModelName: modelConfig.Name,
				Backend:   modelConfig.Backend,
				Summary:   trace.TruncateString(requestJSON, 200),
				Error:     errStr,
			})
		}
		if err != nil {
			return "", err
		}
		return resp.GetResponseJson(), nil
	}, nil
}
