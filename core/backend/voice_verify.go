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

func VoiceVerify(
	audio1, audio2 string,
	threshold float32,
	antiSpoofing bool,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (*proto.VoiceVerifyResponse, error) {
	opts := ModelOptions(modelConfig, appConfig)
	voiceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if voiceModel == nil {
		return nil, fmt.Errorf("could not load voice recognition model")
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)
		startTime = time.Now()
	}

	res, err := voiceModel.VoiceVerify(context.Background(), &proto.VoiceVerifyRequest{
		Audio1:       audio1,
		Audio2:       audio2,
		Threshold:    threshold,
		AntiSpoofing: antiSpoofing,
	})

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceVoiceVerify,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Error:     errStr,
		})
	}

	return res, err
}
