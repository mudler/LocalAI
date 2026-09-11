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

func VoiceAnalyze(
	ctx context.Context,
	audio string,
	actions []string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (*proto.VoiceAnalyzeResponse, error) {
	opts := ModelOptions(modelConfig, appConfig)
	voiceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if voiceModel == nil {
		return nil, fmt.Errorf("could not load voice recognition model")
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
		traceID = trace.BeginBackendTrace(trace.BackendTrace{Timestamp: startTime, Type: trace.BackendTraceVoiceAnalyze, ModelName: modelConfig.Name, Backend: modelConfig.Backend, Summary: "voice analysis"})
	}
	defer trace.CancelBackendTrace(traceID)

	res, err := voiceModel.VoiceAnalyze(ctx, &proto.VoiceAnalyzeRequest{
		ModelIdentity: modelConfig.Model,
		Audio:         audio,
		Actions:       actions,
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
			Type:      trace.BackendTraceVoiceAnalyze,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Error:     errStr,
		})
	}

	return res, err
}
