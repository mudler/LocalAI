package openai

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"net/http"

	"github.com/go-audio/audio"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/backend"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/respcoord"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/turncoord"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/reasoning"
	"github.com/mudler/LocalAI/pkg/sound"
	"github.com/mudler/LocalAI/pkg/utils"

	"github.com/mudler/xlog"
)

const (
	// XXX: Presently it seems all ASR/VAD backends use 16Khz. If a backend uses 24Khz then it will likely still work, but have reduced performance
	localSampleRate         = 16000
	defaultRemoteSampleRate = 24000
	// Maximum audio buffer size in bytes (100MB) to prevent memory exhaustion
	maxAudioBufferSize = 100 * 1024 * 1024
	// Maximum WebSocket message size in bytes (10MB) to prevent DoS attacks
	maxWebSocketMessageSize = 10 * 1024 * 1024

	defaultInstructions = "You are a helpful voice assistant. " +
		"Your responses will be spoken aloud using text-to-speech, so keep them concise and conversational. " +
		"Do not use markdown formatting, bullet points, numbered lists, code blocks, or special characters. " +
		"Speak naturally as you would in a phone conversation. " +
		"Avoid parenthetical asides, URLs, and anything that cannot be clearly vocalized."
)

// resolveOutputModalities returns the effective output modalities for a
// response: response-level overrides session-level, and the OpenAI Realtime
// spec default is ["audio"] when neither is set.
func resolveOutputModalities(session, response []types.Modality) []types.Modality {
	if len(response) > 0 {
		return response
	}
	if len(session) > 0 {
		return session
	}
	return []types.Modality{types.ModalityAudio}
}

// modalitiesWithAlias returns output_modalities, falling back to the legacy
// beta `modalities` field when only the alias was supplied. OpenAI's Realtime
// beta named this field `modalities`; the GA field is `output_modalities`.
// Accepting the alias keeps beta clients (and the large amount of community
// sample code that still sends `modalities`) from silently receiving audio
// when they asked for text-only. The GA field wins when both are present.
func modalitiesWithAlias(output, alias []types.Modality) []types.Modality {
	if len(output) > 0 {
		return output
	}
	return alias
}

// modalitiesContainAudio reports whether the resolved modalities include audio
// output.
func modalitiesContainAudio(m []types.Modality) bool {
	for _, x := range m {
		if x == types.ModalityAudio {
			return true
		}
	}
	return false
}

// A model can be "emulated" that is: transcribe audio to text -> feed text to the LLM -> generate audio as result
// If the model support instead audio-to-audio, we will use the specific gRPC calls instead

// Session represents a single WebSocket connection and its state
type Session struct {
	ID                string
	TranscriptionOnly bool
	// The pipeline or any-to-any model name (full realtime mode)
	Model string
	// The voice may be a TTS model name or a parameter passed to a TTS model
	Voice                   string
	TurnDetection           *types.TurnDetectionUnion // "server_vad", "semantic_vad" or "none"
	InputAudioTranscription *types.AudioTranscription

	// SoundDetectionEnabled is set when pipeline.sound_detection names a
	// sound-event-classification model. When true, each committed utterance is
	// also run through ModelInterface.SoundDetection and the scored tags are
	// emitted as a conversation.item.sound_detection event. SoundDetectionTopK
	// and SoundDetectionThreshold are the knobs passed to that call (defaults:
	// top_k=5, threshold=0).
	SoundDetectionEnabled   bool
	SoundDetectionTopK      int
	SoundDetectionThreshold float32
	// SoundDetectionWindowMs / SoundDetectionHopMs, when both > 0, enable
	// server-side windowing for a sound-only session: the server classifies the
	// last WindowMs of streamed audio every HopMs (no client commits needed).
	SoundDetectionWindowMs int
	SoundDetectionHopMs    int
	Tools                  []types.ToolUnion
	ToolChoice             *types.ToolChoiceUnion
	Conversations          map[string]*Conversation
	InputAudioBuffer       []byte
	AudioBufferLock        sync.Mutex
	OpusFrames             [][]byte
	OpusFramesLock         sync.Mutex
	Instructions           string
	DefaultConversationID  string
	ModelInterface         Model
	// The pipeline model config or the config for an any-to-any model
	ModelConfig      *config.ModelConfig
	InputSampleRate  int
	OutputSampleRate int
	MaxOutputTokens  types.IntOrInf
	// OutputModalities mirrors the OpenAI Realtime spec field of the same
	// name. Empty means "use the spec default" (audio). ["text"] suppresses
	// TTS so the client receives only response.output_text.* events.
	OutputModalities []types.Modality
	// MaxHistoryItems caps the number of MessageItems passed to the LLM each
	// turn (0 = unlimited). Small models — especially the LFM2.5-Audio 1.5B
	// served via the liquid-audio backend — degrade quickly past a handful
	// of turns. Counted from the tail; FunctionCall + FunctionCallOutput
	// pairs are kept together so we never feed an orphaned tool result.
	MaxHistoryItems int

	// Compaction settings resolved from pipeline.compaction (see resolveCompaction).
	CompactionEnabled bool
	CompactionTrigger int
	SummaryModel      string
	MaxSummaryTokens  int

	// summarizerFactory lazily builds the model used for compaction summaries
	// when summary_model is configured; nil means reuse the pipeline LLM.
	summarizerFactory func() (Model, error)
	summarizerOnce    sync.Once
	summarizerCached  Model

	// AssistantExecutor is non-nil when the session opted into the in-process
	// LocalAI Assistant tool surface. Tool calls whose name matches this
	// executor's catalog are run inproc and their output is fed back to the
	// model server-side; the client never sees a function_call_arguments
	// event for those. Mirrors the chat handler's metadata.localai_assistant
	// path.
	AssistantExecutor mcpTools.ToolExecutor

	// AssistantTools is the cached ToolUnion slice we injected at session
	// creation. Re-applied after every client session.update so a
	// client-driven tool refresh (e.g. toggling a client MCP server) doesn't
	// silently strip Manage Mode's tools.
	AssistantTools []types.ToolUnion

	// voiceGate is non-nil when pipeline.voice_recognition is configured. It
	// authorizes each committed utterance's speaker before the LLM runs.
	voiceGate *voiceGate
	// gateMu guards the when:first verification state below.
	gateMu        sync.Mutex
	voiceVerified bool

	// respSink is the explicit response-coordination state machine (respcoord,
	// machine M3). It replaces the legacy startResponse/cancelActiveResponse
	// pair and its dual-writer activeResponse* fields: every start/cancel/finish
	// decision is serialized through respcoord.Coordinator, guaranteeing at most
	// one live response. See realtime_respcoord.go.
	respSink *responseSink
}

func (s *Session) FromClient(session *types.SessionUnion) {
}

func (s *Session) ToServer() types.SessionUnion {
	if s.TranscriptionOnly {
		return types.SessionUnion{
			Transcription: &types.TranscriptionSession{
				ID:     s.ID,
				Object: "realtime.transcription_session",
				Audio: &types.TranscriptionSessionAudio{
					Input: &types.SessionAudioInput{
						Transcription: s.InputAudioTranscription,
					},
				},
			},
		}
	} else {
		return types.SessionUnion{
			Realtime: &types.RealtimeSession{
				ID:               s.ID,
				Object:           "realtime.session",
				Model:            s.Model,
				Instructions:     s.Instructions,
				Tools:            s.Tools,
				ToolChoice:       s.ToolChoice,
				MaxOutputTokens:  s.MaxOutputTokens,
				OutputModalities: s.OutputModalities,
				Audio: &types.RealtimeSessionAudio{
					Input: &types.SessionAudioInput{
						TurnDetection: s.TurnDetection,
						Transcription: s.InputAudioTranscription,
					},
					Output: &types.SessionAudioOutput{
						Voice: types.Voice(s.Voice),
					},
				},
			},
		}
	}
}

// Conversation represents a conversation with a list of items
type Conversation struct {
	ID    string
	Items []*types.MessageItemUnion
	Lock  sync.Mutex
	// Memory is the rolling summary of items already evicted by compaction. It
	// is kept out of Items (so trimRealtimeItems never drops it) and rendered
	// as a system message right after the session instructions.
	Memory string
	// compaction is the explicit single-flight compaction coordinator (M4): at
	// most one background summarize+evict runs per conversation at a time. It
	// replaces the legacy `compacting atomic.Bool`. See realtime_compactcoord.go.
	compaction *compactionSink
}

func (c *Conversation) ToServer() types.Conversation {
	return types.Conversation{
		ID:     c.ID,
		Object: "realtime.conversation",
	}
}

// Map to store sessions (in-memory)
var sessions = make(map[string]*Session)
var sessionLock sync.Mutex

type Model interface {
	VAD(ctx context.Context, request *schema.VADRequest) (*schema.VADResponse, error)
	Transcribe(ctx context.Context, audio, language string, translate bool, diarize bool, prompt string) (*schema.TranscriptionResult, error)
	Predict(ctx context.Context, messages schema.Messages, images, videos, audios []string, tokenCallback func(string, backend.TokenUsage) bool, tools []types.ToolUnion, toolChoice *types.ToolChoiceUnion, logprobs *int, topLogprobs *int, logitBias map[string]float64) (func() (backend.LLMResponse, error), error)
	TTS(ctx context.Context, text, voice, language string) (string, *proto.Result, error)
	// TTSStream synthesizes speech incrementally, invoking onAudio with raw PCM
	// chunks (and the backend sample rate) as they are produced.
	TTSStream(ctx context.Context, text, voice, language string, onAudio func(pcm []byte, sampleRate int) error) error
	// TranscribeStream transcribes audio incrementally, invoking onDelta for each
	// transcript text fragment and returning the final aggregated result.
	TranscribeStream(ctx context.Context, audio, language string, translate, diarize bool, prompt string, onDelta func(text string)) (*schema.TranscriptionResult, error)
	// SoundDetection classifies a committed audio window into scored AudioSet
	// sound-event tags. topK caps the number of returned tags (0 = backend
	// default), threshold drops tags below the given score (0 = keep all).
	SoundDetection(ctx context.Context, audio string, topK int, threshold float32) (*schema.SoundClassificationResult, error)
	// TranscribeLive opens a live (bidirectional) transcription session on the
	// pipeline's transcription backend, used by semantic_vad turn detection;
	// onEvent fires from a background goroutine for every delta/EOU/final
	// event. Backends without live support fail with an error satisfying
	// grpcerrors.IsLiveTranscriptionUnsupported.
	TranscribeLive(ctx context.Context, language string, onEvent func(backend.LiveTranscriptionEvent)) (backend.LiveTranscriptionSession, error)
	PredictConfig() *config.ModelConfig
	// Warmup eagerly loads the pipeline's sub-model backends into memory so the
	// first realtime turn doesn't pay each backend's cold-start load cost. Loads
	// run concurrently; Warmup blocks until they all finish and returns a joined
	// error naming every stage that failed to load (nil if all succeeded), so a
	// caller can surface model-load failures at session start instead of mid-call.
	Warmup(ctx context.Context) error
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins
	},
}

// TODO: Implement ephemeral keys to allow these endpoints to be used
func RealtimeSessions(application *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.NoContent(501)
	}
}

func RealtimeTranscriptionSession(application *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.NoContent(501)
	}
}

// RealtimeSessionOptions bundles per-session knobs decoded from the WS query
// string (or the WebRTC handshake body). Mirrors what chat.go pulls off
// `metadata.localai_assistant` — admin-only opt-in to the in-process
// management tool surface.
type RealtimeSessionOptions struct {
	LocalAIAssistant bool
	// AuthEnabled mirrors chat.go's requireAssistantAccess gate. We resolve
	// admin role at handshake time (where the echo.Context has the auth
	// cookie/Bearer) and drop the result here so runRealtimeSession can
	// decide without holding onto the request.
	IsAdmin bool
}

func Realtime(application *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		// Set maximum message size to prevent DoS attacks
		ws.SetReadLimit(maxWebSocketMessageSize)

		// Extract query parameters from Echo context before passing to websocket handler
		model := c.QueryParam("model")
		assistantFlag, _ := strconv.ParseBool(c.QueryParam("localai_assistant"))
		opts := RealtimeSessionOptions{
			LocalAIAssistant: assistantFlag,
			IsAdmin:          isCurrentUserAdmin(c, application),
		}

		registerRealtime(application, model, opts)(ws)
		return nil
	}
}

// isCurrentUserAdmin replicates the chat-side admin check at the realtime
// handshake. When auth is disabled, every caller is treated as admin (same
// as chat's requireAssistantAccess).
func isCurrentUserAdmin(c echo.Context, application *application.Application) bool {
	if application == nil || application.ApplicationConfig() == nil || !application.ApplicationConfig().Auth.Enabled {
		return true
	}
	user := auth.GetUser(c)
	return user != nil && user.Role == auth.RoleAdmin
}

func registerRealtime(application *application.Application, model string, opts RealtimeSessionOptions) func(c *websocket.Conn) {
	return func(conn *websocket.Conn) {
		t := NewWebSocketTransport(conn)
		evaluator := application.TemplatesEvaluator()
		xlog.Debug("Realtime WebSocket connection established", "address", conn.RemoteAddr().String(), "model", model)
		runRealtimeSession(application, t, model, evaluator, opts)
	}
}

// defaultMaxHistoryItems picks a sensible default cap for the session.
// Small any-to-any audio models degrade quickly past a handful of turns;
// legacy pipelines composing larger LLMs keep the historical "unlimited"
// default and rely on the LLM's own context window.
func defaultMaxHistoryItems(cfg *config.ModelConfig) int {
	if cfg != nil && cfg.HasUsecases(config.FLAG_REALTIME_AUDIO) {
		return 6
	}
	return 0
}

// resolveMaxHistoryItems honors an explicit pipeline.max_history_items when set,
// otherwise falls back to the per-model-type default. This lets a composed
// pipeline (VAD+STT+LLM+TTS) cap its history so a long-running session doesn't
// grow until the LLM's context window fills.
func resolveMaxHistoryItems(cfg *config.ModelConfig) int {
	if cfg != nil && cfg.Pipeline.MaxHistoryItems != nil {
		return *cfg.Pipeline.MaxHistoryItems
	}
	return defaultMaxHistoryItems(cfg)
}

// trimRealtimeItems returns the tail of items capped at maxItems (0 = no cap).
// Walks backwards keeping function_call + function_call_output pairs together
// so we never feed the LLM an orphaned tool result that references a call it
// can't see.
func trimRealtimeItems(items []*types.MessageItemUnion, maxItems int) []*types.MessageItemUnion {
	if maxItems <= 0 || len(items) <= maxItems {
		return items
	}
	// Find the cut point starting from len-maxItems and pull it left until
	// we're not in the middle of a tool-call pair.
	cut := len(items) - maxItems
	for cut > 0 && items[cut] != nil && items[cut].FunctionCallOutput != nil {
		cut--
	}
	return items[cut:]
}

// prepareRealtimeConfig validates a model config for use in a realtime session
// and fills in pipeline slots for self-contained any-to-any models. It returns
// an error code + message pair suitable for sendError; the bool indicates
// whether the caller should proceed. Extracted from runRealtimeSession so the
// gate logic can be exercised in unit tests without a full Application.
func prepareRealtimeConfig(cfg *config.ModelConfig) (errCode, errMsg string, ok bool) {
	if cfg == nil {
		return "invalid_model", "Model is not a pipeline model", false
	}

	// Self-contained any-to-any models (e.g. liquid-audio) own the whole
	// loop in one engine — surface them by populating empty pipeline slots
	// with the model's own name so newModel can resolve a config for each
	// role. The user can still pin individual slots (e.g. Pipeline.VAD =
	// silero-vad) and those wins.
	if cfg.HasUsecases(config.FLAG_REALTIME_AUDIO) {
		if cfg.Pipeline.VAD == "" {
			cfg.Pipeline.VAD = cfg.Name
		}
		if cfg.Pipeline.Transcription == "" {
			cfg.Pipeline.Transcription = cfg.Name
		}
		if cfg.Pipeline.LLM == "" {
			cfg.Pipeline.LLM = cfg.Name
		}
		if cfg.Pipeline.TTS == "" {
			cfg.Pipeline.TTS = cfg.Name
		}
		return "", "", true
	}

	if cfg.Pipeline.VAD == "" && cfg.Pipeline.Transcription == "" && cfg.Pipeline.TTS == "" && cfg.Pipeline.LLM == "" && cfg.Pipeline.SoundDetection == "" {
		return "invalid_model", "Model is not a pipeline model", false
	}
	return "", "", true
}

// runRealtimeSession runs the main event loop for a realtime session.
// It is transport-agnostic and works with both WebSocket and WebRTC.
func runRealtimeSession(application *application.Application, t Transport, model string, evaluator *templates.Evaluator, opts RealtimeSessionOptions) {
	cl := application.ModelConfigLoader()
	cfg, err := cl.LoadModelConfigFileByNameDefaultOptions(model, application.ApplicationConfig())
	if err != nil {
		xlog.Error("failed to load model config", "error", err)
		sendError(t, "model_load_error", "Failed to load model config", "", "")
		return
	}

	if code, msg, ok := prepareRealtimeConfig(cfg); !ok {
		xlog.Error("model is not a pipeline", "model", model)
		sendError(t, code, msg, "", "")
		return
	}

	// LocalAI Assistant opt-in: gate on admin (same rule as chat.go's
	// requireAssistantAccess) and grab the process-wide holder's executor.
	// We collect tools + system prompt here and merge them into the session
	// below so they're live from the first response.create.
	var assistantTools []types.ToolUnion
	var assistantSystemPrompt string
	var assistantExecutor mcpTools.ToolExecutor
	if opts.LocalAIAssistant {
		if !opts.IsAdmin {
			sendError(t, "forbidden", "localai_assistant requires admin", "", "")
			return
		}
		appCfg := application.ApplicationConfig()
		if appCfg != nil && appCfg.DisableLocalAIAssistant {
			sendError(t, "unavailable", "LocalAI Assistant is disabled on this server", "", "")
			return
		}
		holder := application.LocalAIAssistant()
		if holder == nil || !holder.HasTools() {
			sendError(t, "unavailable", "LocalAI Assistant is not available on this server", "", "")
			return
		}
		exec := holder.Executor()
		fns, discErr := exec.DiscoverTools(context.Background())
		if discErr != nil {
			xlog.Error("realtime: failed to discover LocalAI Assistant tools", "error", discErr)
			sendError(t, "tool_discovery_failed", "failed to discover assistant tools: "+discErr.Error(), "", "")
			return
		}
		assistantExecutor = exec
		assistantSystemPrompt = holder.SystemPrompt()
		assistantTools = make([]types.ToolUnion, 0, len(fns))
		for _, fn := range fns {
			fnCopy := fn
			assistantTools = append(assistantTools, types.ToolUnion{
				Function: &types.ToolFunction{
					Name:        fnCopy.Name,
					Description: fnCopy.Description,
					Parameters:  fnCopy.Parameters,
				},
			})
		}
		xlog.Debug("realtime: LocalAI Assistant tools injected", "count", len(fns))
	}

	sttModel := cfg.Pipeline.Transcription

	// A sound-detection-only pipeline (sound_detection set, no transcription/LLM)
	// activates on sounds, not speech, so it runs WITHOUT the voice VAD: the
	// session defaults to turn_detection none and the client drives windowing via
	// input_audio_buffer.commit. There is no transcription stage in that case.
	soundOnly := cfg.Pipeline.SoundDetection != "" && cfg.Pipeline.Transcription == "" && cfg.Pipeline.LLM == ""

	// defaultTurnDetection seeds server_vad by default, or semantic_vad when the
	// pipeline opts in (turn_detection.type: semantic_vad); clients can still
	// override per session via session.update.
	turnDetection := defaultTurnDetection(cfg)
	inputAudioTranscription := &types.AudioTranscription{Model: sttModel}
	if soundOnly {
		turnDetection = nil           // turn_detection none: no VAD
		inputAudioTranscription = nil // no transcription stage
	}

	// Compose the system prompt: prepend the assistant prompt when we have
	// one (it teaches the model the safety rules and tool recipes), then the
	// session's default voice instructions. Order matches chat.go's
	// hasSystemMessage check — assistant prompt comes first.
	instructions := defaultInstructions
	if assistantSystemPrompt != "" {
		instructions = assistantSystemPrompt + "\n\n" + defaultInstructions
	}

	sessionID := generateSessionID()
	session := &Session{
		ID:                      sessionID,
		TranscriptionOnly:       false,
		Model:                   model,
		Voice:                   cfg.TTSConfig.Voice,
		Instructions:            instructions,
		ModelConfig:             cfg,
		Tools:                   assistantTools,
		AssistantTools:          assistantTools,
		AssistantExecutor:       assistantExecutor,
		TurnDetection:           turnDetection,
		InputAudioTranscription: inputAudioTranscription,
		Conversations:           make(map[string]*Conversation),
		InputSampleRate:         defaultRemoteSampleRate,
		OutputSampleRate:        defaultRemoteSampleRate,
		MaxHistoryItems:         resolveMaxHistoryItems(cfg),
		SoundDetectionEnabled:   cfg.Pipeline.SoundDetection != "",
		SoundDetectionTopK:      defaultSoundDetectionTopK,
		SoundDetectionThreshold: 0,
		SoundDetectionWindowMs:  cfg.Pipeline.SoundDetectionWindowMs,
		SoundDetectionHopMs:     cfg.Pipeline.SoundDetectionHopMs,
	}
	session.CompactionEnabled, session.CompactionTrigger, session.MaxSummaryTokens, session.SummaryModel = resolveCompaction(cfg, session.MaxHistoryItems)

	// Single-writer response coordinator (machine M3). All response starts and
	// cancels go through this, so the read-loop and VAD goroutine can never race
	// into two overlapping responses (see realtime_respcoord.go).
	session.respSink = newResponseSink()

	// Create a default conversation
	conversationID := generateConversationID()
	conversation := &Conversation{
		ID:    conversationID,
		Items: []*types.MessageItemUnion{},
	}
	// The compaction coordinator's work closure resolves the summarizer (lazily
	// loading a configured summary_model) and runs the summarize+evict off the
	// response path — only when a compaction actually starts.
	conversation.compaction = newCompactionSink(func(ctx context.Context) {
		model := session.summarizerModel()
		if model == nil {
			return
		}
		session.compact(ctx, conversation, model)
	})
	session.Conversations[conversationID] = conversation
	session.DefaultConversationID = conversationID

	var m Model
	if soundOnly {
		m, err = newSoundDetectionOnlyModel(
			&cfg.Pipeline,
			application.ModelConfigLoader(),
			application.ModelLoader(),
			application.ApplicationConfig(),
		)
	} else {
		m, err = newModel(
			&cfg.Pipeline,
			application.ModelConfigLoader(),
			application.ModelLoader(),
			application.ApplicationConfig(),
			evaluator,
			buildRealtimeRoutingContext(application, sessionID),
		)
	}
	if err != nil {
		xlog.Error("failed to load model", "error", err)
		sendError(t, "model_load_error", "Failed to load model", "", "")
		return
	}
	session.ModelInterface = m

	// The voice gate is built before the warm-up below so its
	// speaker-recognition model can warm alongside the pipeline stages.
	if cfg.Pipeline.VoiceGateEnabled() {
		gate, gerr := newVoiceGate(
			*cfg.Pipeline.VoiceRecognition,
			application.ModelConfigLoader(),
			application.ModelLoader(),
			application.ApplicationConfig(),
			application.VoiceRegistry(),
		)
		if gerr != nil {
			xlog.Error("failed to initialize voice recognition gate", "error", gerr)
			sendError(t, "voice_gate_error", gerr.Error(), "", "")
			return
		}
		session.voiceGate = gate
		xlog.Info("realtime voice recognition gate enabled", "mode", gate.cfg.Mode, "when", gate.cfg.When)
	}

	// Warm the pipeline's sub-model backends before announcing the session.
	// Loads run concurrently but we block here until they all finish, so a model
	// that fails to load (missing weights, bad backend, OOM) surfaces as an error
	// at session start rather than stalling — or failing — mid-call on the first
	// turn (VAD on the first audio chunk, STT at end-of-speech, LLM on the first
	// reply, TTS on the first spoken output). On success the backends are already
	// resident, so the first turn pays no cold-start cost. Opt out per pipeline
	// with `pipeline.disable_warmup: true` to restore lazy load-on-first-use
	// (errors then surface on first use instead of at session start).
	if !cfg.Pipeline.DisableWarmup {
		warmErr := make(chan error, 1)
		go func() { warmErr <- m.Warmup(context.Background()) }()
		// The voice-gate model warms concurrently with the pipeline stages: an
		// enforced gate blocks each utterance on speaker resolution, so its
		// cold-start would otherwise land on the first turn too. (Compaction's
		// summary_model stays lazy — it only runs off the response path.)
		var gateErr error
		if session.voiceGate != nil {
			_, gateErr = backend.PreloadStages(context.Background(), application.ModelLoader(), application.ApplicationConfig(), []backend.PreloadStage{
				{Role: "voice_recognition", Cfg: session.voiceGate.recCfg},
			})
		}
		if err := errors.Join(<-warmErr, gateErr); err != nil {
			xlog.Error("realtime warmup failed", "model", model, "error", err)
			sendError(t, "model_load_error", "Failed to load pipeline models: "+err.Error(), "", "")
			return
		}
	}

	if session.SummaryModel != "" {
		summaryModelName := session.SummaryModel
		sid := sessionID
		session.summarizerFactory = func() (Model, error) {
			summaryCfg, lerr := application.ModelConfigLoader().LoadModelConfigFileByNameDefaultOptions(summaryModelName, application.ApplicationConfig())
			if lerr != nil {
				return nil, fmt.Errorf("load summary model config %q: %w", summaryModelName, lerr)
			}
			return newModel(&summaryCfg.Pipeline, application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig(), evaluator, buildRealtimeRoutingContext(application, sid))
		}
	}

	// Store the session and notify the transport (for WebRTC audio track handling)
	sessionLock.Lock()
	sessions[sessionID] = session
	sessionLock.Unlock()

	// For WebRTC, inbound audio arrives as Opus (48kHz) and is decoded+resampled
	// to localSampleRate in handleIncomingAudioTrack. Set InputSampleRate to
	// match so handleVAD doesn't needlessly double-resample.
	if _, ok := t.(*WebRTCTransport); ok {
		session.InputSampleRate = localSampleRate
	}

	if sn, ok := t.(interface{ SetSession(*Session) }); ok {
		sn.SetSession(session)
	}

	sendEvent(t, types.SessionCreatedEvent{
		ServerEventBase: types.ServerEventBase{
			EventID: "event_TODO",
		},
		Session: session.ToServer(),
	})

	var (
		msg []byte
		wg  sync.WaitGroup
	)

	// M1 connection lifecycle. The VAD goroutine's run/stop (and its done channel)
	// and the once-only teardown are owned by this coordinator, so the channel is
	// closed exactly once and never resurrected after teardown (Part 2, failure
	// mode 6; invariants #8, #10). See realtime_conncoord.go and conncoord/.
	conn := newConnSink(session, sessionID, t, &wg)
	toggleVAD := func() { conn.setVAD(turnDetectionActive(session.TurnDetection)) }

	// For WebRTC sessions, start the Opus decode loop before VAD so that
	// decoded PCM is already flowing when VAD's first tick fires.
	if wt, ok := t.(*WebRTCTransport); ok {
		conn.decodeDone = make(chan struct{})
		go decodeOpusLoop(session, wt.opusBackend, conn.decodeDone)
	}

	toggleVAD()

	// Server-side sound-detection windowing (option B): for a sound-only session
	// with window/hop configured, the server classifies the last window of
	// streamed audio on a timer, so the client only has to stream (no commits).
	// This runs independent of VAD (sound events are not speech).
	if soundOnly && session.SoundDetectionWindowMs > 0 && session.SoundDetectionHopMs > 0 {
		conn.soundWindowDone = make(chan struct{})
		soundWindowDone := conn.soundWindowDone
		wg.Go(func() {
			handleSoundWindow(session, t, soundWindowDone)
		})
		xlog.Debug("Starting server-side sound-detection windowing",
			"window_ms", session.SoundDetectionWindowMs, "hop_ms", session.SoundDetectionHopMs)
	}

	for {
		msg, err = t.ReadEvent()
		if err != nil {
			xlog.Error("read error", "error", err)
			break
		}

		// Handle diagnostic events that aren't part of the OpenAI protocol
		var rawType struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(msg, &rawType) == nil && rawType.Type == "test_tone" {
			if _, ok := t.(*WebSocketTransport); ok {
				sendError(t, "not_supported", "test_tone is only supported on WebRTC connections", "", "")
			} else {
				xlog.Debug("Generating test tone")
				go sendTestTone(t)
			}
			continue
		}

		// Parse the incoming message
		event, err := types.UnmarshalClientEvent(msg)
		if err != nil {
			xlog.Error("invalid json", "error", err)
			sendError(t, "invalid_json", "Invalid JSON format", "", "")
			continue
		}

		switch e := event.(type) {
		case types.SessionUpdateEvent:
			xlog.Debug("recv", "message", string(msg))

			// Handle transcription session update
			if e.Session.Transcription != nil {
				if err := updateTransSession(
					session,
					&e.Session,
					application.ModelConfigLoader(),
					application.ModelLoader(),
					application.ApplicationConfig(),
				); err != nil {
					xlog.Error("failed to update session", "error", err)
					sendError(t, "session_update_error", "Failed to update session", "", "")
					continue
				}

				toggleVAD()

				sendEvent(t, types.SessionUpdatedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
					},
					Session: session.ToServer(),
				})
			}

			// Handle realtime session update
			if e.Session.Realtime != nil {
				if err := updateSession(
					session,
					&e.Session,
					application.ModelConfigLoader(),
					application.ModelLoader(),
					application.ApplicationConfig(),
					evaluator,
					buildRealtimeRoutingContext(application, session.ID),
				); err != nil {
					xlog.Error("failed to update session", "error", err)
					sendError(t, "session_update_error", "Failed to update session", "", "")
					continue
				}

				toggleVAD()

				sendEvent(t, types.SessionUpdatedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
					},
					Session: session.ToServer(),
				})
			}

		case types.InputAudioBufferAppendEvent:
			// Handle 'input_audio_buffer.append'
			if e.Audio == "" {
				xlog.Error("Audio data is missing in 'input_audio_buffer.append'")
				sendError(t, "missing_audio_data", "Audio data is missing", "", "")
				continue
			}

			// Decode base64 audio data
			decodedAudio, err := base64.StdEncoding.DecodeString(e.Audio)
			if err != nil {
				xlog.Error("failed to decode audio data", "error", err)
				sendError(t, "invalid_audio_data", "Failed to decode audio data", "", "")
				continue
			}

			// Check buffer size limits before appending
			session.AudioBufferLock.Lock()
			newSize := len(session.InputAudioBuffer) + len(decodedAudio)
			if newSize > maxAudioBufferSize {
				session.AudioBufferLock.Unlock()
				xlog.Error("audio buffer size limit exceeded", "current_size", len(session.InputAudioBuffer), "incoming_size", len(decodedAudio), "limit", maxAudioBufferSize)
				sendError(t, "buffer_size_exceeded", fmt.Sprintf("Audio buffer size limit exceeded (max %d bytes)", maxAudioBufferSize), "", "")
				continue
			}

			// Append to InputAudioBuffer
			session.InputAudioBuffer = append(session.InputAudioBuffer, decodedAudio...)
			session.AudioBufferLock.Unlock()

		case types.InputAudioBufferCommitEvent:
			xlog.Debug("recv", "message", string(msg))

			sessionLock.Lock()
			autoTurnDetection := turnDetectionActive(session.TurnDetection)
			sessionLock.Unlock()

			// TODO: At the least need to check locking and timer state in the VAD Go routine before allowing this
			if autoTurnDetection {
				sendNotImplemented(t, "input_audio_buffer.commit in conjunction with VAD")
				continue
			}

			session.AudioBufferLock.Lock()
			allAudio := make([]byte, len(session.InputAudioBuffer))
			copy(allAudio, session.InputAudioBuffer)
			session.InputAudioBuffer = nil
			session.AudioBufferLock.Unlock()

			sendEvent(t, types.InputAudioBufferCommittedEvent{
				ServerEventBase: types.ServerEventBase{},
				ItemID:          generateItemID(),
			})

			session.respSink.issue(context.Background(), respcoord.SourceClient, func(ctx context.Context) {
				commitUtterance(ctx, allAudio, session, conversation, t)
			})

		case types.InputAudioBufferClearEvent:
			xlog.Debug("recv", "message", string(msg))
			// Discard a partially-captured utterance so the client can restart
			// input cleanly without the stale buffer leaking into the next commit.
			clearInputAudio(session)
			sendEvent(t, types.InputAudioBufferClearedEvent{
				ServerEventBase: types.ServerEventBase{EventID: e.EventID},
			})

		case types.ConversationItemCreateEvent:
			xlog.Debug("recv", "message", string(msg))
			// Add the item to the conversation
			item := e.Item
			// Ensure IDs are present
			if item.User != nil && item.User.ID == "" {
				item.User.ID = generateItemID()
			}
			if item.Assistant != nil && item.Assistant.ID == "" {
				item.Assistant.ID = generateItemID()
			}
			if item.System != nil && item.System.ID == "" {
				item.System.ID = generateItemID()
			}
			if item.FunctionCall != nil && item.FunctionCall.ID == "" {
				item.FunctionCall.ID = generateItemID()
			}
			if item.FunctionCallOutput != nil && item.FunctionCallOutput.ID == "" {
				item.FunctionCallOutput.ID = generateItemID()
			}

			conversation.Lock.Lock()
			conversation.Items = append(conversation.Items, &item)
			conversation.Lock.Unlock()

			sendEvent(t, types.ConversationItemAddedEvent{
				ServerEventBase: types.ServerEventBase{
					EventID: e.EventID,
				},
				PreviousItemID: e.PreviousItemID,
				Item:           item,
			})

		case types.ConversationItemDeleteEvent:
			xlog.Debug("recv", "message", string(msg))
			if e.ItemID == "" {
				sendError(t, "invalid_item_id", "Need item_id, but none specified", "", "event_TODO")
				continue
			}
			conversation.Lock.Lock()
			updated, ok := deleteItem(conversation.Items, e.ItemID)
			conversation.Items = updated
			conversation.Lock.Unlock()
			if !ok {
				sendError(t, "invalid_item_id", "Item to delete not found", "", "event_TODO")
				continue
			}
			sendEvent(t, types.ConversationItemDeletedEvent{
				ServerEventBase: types.ServerEventBase{EventID: e.EventID},
				ItemID:          e.ItemID,
			})

		case types.ConversationItemTruncateEvent:
			xlog.Debug("recv", "message", string(msg))
			conversation.Lock.Lock()
			ok := truncateAssistantText(conversation.Items, e.ItemID, e.ContentIndex)
			conversation.Lock.Unlock()
			if !ok {
				sendError(t, "invalid_item_id", "Item to truncate not found", "", "event_TODO")
				continue
			}
			sendEvent(t, types.ConversationItemTruncatedEvent{
				ServerEventBase: types.ServerEventBase{EventID: e.EventID},
				ItemID:          e.ItemID,
				ContentIndex:    e.ContentIndex,
				AudioEndMs:      e.AudioEndMs,
			})

		case types.ConversationItemRetrieveEvent:
			xlog.Debug("recv", "message", string(msg))

			if e.ItemID == "" {
				sendError(t, "invalid_item_id", "Need item_id, but none specified", "", "event_TODO")
				continue
			}

			conversation.Lock.Lock()
			var retrievedItem types.MessageItemUnion
			for _, item := range conversation.Items {
				if itemID(item) == e.ItemID {
					retrievedItem = *item
					break
				}
			}
			conversation.Lock.Unlock()

			sendEvent(t, types.ConversationItemRetrievedEvent{
				ServerEventBase: types.ServerEventBase{
					EventID: "event_TODO",
				},
				Item: retrievedItem,
			})

		case types.ResponseCreateEvent:
			xlog.Debug("recv", "message", string(msg))

			// Handle optional items to add to context
			if len(e.Response.Input) > 0 {
				conversation.Lock.Lock()
				for _, item := range e.Response.Input {
					// Ensure IDs are present
					if item.User != nil && item.User.ID == "" {
						item.User.ID = generateItemID()
					}
					if item.Assistant != nil && item.Assistant.ID == "" {
						item.Assistant.ID = generateItemID()
					}
					if item.System != nil && item.System.ID == "" {
						item.System.ID = generateItemID()
					}
					if item.FunctionCall != nil && item.FunctionCall.ID == "" {
						item.FunctionCall.ID = generateItemID()
					}
					if item.FunctionCallOutput != nil && item.FunctionCallOutput.ID == "" {
						item.FunctionCallOutput.ID = generateItemID()
					}

					conversation.Items = append(conversation.Items, &item)
				}
				conversation.Lock.Unlock()
			}

			resp := e.Response
			session.respSink.issue(context.Background(), respcoord.SourceClient, func(ctx context.Context) {
				triggerResponse(ctx, session, conversation, t, &resp)
			})

		case types.ResponseCancelEvent:
			xlog.Debug("recv", "message", string(msg))
			session.respSink.cancel(respcoord.SourceClient)

		default:
			xlog.Error("unknown message type")
			// sendError(t, "unknown_message_type", fmt.Sprintf("Unknown message type: %s", incomingMsg.Type), "", "")
		}
	}

	// Tear down through the connection coordinator (once). It stops any running
	// VAD goroutine, then the opus-decode and sound-window goroutines, joins them,
	// cancels the in-flight response and drains all response goroutines, and
	// finally removes the session — all in dependency order, exactly once.
	conn.close()
}

// sendEvent sends a server event via the transport, logging any errors.
func sendEvent(t Transport, event types.ServerEvent) {
	if err := t.SendEvent(event); err != nil {
		xlog.Error("write error", "error", err)
	}
}

// sendError sends an error event to the client.
func sendError(t Transport, code, message, param, eventID string) {
	errorEvent := types.ErrorEvent{
		ServerEventBase: types.ServerEventBase{
			EventID: eventID,
		},
		Error: types.Error{
			Type:    "invalid_request_error",
			Code:    code,
			Message: message,
			Param:   param,
			EventID: eventID,
		},
	}

	sendEvent(t, errorEvent)
}

func sendNotImplemented(t Transport, message string) {
	sendError(t, "not_implemented", message, "", "event_TODO")
}

// sendTestTone generates a 1-second 440 Hz sine wave and sends it through
// the transport's audio path. This exercises the full Opus encode → RTP →
// browser decode pipeline without involving TTS.
func sendTestTone(t Transport) {
	const (
		freq       = 440.0
		sampleRate = 24000
		duration   = 1 // seconds
		amplitude  = 16000
		numSamples = sampleRate * duration
	)

	pcm := make([]byte, numSamples*2) // 16-bit samples = 2 bytes each
	for i := range numSamples {
		sample := int16(amplitude * math.Sin(2*math.Pi*freq*float64(i)/sampleRate))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}

	xlog.Debug("Sending test tone", "samples", numSamples, "sample_rate", sampleRate, "freq", freq)
	if err := t.SendAudio(context.Background(), pcm, sampleRate); err != nil {
		xlog.Error("test tone send failed", "error", err)
	}
}

func updateTransSession(session *Session, update *types.SessionUnion, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) error {
	sessionLock.Lock()
	defer sessionLock.Unlock()

	// In transcription session update, we look at Transcription field
	if update.Transcription == nil || update.Transcription.Audio == nil || update.Transcription.Audio.Input == nil {
		return nil
	}

	trUpd := update.Transcription.Audio.Input.Transcription
	trCur := session.InputAudioTranscription

	session.TranscriptionOnly = true

	if trUpd != nil && trUpd.Model != "" && trUpd.Model != trCur.Model {
		cfg, err := cl.LoadModelConfigFileByNameDefaultOptions(trUpd.Model, appConfig)
		if err != nil {
			return err
		}
		if cfg == nil || (cfg.Pipeline.VAD == "" || cfg.Pipeline.Transcription == "") {
			return fmt.Errorf("model is not a valid pipeline model: %s", trUpd.Model)
		}

		m, cfg, err := newTranscriptionOnlyModel(&cfg.Pipeline, cl, ml, appConfig)
		if err != nil {
			return err
		}

		session.ModelInterface = m
		session.ModelConfig = cfg
		session.SoundDetectionEnabled = cfg.Pipeline.SoundDetection != ""
		if session.SoundDetectionTopK <= 0 {
			session.SoundDetectionTopK = defaultSoundDetectionTopK
		}
	}

	if trUpd != nil {
		trCur.Language = trUpd.Language
		trCur.Prompt = trUpd.Prompt
	}

	if update.Transcription.Audio.Input.TurnDetectionSet {
		session.TurnDetection = update.Transcription.Audio.Input.TurnDetection
	}

	if update.Transcription.Audio.Input.Format != nil && update.Transcription.Audio.Input.Format.PCM != nil {
		if update.Transcription.Audio.Input.Format.PCM.Rate > 0 {
			session.InputSampleRate = update.Transcription.Audio.Input.Format.PCM.Rate
		}
	}

	return nil
}

func updateSession(session *Session, update *types.SessionUnion, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, evaluator *templates.Evaluator, routing *RealtimeRoutingContext) error {
	sessionLock.Lock()
	defer sessionLock.Unlock()

	if update.Realtime == nil {
		return nil
	}

	session.TranscriptionOnly = false
	rt := update.Realtime

	if rt.Model != "" {
		cfg, err := cl.LoadModelConfigFileByNameDefaultOptions(rt.Model, appConfig)
		if err != nil {
			return err
		}
		if cfg == nil || (cfg.Pipeline.VAD == "" || cfg.Pipeline.Transcription == "" || cfg.Pipeline.TTS == "" || cfg.Pipeline.LLM == "") {
			return fmt.Errorf("model is not a valid pipeline model: %s", rt.Model)
		}

		if session.InputAudioTranscription == nil {
			session.InputAudioTranscription = &types.AudioTranscription{}
		}
		session.InputAudioTranscription.Model = cfg.Pipeline.Transcription
		session.Voice = cfg.TTSConfig.Voice
		session.Model = rt.Model
		session.ModelConfig = cfg
	}

	if rt.Audio != nil && rt.Audio.Output != nil && rt.Audio.Output.Voice != "" {
		session.Voice = string(rt.Audio.Output.Voice)
	}

	if rt.Audio != nil && rt.Audio.Input != nil && rt.Audio.Input.Transcription != nil {
		trUpd := rt.Audio.Input.Transcription
		// A language-only update (e.g. a client forcing the STT language) carries
		// an empty Model. Preserve the pipeline's configured transcription backend
		// instead of blanking it — otherwise the next utterance transcribes against
		// an empty model and the backend RPC fails with "unimplemented".
		if trUpd.Model == "" && session.InputAudioTranscription != nil {
			trUpd.Model = session.InputAudioTranscription.Model
		}
		session.InputAudioTranscription = trUpd
		if trUpd.Model != "" {
			session.ModelConfig.Pipeline.Transcription = trUpd.Model
		}
	}

	if rt.Model != "" || (rt.Audio != nil && rt.Audio.Output != nil && rt.Audio.Output.Voice != "") || (rt.Audio != nil && rt.Audio.Input != nil && rt.Audio.Input.Transcription != nil) {
		m, err := newModel(&session.ModelConfig.Pipeline, cl, ml, appConfig, evaluator, routing)
		if err != nil {
			return err
		}
		session.ModelInterface = m
		// A session.update that swaps the model/voice rebuilds the pipeline, so
		// warm the new backends too (unless opted out) — otherwise the next turn
		// pays the cold-start load the original session warm-up already avoided.
		// Unlike session start this stays non-blocking: updateSession runs under
		// the global sessionLock, so blocking on a multi-second load here would
		// stall every other session. Load errors are logged (and still surface on
		// first use); per-stage failures are already warned inside
		// backend.PreloadStages.
		if !session.ModelConfig.Pipeline.DisableWarmup {
			go func() {
				if err := m.Warmup(context.Background()); err != nil {
					xlog.Error("realtime warmup failed after session.update", "error", err)
				}
			}()
		}
	}

	if rt.Audio != nil && rt.Audio.Input != nil && rt.Audio.Input.TurnDetectionSet {
		session.TurnDetection = rt.Audio.Input.TurnDetection
	}

	if rt.Audio != nil && rt.Audio.Input != nil && rt.Audio.Input.Format != nil && rt.Audio.Input.Format.PCM != nil {
		if rt.Audio.Input.Format.PCM.Rate > 0 {
			session.InputSampleRate = rt.Audio.Input.Format.PCM.Rate
		}
	}

	if rt.Audio != nil && rt.Audio.Output != nil && rt.Audio.Output.Format != nil && rt.Audio.Output.Format.PCM != nil {
		if rt.Audio.Output.Format.PCM.Rate > 0 {
			session.OutputSampleRate = rt.Audio.Output.Format.PCM.Rate
		}
	}

	if rt.Instructions != "" {
		session.Instructions = rt.Instructions
	}

	if rt.Tools != nil {
		// Manage Mode tools survive a client-driven session.update — the
		// alternative is silently dropping them whenever the user toggles
		// a client MCP server, which would break the modality mid-session.
		// Names from rt.Tools win on collision (the client is explicit;
		// we preserve, we don't override).
		merged := append([]types.ToolUnion(nil), rt.Tools...)
		seen := make(map[string]struct{}, len(merged))
		for _, t := range merged {
			if t.Function != nil {
				seen[t.Function.Name] = struct{}{}
			}
		}
		for _, t := range session.AssistantTools {
			if t.Function == nil {
				continue
			}
			if _, ok := seen[t.Function.Name]; ok {
				continue
			}
			merged = append(merged, t)
		}
		session.Tools = merged
	}
	if rt.ToolChoice != nil {
		session.ToolChoice = rt.ToolChoice
	}

	if rt.MaxOutputTokens != 0 {
		session.MaxOutputTokens = rt.MaxOutputTokens
	}

	if mods := modalitiesWithAlias(rt.OutputModalities, rt.Modalities); len(mods) > 0 {
		session.OutputModalities = mods
	}

	return nil
}

// decodeOpusLoop runs a ticker that drains buffered raw Opus frames from the
// session, decodes them in a single batched gRPC call, and appends the
// resulting PCM to InputAudioBuffer. This gives ~3 gRPC calls/sec instead of
// 50 (one per RTP packet) and keeps decode diagnostics once-per-batch.
func decodeOpusLoop(session *Session, opusBackend grpc.Backend, done chan struct{}) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			session.OpusFramesLock.Lock()
			frames := session.OpusFrames
			session.OpusFrames = nil
			session.OpusFramesLock.Unlock()
			if len(frames) == 0 {
				continue
			}

			result, err := opusBackend.AudioDecode(context.Background(), &proto.AudioDecodeRequest{
				Frames: frames,
				Options: map[string]string{
					"session_id": session.ID,
				},
			})
			if err != nil {
				xlog.Warn("opus decode batch error", "error", err, "frames", len(frames))
				continue
			}

			samples := sound.BytesToInt16sLE(result.PcmData)

			xlog.Debug("opus decode batch",
				"frames", len(frames),
				"decoded_samples", len(samples),
				"sample_rate", result.SampleRate,
			)

			// Resample from 48kHz to session input rate (16kHz) if needed
			if result.SampleRate != int32(session.InputSampleRate) {
				samples = sound.ResampleInt16(samples, int(result.SampleRate), session.InputSampleRate)
			}

			pcmBytes := sound.Int16toBytesLE(samples)
			session.AudioBufferLock.Lock()
			newSize := len(session.InputAudioBuffer) + len(pcmBytes)
			if newSize <= maxAudioBufferSize {
				session.InputAudioBuffer = append(session.InputAudioBuffer, pcmBytes...)
			}
			session.AudioBufferLock.Unlock()
		case <-done:
			return
		}
	}
}

// noSpeechHoldbackSec is how much of the tail of an inspected, segment-free
// buffer survives the periodic no-speech clear. It must cover the VAD's
// onset-detection latency: a word can already be underway in the newest part
// of the window without silero having crossed its threshold yet, and clearing
// it cuts the start of the utterance the next tick will detect.
const noSpeechHoldbackSec = 0.5

// dropInspectedPrefix removes the head of the audio buffer that a VAD tick
// inspected (the first inspected bytes), keeping the newest holdbackBytes of
// that window plus everything appended while the tick ran — audio the VAD
// never saw. When something is dropped the result is a fresh copy, never a
// sub-slice, so later appends can't scribble on memory shared with the old
// backing array; when nothing is dropped buf is returned unchanged.
func dropInspectedPrefix(buf []byte, inspected, holdbackBytes int) []byte {
	cut := inspected - holdbackBytes
	if cut <= 0 {
		return buf
	}
	if cut > len(buf) {
		cut = len(buf)
	}
	return append([]byte(nil), buf[cut:]...)
}

// handleVAD is a goroutine that listens for audio data from the client,
// runs VAD on the audio data, and commits utterances to the conversation.
//
// With turn_detection.type == "semantic_vad" (sv != nil below) the silero
// loop is augmented by a live transcription stream: the buffer's new audio
// is fed to the transcription model every tick and its end-of-utterance
// token switches the commit threshold between a short post-EOU window and
// the long eagerness fallback. The server_vad path is untouched.
func handleVAD(session *Session, conv *Conversation, t Transport, done chan struct{}) {
	vadContext, cancel := context.WithCancel(context.Background())
	go func() {
		<-done
		cancel()
	}()

	silenceThreshold := 0.5 // Default 500ms
	if session.TurnDetection != nil && session.TurnDetection.ServerVad != nil {
		silenceThreshold = float64(session.TurnDetection.ServerVad.SilenceDurationMs) / 1000
	}

	lts := newLiveTurnState(session, t)
	startTime := time.Now()

	// M2 turn-detection state machine. "Speech started" and "a turn's live ASR
	// stream is open" are ONE coordinator state (Idle/Speaking), so they cannot
	// desync the way the legacy speechStarted bool and lts.open() could (Part 2,
	// failure mode 4). See realtime_turncoord.go and turncoord/.
	sink := newTurnSink(session, conv, t, lts, vadContext, startTime)
	// Teardown: end any open turn through the coordinator (DiscardTurn closes the
	// live stream; no-op if already idle). Replaces the bare lts.discardTurn().
	defer func() {
		if err := sink.coord.Apply(turncoord.Abort{Reason: turncoord.AbortTeardown}); err != nil {
			xlog.Error("turncoord: abort(teardown) failed", "error", err)
		}
	}()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			// Semantic mode is re-read each tick: session.update can switch
			// turn-detection modes (and the retranscribe gate) mid-session.
			sessionLock.Lock()
			var sv *types.RealtimeSessionSemanticVad
			if session.TurnDetection != nil {
				sv = session.TurnDetection.SemanticVad
			}
			retranscribe := sv != nil && session.ModelConfig != nil &&
				session.ModelConfig.Pipeline.TurnDetectionRetranscribe()
			sessionLock.Unlock()

			// The turn coordinator's data-heavy effects (OpenTurn/CommitTurn)
			// need this tick's mode; set it before any Apply below.
			sink.sv = sv

			// session.update switched semantic -> server mid-turn: drop the
			// orphaned live stream. This is NOT a turn abort — the turn continues
			// under server_vad (a config change must not cut off a mid-utterance
			// speaker), so the coordinator stays Speaking; only the orphaned live
			// stream is closed.
			if sv == nil && lts.open() {
				lts.discardTurn()
			}

			session.AudioBufferLock.Lock()
			allAudio := make([]byte, len(session.InputAudioBuffer))
			copy(allAudio, session.InputAudioBuffer)
			session.AudioBufferLock.Unlock()

			aints := sound.BytesToInt16sLE(allAudio)
			if len(aints) == 0 || len(aints) < int(silenceThreshold*float64(session.InputSampleRate)) {
				continue
			}

			// Resample from InputSampleRate to 16kHz
			aints = sound.ResampleInt16(aints, session.InputSampleRate, localSampleRate)

			audioLength := float64(len(aints)) / localSampleRate

			if sv != nil && lts.open() {
				lts.feedNewAudio(aints)
				lts.drainEvents(audioLength)
			}

			segments, err := runVAD(vadContext, session, aints)
			if err != nil {
				if err.Error() == "unexpected speech end" {
					xlog.Debug("VAD cancelled")
					continue
				}
				xlog.Error("failed to process audio", "error", err)
				sendError(t, "processing_error", "Failed to process audio: "+err.Error(), "", "")
				continue
			}

			// NOTE: the no-speech clear and the min-buffer gate above stay on
			// the short silenceThreshold even in semantic mode — the eagerness
			// fallback applies only to the end-of-speech commit decision, or a
			// low eagerness would delay speech_started/barge-in by seconds.
			if len(segments) == 0 && audioLength > silenceThreshold {
				// "No segments" is not "no speech": silero (threshold 0.5)
				// crosses up to a few hundred ms into a soft word onset, so
				// the newest audio in the inspected window may be the start
				// of a word the next tick will recognize — and more audio
				// arrived while this tick ran. Keep both; drop only the
				// older, confirmed-silent head, or utterance onsets get cut.
				holdback := int(noSpeechHoldbackSec*float64(session.InputSampleRate)) * 2
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = dropInspectedPrefix(session.InputAudioBuffer, len(allAudio), holdback)
				session.AudioBufferLock.Unlock()

				// No-speech clear: end any open turn (Speaking -> Idle, discarding
				// the partial). Returning to Idle is the fix for failure mode 4 —
				// the legacy discardTurn left speechStarted true, suppressing the
				// next onset. Idle while not speaking is a no-op.
				if err := sink.coord.Apply(turncoord.Abort{Reason: turncoord.AbortNoSpeech}); err != nil {
					xlog.Error("turncoord: abort(no_speech) failed", "error", err)
				}
				continue
			} else if len(segments) == 0 {
				continue
			}

			// Speech detected this tick: open the turn (Idle -> Speaking) through
			// the coordinator. On that transition it opens the turn's live ASR
			// stream + feeds the buffered prefix (OpenTurn), cancels any in-flight
			// response (BargeIn, non-blocking — the VAD tick is never stalled), and
			// emits speech_started. While already Speaking it is a no-op, so "turn
			// open" and "speech started" can never disagree. The turn id is minted
			// here and carried by the coordinator through to the committed event.
			sink.onsetAudio = aints
			if err := sink.coord.Apply(turncoord.Onset{Turn: turncoord.TurnID(generateItemID())}); err != nil {
				xlog.Error("turncoord: onset failed", "error", err)
			}

			if sv != nil {
				// Drain again: events produced by THIS tick's feed have
				// usually arrived by the time runVAD returns, and leaving
				// them for the next tick adds 300ms to every EOU-triggered
				// commit.
				lts.drainEvents(audioLength)
			}

			// Segment still in progress when audio ended
			segEndTime := segments[len(segments)-1].End
			if segEndTime == 0 {
				continue
			}

			threshold := silenceThreshold
			eouPending := false
			if sv != nil {
				eouPending = lts.eouPending(segments)
				threshold = lts.thresholdSec(eouPending, sv)
			}

			if float32(audioLength)-segEndTime > float32(threshold) {
				if sv != nil {
					trigger, eouLag := lts.commitTrigger(eouPending, float64(segEndTime))
					xlog.Info("semantic_vad: committing turn",
						"trigger", trigger,
						"speech_end_s", segEndTime,
						"eou_lag_s", eouLag,
						"silence_s", audioLength-float64(segEndTime),
						"audio_s", audioLength)
				}
				// Retranscribe gate (semantic mode, EOU-triggered commits
				// only): cross-check the streamed EOU with an offline decode
				// of the buffered turn before committing. Runs synchronously
				// on the tick — the engine would serialize a concurrent feed
				// against it anyway. Timeout-triggered commits skip the gate.
				var gated *schema.TranscriptionResult
				if retranscribe && eouPending {
					batch, gerr := transcribeUtterance(vadContext, sound.Int16toBytesLE(aints), session)
					switch {
					case gerr != nil:
						xlog.Warn("semantic_vad: retranscribe gate failed; committing via the file path", "error", gerr)
					case !batch.Eou:
						xlog.Info("semantic_vad: batch decode did not confirm the streamed EOU; continuing to listen",
							"streamed", lts.previewText(), "batch", batch.Text)
						// The batch decode rejected the streamed EOU as a false
						// positive: consume the recorded EOU so the next tick
						// falls back to the eagerness window instead of
						// re-triggering on the same token.
						lts.eouAtSec = 0
						continue
					default:
						xlog.Info("semantic_vad: batch decode confirmed the streamed EOU",
							"streamed", lts.previewText(), "batch", batch.Text)
						gated = batch
					}
				}

				xlog.Debug("Detected end of speech segment")
				session.AudioBufferLock.Lock()
				// Keep audio appended while this tick ran — it belongs to
				// the next turn (in any mode: nil-ing it dropped the onset
				// of an utterance started right after a commit).
				session.InputAudioBuffer = dropInspectedPrefix(session.InputAudioBuffer, len(allAudio), 0)
				session.AudioBufferLock.Unlock()

				// Commit the turn through the coordinator: it emits speech_stopped
				// (EmitSpeechStopped) then the committed event, finalizes the live
				// stream, and issues the response (CommitTurn). The committed item
				// id is the coordinator's turn id (== the id the live captions
				// streamed under), so the client replaces the partial text.
				sink.commitAudio = sound.Int16toBytesLE(aints)
				sink.commitAudioLength = audioLength
				sink.commitRetranscribe = retranscribe
				sink.commitGated = gated
				// TODO: Remove prefix silence that is over TurnDetectionParams.PrefixPaddingMs
				if err := sink.coord.Apply(turncoord.Silence{}); err != nil {
					xlog.Error("turncoord: commit failed", "error", err)
				}
			}
		}
	}
}

func commitUtterance(ctx context.Context, utt []byte, session *Session, conv *Conversation, t Transport) {
	commitUtteranceWithTranscript(ctx, utt, nil, nil, "", session, conv, t)
}

// commitUtteranceWithTranscript commits one user turn. live carries the
// transcript semantic_vad's live stream already produced (its caption deltas
// were streamed to the client during the turn, so only the completed event
// is emitted here); gated carries the retranscribe gate's batch decode (the
// authoritative transcript in that mode). With neither — server_vad, manual
// commits, semantic degrade, or a live stream that heard nothing — the audio
// is written to a temp WAV and transcribed via the file path as before.
// itemID is the turn's conversation item id ("" mints a fresh one); it must
// match the id any live deltas were sent under.
func commitUtteranceWithTranscript(ctx context.Context, utt []byte, live *liveUtterance, gated *schema.TranscriptionResult, itemID string, session *Session, conv *Conversation, t Transport) {
	if len(utt) == 0 {
		return
	}

	f, err := os.CreateTemp("", "realtime-audio-chunk-*.wav")
	if err != nil {
		xlog.Error("failed to create temp file", "error", err)
		return
	}
	defer f.Close()
	defer os.Remove(f.Name())
	xlog.Debug("Writing to file", "file", f.Name())

	hdr := laudio.NewWAVHeader(uint32(len(utt)))
	if err := hdr.Write(f); err != nil {
		xlog.Error("Failed to write WAV header", "error", err)
		return
	}

	if _, err := f.Write(utt); err != nil {
		xlog.Error("Failed to write audio data", "error", err)
		return
	}

	f.Sync()

	// Start speaker verification concurrently with transcription. This is a
	// latency optimization only: there is a hard join below before the LLM, so
	// an unauthorized utterance never reaches generateResponse (no LLM, no
	// tools, no TTS) regardless of how fast transcription finishes. A rejected
	// turn wastes only transcription compute, which has no side effects. The
	// transcript is still emitted to the same peer that sent the audio, which
	// reveals nothing new to them.
	// Resolve the speaker when the gate must authorize this turn, or when identity
	// surfacing/personalization needs a fresh identity. Identity resolution
	// ignores the when:first short-circuit (that only skips re-authorization).
	type resolveOutcome struct {
		res resolution
		err error
	}
	var resolveCh chan resolveOutcome
	runResolve := false
	if session.voiceGate != nil && session.InputAudioTranscription != nil {
		enforce := session.voiceGate.cfg.EnforceGate()
		gateNeedsAuth := enforce
		if enforce && session.voiceGate.cfg.When == config.VoiceGateWhenFirst {
			session.gateMu.Lock()
			if session.voiceVerified {
				gateNeedsAuth = false
			}
			session.gateMu.Unlock()
		}
		if gateNeedsAuth || session.voiceGate.cfg.IdentityEnabled() {
			runResolve = true
			resolveCh = make(chan resolveOutcome, 1)
			wavPath := f.Name()
			go func() {
				r, rerr := session.voiceGate.Resolve(ctx, wavPath)
				resolveCh <- resolveOutcome{res: r, err: rerr}
			}()
		}
	}

	// TODO: If we have a real any-to-any model then transcription is optional

	// The turn's live captions (semantic_vad) already streamed under this
	// itemID; the completed event below reuses it so the client replaces the
	// partial text. server_vad / manual commits arrive with no itemID, so mint
	// one here.
	if itemID == "" {
		itemID = generateItemID()
	}

	var transcript string
	switch {
	case gated != nil:
		// semantic_vad retranscribe gate: the batch decode is authoritative.
		transcript = gated.Text
		if err := emitPrecomputedTranscription(t, itemID, nil, transcript); err != nil {
			sendError(t, "transcription_failed", err.Error(), "", "event_TODO")
			return
		}
	case live != nil && live.Text != "":
		// The caption deltas already streamed during the turn under this
		// itemID; the completed event replaces the partial text client-side.
		transcript = live.Text
		if err := emitPrecomputedTranscription(t, itemID, nil, transcript); err != nil {
			sendError(t, "transcription_failed", err.Error(), "", "event_TODO")
			return
		}
	case session.InputAudioTranscription != nil:
		// emitTranscription streams transcript deltas when
		// pipeline.streaming.transcription is set, otherwise emits a single
		// completed event; either way it returns the final transcript text.
		transcript, err = emitTranscription(ctx, t, session, itemID, f.Name())
		if err != nil {
			// Drain the gate goroutine before returning so its in-flight read of
			// the temp WAV finishes before the deferred os.Remove fires.
			if runResolve {
				<-resolveCh
			}
			sendError(t, "transcription_failed", err.Error(), "", "event_TODO")
			return
		}
	case session.SoundDetectionEnabled:
		// Sound-detection-only session: no transcription and no LLM. The
		// sound-detection emit below carries the result; there is no any-to-any
		// path to fall into. Windowing is client-driven (turn_detection none +
		// input_audio_buffer.commit), so this is not voice-gated.
	default:
		// The voice gate runs only on the transcription path above; if an
		// any-to-any model path is added here, join the gate before responding.
		sendNotImplemented(t, "any-to-any models")
		return
	}

	// Sound-event detection is additive to transcription: classify the same
	// committed window and emit its scored AudioSet tags as a separate event.
	// A failure here is logged but must never abort the turn.
	if session.SoundDetectionEnabled {
		if sderr := emitSoundDetection(ctx, t, session, generateItemID(), f.Name()); sderr != nil {
			xlog.Error("sound detection failed", "error", sderr)
		}
	}

	// Join on the resolution before any side-effecting step.
	var speaker *types.Speaker
	if runResolve {
		out := <-resolveCh
		enforce := session.voiceGate.cfg.EnforceGate()

		if out.err != nil {
			if enforce {
				// Fail closed: a gate that cannot decide must not let audio through.
				xlog.Error("voice recognition gate error", "error", out.err)
				if session.voiceGate.cfg.OnReject == config.VoiceGateRejectEvent {
					sendError(t, "speaker_not_authorized", "speaker not authorized: verification error", "", "event_TODO")
				}
				return
			}
			// Non-enforcing: degrade to an unknown speaker and continue.
			xlog.Warn("voice identity resolve failed; continuing as unknown speaker", "error", out.err)
		} else {
			s := out.res.speaker
			speaker = &s
		}

		if enforce {
			alreadyVerified := false
			if session.voiceGate.cfg.When == config.VoiceGateWhenFirst {
				session.gateMu.Lock()
				alreadyVerified = session.voiceVerified
				session.gateMu.Unlock()
			}
			allowed, reason := false, "verification error"
			if out.err == nil {
				allowed, reason = session.voiceGate.authorize(out.res)
			}
			proceed, markVerified := session.voiceGate.decide(alreadyVerified, allowed)
			if !proceed {
				xlog.Debug("voice recognition gate rejected utterance", "reason", reason)
				if session.voiceGate.cfg.OnReject == config.VoiceGateRejectEvent {
					sendError(t, "speaker_not_authorized", "speaker not authorized: "+reason, "", "event_TODO")
				}
				return
			}
			if markVerified {
				session.gateMu.Lock()
				session.voiceVerified = true
				session.gateMu.Unlock()
			}
			xlog.Debug("voice recognition gate authorized utterance", "speaker", out.res.speaker.Name)
		}
	}

	// Generate an LLM response only when there is a transcript to feed it. A
	// sound-detection-only session (no transcription) has no LLM stage, so it
	// stops here after emitting the sound-detection event.
	if session.InputAudioTranscription != nil && !session.TranscriptionOnly {
		generateResponse(ctx, session, utt, transcript, speaker, conv, t)
	}
}

// handleSoundWindow runs server-side windowed sound-event detection (option B):
// every HopMs it classifies the last WindowMs of streamed audio and emits a
// sound_detection event, so a sound-only client only has to stream audio (no
// input_audio_buffer.commit). It keeps the input buffer trimmed to one window
// so a long stream stays bounded. Runs until done is closed. This is
// independent of VAD: sound events are not speech.
func handleSoundWindow(session *Session, t Transport, done chan struct{}) {
	ticker := time.NewTicker(time.Duration(session.SoundDetectionHopMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			classifySoundWindow(session, t)
		}
	}
}

// classifySoundWindow is one windowing tick: it snapshots the most recent
// WindowMs of buffered audio (trimming the buffer so a long stream stays
// bounded) and, when there is enough, classifies it and emits a sound_detection
// event. Extracted from handleSoundWindow so it can be driven synchronously in
// tests.
func classifySoundWindow(session *Session, t Transport) {
	const bytesPerSample = 2 // 16-bit mono PCM
	sr := session.InputSampleRate
	windowBytes := session.SoundDetectionWindowMs * sr / 1000 * bytesPerSample
	minBytes := sr / 100 * bytesPerSample // ~10ms before classifying

	session.AudioBufferLock.Lock()
	// Keep only the most recent window so a long stream stays bounded.
	if windowBytes > 0 && len(session.InputAudioBuffer) > windowBytes {
		trimmed := make([]byte, windowBytes)
		copy(trimmed, session.InputAudioBuffer[len(session.InputAudioBuffer)-windowBytes:])
		session.InputAudioBuffer = trimmed
	}
	window := make([]byte, len(session.InputAudioBuffer))
	copy(window, session.InputAudioBuffer)
	session.AudioBufferLock.Unlock()

	if len(window) < minBytes {
		return // not enough audio buffered yet
	}
	path, err := writeWindowWAV(window, sr)
	if err != nil {
		xlog.Error("sound window: failed to write wav", "error", err)
		return
	}
	if sderr := emitSoundDetection(context.Background(), t, session, generateItemID(), path); sderr != nil {
		xlog.Error("sound window: detection failed", "error", sderr)
	}
	if rerr := os.Remove(path); rerr != nil {
		xlog.Debug("sound window: temp cleanup failed", "error", rerr)
	}
}

// writeWindowWAV writes mono 16-bit PCM to a temp WAV at the given sample rate
// (the ced classifier reads the declared rate and resamples). Returns the path;
// the caller removes it.
func writeWindowWAV(pcm []byte, sampleRate int) (string, error) {
	f, err := os.CreateTemp("", "realtime-sound-window-*.wav")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	hdr := laudio.NewWAVHeaderWithRate(uint32(len(pcm)), uint32(sampleRate))
	if err := hdr.Write(f); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if _, err := f.Write(pcm); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	_ = f.Sync()
	return f.Name(), nil
}

// writeUtteranceWAV persists raw 16 kHz mono PCM to a temp WAV for the
// file-based transcription paths. The caller must invoke cleanup.
func writeUtteranceWAV(utt []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "realtime-audio-chunk-*.wav")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	xlog.Debug("Writing to file", "file", f.Name())

	hdr := laudio.NewWAVHeader(uint32(len(utt)))
	if err := hdr.Write(f); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := f.Write(utt); err != nil {
		cleanup()
		return "", nil, err
	}
	_ = f.Sync()
	return f.Name(), cleanup, nil
}

// transcribeUtterance runs one offline (unary) decode of the buffered turn —
// the semantic_vad retranscribe gate. The result's Eou flag reports whether
// the batch decode also ended on the end-of-utterance token.
func transcribeUtterance(ctx context.Context, utt []byte, session *Session) (*schema.TranscriptionResult, error) {
	path, cleanup, err := writeUtteranceWAV(utt)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	language, prompt := "", ""
	if cfg := session.InputAudioTranscription; cfg != nil {
		language, prompt = cfg.Language, cfg.Prompt
	}
	tr, err := session.ModelInterface.Transcribe(ctx, path, language, false, false, prompt)
	if err != nil {
		return nil, err
	}
	if tr == nil {
		return nil, fmt.Errorf("transcribe result is nil")
	}
	return tr, nil
}

func runVAD(ctx context.Context, session *Session, adata []int16) ([]schema.VADSegment, error) {
	soundIntBuffer := &audio.IntBuffer{
		Format:         &audio.Format{SampleRate: localSampleRate, NumChannels: 1},
		SourceBitDepth: 16,
		Data:           sound.ConvertInt16ToInt(adata),
	}

	float32Data := soundIntBuffer.AsFloat32Buffer().Data

	resp, err := session.ModelInterface.VAD(ctx, &schema.VADRequest{
		Audio: float32Data,
	})
	if err != nil {
		return nil, err
	}

	// If resp.Segments is empty => no speech
	return resp.Segments, nil
}

// speakerNote renders the system-prompt note for the current speaker. Returns
// an empty string when there is no name and unknown notes are disabled.
func speakerNote(s *types.Speaker, noteUnknown bool) string {
	if s != nil && s.Matched && s.Name != "" {
		return "The current speaker is " + s.Name + "."
	}
	if noteUnknown {
		return "The current speaker is unknown."
	}
	return ""
}

// Function to generate a response based on the conversation
func generateResponse(ctx context.Context, session *Session, utt []byte, transcript string, speaker *types.Speaker, conv *Conversation, t Transport) {
	xlog.Debug("Generating realtime response...")

	// Create user message item
	item := types.MessageItemUnion{
		User: &types.MessageItemUser{
			ID:      generateItemID(),
			Status:  types.ItemStatusCompleted,
			Speaker: speaker,
			Content: []types.MessageContentInput{
				{
					Type:       types.MessageContentTypeInputAudio,
					Audio:      base64.StdEncoding.EncodeToString(utt),
					Transcript: transcript,
				},
			},
		},
	}
	conv.Lock.Lock()
	conv.Items = append(conv.Items, &item)
	conv.Lock.Unlock()

	sendEvent(t, types.ConversationItemAddedEvent{
		Item: item,
	})

	// Surface the recognized speaker to the client. Skip the event for an
	// unidentified speaker unless announce_unknown is set.
	if speaker != nil && session.voiceGate != nil && session.voiceGate.cfg.AnnounceEnabled() {
		if speaker.Matched || session.voiceGate.cfg.Identity.AnnounceUnknown {
			sendEvent(t, types.ConversationItemSpeakerEvent{
				ItemID:  item.User.ID,
				Speaker: *speaker,
			})
		}
	}

	triggerResponse(ctx, session, conv, t, nil)
}

// maxAssistantToolTurns caps the server-side agentic loop. Mirrors the
// chat-page maxToolTurns:10 from useChat.js — the model gets up to this
// many consecutive tool round-trips before we return control to the user
// without another response cycle.
const maxAssistantToolTurns = 10

// responseOutcome is how a response ended, decided by the response body and
// read once by triggerResponse to emit the single terminal event.
type responseOutcome int

const (
	outcomeCompleted responseOutcome = iota
	outcomeCancelled
	outcomeFailed // an error event was already sent; emit no terminal (legacy behavior)
)

// liveResponse accumulates the wire-visible result of ONE response.create across
// the whole agentic tool-turn recursion: a single id, the output items as they
// complete, the summed token usage, and the final outcome. triggerResponse owns
// it; triggerResponseAtTurn / streamLLMResponse / emitToolCallItems fill it in.
// This is what makes "exactly one response.done per response.create, with Output
// and Usage populated" true — the body no longer emits per-turn terminals.
type liveResponse struct {
	id      string
	output  []types.MessageItemUnion
	usage   backend.TokenUsage
	outcome responseOutcome
}

func (r *liveResponse) addItem(it types.MessageItemUnion) { r.output = append(r.output, it) }

func (r *liveResponse) addUsage(u backend.TokenUsage) {
	r.usage.Prompt += u.Prompt
	r.usage.Completion += u.Completion
}

// responseUsage maps the backend's token counts onto the OpenAI Realtime
// response.usage shape. Returns nil when there is nothing to report so the
// field is omitted rather than sent as zeros.
func responseUsage(u backend.TokenUsage) *types.TokenUsage {
	if u.Prompt == 0 && u.Completion == 0 {
		return nil
	}
	return &types.TokenUsage{
		InputTokens:  u.Prompt,
		OutputTokens: u.Completion,
		TotalTokens:  u.Prompt + u.Completion,
	}
}

func triggerResponse(ctx context.Context, session *Session, conv *Conversation, t Transport, overrides *types.ResponseCreateParams) {
	// One response.created and one response.done per response.create — even when
	// the server-side tool loop runs several inference turns. The per-turn
	// terminals the legacy code emitted (one response.done per turn, with empty
	// Output/Usage) are gone; tool turns are now internal to this single response.
	r := &liveResponse{id: generateUniqueID()}
	sendEvent(t, types.ResponseCreatedEvent{
		ServerEventBase: types.ServerEventBase{},
		Response: types.Response{
			ID:     r.id,
			Object: "realtime.response",
			Status: types.ResponseStatusInProgress,
		},
	})

	triggerResponseAtTurn(ctx, session, conv, t, overrides, 0, r)

	switch r.outcome {
	case outcomeCancelled:
		sendEvent(t, types.ResponseDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			Response: types.Response{
				ID:     r.id,
				Object: "realtime.response",
				Status: types.ResponseStatusCancelled,
				Output: r.output,
			},
		})
	case outcomeFailed:
		// A specific error event was already sent; emit no terminal (matches the
		// legacy behavior where failed responses had no response.done).
	default:
		sendEvent(t, types.ResponseDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			Response: types.Response{
				ID:     r.id,
				Object: "realtime.response",
				Status: types.ResponseStatusCompleted,
				Output: r.output,
				Usage:  responseUsage(r.usage),
			},
		})
	}

	// Fold aged-out turns into the rolling memory off the critical path; the
	// next turn reaps the smaller buffer.
	session.maybeCompact(conv)
}

func triggerResponseAtTurn(ctx context.Context, session *Session, conv *Conversation, t Transport, overrides *types.ResponseCreateParams, toolTurn int, r *liveResponse) {
	config := session.ModelInterface.PredictConfig()

	// Default values
	tools := session.Tools
	toolChoice := session.ToolChoice
	instructions := session.Instructions
	maxOutputTokens := session.MaxOutputTokens
	// Overrides
	if overrides != nil {
		if overrides.Tools != nil {
			tools = overrides.Tools
		}
		if overrides.ToolChoice != nil {
			toolChoice = overrides.ToolChoice
		}
		if overrides.Instructions != "" {
			instructions = overrides.Instructions
		}
		if overrides.MaxOutputTokens != 0 {
			maxOutputTokens = overrides.MaxOutputTokens
		}
	}

	// Apply MaxOutputTokens to model config if specified
	// Save original value to restore after prediction
	var originalMaxTokens *int
	if config != nil {
		originalMaxTokens = config.Maxtokens
		if maxOutputTokens != 0 && !maxOutputTokens.IsInf() {
			tokenValue := int(maxOutputTokens)
			config.Maxtokens = &tokenValue
			xlog.Debug("Applied max_output_tokens to config", "value", tokenValue)
		}
	}
	// Defer restoration of original value
	defer func() {
		if config != nil {
			config.Maxtokens = originalMaxTokens
		}
	}()

	var conversationHistory schema.Messages
	conversationHistory = append(conversationHistory, schema.Message{
		Role:          string(types.MessageRoleSystem),
		StringContent: instructions,
		Content:       instructions,
	})

	imgIndex := 0
	var lastUserSpeaker *types.Speaker
	personalize := session.voiceGate != nil && session.voiceGate.cfg.PersonalizeEnabled()
	conv.Lock.Lock()
	conversationHistory = withMemory(conversationHistory, conv.Memory)
	items := trimRealtimeItems(conv.Items, session.MaxHistoryItems)
	for _, item := range items {
		if item.User != nil {
			msg := schema.Message{
				Role: string(types.MessageRoleUser),
			}
			lastUserSpeaker = item.User.Speaker
			if personalize && session.voiceGate.cfg.Identity.InjectName &&
				item.User.Speaker != nil && item.User.Speaker.Matched && item.User.Speaker.Name != "" {
				msg.Name = item.User.Speaker.Name
			}
			textContent := ""
			nrOfImgsInMessage := 0
			for _, content := range item.User.Content {
				switch content.Type {
				case types.MessageContentTypeInputText:
					textContent += content.Text
				case types.MessageContentTypeInputAudio:
					textContent += content.Transcript
				case types.MessageContentTypeInputImage:
					img, err := utils.GetContentURIAsBase64(content.ImageURL)
					if err != nil {
						xlog.Warn("Failed to process image", "error", err)
						continue
					}
					msg.StringImages = append(msg.StringImages, img)
					imgIndex++
					nrOfImgsInMessage++
				}
			}
			if nrOfImgsInMessage > 0 && !config.TemplateConfig.UseTokenizerTemplate {
				templated, err := templates.TemplateMultiModal(config.TemplateConfig.Multimodal, templates.MultiModalOptions{
					TotalImages:     imgIndex,
					ImagesInMessage: nrOfImgsInMessage,
				}, textContent)
				if err != nil {
					xlog.Warn("Failed to apply multimodal template", "error", err)
					templated = textContent
				}
				msg.StringContent = templated
				msg.Content = templated
			} else {
				msg.StringContent = textContent
				msg.Content = textContent
			}
			conversationHistory = append(conversationHistory, msg)
		} else if item.Assistant != nil {
			for _, content := range item.Assistant.Content {
				switch content.Type {
				case types.MessageContentTypeOutputText:
					conversationHistory = append(conversationHistory, schema.Message{
						Role:          string(types.MessageRoleAssistant),
						StringContent: content.Text,
						Content:       content.Text,
					})
				case types.MessageContentTypeOutputAudio:
					conversationHistory = append(conversationHistory, schema.Message{
						Role:          string(types.MessageRoleAssistant),
						StringContent: content.Transcript,
						Content:       content.Transcript,
						StringAudios:  []string{content.Audio},
					})
				}
			}
		} else if item.System != nil {
			for _, content := range item.System.Content {
				conversationHistory = append(conversationHistory, schema.Message{
					Role:          string(types.MessageRoleSystem),
					StringContent: content.Text,
					Content:       content.Text,
				})
			}
		} else if item.FunctionCall != nil {
			conversationHistory = append(conversationHistory, schema.Message{
				Role: string(types.MessageRoleAssistant),
				ToolCalls: []schema.ToolCall{
					{
						ID:   item.FunctionCall.CallID,
						Type: "function",
						FunctionCall: schema.FunctionCall{
							Name:      item.FunctionCall.Name,
							Arguments: item.FunctionCall.Arguments,
						},
					},
				},
			})
		} else if item.FunctionCallOutput != nil {
			conversationHistory = append(conversationHistory, schema.Message{
				Role:          "tool",
				Name:          item.FunctionCallOutput.CallID,
				Content:       item.FunctionCallOutput.Output,
				StringContent: item.FunctionCallOutput.Output,
			})
		}
	}
	conv.Lock.Unlock()

	if personalize && session.voiceGate.cfg.Identity.InjectSystemNote {
		if note := speakerNote(lastUserSpeaker, session.voiceGate.cfg.Identity.NoteUnknown); note != "" {
			conversationHistory[0].StringContent += "\n\n" + note
			conversationHistory[0].Content = conversationHistory[0].StringContent
		}
	}

	var images []string
	for _, m := range conversationHistory {
		images = append(images, m.StringImages...)
	}

	// response.created/done are emitted once per response.create by triggerResponse;
	// every turn (including agentic recursion) shares this id.
	responseID := r.id

	// Streamed LLM path: when the pipeline opts into LLM streaming, stream the
	// transcript to the client as it is generated and synthesize the buffered
	// message once. Tool turns are supported only when the model uses its
	// tokenizer template: the C++ autoparser then delivers content and tool
	// calls via ChatDeltas (clearing the text stream), so the spoken transcript
	// never leaks tool-call tokens. Grammar-based function calling emits the
	// call as JSON in the token stream, so those turns keep the buffered path.
	if config != nil && session.ModelConfig != nil && session.ModelConfig.Pipeline.StreamLLM() {
		canStream := len(tools) == 0 || config.TemplateConfig.UseTokenizerTemplate
		var respMods []types.Modality
		if overrides != nil {
			respMods = modalitiesWithAlias(overrides.OutputModalities, overrides.Modalities)
		}
		if canStream && modalitiesContainAudio(resolveOutputModalities(session.OutputModalities, respMods)) {
			if streamLLMResponse(ctx, session, conv, t, r, conversationHistory, images, config, tools, toolChoice, toolTurn) {
				return
			}
		}
	}

	predFunc, err := session.ModelInterface.Predict(ctx, conversationHistory, images, nil, nil, nil, tools, toolChoice, nil, nil, nil)
	if err != nil {
		sendError(t, "inference_failed", fmt.Sprintf("backend error: %v", err), "", "") // item.Assistant.ID is unknown here
		r.outcome = outcomeFailed
		return
	}

	pred, err := predFunc()
	if err != nil {
		sendError(t, "prediction_failed", fmt.Sprintf("backend error: %v", err), "", "")
		r.outcome = outcomeFailed
		return
	}
	r.addUsage(pred.Usage)

	// Check for cancellation after LLM inference (barge-in may have fired)
	if ctx.Err() != nil {
		xlog.Debug("Response cancelled after LLM inference (barge-in)")
		r.outcome = outcomeCancelled
		return
	}

	xlog.Debug("Function config for parsing", "function_name_key", config.FunctionsConfig.FunctionNameKey, "function_arguments_key", config.FunctionsConfig.FunctionArgumentsKey)
	xlog.Debug("LLM raw response", "text", pred.Response, "response_length", len(pred.Response), "usage", pred.Usage)

	// Safely dereference pointer fields for logging
	maxTokens := "nil"
	if config.Maxtokens != nil {
		maxTokens = fmt.Sprintf("%d", *config.Maxtokens)
	}
	contextSize := "nil"
	if config.ContextSize != nil {
		contextSize = fmt.Sprintf("%d", *config.ContextSize)
	}
	xlog.Debug("Model parameters", "max_tokens", maxTokens, "context_size", contextSize, "stopwords", config.StopWords)

	rawResponse := pred.Response
	if config.TemplateConfig.ReplyPrefix != "" {
		rawResponse = config.TemplateConfig.ReplyPrefix + rawResponse
	}

	// Detect thinking start token from template for reasoning extraction
	var template string
	if config.TemplateConfig.UseTokenizerTemplate {
		template = config.GetModelTemplate()
	} else {
		template = config.TemplateConfig.Chat
	}
	thinkingStartToken := reasoning.DetectThinkingStartToken(template, &config.ReasoningConfig)

	// When the C++ autoparser emitted ChatDeltas with actionable data,
	// prefer them — the backend clears Reply.Message in that path and
	// delivers parsed content/reasoning/tool-calls via the delta stream
	// (see pkg/functions/chat_deltas.go, mirrored from chat.go's non-SSE
	// handling). Without this, Response is empty and realtime would
	// synthesize silence for replies that actually produced tokens.
	var reasoningText, responseWithoutReasoning, textContent, cleanedResponse string
	var toolCalls []functions.FuncCallResults
	deltaToolCalls := functions.ToolCallsFromChatDeltas(pred.ChatDeltas)
	deltaContent := functions.ContentFromChatDeltas(pred.ChatDeltas)
	deltaReasoning := functions.ReasoningFromChatDeltas(pred.ChatDeltas)
	if len(deltaToolCalls) > 0 || deltaContent != "" {
		xlog.Debug("[ChatDeltas] realtime: using C++ autoparser deltas",
			"tool_calls", len(deltaToolCalls),
			"content_len", len(deltaContent),
			"reasoning_len", len(deltaReasoning))
		// Issue #9985: when the autoparser only delivered content (no
		// reasoning_content), it may be running in the "pure content"
		// PEG fallback (non-jinja path) which leaves <think>…</think>
		// embedded in the content. Run Go-side extraction defensively.
		// ExtractReasoningWithConfig is a no-op when no tag pair matches,
		// so it's safe to apply unconditionally in the no-reasoning branch.
		if deltaReasoning == "" && deltaContent != "" {
			deltaReasoning, deltaContent = reasoning.ExtractReasoningComplete(deltaContent, thinkingStartToken, spokenReasoningConfig(config.ReasoningConfig))
		}
		reasoningText = deltaReasoning
		responseWithoutReasoning = deltaContent
		textContent = deltaContent
		cleanedResponse = deltaContent
		toolCalls = deltaToolCalls
	} else {
		reasoningText, responseWithoutReasoning = reasoning.ExtractReasoningComplete(rawResponse, thinkingStartToken, spokenReasoningConfig(config.ReasoningConfig))
		textContent = functions.ParseTextContent(responseWithoutReasoning, config.FunctionsConfig)
		cleanedResponse = functions.CleanupLLMResult(responseWithoutReasoning, config.FunctionsConfig)
		toolCalls = functions.ParseFunctionCall(cleanedResponse, config.FunctionsConfig)
	}
	xlog.Debug("LLM Response", "reasoning", reasoningText, "response_without_reasoning", responseWithoutReasoning)

	xlog.Debug("Function call parsing", "textContent", textContent, "cleanedResponse", cleanedResponse, "toolCallsCount", len(toolCalls))

	noActionName := "answer"
	if config.FunctionsConfig.NoActionFunctionName != "" {
		noActionName = config.FunctionsConfig.NoActionFunctionName
	}
	isNoAction := len(toolCalls) > 0 && toolCalls[0].Name == noActionName

	var finalSpeech string
	var finalToolCalls []functions.FuncCallResults

	if isNoAction {
		arg := toolCalls[0].Arguments
		arguments := map[string]any{}
		if err := json.Unmarshal([]byte(arg), &arguments); err == nil {
			if m, exists := arguments["message"]; exists {
				if message, ok := m.(string); ok {
					finalSpeech = message
				} else {
					xlog.Warn("NoAction function message field is not a string", "type", fmt.Sprintf("%T", m))
				}
			} else {
				xlog.Warn("NoAction function missing 'message' field in arguments")
			}
		} else {
			xlog.Warn("Failed to unmarshal NoAction function arguments", "error", err, "arguments", arg)
		}
		if finalSpeech == "" {
			// Fallback if parsing failed
			xlog.Warn("NoAction function did not produce speech, using cleaned response as fallback")
			finalSpeech = cleanedResponse
		}
	} else {
		finalToolCalls = toolCalls
		xlog.Debug("Setting finalToolCalls", "count", len(finalToolCalls))
		if len(toolCalls) > 0 {
			finalSpeech = textContent
		} else {
			finalSpeech = cleanedResponse
		}
	}

	if finalSpeech != "" {
		// Create the assistant item now that we have content
		item := types.MessageItemUnion{
			Assistant: &types.MessageItemAssistant{
				ID:     generateItemID(),
				Status: types.ItemStatusInProgress,
				Content: []types.MessageContentOutput{
					{
						Type:       types.MessageContentTypeOutputAudio,
						Transcript: finalSpeech,
					},
				},
			},
		}

		conv.Lock.Lock()
		conv.Items = append(conv.Items, &item)
		conv.Lock.Unlock()

		sendEvent(t, types.ResponseOutputItemAddedEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			OutputIndex:     0,
			Item:            item,
		})

		sendEvent(t, types.ResponseContentPartAddedEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          item.Assistant.ID,
			OutputIndex:     0,
			ContentIndex:    0,
			Part:            item.Assistant.Content[0],
		})

		// removeItemFromConv removes the last occurrence of an item with
		// the given assistant ID from conversation history.
		removeItemFromConv := func(assistantID string) {
			conv.Lock.Lock()
			for i := len(conv.Items) - 1; i >= 0; i-- {
				if conv.Items[i].Assistant != nil && conv.Items[i].Assistant.ID == assistantID {
					conv.Items = append(conv.Items[:i], conv.Items[i+1:]...)
					break
				}
			}
			conv.Lock.Unlock()
		}

		// sendCancelledResponse records the cancelled outcome (triggerResponse
		// emits the single terminal) and cleans up the partial assistant item so
		// the interrupted reply is not in chat history.
		sendCancelledResponse := func() {
			removeItemFromConv(item.Assistant.ID)
			r.outcome = outcomeCancelled
		}

		var audioString string
		_, isWebRTC := t.(*WebRTCTransport)
		var respMods []types.Modality
		if overrides != nil {
			respMods = modalitiesWithAlias(overrides.OutputModalities, overrides.Modalities)
		}
		modalities := resolveOutputModalities(session.OutputModalities, respMods)
		if modalitiesContainAudio(modalities) {
			// Check for cancellation before TTS
			if ctx.Err() != nil {
				xlog.Debug("Response cancelled before TTS (barge-in)")
				sendCancelledResponse()
				return
			}

			// Transcript of the spoken reply (the audio's text).
			sendEvent(t, types.ResponseOutputAudioTranscriptDeltaEvent{
				ServerEventBase: types.ServerEventBase{},
				ResponseID:      responseID,
				ItemID:          item.Assistant.ID,
				OutputIndex:     0,
				ContentIndex:    0,
				Delta:           finalSpeech,
			})
			sendEvent(t, types.ResponseOutputAudioTranscriptDoneEvent{
				ServerEventBase: types.ServerEventBase{},
				ResponseID:      responseID,
				ItemID:          item.Assistant.ID,
				OutputIndex:     0,
				ContentIndex:    0,
				Transcript:      finalSpeech,
			})

			// Synthesize and send the audio. With pipeline.streaming.tts enabled
			// emitSpeech forwards a response.output_audio.delta per backend PCM
			// chunk as it's produced; otherwise it sends the whole utterance as a
			// single delta. The returned PCM is stored (base64) on the item below.
			pcmAudio, err := emitSpeech(ctx, t, session, responseID, item.Assistant.ID, finalSpeech)
			if err != nil {
				if ctx.Err() != nil {
					xlog.Debug("TTS cancelled (barge-in)")
					sendCancelledResponse()
					return
				}
				xlog.Error("TTS failed", "error", err)
				sendError(t, "tts_error", fmt.Sprintf("TTS generation failed: %v", err), "", item.Assistant.ID)
				r.outcome = outcomeFailed
				return
			}
			if !isWebRTC {
				audioString = base64.StdEncoding.EncodeToString(pcmAudio)
			}

			if !isWebRTC {
				sendEvent(t, types.ResponseOutputAudioDoneEvent{
					ServerEventBase: types.ServerEventBase{},
					ResponseID:      responseID,
					ItemID:          item.Assistant.ID,
					OutputIndex:     0,
					ContentIndex:    0,
				})
			}
		} else {
			// Text-only mode: skip TTS, emit only the text events.
			sendEvent(t, types.ResponseOutputTextDeltaEvent{
				ServerEventBase: types.ServerEventBase{},
				ResponseID:      responseID,
				ItemID:          item.Assistant.ID,
				OutputIndex:     0,
				ContentIndex:    0,
				Delta:           finalSpeech,
			})
			sendEvent(t, types.ResponseOutputTextDoneEvent{
				ServerEventBase: types.ServerEventBase{},
				ResponseID:      responseID,
				ItemID:          item.Assistant.ID,
				OutputIndex:     0,
				ContentIndex:    0,
				Text:            finalSpeech,
			})
		}

		sendEvent(t, types.ResponseContentPartDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          item.Assistant.ID,
			OutputIndex:     0,
			ContentIndex:    0,
			Part:            item.Assistant.Content[0],
		})

		conv.Lock.Lock()
		item.Assistant.Status = types.ItemStatusCompleted
		if !isWebRTC {
			item.Assistant.Content[0].Audio = audioString
		}
		conv.Lock.Unlock()

		sendEvent(t, types.ResponseOutputItemDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			OutputIndex:     0,
			Item:            item,
		})
		r.addItem(item)
	}

	// Emit the parsed tool calls and (for server-side assistant tools) the
	// follow-up turn. Shared with the streamed path so both finalize tool calls
	// identically. The single terminal is emitted by triggerResponse.
	emitToolCallItems(ctx, session, conv, t, r, finalToolCalls, finalSpeech != "", toolTurn)
}

// emitToolCallItems emits the realtime function_call items for the parsed tool
// calls, the terminal response.done, and — for server-side LocalAI Assistant
// tools — re-triggers a follow-up response so the model can speak the result.
// hasContent shifts the tool-call output index past the assistant content item
// when the same turn also produced spoken/text content. Two tool paths:
//   - LocalAI Assistant tools (session.AssistantExecutor.IsTool) run server-side;
//     we append both the call and its output to conv.Items and re-trigger. The
//     client only sees observability events.
//   - All other tools follow the standard OpenAI flow: emit
//     function_call_arguments.done and wait for the client to send
//     conversation.item.create back.
func emitToolCallItems(ctx context.Context, session *Session, conv *Conversation, t Transport, r *liveResponse, toolCalls []functions.FuncCallResults, hasContent bool, toolTurn int) {
	responseID := r.id
	xlog.Debug("About to handle tool calls", "finalToolCallsCount", len(toolCalls))
	executedAssistantTool := false
	for i, tc := range toolCalls {
		toolCallID := generateItemID()
		callID := "call_" + generateUniqueID() // OpenAI uses call_xyz

		// Create FunctionCall Item
		fcItem := types.MessageItemUnion{
			FunctionCall: &types.MessageItemFunctionCall{
				ID:        toolCallID,
				CallID:    callID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
				Status:    types.ItemStatusCompleted,
			},
		}

		conv.Lock.Lock()
		conv.Items = append(conv.Items, &fcItem)
		conv.Lock.Unlock()

		outputIndex := i
		if hasContent {
			outputIndex++
		}

		sendEvent(t, types.ResponseOutputItemAddedEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			OutputIndex:     outputIndex,
			Item:            fcItem,
		})

		serverSide := session.AssistantExecutor != nil && session.AssistantExecutor.IsTool(tc.Name)
		if serverSide {
			output, execErr := session.AssistantExecutor.ExecuteTool(ctx, tc.Name, tc.Arguments)
			if execErr != nil {
				output = "Error: " + execErr.Error()
				xlog.Error("realtime: assistant tool execution failed", "tool", tc.Name, "error", execErr)
			}
			foItem := types.MessageItemUnion{
				FunctionCallOutput: &types.MessageItemFunctionCallOutput{
					ID:     generateItemID(),
					CallID: callID,
					Output: output,
					Status: types.ItemStatusCompleted,
				},
			}
			conv.Lock.Lock()
			conv.Items = append(conv.Items, &foItem)
			conv.Lock.Unlock()
			// Close the call out and emit the output as its own paired
			// added/done — the OpenAI spec pairs every item-done with a
			// preceding item-added, so we re-pair here for the output.
			// The UI renders the transcript entry on item.done for both
			// shapes (FunctionCall + FunctionCallOutput).
			sendEvent(t, types.ResponseOutputItemDoneEvent{
				ServerEventBase: types.ServerEventBase{},
				ResponseID:      responseID,
				OutputIndex:     outputIndex,
				Item:            fcItem,
			})
			r.addItem(fcItem)
			sendEvent(t, types.ResponseOutputItemAddedEvent{
				ServerEventBase: types.ServerEventBase{},
				ResponseID:      responseID,
				OutputIndex:     outputIndex,
				Item:            foItem,
			})
			sendEvent(t, types.ResponseOutputItemDoneEvent{
				ServerEventBase: types.ServerEventBase{},
				ResponseID:      responseID,
				OutputIndex:     outputIndex,
				Item:            foItem,
			})
			r.addItem(foItem)
			executedAssistantTool = true
			continue
		}

		sendEvent(t, types.ResponseFunctionCallArgumentsDeltaEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          toolCallID,
			OutputIndex:     outputIndex,
			CallID:          callID,
			Delta:           tc.Arguments,
		})

		sendEvent(t, types.ResponseFunctionCallArgumentsDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          toolCallID,
			OutputIndex:     outputIndex,
			CallID:          callID,
			Arguments:       tc.Arguments,
			Name:            tc.Name,
		})

		sendEvent(t, types.ResponseOutputItemDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			OutputIndex:     outputIndex,
			Item:            fcItem,
		})
		r.addItem(fcItem)
	}

	// No terminal here: triggerResponse emits the single response.done once the
	// whole turn (including the agentic recursion below) completes.

	// If we executed any assistant tools inproc, run another response cycle
	// so the model can speak the result. Mirrors the chat-side agentic loop
	// but driven server-side rather than by client round-trip. Bounded so a
	// degenerate "model keeps calling tools" doesn't blow the stack. The
	// follow-up turn shares the same liveResponse, so its output accumulates
	// into the one response.done.
	if executedAssistantTool {
		if toolTurn+1 >= maxAssistantToolTurns {
			xlog.Warn("realtime: assistant tool-turn limit reached, stopping the agentic loop",
				"limit", maxAssistantToolTurns, "model", session.Model)
			return
		}
		triggerResponseAtTurn(ctx, session, conv, t, nil, toolTurn+1, r)
	}
}

// Helper functions to generate unique IDs
func generateSessionID() string {
	// Generate a unique session ID
	// Implement as needed
	return "sess_" + generateUniqueID()
}

func generateConversationID() string {
	// Generate a unique conversation ID
	// Implement as needed
	return "conv_" + generateUniqueID()
}

func generateItemID() string {
	// Generate a unique item ID
	// Implement as needed
	return "item_" + generateUniqueID()
}

func generateUniqueID() string {
	// 16 random bytes, hex-encoded. Must be collision-free: session, item,
	// response and call IDs build on this, and the conversation tracks/removes
	// items by ID (e.g. cancel() in realtime_stream.go, conversation.item.retrieve).
	// A constant would make every ID alias and corrupt that bookkeeping.
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
