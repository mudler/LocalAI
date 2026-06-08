package openai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
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
	VADConfig           *config.ModelConfig

	appConfig   *config.ApplicationConfig
	modelLoader *model.ModelLoader
	confLoader  *config.ModelConfigLoader
	evaluator   *templates.Evaluator

	// Routing — populated by newModel when the application wires routing
	// deps in. nil-safe: with classifierRegistry == nil the per-turn
	// routing block in Predict is skipped, preserving today's "one LLM
	// for the whole session" behaviour.
	routerDeps      *middleware.ClassifierDeps
	routerStore     router.DecisionStore
	routerSessionID string
	routerUserID    string
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
	return backend.ModelTranscription(ctx, audio, language, translate, diarize, prompt, m.modelLoader, *m.TranscriptionConfig, m.appConfig)
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
	return backend.ModelTranscription(ctx, audio, language, translate, diarize, prompt, m.modelLoader, *m.TranscriptionConfig, m.appConfig)
}

func (m *wrappedModel) Predict(ctx context.Context, messages schema.Messages, images, videos, audios []string, tokenCallback func(string, backend.TokenUsage) bool, tools []types.ToolUnion, toolChoice *types.ToolChoiceUnion, logprobs *int, topLogprobs *int, logitBias map[string]float64) (func() (backend.LLMResponse, error), error) {
	input := schema.OpenAIRequest{
		Messages: messages,
	}

	// Per-turn routing: when the session's LLMConfig is a router, swap
	// to the candidate the classifier picks for this turn's prompt.
	// LLMConfig itself is held by value (we never mutate it) — turnCfg
	// is the config we dispatch against.
	turnCfg := m.LLMConfig
	if m.LLMConfig.HasRouter() && m.routerDeps != nil {
		chosen, err := m.routeTurn(ctx, &input)
		if err != nil {
			xlog.Warn("realtime routing failed; using session default LLM",
				"router_model", m.LLMConfig.Name, "error", err)
		} else if chosen != nil {
			turnCfg = chosen
		}
	}

	// Surface the resolved reasoning effort to the Go-side template path too
	// (jinja models get it via backend metadata in gRPCPredictOpts; Go-templated
	// models like gpt-oss read it from the template's .ReasoningEffort).
	input.ReasoningEffort = turnCfg.ReasoningEffort

	var predInput string
	var funcs []functions.Function
	if !turnCfg.TemplateConfig.UseTokenizerTemplate {
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

			// Add noAction function before templating so it's included in the prompt
			// Allow the user to set custom actions via config file
			noActionName := "answer"
			noActionDescription := "use this action to answer without performing any action"

			if turnCfg.FunctionsConfig.NoActionFunctionName != "" {
				noActionName = turnCfg.FunctionsConfig.NoActionFunctionName
			}
			if turnCfg.FunctionsConfig.NoActionDescriptionName != "" {
				noActionDescription = turnCfg.FunctionsConfig.NoActionDescriptionName
			}

			noActionGrammar := functions.Function{
				Name:        noActionName,
				Description: noActionDescription,
				Parameters: map[string]any{
					"properties": map[string]any{
						"message": map[string]any{
							"type":        "string",
							"description": "The message to reply the user with",
						},
					},
				},
			}

			if !turnCfg.FunctionsConfig.DisableNoAction {
				funcs = append(funcs, noActionGrammar)
			}
		}

		predInput = m.evaluator.TemplateMessages(input, input.Messages, turnCfg, funcs, len(funcs) > 0)

		xlog.Debug("Prompt (after templating)", "prompt", predInput)
		if turnCfg.Grammar != "" {
			xlog.Debug("Grammar", "grammar", turnCfg.Grammar)
		}
	}

	// Handle tool_choice parameter similar to the chat endpoint
	if toolChoice != nil {
		if toolChoice.Mode != "" {
			// String values: "auto", "required", "none"
			switch toolChoice.Mode {
			case types.ToolChoiceModeRequired:
				turnCfg.SetFunctionCallString("required")
			case types.ToolChoiceModeNone:
				// Don't use tools
				turnCfg.SetFunctionCallString("none")
			case types.ToolChoiceModeAuto:
				// Default behavior - let model decide
			}
		} else if toolChoice.Function != nil {
			// Specific function specified
			turnCfg.SetFunctionCallNameString(toolChoice.Function.Name)
		}
	}

	// Generate grammar for function calling if tools are provided and grammar generation is enabled
	shouldUseFn := len(tools) > 0 && turnCfg.ShouldUseFunctions()

	if !turnCfg.FunctionsConfig.GrammarConfig.NoGrammar && shouldUseFn {
		// Force picking one of the functions by the request
		if turnCfg.FunctionToCall() != "" {
			funcs = functions.Functions(funcs).Select(turnCfg.FunctionToCall())
		}

		// Generate grammar from function definitions
		jsStruct := functions.Functions(funcs).ToJSONStructure(turnCfg.FunctionsConfig.FunctionNameKey, turnCfg.FunctionsConfig.FunctionNameKey)
		g, err := jsStruct.Grammar(turnCfg.FunctionsConfig.GrammarOptions()...)
		if err == nil {
			turnCfg.Grammar = g
			xlog.Debug("Generated grammar for function calling", "grammar", g)
		} else {
			xlog.Error("Failed generating grammar", "error", err)
		}
	}

	var toolsJSON string
	if len(tools) > 0 {
		// Convert tools to OpenAI Chat Completions format (nested)
		// as expected by most backends (including llama.cpp)
		var chatTools []functions.Tool
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
				case nil:
					params = map[string]any{}
				default:
					// Try to marshal/unmarshal to get map
					b, err := json.Marshal(p)
					if err == nil {
						_ = json.Unmarshal(b, &params)
					}
				}

				chatTools = append(chatTools, functions.Tool{
					Type: "function",
					Function: functions.Function{
						Name:        t.Function.Name,
						Description: t.Function.Description,
						Parameters:  params,
					},
				})
			}
		}
		b, _ := json.Marshal(chatTools)
		toolsJSON = string(b)
	}

	var toolChoiceJSON string
	if toolChoice != nil {
		b, _ := json.Marshal(toolChoice)
		toolChoiceJSON = string(b)
	}

	return backend.ModelInference(ctx, predInput, messages, images, videos, audios, m.modelLoader, turnCfg, m.confLoader, m.appConfig, tokenCallback, toolsJSON, toolChoiceJSON, logprobs, topLogprobs, logitBias, nil)
}

// routeTurn classifies this turn's prompt against the session's router
// LLM config and returns the candidate ModelConfig to dispatch against.
// Returns nil with no error when routing was attempted but the resolver
// signalled "no decision" — the caller falls back to the session
// default. Records the decision in the store using the realtime session
// id as the correlation id so the admin UI can group turn-by-turn
// decisions under one session row.
func (m *wrappedModel) routeTurn(ctx context.Context, req *schema.OpenAIRequest) (*config.ModelConfig, error) {
	if m.routerDeps == nil {
		return nil, nil
	}
	registry := m.routerDeps.Registry
	if registry == nil {
		registry = router.NewRegistry()
	}
	classifier, classifierErr := middleware.GetOrBuildClassifier(registry, m.LLMConfig, *m.routerDeps)
	if classifierErr != nil {
		xlog.Warn("realtime router: classifier unavailable — using fallback",
			"router_model", m.LLMConfig.Name, "error", classifierErr)
		classifier = nil
	}
	loader := func(name string) (*config.ModelConfig, error) {
		return m.confLoader.LoadModelConfigFileByNameDefaultOptions(name, m.appConfig)
	}
	probe := middleware.OpenAIProbeFromRequest(req)

	result, err := router.Resolve(ctx, m.LLMConfig, classifier, loader, probe)
	if err != nil {
		return nil, err
	}

	if m.routerStore != nil {
		_ = m.routerStore.Record(context.Background(), result.ToDecisionRecord(newRealtimeDecisionID(), m.routerSessionID, m.routerUserID, router.SourceRealtime))
	}
	return result.ChosenConfig, nil
}

func newRealtimeDecisionID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "rd_" + hex.EncodeToString(b[:])
}

func (m *wrappedModel) TTS(ctx context.Context, text, voice, language string) (string, *proto.Result, error) {
	return backend.ModelTTS(ctx, text, voice, language, "", nil, m.modelLoader, m.appConfig, *m.TTSConfig)
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
		VADConfig:           cfgVAD,

		confLoader:  cl,
		modelLoader: ml,
		appConfig:   appConfig,
	}, cfgSST, nil
}

// RealtimeRoutingContext is the bundle of routing dependencies the
// realtime pipeline needs to consult router.Resolve per turn. nil-safe:
// passing nil skips routing entirely and preserves the historical "one
// LLM for the whole session" behaviour.
type RealtimeRoutingContext struct {
	Deps      *middleware.ClassifierDeps
	Store     router.DecisionStore
	SessionID string
	UserID    string
}

// buildRealtimeRoutingContext assembles the routing dependencies the
// realtime pipeline needs from the application container. Returns nil
// when no Application is wired (tests, stripped builds) — that path
// leaves wrappedModel.Predict on the historical "no routing" path
// instead of failing at session start.
func buildRealtimeRoutingContext(a *application.Application, sessionID string) *RealtimeRoutingContext {
	if a == nil {
		return nil
	}
	deps := &middleware.ClassifierDeps{
		Scorer:       a.Scorer,
		TokenCounter: a.TokenCounter,
		Embedder:     a.Embedder,
		VectorStore:  a.VectorStore,
		Reranker:     a.Reranker,
		ModelLookup:  a.ModelConfigLookup(),
		Registry:     a.RouterClassifierRegistry(),
		Evaluator:    a.TemplatesEvaluator(),
	}
	userID := ""
	if u := a.FallbackUser(); u != nil {
		userID = u.ID
	}
	return &RealtimeRoutingContext{
		Deps:      deps,
		Store:     a.RouterDecisions(),
		SessionID: sessionID,
		UserID:    userID,
	}
}

// returns and loads either a wrapped model or a model that support audio-to-audio
func newModel(pipeline *config.Pipeline, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, evaluator *templates.Evaluator, routing *RealtimeRoutingContext) (Model, error) {
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

	// Let the pipeline set the LLM's reasoning effort (cfgLLM is a per-session copy).
	applyPipelineReasoning(cfgLLM, *pipeline)

	cfgTTS, err := cl.LoadModelConfigFileByName(pipeline.TTS, ml.ModelPath)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgTTS.Validate(); !valid {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	wm := &wrappedModel{
		TTSConfig:           cfgTTS,
		TranscriptionConfig: cfgSST,
		LLMConfig:           cfgLLM,
		VADConfig:           cfgVAD,

		confLoader:  cl,
		modelLoader: ml,
		appConfig:   appConfig,
		evaluator:   evaluator,
	}
	if routing != nil {
		wm.routerDeps = routing.Deps
		wm.routerStore = routing.Store
		wm.routerSessionID = routing.SessionID
		wm.routerUserID = routing.UserID
	}
	return wm, nil
}
