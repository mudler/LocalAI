package openai

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	grpcClient "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

var (
	_ Model = new(wrappedModel)
	_ Model = new(anyToAnyModel)
)

// wrappedModel represent a model which does not support Any-to-Any operations
// This means that we will fake an Any-to-Any model by overriding some of the gRPC client methods
// which are for Any-To-Any models, but instead we will call a pipeline (for e.g STT->LLM->TTS)
type wrappedModel struct {
	TTSConfig           *config.BackendConfig
	TranscriptionConfig *config.BackendConfig
	LLMConfig           *config.BackendConfig
	TTSClient           grpcClient.Backend
	TranscriptionClient grpcClient.Backend
	LLMClient           grpcClient.Backend

	VADConfig *config.BackendConfig
	VADClient grpcClient.Backend
}

// anyToAnyModel represent a model which supports Any-to-Any operations
// We have to wrap this out as well because we want to load two models one for VAD and one for the actual model.
// In the future there could be models that accept continous audio input only so this design will be useful for that
type anyToAnyModel struct {
	LLMConfig *config.BackendConfig
	LLMClient grpcClient.Backend

	VADConfig *config.BackendConfig
	VADClient grpcClient.Backend
}

func (m *wrappedModel) VAD(ctx context.Context, in *proto.VADRequest, opts ...grpc.CallOption) (*proto.VADResponse, error) {
	return m.VADClient.VAD(ctx, in)
}

func (m *anyToAnyModel) VAD(ctx context.Context, in *proto.VADRequest, opts ...grpc.CallOption) (*proto.VADResponse, error) {
	return m.VADClient.VAD(ctx, in)
}

func (m *wrappedModel) Predict(ctx context.Context, in *proto.PredictOptions, opts ...grpc.CallOption) (*proto.Reply, error) {
	// TODO: Convert with pipeline (audio to text, text to llm, result to tts, and return it)
	// sound.BufferAsWAV(audioData, "audio.wav")

	return m.LLMClient.Predict(ctx, in)
}

func (m *wrappedModel) PredictStream(ctx context.Context, in *proto.PredictOptions, f func(s []byte), opts ...grpc.CallOption) error {
	// TODO: Convert with pipeline (audio to text, text to llm, result to tts, and return it)

	return m.LLMClient.PredictStream(ctx, in, f)
}

func (m *anyToAnyModel) Predict(ctx context.Context, in *proto.PredictOptions, opts ...grpc.CallOption) (*proto.Reply, error) {
	return m.LLMClient.Predict(ctx, in)
}

func (m *anyToAnyModel) PredictStream(ctx context.Context, in *proto.PredictOptions, f func(s []byte), opts ...grpc.CallOption) error {
	return m.LLMClient.PredictStream(ctx, in, f)
}

// returns and loads either a wrapped model or a model that support audio-to-audio
func newModel(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, modelName string) (Model, error) {

	cfg, err := cl.LoadBackendConfigFileByName(modelName, ml.ModelPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if !cfg.Validate() {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	// Prepare VAD model
	cfgVAD, err := cl.LoadBackendConfigFileByName(cfg.Pipeline.VAD, ml.ModelPath)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if !cfgVAD.Validate() {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	opts := backend.ModelOptions(*cfgVAD, appConfig)
	VADClient, err := ml.Load(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load tts model: %w", err)
	}

	// If we don't have Wrapped model definitions, just return a standard model
	if cfg.Pipeline.IsNotConfigured() {

		// Otherwise we want to return a wrapped model, which is a "virtual" model that re-uses other models to perform operations
		cfgAnyToAny, err := cl.LoadBackendConfigFileByName(cfg.Model, ml.ModelPath)
		if err != nil {

			return nil, fmt.Errorf("failed to load backend config: %w", err)
		}

		if !cfgAnyToAny.Validate() {
			return nil, fmt.Errorf("failed to validate config: %w", err)
		}

		opts := backend.ModelOptions(*cfgAnyToAny, appConfig)
		anyToAnyClient, err := ml.Load(opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to load tts model: %w", err)
		}

		return &anyToAnyModel{
			LLMConfig: cfgAnyToAny,
			LLMClient: anyToAnyClient,
			VADConfig: cfgVAD,
			VADClient: VADClient,
		}, nil
	}

	log.Debug().Msg("Loading a wrapped model")

	// Otherwise we want to return a wrapped model, which is a "virtual" model that re-uses other models to perform operations
	cfgLLM, err := cl.LoadBackendConfigFileByName(cfg.Pipeline.LLM, ml.ModelPath)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if !cfg.Validate() {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	cfgTTS, err := cl.LoadBackendConfigFileByName(cfg.Pipeline.TTS, ml.ModelPath)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if !cfg.Validate() {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	cfgSST, err := cl.LoadBackendConfigFileByName(cfg.Pipeline.Transcription, ml.ModelPath)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if !cfg.Validate() {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	opts = backend.ModelOptions(*cfgTTS, appConfig)
	ttsClient, err := ml.Load(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load tts model: %w", err)
	}

	opts = backend.ModelOptions(*cfgSST, appConfig)
	transcriptionClient, err := ml.Load(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load SST model: %w", err)
	}

	opts = backend.ModelOptions(*cfgLLM, appConfig)
	llmClient, err := ml.Load(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load LLM model: %w", err)
	}

	return &wrappedModel{
		TTSConfig:           cfgTTS,
		TranscriptionConfig: cfgSST,
		LLMConfig:           cfgLLM,
		TTSClient:           ttsClient,
		TranscriptionClient: transcriptionClient,
		LLMClient:           llmClient,

		VADConfig: cfgVAD,
		VADClient: VADClient,
	}, nil
}
