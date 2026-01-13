package openai

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

var (
	_ Model = new(wrappedModel)
	_ Model = new(transcriptOnlyModel)
)

// wrappedModel represent a model which does not support Any-to-Any operations
// This means that we will fake an Any-to-Any model by overriding some of the gRPC client methods
// which are for Any-To-Any models, but instead we will call a pipeline (for e.g STT->LLM->TTS)
type wrappedModel struct {
	TTSConfig           *config.ModelConfig
	TranscriptionConfig *config.ModelConfig
	LLMConfig           *config.ModelConfig
	VADConfig *config.ModelConfig

	appConfig   *config.ApplicationConfig
	modelLoader *model.ModelLoader
	confLoader  *config.ModelConfigLoader
	evaluator   *templates.Evaluator
}

// anyToAnyModel represent a model which supports Any-to-Any operations
// We have to wrap this out as well because we want to load two models one for VAD and one for the actual model.
// In the future there could be models that accept continous audio input only so this design will be useful for that
type anyToAnyModel struct {
	LLMConfig *config.ModelConfig
	VADConfig *config.ModelConfig

	appConfig   *config.ApplicationConfig
	modelLoader *model.ModelLoader
	confLoader  *config.ModelConfigLoader
}

type transcriptOnlyModel struct {
	TranscriptionConfig *config.ModelConfig
	VADConfig           *config.ModelConfig

	appConfig   *config.ApplicationConfig
	modelLoader *model.ModelLoader
	confLoader  *config.ModelConfigLoader
}

func (m *transcriptOnlyModel) VAD(ctx context.Context, request *schema.VADRequest) (*schema.VADResponse, error) {
	return backend.VAD(request, ctx, m.modelLoader, m.appConfig, *m.VADConfig)
}

func (m *transcriptOnlyModel) Transcribe(ctx context.Context, audio, language string, translate bool, diarize bool, prompt string) (*schema.TranscriptionResult, error) {
	return backend.ModelTranscription(audio, language, translate, diarize, prompt, m.modelLoader, *m.TranscriptionConfig, m.appConfig)
}

func (m *transcriptOnlyModel) Predict(ctx context.Context, messages schema.Messages, images, videos, audios []string, tokenCallback func(string, backend.TokenUsage) bool, tools string, toolChoice string, logprobs *int, topLogprobs *int, logitBias map[string]float64) (func() (backend.LLMResponse, error), error) {
	return nil, fmt.Errorf("predict operation not supported in transcript-only mode")
}

func (m *transcriptOnlyModel) TTS(ctx context.Context, text, voice, language string) (string, *proto.Result, error) {
	return "", nil, fmt.Errorf("TTS not supported in transcript-only mode")
}

func (m *wrappedModel) VAD(ctx context.Context, request *schema.VADRequest) (*schema.VADResponse, error) {
	return backend.VAD(request, ctx, m.modelLoader, m.appConfig, *m.VADConfig)
}

func (m *wrappedModel) Transcribe(ctx context.Context, audio, language string, translate bool, diarize bool, prompt string) (*schema.TranscriptionResult, error) {
	return backend.ModelTranscription(audio, language, translate, diarize, prompt, m.modelLoader, *m.TranscriptionConfig, m.appConfig)
}

func (m *wrappedModel) Predict(ctx context.Context, messages schema.Messages, images, videos, audios []string, tokenCallback func(string, backend.TokenUsage) bool, tools string, toolChoice string, logprobs *int, topLogprobs *int, logitBias map[string]float64) (func() (backend.LLMResponse, error), error) {
	input := schema.OpenAIRequest{
		Messages: messages,
	}

	var predInput string
	if !m.LLMConfig.TemplateConfig.UseTokenizerTemplate {
		predInput = m.evaluator.TemplateMessages(input, input.Messages, m.LLMConfig, []functions.Function{}, false)

		xlog.Debug("Prompt (after templating)", "prompt", predInput)
		if m.LLMConfig.Grammar != "" {
			xlog.Debug("Grammar", "grammar", m.LLMConfig.Grammar)
		}
	}

	return backend.ModelInference(ctx, predInput, messages, images, videos, audios, m.modelLoader, m.LLMConfig, m.confLoader, m.appConfig, tokenCallback, tools, toolChoice, logprobs, topLogprobs, logitBias, )
}

func (m *wrappedModel) TTS(ctx context.Context, text, voice, language string) (string, *proto.Result, error) {
	return backend.ModelTTS(text, voice, language, m.modelLoader, m.appConfig, *m.TTSConfig)
}

func newTranscriptionOnlyModel(pipeline *config.Pipeline, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) (Model, *config.ModelConfig, error) {
	cfgVAD, err := cl.LoadModelConfigFileByName(pipeline.VAD, ml.ModelPath)
	if err != nil {

		return nil, nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgVAD.Validate(); !valid {
		return nil, nil, fmt.Errorf("failed to validate config: %w", err)
	}

	cfgSST, err := cl.LoadModelConfigFileByName(pipeline.Transcription, ml.ModelPath)
	if err != nil {

		return nil, nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgSST.Validate(); !valid {
		return nil, nil, fmt.Errorf("failed to validate config: %w", err)
	}

	return &transcriptOnlyModel{
		TranscriptionConfig: cfgSST,
		VADConfig: cfgVAD,

		confLoader: cl,
		modelLoader: ml,
		appConfig: appConfig,
	}, cfgSST, nil
}

// returns and loads either a wrapped model or a model that support audio-to-audio
func newModel(pipeline *config.Pipeline, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, evaluator *templates.Evaluator) (Model, error) {
	xlog.Debug("Creating new model pipeline model", "pipeline", pipeline)

	cfgVAD, err := cl.LoadModelConfigFileByName(pipeline.VAD, ml.ModelPath)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgVAD.Validate(); !valid {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	// TODO: Do we always need a transcription model? It can be disabled. Note that any-to-any instruction following models don't transcribe as such, so if transcription is required it is a separate process
	cfgSST, err := cl.LoadModelConfigFileByName(pipeline.Transcription, ml.ModelPath)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgSST.Validate(); !valid {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	// TODO: Decide when we have a real any-to-any model
	// if false {
	//
	// 	cfgAnyToAny, err := cl.LoadModelConfigFileByName(pipeline.LLM, ml.ModelPath)
	// 	if err != nil {
	//
	// 		return nil, fmt.Errorf("failed to load backend config: %w", err)
	// 	}
	//
	// 	if valid, _ := cfgAnyToAny.Validate(); !valid {
	// 		return nil, fmt.Errorf("failed to validate config: %w", err)
	// 	}
	//
	// 	return &anyToAnyModel{
	// 		LLMConfig: cfgAnyToAny,
	// 		VADConfig: cfgVAD,
	// 	}, nil
	// }

	xlog.Debug("Loading a wrapped model")

	// Otherwise we want to return a wrapped model, which is a "virtual" model that re-uses other models to perform operations
	cfgLLM, err := cl.LoadModelConfigFileByName(pipeline.LLM, ml.ModelPath)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgLLM.Validate(); !valid {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	cfgTTS, err := cl.LoadModelConfigFileByName(pipeline.TTS, ml.ModelPath)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgTTS.Validate(); !valid {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	return &wrappedModel{
		TTSConfig:           cfgTTS,
		TranscriptionConfig: cfgSST,
		LLMConfig:           cfgLLM,
		VADConfig: cfgVAD,

		confLoader: cl,
		modelLoader: ml,
		appConfig: appConfig,
		evaluator: evaluator,
	}, nil
}
