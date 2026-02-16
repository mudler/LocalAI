package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"net/http"

	"github.com/go-audio/audio"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/backend"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/functions"
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
)

// A model can be "emulated" that is: transcribe audio to text -> feed text to the LLM -> generate audio as result
// If the model support instead audio-to-audio, we will use the specific gRPC calls instead

// LockedWebsocket wraps a websocket connection with a mutex for safe concurrent writes
type LockedWebsocket struct {
	*websocket.Conn
	sync.Mutex
}

func (l *LockedWebsocket) WriteMessage(messageType int, data []byte) error {
	l.Lock()
	defer l.Unlock()
	return l.Conn.WriteMessage(messageType, data)
}

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
	Instructions            string
	DefaultConversationID   string
	ModelInterface          Model
	// The pipeline model config or the config for an any-to-any model
	ModelConfig     *config.ModelConfig
	InputSampleRate int
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
				ID:           s.ID,
				Object:       "realtime.session",
				Model:        s.Model,
				Instructions: s.Instructions,
				Tools:        s.Tools,
				ToolChoice:   s.ToolChoice,
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

func Realtime(application *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		// Extract query parameters from Echo context before passing to websocket handler
		model := c.QueryParam("model")

		registerRealtime(application, model)(ws)
		return nil
	}
}

func registerRealtime(application *application.Application, model string) func(c *websocket.Conn) {
	return func(conn *websocket.Conn) {
		c := &LockedWebsocket{Conn: conn}

		evaluator := application.TemplatesEvaluator()
		xlog.Debug("Realtime WebSocket connection established", "address", c.RemoteAddr().String(), "model", model)

		// TODO: Allow any-to-any model to be specified
		cl := application.ModelConfigLoader()
		cfg, err := cl.LoadModelConfigFileByNameDefaultOptions(model, application.ApplicationConfig())
		if err != nil {
			xlog.Error("failed to load model config", "error", err)
			sendError(c, "model_load_error", "Failed to load model config", "", "")
			return
		}

		if cfg == nil || (cfg.Pipeline.VAD == "" && cfg.Pipeline.Transcription == "" && cfg.Pipeline.TTS == "" && cfg.Pipeline.LLM == "") {
			xlog.Error("model is not a pipeline", "model", model)
			sendError(c, "invalid_model", "Model is not a pipeline model", "", "")
			return
		}

		sttModel := cfg.Pipeline.Transcription

		sessionID := generateSessionID()
		session := &Session{
			ID:                sessionID,
			TranscriptionOnly: false,
			Model:             model,
			Voice:             cfg.TTSConfig.Voice,
			ModelConfig:       cfg,
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
			Conversations:   make(map[string]*Conversation),
			InputSampleRate: defaultRemoteSampleRate,
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
			sendError(c, "model_load_error", "Failed to load model", "", "")
			return
		}
		session.ModelInterface = m

		// Store the session
		sessionLock.Lock()
		sessions[sessionID] = session
		sessionLock.Unlock()

		sendEvent(c, types.SessionCreatedEvent{
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
			if session.TurnDetection.ServerVad != nil && !vadServerStarted {
				xlog.Debug("Starting VAD goroutine...")
				wg.Add(1)
				go func() {
					defer wg.Done()
					conversation := session.Conversations[session.DefaultConversationID]
					handleVAD(session, conversation, c, done)
				}()
				vadServerStarted = true
			} else if session.TurnDetection.ServerVad == nil && vadServerStarted {
				xlog.Debug("Stopping VAD goroutine...")

				go func() {
					done <- struct{}{}
				}()
				vadServerStarted = false
			}
		}

		toggleVAD()

		for {
			if _, msg, err = c.ReadMessage(); err != nil {
				xlog.Error("read error", "error", err)
				break
			}

			// Parse the incoming message
			event, err := types.UnmarshalClientEvent(msg)
			if err != nil {
				xlog.Error("invalid json", "error", err)
				sendError(c, "invalid_json", "Invalid JSON format", "", "")
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
						sendError(c, "session_update_error", "Failed to update session", "", "")
						continue
					}

					toggleVAD()

					sendEvent(c, types.SessionUpdatedEvent{
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
						sendError(c, "session_update_error", "Failed to update session", "", "")
						continue
					}

					toggleVAD()

					sendEvent(c, types.SessionUpdatedEvent{
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
					sendError(c, "missing_audio_data", "Audio data is missing", "", "")
					continue
				}

				// Decode base64 audio data
				decodedAudio, err := base64.StdEncoding.DecodeString(e.Audio)
				if err != nil {
					xlog.Error("failed to decode audio data", "error", err)
					sendError(c, "invalid_audio_data", "Failed to decode audio data", "", "")
					continue
				}

				// Append to InputAudioBuffer
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = append(session.InputAudioBuffer, decodedAudio...)
				session.AudioBufferLock.Unlock()

			case types.InputAudioBufferCommitEvent:
				xlog.Debug("recv", "message", string(msg))

				sessionLock.Lock()
				isServerVAD := session.TurnDetection.ServerVad != nil
				sessionLock.Unlock()

				// TODO: At the least need to check locking and timer state in the VAD Go routine before allowing this
				if isServerVAD {
					sendNotImplemented(c, "input_audio_buffer.commit in conjunction with VAD")
					continue
				}

				session.AudioBufferLock.Lock()
				allAudio := make([]byte, len(session.InputAudioBuffer))
				copy(allAudio, session.InputAudioBuffer)
				session.InputAudioBuffer = nil
				session.AudioBufferLock.Unlock()

				go commitUtterance(context.TODO(), allAudio, session, conversation, c)

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

				sendEvent(c, types.ConversationItemAddedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: e.EventID,
					},
					PreviousItemID: e.PreviousItemID,
					Item:           item,
				})

			case types.ConversationItemDeleteEvent:
				sendError(c, "not_implemented", "Deleting items not implemented", "", "event_TODO")

			case types.ConversationItemRetrieveEvent:
				xlog.Debug("recv", "message", string(msg))

				if e.ItemID == "" {
					sendError(c, "invalid_item_id", "Need item_id, but none specified", "", "event_TODO")
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

				sendEvent(c, types.ConversationItemRetrievedEvent{
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

				go triggerResponse(session, conversation, c, &e.Response)

			case types.ResponseCancelEvent:
				xlog.Debug("recv", "message", string(msg))

				// Handle cancellation of ongoing responses
				// Implement cancellation logic as needed
				sendNotImplemented(c, "response.cancel")

			default:
				xlog.Error("unknown message type")
				// sendError(c, "unknown_message_type", fmt.Sprintf("Unknown message type: %s", incomingMsg.Type), "", "")
			}
		}

		// Close the done channel to signal goroutines to exit
		close(done)
		wg.Wait()

		// Remove the session from the sessions map
		sessionLock.Lock()
		delete(sessions, sessionID)
		sessionLock.Unlock()
	}
}

// Helper function to send events to the client
func sendEvent(c *LockedWebsocket, event types.ServerEvent) {
	eventBytes, err := json.Marshal(event)
	if err != nil {
		xlog.Error("failed to marshal event", "error", err)
		return
	}
	if err = c.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		xlog.Error("write error", "error", err)
	}
}

// Helper function to send errors to the client
func sendError(c *LockedWebsocket, code, message, param, eventID string) {
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

	sendEvent(c, errorEvent)
}

func sendNotImplemented(c *LockedWebsocket, message string) {
	sendError(c, "not_implemented", message, "", "event_TODO")
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

	if update.Transcription.Audio.Input.TurnDetection != nil {
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

	if rt.Audio != nil && rt.Audio.Input != nil && rt.Audio.Input.TurnDetection != nil {
		session.TurnDetection = rt.Audio.Input.TurnDetection
	}

	if rt.Audio != nil && rt.Audio.Input != nil && rt.Audio.Input.Format != nil && rt.Audio.Input.Format.PCM != nil {
		if rt.Audio.Input.Format.PCM.Rate > 0 {
			session.InputSampleRate = rt.Audio.Input.Format.PCM.Rate
		}
	}

	if rt.Instructions != "" {
		session.Instructions = rt.Instructions
	}

	if rt.Tools != nil {
		session.Tools = rt.Tools
	}
	if rt.ToolChoice != nil {
		session.ToolChoice = rt.ToolChoice
	}

	return nil
}

// handleVAD is a goroutine that listens for audio data from the client,
// runs VAD on the audio data, and commits utterances to the conversation
func handleVAD(session *Session, conv *Conversation, c *LockedWebsocket, done chan struct{}) {
	vadContext, cancel := context.WithCancel(context.Background())
	go func() {
		<-done
		cancel()
	}()

	silenceThreshold := 0.5 // Default 500ms
	if session.TurnDetection.ServerVad != nil {
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
			if len(aints) == 0 || len(aints) < int(silenceThreshold)*session.InputSampleRate {
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
				sendError(c, "processing_error", "Failed to process audio: "+err.Error(), "", "")
				continue
			}

			audioLength := float64(len(aints)) / localSampleRate

			// TODO: When resetting the buffer we should retain a small postfix
			// TODO: The OpenAI documentation seems to suggest that only the client decides when to clear the buffer
			if len(segments) == 0 && audioLength > silenceThreshold {
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = nil
				session.AudioBufferLock.Unlock()
				xlog.Debug("Detected silence for a while, clearing audio buffer")

				sendEvent(c, types.InputAudioBufferClearedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
					},
				})

				continue
			} else if len(segments) == 0 {
				continue
			}

			if !speechStarted {
				sendEvent(c, types.InputAudioBufferSpeechStartedEvent{
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

				sendEvent(c, types.InputAudioBufferSpeechStoppedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
					},
					AudioEndMs: time.Since(startTime).Milliseconds(),
				})
				speechStarted = false

				sendEvent(c, types.InputAudioBufferCommittedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
					},
					ItemID:         generateItemID(),
					PreviousItemID: "TODO",
				})

				abytes := sound.Int16toBytesLE(aints)
				// TODO: Remove prefix silence that is is over TurnDetectionParams.PrefixPaddingMs
				go commitUtterance(vadContext, abytes, session, conv, c)
			}
		}
	}
}

func commitUtterance(ctx context.Context, utt []byte, session *Session, conv *Conversation, c *LockedWebsocket) {
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
			sendError(c, "transcription_failed", err.Error(), "", "event_TODO")
			return
		} else if tr == nil {
			sendError(c, "transcription_failed", "trancribe result is nil", "", "event_TODO")
			return
		}

		transcript = tr.Text
		sendEvent(c, types.ConversationItemInputAudioTranscriptionCompletedEvent{
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
		sendNotImplemented(c, "any-to-any models")
		return
	}

	if !session.TranscriptionOnly {
		generateResponse(session, utt, transcript, conv, c, websocket.TextMessage)
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
func generateResponse(session *Session, utt []byte, transcript string, conv *Conversation, c *LockedWebsocket, mt int) {
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

	sendEvent(c, types.ConversationItemAddedEvent{
		Item: item,
	})

	triggerResponse(session, conv, c, nil)
}

func triggerResponse(session *Session, conv *Conversation, c *LockedWebsocket, overrides *types.ResponseCreateParams) {
	config := session.ModelInterface.PredictConfig()

	// Default values
	tools := session.Tools
	toolChoice := session.ToolChoice
	instructions := session.Instructions
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
	}

	var conversationHistory schema.Messages
	conversationHistory = append(conversationHistory, schema.Message{
		Role:          string(types.MessageRoleSystem),
		StringContent: instructions,
		Content:       instructions,
	})

	imgIndex := 0
	conv.Lock.Lock()
	for _, item := range conv.Items {
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
			if nrOfImgsInMessage > 0 {
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
		}
	}
	conv.Lock.Unlock()

	var images []string
	for _, m := range conversationHistory {
		images = append(images, m.StringImages...)
	}

	responseID := generateUniqueID()
	sendEvent(c, types.ResponseCreatedEvent{
		ServerEventBase: types.ServerEventBase{},
		Response: types.Response{
			ID:     responseID,
			Object: "realtime.response",
			Status: types.ResponseStatusInProgress,
		},
	})

	predFunc, err := session.ModelInterface.Predict(context.TODO(), conversationHistory, images, nil, nil, nil, tools, toolChoice, nil, nil, nil)
	if err != nil {
		sendError(c, "inference_failed", fmt.Sprintf("backend error: %v", err), "", "") // item.Assistant.ID is unknown here
		return
	}

	pred, err := predFunc()
	if err != nil {
		sendError(c, "prediction_failed", fmt.Sprintf("backend error: %v", err), "", "")
		return
	}

	xlog.Debug("Function config for parsing", "function_name_key", config.FunctionsConfig.FunctionNameKey, "function_arguments_key", config.FunctionsConfig.FunctionArgumentsKey)

	rawResponse := pred.Response
	if config.TemplateConfig.ReplyPrefix != "" {
		rawResponse = config.TemplateConfig.ReplyPrefix + rawResponse
	}

	reasoningText, responseWithoutReasoning := reasoning.ExtractReasoningWithConfig(rawResponse, "", config.ReasoningConfig)
	xlog.Debug("LLM Response", "reasoning", reasoningText, "response_without_reasoning", responseWithoutReasoning)

	textContent := functions.ParseTextContent(responseWithoutReasoning, config.FunctionsConfig)
	cleanedResponse := functions.CleanupLLMResult(responseWithoutReasoning, config.FunctionsConfig)
	toolCalls := functions.ParseFunctionCall(cleanedResponse, config.FunctionsConfig)

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
		arguments := map[string]interface{}{}
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

		sendEvent(c, types.ResponseOutputItemAddedEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			OutputIndex:     0,
			Item:            item,
		})

		sendEvent(c, types.ResponseContentPartAddedEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          item.Assistant.ID,
			OutputIndex:     0,
			ContentIndex:    0,
			Part:            item.Assistant.Content[0],
		})

		audioFilePath, res, err := session.ModelInterface.TTS(context.TODO(), finalSpeech, session.Voice, session.InputAudioTranscription.Language)
		if err != nil {
			xlog.Error("TTS failed", "error", err)
			sendError(c, "tts_error", fmt.Sprintf("TTS generation failed: %v", err), "", item.Assistant.ID)
			return
		}
		if !res.Success {
			xlog.Error("TTS failed", "message", res.Message)
			sendError(c, "tts_error", fmt.Sprintf("TTS generation failed: %s", res.Message), "", item.Assistant.ID)
			return
		}
		defer os.Remove(audioFilePath)

		audioBytes, err := os.ReadFile(audioFilePath)
		if err != nil {
			xlog.Error("failed to read TTS file", "error", err)
			sendError(c, "tts_error", fmt.Sprintf("Failed to read TTS audio: %v", err), "", item.Assistant.ID)
			return
		}

		// Strip WAV header (44 bytes) to get raw PCM data
		// The OpenAI Realtime API expects raw PCM, not WAV files
		const wavHeaderSize = 44
		pcmData := audioBytes
		if len(audioBytes) > wavHeaderSize {
			pcmData = audioBytes[wavHeaderSize:]
		}

		audioString := base64.StdEncoding.EncodeToString(pcmData)

		sendEvent(c, types.ResponseOutputAudioTranscriptDeltaEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          item.Assistant.ID,
			OutputIndex:     0,
			ContentIndex:    0,
			Delta:           finalSpeech,
		})
		sendEvent(c, types.ResponseOutputAudioTranscriptDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          item.Assistant.ID,
			OutputIndex:     0,
			ContentIndex:    0,
			Transcript:      finalSpeech,
		})

		sendEvent(c, types.ResponseOutputAudioDeltaEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          item.Assistant.ID,
			OutputIndex:     0,
			ContentIndex:    0,
			Delta:           audioString,
		})
		sendEvent(c, types.ResponseOutputAudioDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          item.Assistant.ID,
			OutputIndex:     0,
			ContentIndex:    0,
		})

		sendEvent(c, types.ResponseContentPartDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          item.Assistant.ID,
			OutputIndex:     0,
			ContentIndex:    0,
			Part:            item.Assistant.Content[0],
		})

		conv.Lock.Lock()
		item.Assistant.Status = types.ItemStatusCompleted
		item.Assistant.Content[0].Audio = audioString
		conv.Lock.Unlock()

		sendEvent(c, types.ResponseOutputItemDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			OutputIndex:     0,
			Item:            item,
		})
	}

	// Handle Tool Calls
	xlog.Debug("About to handle tool calls", "finalToolCallsCount", len(finalToolCalls))
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

		sendEvent(c, types.ResponseOutputItemAddedEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			OutputIndex:     outputIndex,
			Item:            fcItem,
		})

		sendEvent(c, types.ResponseFunctionCallArgumentsDeltaEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          toolCallID,
			OutputIndex:     outputIndex,
			CallID:          callID,
			Delta:           tc.Arguments,
		})

		sendEvent(c, types.ResponseFunctionCallArgumentsDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          toolCallID,
			OutputIndex:     outputIndex,
			CallID:          callID,
			Arguments:       tc.Arguments,
			Name:            tc.Name,
		})

		sendEvent(c, types.ResponseOutputItemDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			OutputIndex:     outputIndex,
			Item:            fcItem,
		})
	}

	sendEvent(c, types.ResponseDoneEvent{
		ServerEventBase: types.ServerEventBase{},
		Response: types.Response{
			ID:     responseID,
			Object: "realtime.response",
			Status: types.ResponseStatusCompleted,
		},
	})
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
