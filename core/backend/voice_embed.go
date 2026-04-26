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

// VoiceEmbed returns a speaker embedding (typically 192-d for ECAPA-TDNN)
// for the audio file at audioPath. Unlike ModelEmbedding (which is
// OpenAI-compatible and text-only), this call takes an audio path and
// returns the backend's speaker-encoder output.
func VoiceEmbed(
	audioPath string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (*proto.VoiceEmbedResponse, error) {
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

	res, err := voiceModel.VoiceEmbed(context.Background(), &proto.VoiceEmbedRequest{
		Audio: audioPath,
	})

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceVoiceEmbed,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Error:     errStr,
		})
	}

	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Embedding) == 0 {
		return nil, fmt.Errorf("voice embedding returned empty vector (no speech detected?)")
	}
	return res, nil
}
