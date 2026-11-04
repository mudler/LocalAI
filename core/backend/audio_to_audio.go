package backend

import (
	"context"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
)

// AudioToAudioStream is the duplex stream to a speech-to-speech backend. It
// holds the global backend slot until Close, so a long realtime session counts
// as one in-flight backend call, like any other streaming RPC.
type AudioToAudioStream struct {
	grpc.AudioToAudioStreamClient
	release     func()
	releaseOnce sync.Once
}

// Close ends the send side and releases the backend slot. It is safe to call
// more than once. The caller should also cancel the context given to
// ModelAudioToAudioStream so a blocked Recv returns.
func (s *AudioToAudioStream) Close() error {
	err := s.CloseSend()
	s.releaseOnce.Do(s.release)
	return err
}

// ModelAudioToAudioStream loads the model with its standard options (so
// options such as parallel and the multimodal projector apply) and opens the
// bidirectional AudioToAudioStream RPC. The caller sends the config message
// first and owns the stream until Close.
func ModelAudioToAudioStream(
	ctx context.Context,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (*AudioToAudioStream, error) {
	mopts := ModelOptions(modelConfig, appConfig)
	s2sModel, err := loader.Load(mopts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if s2sModel == nil {
		return nil, fmt.Errorf("could not load speech-to-speech model %q", modelConfig.Model)
	}
	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return nil, err
	}
	stream, err := s2sModel.AudioToAudioStream(ctx)
	if err != nil {
		release()
		return nil, err
	}
	return &AudioToAudioStream{AudioToAudioStreamClient: stream, release: release}, nil
}
