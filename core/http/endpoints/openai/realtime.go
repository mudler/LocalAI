package openai

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
	Tools                   []types.ToolUnion
	ToolChoice              *types.ToolChoiceUnion
	Conversations           map[string]*Conversation
	InputAudioBuffer        []byte
	AudioBufferLock         sync.Mutex
	OpusFrames              [][]byte
	OpusFramesLock          sync.Mutex
	Instructions            string
	DefaultConversationID   string
	ModelInterface          Model
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

	// Response cancellation: protects activeResponseCancel/activeResponseDone
	responseMu           sync.Mutex
	activeResponseCancel context.CancelFunc
	activeResponseDone   chan struct{}
}

// cancelActiveResponse cancels any in-flight response and waits for its
// goroutine to exit. This ensures we never have overlapping responses and
// that interrupted responses are fully cleaned up before starting a new one.
func (s *Session) cancelActiveResponse() {
	s.responseMu.Lock()
	cancel := s.activeResponseCancel
	done := s.activeResponseDone
	s.responseMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// startResponse cancels any active response and returns a new context for
// the replacement response. The caller MUST close the returned done channel
// when the response goroutine exits.
func (s *Session) startResponse(parent context.Context) (context.Context, chan struct{}) {
	s.cancelActiveResponse()

	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})

	s.responseMu.Lock()
	s.activeResponseCancel = cancel
	s.activeResponseDone = done
	s.responseMu.Unlock()

	return ctx, done
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
	PredictConfig() *config.ModelConfig
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

	if cfg.Pipeline.VAD == "" && cfg.Pipeline.Transcription == "" && cfg.Pipeline.TTS == "" && cfg.Pipeline.LLM == "" {
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
		ID:                sessionID,
		TranscriptionOnly: false,
		Model:             model,
		Voice:             cfg.TTSConfig.Voice,
		Instructions:      instructions,
		ModelConfig:       cfg,
		Tools:             assistantTools,
		AssistantTools:    assistantTools,
		AssistantExecutor: assistantExecutor,
		TurnDetection: &types.TurnDetectionUnion{
			ServerVad: &types.ServerVad{
				Threshold:         0.5,
				PrefixPaddingMs:   300,
				SilenceDurationMs: 500,
				CreateResponse:    true,
			},
		},
		InputAudioTranscription: &types.AudioTranscription{
			Model: sttModel,
		},
		Conversations:    make(map[string]*Conversation),
		InputSampleRate:  defaultRemoteSampleRate,
		OutputSampleRate: defaultRemoteSampleRate,
		MaxHistoryItems:  defaultMaxHistoryItems(cfg),
	}

	// Create a default conversation
	conversationID := generateConversationID()
	conversation := &Conversation{
		ID: conversationID,
		// TODO: We need to truncate the conversation items when a new item is added and we have run out of space. There are multiple places where items
		//       can be added so we could use a datastructure here that enforces truncation upon addition
		Items: []*types.MessageItemUnion{},
	}
	session.Conversations[conversationID] = conversation
	session.DefaultConversationID = conversationID

	m, err := newModel(
		&cfg.Pipeline,
		application.ModelConfigLoader(),
		application.ModelLoader(),
		application.ApplicationConfig(),
		evaluator,
	)
	if err != nil {
		xlog.Error("failed to load model", "error", err)
		sendError(t, "model_load_error", "Failed to load model", "", "")
		return
	}
	session.ModelInterface = m

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
		msg  []byte
		wg   sync.WaitGroup
		done = make(chan struct{})
	)

	vadServerStarted := false
	toggleVAD := func() {
		if session.TurnDetection != nil && session.TurnDetection.ServerVad != nil && !vadServerStarted {
			xlog.Debug("Starting VAD goroutine...")
			done = make(chan struct{})
			wg.Go(func() {
				conversation := session.Conversations[session.DefaultConversationID]
				handleVAD(session, conversation, t, done)
			})
			vadServerStarted = true
		} else if (session.TurnDetection == nil || session.TurnDetection.ServerVad == nil) && vadServerStarted {
			xlog.Debug("Stopping VAD goroutine...")
			close(done)
			vadServerStarted = false
		}
	}

	// For WebRTC sessions, start the Opus decode loop before VAD so that
	// decoded PCM is already flowing when VAD's first tick fires.
	var decodeDone chan struct{}
	if wt, ok := t.(*WebRTCTransport); ok {
		decodeDone = make(chan struct{})
		go decodeOpusLoop(session, wt.opusBackend, decodeDone)
	}

	toggleVAD()

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
			isServerVAD := session.TurnDetection != nil && session.TurnDetection.ServerVad != nil
			sessionLock.Unlock()

			// TODO: At the least need to check locking and timer state in the VAD Go routine before allowing this
			if isServerVAD {
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

			respCtx, respDone := session.startResponse(context.Background())
			go func() {
				defer close(respDone)
				commitUtterance(respCtx, allAudio, session, conversation, t)
			}()

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
			sendError(t, "not_implemented", "Deleting items not implemented", "", "event_TODO")

		case types.ConversationItemRetrieveEvent:
			xlog.Debug("recv", "message", string(msg))

			if e.ItemID == "" {
				sendError(t, "invalid_item_id", "Need item_id, but none specified", "", "event_TODO")
				continue
			}

			conversation.Lock.Lock()
			var retrievedItem types.MessageItemUnion
			for _, item := range conversation.Items {
				// We need to check ID in the union
				var id string
				if item.System != nil {
					id = item.System.ID
				} else if item.User != nil {
					id = item.User.ID
				} else if item.Assistant != nil {
					id = item.Assistant.ID
				} else if item.FunctionCall != nil {
					id = item.FunctionCall.ID
				} else if item.FunctionCallOutput != nil {
					id = item.FunctionCallOutput.ID
				}

				if id == e.ItemID {
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

			respCtx, respDone := session.startResponse(context.Background())
			go func() {
				defer close(respDone)
				triggerResponse(respCtx, session, conversation, t, &e.Response)
			}()

		case types.ResponseCancelEvent:
			xlog.Debug("recv", "message", string(msg))
			session.cancelActiveResponse()

		default:
			xlog.Error("unknown message type")
			// sendError(t, "unknown_message_type", fmt.Sprintf("Unknown message type: %s", incomingMsg.Type), "", "")
		}
	}

	// Cancel any in-flight response before tearing down
	session.cancelActiveResponse()

	// Stop the Opus decode goroutine (if running)
	if decodeDone != nil {
		close(decodeDone)
	}

	// Signal any running VAD goroutine to exit.
	if vadServerStarted {
		close(done)
	}
	wg.Wait()

	// Remove the session from the sessions map
	sessionLock.Lock()
	delete(sessions, sessionID)
	sessionLock.Unlock()
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

func updateSession(session *Session, update *types.SessionUnion, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, evaluator *templates.Evaluator) error {
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
		session.InputAudioTranscription = rt.Audio.Input.Transcription
		session.ModelConfig.Pipeline.Transcription = rt.Audio.Input.Transcription.Model
	}

	if rt.Model != "" || (rt.Audio != nil && rt.Audio.Output != nil && rt.Audio.Output.Voice != "") || (rt.Audio != nil && rt.Audio.Input != nil && rt.Audio.Input.Transcription != nil) {
		m, err := newModel(&session.ModelConfig.Pipeline, cl, ml, appConfig, evaluator)
		if err != nil {
			return err
		}
		session.ModelInterface = m
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

	if len(rt.OutputModalities) > 0 {
		session.OutputModalities = rt.OutputModalities
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

// handleVAD is a goroutine that listens for audio data from the client,
// runs VAD on the audio data, and commits utterances to the conversation
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

	speechStarted := false
	startTime := time.Now()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
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

			audioLength := float64(len(aints)) / localSampleRate

			// TODO: When resetting the buffer we should retain a small postfix
			if len(segments) == 0 && audioLength > silenceThreshold {
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = nil
				session.AudioBufferLock.Unlock()

				continue
			} else if len(segments) == 0 {
				continue
			}

			if !speechStarted {
				// Barge-in: cancel any in-flight response so we stop
				// sending audio and don't keep the interrupted reply in history.
				session.cancelActiveResponse()

				sendEvent(t, types.InputAudioBufferSpeechStartedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
					},
					AudioStartMs: time.Since(startTime).Milliseconds(),
				})
				speechStarted = true
			}

			// Segment still in progress when audio ended
			segEndTime := segments[len(segments)-1].End
			if segEndTime == 0 {
				continue
			}

			if float32(audioLength)-segEndTime > float32(silenceThreshold) {
				xlog.Debug("Detected end of speech segment")
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = nil
				session.AudioBufferLock.Unlock()

				sendEvent(t, types.InputAudioBufferSpeechStoppedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
					},
					AudioEndMs: time.Since(startTime).Milliseconds(),
				})
				speechStarted = false

				sendEvent(t, types.InputAudioBufferCommittedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
					},
					ItemID:         generateItemID(),
					PreviousItemID: "TODO",
				})

				abytes := sound.Int16toBytesLE(aints)
				// TODO: Remove prefix silence that is is over TurnDetectionParams.PrefixPaddingMs
				respCtx, respDone := session.startResponse(vadContext)
				go func() {
					defer close(respDone)
					commitUtterance(respCtx, abytes, session, conv, t)
				}()
			}
		}
	}
}

func commitUtterance(ctx context.Context, utt []byte, session *Session, conv *Conversation, t Transport) {
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

	// TODO: If we have a real any-to-any model then transcription is optional
	var transcript string
	if session.InputAudioTranscription != nil {
		tr, err := session.ModelInterface.Transcribe(ctx, f.Name(), session.InputAudioTranscription.Language, false, false, session.InputAudioTranscription.Prompt)
		if err != nil {
			sendError(t, "transcription_failed", err.Error(), "", "event_TODO")
			return
		} else if tr == nil {
			sendError(t, "transcription_failed", "trancribe result is nil", "", "event_TODO")
			return
		}

		transcript = tr.Text
		sendEvent(t, types.ConversationItemInputAudioTranscriptionCompletedEvent{
			ServerEventBase: types.ServerEventBase{
				EventID: "event_TODO",
			},

			ItemID: generateItemID(),
			// ResponseID:   "resp_TODO", // Not needed for transcription completed event
			// OutputIndex:  0,
			ContentIndex: 0,
			Transcript:   transcript,
		})
	} else {
		sendNotImplemented(t, "any-to-any models")
		return
	}

	if !session.TranscriptionOnly {
		generateResponse(ctx, session, utt, transcript, conv, t)
	}
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

// Function to generate a response based on the conversation
func generateResponse(ctx context.Context, session *Session, utt []byte, transcript string, conv *Conversation, t Transport) {
	xlog.Debug("Generating realtime response...")

	// Create user message item
	item := types.MessageItemUnion{
		User: &types.MessageItemUser{
			ID:     generateItemID(),
			Status: types.ItemStatusCompleted,
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

	triggerResponse(ctx, session, conv, t, nil)
}

// maxAssistantToolTurns caps the server-side agentic loop. Mirrors the
// chat-page maxToolTurns:10 from useChat.js — the model gets up to this
// many consecutive tool round-trips before we return control to the user
// without another response cycle.
const maxAssistantToolTurns = 10

func triggerResponse(ctx context.Context, session *Session, conv *Conversation, t Transport, overrides *types.ResponseCreateParams) {
	triggerResponseAtTurn(ctx, session, conv, t, overrides, 0)
}

func triggerResponseAtTurn(ctx context.Context, session *Session, conv *Conversation, t Transport, overrides *types.ResponseCreateParams, toolTurn int) {
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
	conv.Lock.Lock()
	items := trimRealtimeItems(conv.Items, session.MaxHistoryItems)
	for _, item := range items {
		if item.User != nil {
			msg := schema.Message{
				Role: string(types.MessageRoleUser),
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

	var images []string
	for _, m := range conversationHistory {
		images = append(images, m.StringImages...)
	}

	responseID := generateUniqueID()
	sendEvent(t, types.ResponseCreatedEvent{
		ServerEventBase: types.ServerEventBase{},
		Response: types.Response{
			ID:     responseID,
			Object: "realtime.response",
			Status: types.ResponseStatusInProgress,
		},
	})

	predFunc, err := session.ModelInterface.Predict(ctx, conversationHistory, images, nil, nil, nil, tools, toolChoice, nil, nil, nil)
	if err != nil {
		sendError(t, "inference_failed", fmt.Sprintf("backend error: %v", err), "", "") // item.Assistant.ID is unknown here
		return
	}

	pred, err := predFunc()
	if err != nil {
		sendError(t, "prediction_failed", fmt.Sprintf("backend error: %v", err), "", "")
		return
	}

	// Check for cancellation after LLM inference (barge-in may have fired)
	if ctx.Err() != nil {
		xlog.Debug("Response cancelled after LLM inference (barge-in)")
		sendEvent(t, types.ResponseDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			Response: types.Response{
				ID:     responseID,
				Object: "realtime.response",
				Status: types.ResponseStatusCancelled,
			},
		})
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
		reasoningText = deltaReasoning
		responseWithoutReasoning = deltaContent
		textContent = deltaContent
		cleanedResponse = deltaContent
		toolCalls = deltaToolCalls
	} else {
		reasoningText, responseWithoutReasoning = reasoning.ExtractReasoningWithConfig(rawResponse, thinkingStartToken, config.ReasoningConfig)
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

		// sendCancelledResponse emits the cancelled status and cleans up the
		// assistant item so the interrupted reply is not in chat history.
		sendCancelledResponse := func() {
			removeItemFromConv(item.Assistant.ID)
			sendEvent(t, types.ResponseDoneEvent{
				ServerEventBase: types.ServerEventBase{},
				Response: types.Response{
					ID:     responseID,
					Object: "realtime.response",
					Status: types.ResponseStatusCancelled,
				},
			})
		}

		var audioString string
		_, isWebRTC := t.(*WebRTCTransport)
		var respMods []types.Modality
		if overrides != nil {
			respMods = overrides.OutputModalities
		}
		modalities := resolveOutputModalities(session.OutputModalities, respMods)
		if modalitiesContainAudio(modalities) {
			// Check for cancellation before TTS
			if ctx.Err() != nil {
				xlog.Debug("Response cancelled before TTS (barge-in)")
				sendCancelledResponse()
				return
			}

			audioFilePath, res, err := session.ModelInterface.TTS(ctx, finalSpeech, session.Voice, session.InputAudioTranscription.Language)
			if err != nil {
				if ctx.Err() != nil {
					xlog.Debug("TTS cancelled (barge-in)")
					sendCancelledResponse()
					return
				}
				xlog.Error("TTS failed", "error", err)
				sendError(t, "tts_error", fmt.Sprintf("TTS generation failed: %v", err), "", item.Assistant.ID)
				return
			}
			if !res.Success {
				xlog.Error("TTS failed", "message", res.Message)
				sendError(t, "tts_error", fmt.Sprintf("TTS generation failed: %s", res.Message), "", item.Assistant.ID)
				return
			}
			defer func() { _ = os.Remove(audioFilePath) }()

			audioBytes, err := os.ReadFile(audioFilePath)
			if err != nil {
				xlog.Error("failed to read TTS file", "error", err)
				sendError(t, "tts_error", fmt.Sprintf("Failed to read TTS audio: %v", err), "", item.Assistant.ID)
				return
			}

			// Parse WAV header to get raw PCM and the actual sample rate from the TTS backend.
			pcmData, ttsSampleRate := laudio.ParseWAV(audioBytes)
			if ttsSampleRate == 0 {
				ttsSampleRate = localSampleRate
			}
			xlog.Debug("TTS audio parsed", "raw_bytes", len(audioBytes), "pcm_bytes", len(pcmData), "sample_rate", ttsSampleRate)

			// SendAudio (WebRTC) passes PCM at the TTS sample rate directly to the
			// Opus encoder, which resamples to 48kHz internally. This avoids a
			// lossy intermediate resample through 16kHz.
			// XXX: This is a noop in websocket mode; it's included in the JSON instead
			if err := t.SendAudio(ctx, pcmData, ttsSampleRate); err != nil {
				if ctx.Err() != nil {
					xlog.Debug("Audio playback cancelled (barge-in)")
					sendCancelledResponse()
					return
				}
				xlog.Error("failed to send audio via transport", "error", err)
			}

			// For WebSocket clients, resample to the session's output rate and
			// deliver audio as base64 in JSON events. WebRTC clients already
			// received audio over the RTP track, so skip the base64 payload.
			if !isWebRTC {
				wsPCM := pcmData
				if ttsSampleRate != session.OutputSampleRate {
					samples := sound.BytesToInt16sLE(pcmData)
					resampled := sound.ResampleInt16(samples, ttsSampleRate, session.OutputSampleRate)
					wsPCM = sound.Int16toBytesLE(resampled)
				}
				audioString = base64.StdEncoding.EncodeToString(wsPCM)
			}

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

			if !isWebRTC {
				sendEvent(t, types.ResponseOutputAudioDeltaEvent{
					ServerEventBase: types.ServerEventBase{},
					ResponseID:      responseID,
					ItemID:          item.Assistant.ID,
					OutputIndex:     0,
					ContentIndex:    0,
					Delta:           audioString,
				})
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
	}

	// Handle Tool Calls. Two paths:
	//   - LocalAI Assistant tools (session.AssistantExecutor.IsTool) run
	//     server-side; we append both the call and its output to conv.Items
	//     and re-trigger a follow-up response so the model can speak the
	//     result. The client only sees observability events.
	//   - All other tools follow the standard OpenAI flow: emit
	//     function_call_arguments.done and wait for the client to send
	//     conversation.item.create back.
	xlog.Debug("About to handle tool calls", "finalToolCallsCount", len(finalToolCalls))
	executedAssistantTool := false
	for i, tc := range finalToolCalls {
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
		if finalSpeech != "" {
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
	}

	sendEvent(t, types.ResponseDoneEvent{
		ServerEventBase: types.ServerEventBase{},
		Response: types.Response{
			ID:     responseID,
			Object: "realtime.response",
			Status: types.ResponseStatusCompleted,
		},
	})

	// If we executed any assistant tools inproc, run another response cycle
	// so the model can speak the result. Mirrors the chat-side agentic loop
	// but driven server-side rather than by client round-trip. Bounded so a
	// degenerate "model keeps calling tools" doesn't blow the stack.
	if executedAssistantTool {
		if toolTurn+1 >= maxAssistantToolTurns {
			xlog.Warn("realtime: assistant tool-turn limit reached, stopping the agentic loop",
				"limit", maxAssistantToolTurns, "model", session.Model)
			return
		}
		triggerResponseAtTurn(ctx, session, conv, t, nil, toolTurn+1)
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
	// Generate a unique ID string
	// For simplicity, use a counter or UUID
	// Implement as needed
	return "unique_id"
}
