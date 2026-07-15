package openai

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

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
	TTSConfig            *config.ModelConfig
	TranscriptionConfig  *config.ModelConfig
	LLMConfig            *config.ModelConfig
	VADConfig            *config.ModelConfig
	SoundDetectionConfig *config.ModelConfig
	// ScoreConfig is the classifier-mode scoring model
	// (pipeline.classifier.model). nil falls back to LLMConfig — with
	// slot-based Score the same process serves scoring and generation
	// and shares its prompt cache between them.
	ScoreConfig *config.ModelConfig

	appConfig   *config.ApplicationConfig
	modelLoader *model.ModelLoader
	confLoader  *config.ModelConfigLoader
	evaluator   *templates.Evaluator

	// Classifier-mode memo: constructing a ScoreClassifier parses the
	// scoring model's chat template, so reuse it while the option set is
	// unchanged. Guarded by a mutex only because session.update can swap
	// options while a response is in flight.
	classifierMu   sync.Mutex
	classifier     *router.ScoreClassifier
	classifierKey  string
	classifierWarn sync.Once

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
	TranscriptionConfig  *config.ModelConfig
	VADConfig            *config.ModelConfig
	SoundDetectionConfig *config.ModelConfig

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

func (m *transcriptOnlyModel) SoundDetection(ctx context.Context, audio string, topK int, threshold float32) (*schema.SoundClassificationResult, error) {
	return modelSoundDetection(ctx, m.modelLoader, m.appConfig, m.SoundDetectionConfig, audio, topK, threshold)
}

func (m *transcriptOnlyModel) Predict(ctx context.Context, messages schema.Messages, images, videos, audios []string, tokenCallback func(string, backend.TokenUsage) bool, tools []types.ToolUnion, toolChoice *types.ToolChoiceUnion, logprobs *int, topLogprobs *int, logitBias map[string]float64) (func() (backend.LLMResponse, error), error) {
	return nil, fmt.Errorf("predict operation not supported in transcript-only mode")
}

func (m *transcriptOnlyModel) ClassifyTurn(ctx context.Context, messages schema.Messages, options []types.ClassifierOption, normalization string) ([]router.LabelScore, error) {
	return nil, fmt.Errorf("classifier mode not supported in transcript-only mode")
}

func (m *transcriptOnlyModel) TTS(ctx context.Context, text, voice, language string) (string, *proto.Result, error) {
	return "", nil, fmt.Errorf("TTS not supported in transcript-only mode")
}

func (m *transcriptOnlyModel) TTSStream(ctx context.Context, text, voice, language string, onAudio func(pcm []byte, sampleRate int) error) error {
	return fmt.Errorf("TTS not supported in transcript-only mode")
}

func (m *transcriptOnlyModel) TranscribeStream(ctx context.Context, audio, language string, translate, diarize bool, prompt string, onDelta func(text string)) (*schema.TranscriptionResult, error) {
	return transcribeStream(ctx, m.modelLoader, *m.TranscriptionConfig, m.appConfig, audio, language, translate, diarize, prompt, onDelta)
}

func (m *transcriptOnlyModel) TranscribeLive(ctx context.Context, language string, onEvent func(backend.LiveTranscriptionEvent)) (backend.LiveTranscriptionSession, error) {
	return backend.ModelTranscriptionLive(ctx, language, m.modelLoader, *m.TranscriptionConfig, m.appConfig, onEvent)
}

func (m *transcriptOnlyModel) PredictConfig() *config.ModelConfig {
	return nil
}

func (m *transcriptOnlyModel) Warmup(ctx context.Context) error {
	_, err := backend.PreloadStages(ctx, m.modelLoader, m.appConfig, []backend.PreloadStage{
		{Role: "vad", Cfg: m.VADConfig},
		{Role: "transcription", Cfg: m.TranscriptionConfig},
		{Role: "sound_detection", Cfg: m.SoundDetectionConfig},
	})
	return err
}

func (m *wrappedModel) VAD(ctx context.Context, request *schema.VADRequest) (*schema.VADResponse, error) {
	return backend.VAD(request, ctx, m.modelLoader, m.appConfig, *m.VADConfig)
}

func (m *wrappedModel) Transcribe(ctx context.Context, audio, language string, translate bool, diarize bool, prompt string) (*schema.TranscriptionResult, error) {
	return backend.ModelTranscription(ctx, audio, language, translate, diarize, prompt, m.modelLoader, *m.TranscriptionConfig, m.appConfig)
}

func (m *wrappedModel) SoundDetection(ctx context.Context, audio string, topK int, threshold float32) (*schema.SoundClassificationResult, error) {
	return modelSoundDetection(ctx, m.modelLoader, m.appConfig, m.SoundDetectionConfig, audio, topK, threshold)
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

func (m *wrappedModel) TTSStream(ctx context.Context, text, voice, language string, onAudio func(pcm []byte, sampleRate int) error) error {
	return ttsStream(ctx, m.modelLoader, m.appConfig, *m.TTSConfig, text, voice, language, onAudio)
}

func (m *wrappedModel) TranscribeStream(ctx context.Context, audio, language string, translate, diarize bool, prompt string, onDelta func(text string)) (*schema.TranscriptionResult, error) {
	return transcribeStream(ctx, m.modelLoader, *m.TranscriptionConfig, m.appConfig, audio, language, translate, diarize, prompt, onDelta)
}

func (m *wrappedModel) TranscribeLive(ctx context.Context, language string, onEvent func(backend.LiveTranscriptionEvent)) (backend.LiveTranscriptionSession, error) {
	return backend.ModelTranscriptionLive(ctx, language, m.modelLoader, *m.TranscriptionConfig, m.appConfig, onEvent)
}

func (m *wrappedModel) PredictConfig() *config.ModelConfig {
	return m.LLMConfig
}

// scoreConfig resolves the classifier-mode scoring model: the explicit
// pipeline.classifier.model when set, else the pipeline LLM.
func (m *wrappedModel) scoreConfig() *config.ModelConfig {
	if m.ScoreConfig != nil {
		return m.ScoreConfig
	}
	return m.LLMConfig
}

// classifierFor returns a ScoreClassifier for the given option set,
// reusing the previous one while options and normalization are unchanged
// (construction parses the scoring model's chat template).
func (m *wrappedModel) classifierFor(options []types.ClassifierOption, normalization string) (*router.ScoreClassifier, error) {
	scoreCfg := m.scoreConfig()
	if scoreCfg == nil || !scoreCfg.HasUsecases(config.FLAG_SCORE) {
		return nil, fmt.Errorf("classifier: scoring model must include score in known_usecases")
	}
	switch normalization {
	case "", router.ScoreNormalizationRaw, router.ScoreNormalizationMean:
	default:
		// NewScoreClassifier panics on unknown modes; session.update
		// validation should have rejected this — fail soft anyway.
		return nil, fmt.Errorf("classifier: unknown normalization %q", normalization)
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("classifier: no options to score")
	}

	var key strings.Builder
	key.WriteString(normalization)
	for _, o := range options {
		key.WriteString("\x1f")
		key.WriteString(o.ID)
		key.WriteString("\x1e")
		key.WriteString(o.Description)
	}

	m.classifierMu.Lock()
	defer m.classifierMu.Unlock()
	if m.classifier != nil && m.classifierKey == key.String() {
		return m.classifier, nil
	}

	cfg := m.scoreConfig()
	policies := make([]router.ScorePolicy, 0, len(options))
	for _, o := range options {
		if o.ID == "" || o.Description == "" {
			// NewScoreClassifier panics on these; validation upstream
			// should have caught them.
			return nil, fmt.Errorf("classifier: option with empty id or description")
		}
		policies = append(policies, router.ScorePolicy{Label: o.ID, Description: o.Description})
	}

	opts := router.ScoreClassifierOptions{
		// The memo cache stores only label sets — a hit would return an
		// empty distribution and blind the localai.classifier.result
		// event, so keep it off.
		CacheCap:      0,
		Normalization: normalization,
	}
	if m.evaluator != nil {
		if renderer := middleware.NewTemplateRenderer(m.evaluator, cfg); renderer != nil {
			opts.PromptRenderer = renderer
		} else {
			m.classifierWarn.Do(func() {
				xlog.Warn("realtime classifier: scoring model has no Go chat template; falling back to a generic ChatML envelope, which may be off-distribution",
					"model", cfg.Name)
			})
		}
	}
	if st := middleware.PickAssistantTurnEnd(cfg.StopWords, cfg.TemplateConfig.ChatMessage); st != "" {
		opts.StopToken = st
	}

	scorer := backend.NewScorer(m.modelLoader, *cfg, m.appConfig)
	m.classifier = router.NewScoreClassifier(policies, scorer, opts)
	m.classifierKey = key.String()
	return m.classifier, nil
}

func (m *wrappedModel) ClassifyTurn(ctx context.Context, messages schema.Messages, options []types.ClassifierOption, normalization string) ([]router.LabelScore, error) {
	classifier, err := m.classifierFor(options, normalization)
	if err != nil {
		return nil, err
	}
	decision, err := classifier.Classify(ctx, classifierProbe(messages))
	if err != nil {
		return nil, err
	}
	// LabelScores is in policy-declaration order, which mirrors option
	// order by construction.
	if len(decision.LabelScores) != len(options) {
		return nil, fmt.Errorf("classifier: got %d scores for %d options", len(decision.LabelScores), len(options))
	}
	return decision.LabelScores, nil
}

func (m *wrappedModel) Warmup(ctx context.Context) error {
	stages := []backend.PreloadStage{
		{Role: "vad", Cfg: m.VADConfig},
		{Role: "transcription", Cfg: m.TranscriptionConfig},
		{Role: "llm", Cfg: m.LLMConfig},
		{Role: "tts", Cfg: m.TTSConfig},
		{Role: "sound_detection", Cfg: m.SoundDetectionConfig},
	}
	// The scoring model is a separate stage only when it isn't the LLM.
	if m.ScoreConfig != nil && m.ScoreConfig != m.LLMConfig {
		stages = append(stages, backend.PreloadStage{Role: "classifier", Cfg: m.ScoreConfig})
	}
	_, err := backend.PreloadStages(ctx, m.modelLoader, m.appConfig, stages)
	return err
}

// wavStreamHeaderBytes is the size of the WAV header that backend.ModelTTSStream
// emits as its first audio callback; the sample rate lives at byte offset 24.
const wavStreamHeaderBytes = 44

// ttsStream adapts backend.ModelTTSStream (which emits a WAV stream: a 44-byte
// header carrying the sample rate, then raw PCM) to the realtime onAudio
// callback, which wants raw PCM plus the sample rate. The header is buffered
// until complete, the sample rate is read from it, and subsequent bytes are
// forwarded as PCM.
func ttsStream(ctx context.Context, ml *model.ModelLoader, appConfig *config.ApplicationConfig, ttsConfig config.ModelConfig, text, voice, language string, onAudio func(pcm []byte, sampleRate int) error) error {
	var header []byte
	headerDone := false
	sampleRate := 0
	return backend.ModelTTSStream(ctx, text, voice, language, "", nil, ml, appConfig, ttsConfig, func(b []byte) error {
		if headerDone {
			if len(b) == 0 {
				return nil
			}
			return onAudio(b, sampleRate)
		}
		header = append(header, b...)
		if len(header) < wavStreamHeaderBytes {
			return nil
		}
		sampleRate = int(binary.LittleEndian.Uint32(header[24:28]))
		headerDone = true
		if len(header) > wavStreamHeaderBytes {
			return onAudio(header[wavStreamHeaderBytes:], sampleRate)
		}
		return nil
	})
}

// transcribeStream adapts backend.ModelTranscriptionStream to the realtime
// onDelta callback, returning the final aggregated transcription result.
func transcribeStream(ctx context.Context, ml *model.ModelLoader, transcriptionConfig config.ModelConfig, appConfig *config.ApplicationConfig, audio, language string, translate, diarize bool, prompt string, onDelta func(text string)) (*schema.TranscriptionResult, error) {
	var final *schema.TranscriptionResult
	err := backend.ModelTranscriptionStream(ctx, backend.TranscriptionRequest{
		Audio:     audio,
		Language:  language,
		Translate: translate,
		Diarize:   diarize,
		Prompt:    prompt,
	}, ml, transcriptionConfig, appConfig, func(chunk backend.TranscriptionStreamChunk) {
		if chunk.Delta != "" {
			onDelta(chunk.Delta)
		}
		if chunk.Final != nil {
			final = chunk.Final
		}
	})
	if err != nil {
		return nil, err
	}
	return final, nil
}

// modelSoundDetection runs sound-event classification against the session's
// sound-classification model config, mirroring how Transcribe dispatches to
// the transcription backend. Returns an error when no sound-detection model is
// configured for the session.
func modelSoundDetection(ctx context.Context, ml *model.ModelLoader, appConfig *config.ApplicationConfig, soundConfig *config.ModelConfig, audio string, topK int, threshold float32) (*schema.SoundClassificationResult, error) {
	if soundConfig == nil {
		return nil, fmt.Errorf("sound detection is not configured for this session")
	}
	return backend.ModelSoundDetection(ctx, backend.SoundDetectionRequest{
		Audio:     audio,
		TopK:      int32(topK),
		Threshold: threshold,
	}, ml, *soundConfig, appConfig)
}

// loadSoundDetectionConfig resolves the optional sound-classification model
// config named by pipeline.sound_detection. Returns (nil, nil) when no model
// is configured so sound detection stays additive and never blocks session
// setup.
func loadSoundDetectionConfig(pipeline *config.Pipeline, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) (*config.ModelConfig, error) {
	if pipeline.SoundDetection == "" {
		return nil, nil
	}
	cfg, err := cl.LoadResolvedModelConfig(pipeline.SoundDetection, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
	if err != nil {
		return nil, fmt.Errorf("failed to load sound detection config: %w", err)
	}
	if valid, _ := cfg.Validate(); !valid {
		return nil, fmt.Errorf("failed to validate sound detection config %q", pipeline.SoundDetection)
	}
	return cfg, nil
}

func newTranscriptionOnlyModel(pipeline *config.Pipeline, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) (Model, *config.ModelConfig, error) {
	cfgVAD, err := cl.LoadResolvedModelConfig(pipeline.VAD, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
	if err != nil {

		return nil, nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgVAD.Validate(); !valid {
		return nil, nil, fmt.Errorf("failed to validate config: %w", err)
	}

	cfgSST, err := cl.LoadResolvedModelConfig(pipeline.Transcription, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
	if err != nil {

		return nil, nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgSST.Validate(); !valid {
		return nil, nil, fmt.Errorf("failed to validate config: %w", err)
	}

	cfgSound, err := loadSoundDetectionConfig(pipeline, cl, ml, appConfig)
	if err != nil {
		return nil, nil, err
	}

	return &transcriptOnlyModel{
		TranscriptionConfig:  cfgSST,
		VADConfig:            cfgVAD,
		SoundDetectionConfig: cfgSound,

		confLoader:  cl,
		modelLoader: ml,
		appConfig:   appConfig,
	}, cfgSST, nil
}

// newSoundDetectionOnlyModel builds a realtime model that only does sound-event
// classification: no VAD, transcription, LLM or TTS stages are loaded. Used for
// a sound-detection-only realtime session, which activates on sounds (not
// speech) and is driven by client-side windowing (turn_detection none +
// input_audio_buffer.commit) rather than the voice VAD loop.
func newSoundDetectionOnlyModel(pipeline *config.Pipeline, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) (Model, error) {
	cfgSound, err := loadSoundDetectionConfig(pipeline, cl, ml, appConfig)
	if err != nil {
		return nil, err
	}
	if cfgSound == nil {
		return nil, fmt.Errorf("a sound-only realtime session requires pipeline.sound_detection")
	}
	return &transcriptOnlyModel{
		SoundDetectionConfig: cfgSound,
		confLoader:           cl,
		modelLoader:          ml,
		appConfig:            appConfig,
	}, nil
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

	cfgVAD, err := cl.LoadResolvedModelConfig(pipeline.VAD, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgVAD.Validate(); !valid {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	// TODO: Do we always need a transcription model? It can be disabled. Note that any-to-any instruction following models don't transcribe as such, so if transcription is required it is a separate process
	cfgSST, err := cl.LoadResolvedModelConfig(pipeline.Transcription, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
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
	cfgLLM, err := cl.LoadResolvedModelConfig(pipeline.LLM, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgLLM.Validate(); !valid {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	// Let the pipeline set the LLM's reasoning effort and force thinking off
	// (cfgLLM is a per-session copy). disable_thinking applies after the effort.
	applyPipelineReasoning(cfgLLM, *pipeline)
	applyPipelineThinking(cfgLLM, *pipeline)

	cfgTTS, err := cl.LoadResolvedModelConfig(pipeline.TTS, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
	if err != nil {

		return nil, fmt.Errorf("failed to load backend config: %w", err)
	}

	if valid, _ := cfgTTS.Validate(); !valid {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	cfgSound, err := loadSoundDetectionConfig(pipeline, cl, ml, appConfig)
	if err != nil {
		return nil, err
	}

	// Classifier mode scores on its own model config when one is named;
	// otherwise ClassifyTurn falls back to the LLM config at call time
	// (so a client can enable classification via session.update even
	// when the pipeline block is absent).
	var cfgScore *config.ModelConfig
	if pipeline.Classifier != nil && pipeline.Classifier.Model != "" {
		cfgScore, err = cl.LoadResolvedModelConfig(pipeline.Classifier.Model, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
		if err != nil {
			return nil, fmt.Errorf("failed to load classifier scoring config: %w", err)
		}
		if valid, err := cfgScore.Validate(); !valid {
			return nil, fmt.Errorf("failed to validate classifier scoring config: %w", err)
		}
		if !cfgScore.HasUsecases(config.FLAG_SCORE) {
			return nil, fmt.Errorf("pipeline classifier: scoring model %q must declare known_usecases: [score]", cfgScore.Name)
		}
	}
	if pipeline.Classifier != nil && pipeline.Classifier.Enabled {
		effectiveScore := cfgScore
		if effectiveScore == nil {
			effectiveScore = cfgLLM
		}
		if effectiveScore.HasRouter() {
			// A router model has no concrete backend to score on — the
			// per-turn routing decision happens at Predict time, after
			// classification would already have run.
			return nil, fmt.Errorf("pipeline classifier: llm %q is a router model; set pipeline.classifier.model to a concrete scoring model", cfgLLM.Name)
		}
		if !effectiveScore.HasUsecases(config.FLAG_SCORE) {
			return nil, fmt.Errorf("pipeline classifier: scoring model %q must declare known_usecases: [score]", effectiveScore.Name)
		}
	}

	wm := &wrappedModel{
		TTSConfig:            cfgTTS,
		TranscriptionConfig:  cfgSST,
		LLMConfig:            cfgLLM,
		VADConfig:            cfgVAD,
		SoundDetectionConfig: cfgSound,
		ScoreConfig:          cfgScore,

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
