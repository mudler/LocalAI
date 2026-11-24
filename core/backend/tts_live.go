package backend

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	grpcbackend "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
)

// ModelTTSLive streams one persistent utterance. Closing text commits input EOS;
// this call returns only after the final audio or a cancellation/error.
func ModelTTSLive(ctx context.Context, text <-chan string, voice, language string, params map[string]string, loader *model.ModelLoader, appConfig *config.ApplicationConfig, cfg config.ModelConfig, onAudio func([]byte) error) error {
	tts, err := loader.Load(ModelOptions(cfg, appConfig, model.WithContext(ctx))...)
	if err != nil {
		return err
	}
	live, ok := tts.(grpcbackend.TTSLiveBackend)
	if !ok {
		return fmt.Errorf("backend %q does not support incremental text TTS", cfg.Backend)
	}
	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return err
	}
	defer release()
	req := newTTSRequest("", cfg.Model, voice, "", language, "", params, cfg.Model)
	return live.TTSLive(ctx, req, text, onAudio)
}
