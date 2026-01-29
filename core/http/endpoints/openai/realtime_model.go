package openai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
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

func (m *transcriptOnlyModel) Predict(ctx context.Context, messages schema.Messages, images, videos, audios []string, tokenCallback func(string, backend.TokenUsage) bool, tools []types.ToolUnion, toolChoice *types.ToolChoiceUnion, logprobs *int, topLogprobs *int, logitBias map[string]float64) (func() (backend.LLMResponse, error), error) {
	return nil, fmt.Errorf("predict operation not supported in transcript-only mode")
}

func (m *transcriptOnlyModel) TTS(ctx context.Context, text, voice, language string) (string, *proto.Result, error) {
	return "", nil, fmt.Errorf("TTS not supported in transcript-only mode")
}

func (m *transcriptOnlyModel) PredictConfig() *config.ModelConfig {
	return nil
}

func (m *wrappedModel) VAD(ctx context.Context, request *schema.VADRequest) (*schema.VADResponse, error) {
	return backend.VAD(request, ctx, m.modelLoader, m.appConfig, *m.VADConfig)
}

func (m *wrappedModel) Transcribe(ctx context.Context, audio, language string, translate bool, diarize bool, prompt string) (*schema.TranscriptionResult, error) {
	return backend.ModelTranscription(audio, language, translate, diarize, prompt, m.modelLoader, *m.TranscriptionConfig, m.appConfig)
}

func (m *wrappedModel) Predict(ctx context.Context, messages schema.Messages, images, videos, audios []string, tokenCallback func(string, backend.TokenUsage) bool, tools []types.ToolUnion, toolChoice *types.ToolChoiceUnion, logprobs *int, topLogprobs *int, logitBias map[string]float64) (func() (backend.LLMResponse, error), error) {
	input := schema.OpenAIRequest{
		Messages: messages,
	}

	var predInput string
	var funcs []functions.Function
	if !m.LLMConfig.TemplateConfig.UseTokenizerTemplate {
		if len(tools) > 0 {
			for _, t := range tools {
				if t.Function != nil {
					var params map[string]any

					switch p := t.Function.Parameters.(type) {
					case map[string]any:
						params = p
					case string:
						if err := json.Unmarshal([]byte(p), &params); err != nil {
							xlog.Warn("Failed to parse parameters JSON string", "error", err, "function", t.Function.Name)
						}
					}

					funcs = append(funcs, functions.Function{
						Name:        t.Function.Name,
						Description: t.Function.Description,
						Parameters:  params,
					})
				}
			}
		}

		predInput = m.evaluator.TemplateMessages(input, input.Messages, m.LLMConfig, funcs, len(funcs) > 0)

		xlog.Debug("Prompt (after templating)", "prompt", predInput)
		if m.LLMConfig.Grammar != "" {
			xlog.Debug("Grammar", "grammar", m.LLMConfig.Grammar)
		}
	}

	// Generate grammar for function calling if tools are provided and grammar generation is enabled
	shouldUseFn := len(tools) > 0 && m.LLMConfig.ShouldUseFunctions()

	if !m.LLMConfig.FunctionsConfig.GrammarConfig.NoGrammar && shouldUseFn {
		// Allow the user to set custom actions via config file
		noActionName := "answer"
		noActionDescription := "use this action to answer without performing any action"

		if m.LLMConfig.FunctionsConfig.NoActionFunctionName != "" {
			noActionName = m.LLMConfig.FunctionsConfig.NoActionFunctionName
		}
		if m.LLMConfig.FunctionsConfig.NoActionDescriptionName != "" {
			noActionDescription = m.LLMConfig.FunctionsConfig.NoActionDescriptionName
		}

		noActionGrammar := functions.Function{
			Name:        noActionName,
			Description: noActionDescription,
			Parameters: map[string]interface{}{
				"properties": map[string]interface{}{
					"message": map[string]interface{}{
						"type":        "string",
						"description": "The message to reply the user with",
					},
				},
			},
		}

		if !m.LLMConfig.FunctionsConfig.DisableNoAction {
			funcs = append(funcs, noActionGrammar)
		}

		// Force picking one of the functions by the request
		if m.LLMConfig.FunctionToCall() != "" {
			funcs = functions.Functions(funcs).Select(m.LLMConfig.FunctionToCall())
		}

		// Generate grammar from function definitions
		jsStruct := functions.Functions(funcs).ToJSONStructure(m.LLMConfig.FunctionsConfig.FunctionNameKey, m.LLMConfig.FunctionsConfig.FunctionNameKey)
		g, err := jsStruct.Grammar(m.LLMConfig.FunctionsConfig.GrammarOptions()...)
		if err == nil {
			m.LLMConfig.Grammar = g
			xlog.Debug("Generated grammar for function calling", "grammar", g)
		} else {
			xlog.Error("Failed generating grammar", "error", err)
		}
	}

	var toolsJSON string
	if len(tools) > 0 {
		b, _ := json.Marshal(tools)
		toolsJSON = string(b)
	}

	var toolChoiceJSON string
	if toolChoice != nil {
		b, _ := json.Marshal(toolChoice)
		toolChoiceJSON = string(b)
	}

	return backend.ModelInference(ctx, predInput, messages, images, videos, audios, m.modelLoader, m.LLMConfig, m.confLoader, m.appConfig, tokenCallback, toolsJSON, toolChoiceJSON, logprobs, topLogprobs, logitBias, )
}

func (m *wrappedModel) TTS(ctx context.Context, text, voice, language string) (string, *proto.Result, error) {
	return backend.ModelTTS(text, voice, language, m.modelLoader, m.appConfig, *m.TTSConfig)
}

func (m *wrappedModel) PredictConfig() *config.ModelConfig {
	return m.LLMConfig
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
