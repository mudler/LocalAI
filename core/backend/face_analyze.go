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

func FaceAnalyze(
	ctx context.Context,
	img string,
	actions []string,
	antiSpoofing bool,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (*proto.FaceAnalyzeResponse, error) {
	opts := ModelOptions(modelConfig, appConfig)
	faceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if faceModel == nil {
		return nil, fmt.Errorf("could not load face recognition model")
	}

	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return nil, err
	}
	defer release()
	var startTime time.Time
	var traceID string
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
		traceID = trace.BeginBackendTrace(trace.BackendTrace{Timestamp: startTime, Type: trace.BackendTraceFaceAnalyze, ModelName: modelConfig.Name, Backend: modelConfig.Backend, Summary: "face analysis"})
	}
	defer trace.CancelBackendTrace(traceID)

	res, err := faceModel.FaceAnalyze(ctx, &proto.FaceAnalyzeRequest{
		ModelIdentity: modelConfig.Model,
		Img:           img,
		Actions:       actions,
		AntiSpoofing:  antiSpoofing,
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
			Type:      trace.BackendTraceFaceAnalyze,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Error:     errStr,
		})
	}

	return res, err
}
