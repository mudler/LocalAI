package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-audio/audio"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/templates"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/sound"

	"google.golang.org/grpc"

	"github.com/rs/zerolog/log"
)

const (
	localSampleRate  = 16000
	remoteSampleRate = 24000
	vadModel         = "silero-vad-ggml"
)

// A model can be "emulated" that is: transcribe audio to text -> feed text to the LLM -> generate audio as result
// If the model support instead audio-to-audio, we will use the specific gRPC calls instead

// Session represents a single WebSocket connection and its state
type Session struct {
	ID                      string
	TranscriptionOnly       bool
	Model                   string
	Voice                   string
	TurnDetection           *types.ServerTurnDetection `json:"turn_detection"` // "server_vad" or "none"
	InputAudioTranscription *types.InputAudioTranscription
	Functions               functions.Functions
	Conversations           map[string]*Conversation
	InputAudioBuffer        []byte
	AudioBufferLock         sync.Mutex
	Instructions            string
	DefaultConversationID   string
	ModelInterface          Model
}

func (s *Session) FromClient(session *types.ClientSession) {
}

func (s *Session) ToServer() types.ServerSession {
	return types.ServerSession{
		ID: s.ID,
		Object: func() string {
			if s.TranscriptionOnly {
				return "realtime.transcription_session"
			} else {
				return "realtime.session"
			}
		}(),
		Model:                   s.Model,
		Modalities:              []types.Modality{types.ModalityText, types.ModalityAudio},
		Instructions:            s.Instructions,
		Voice:                   s.Voice,
		InputAudioFormat:        types.AudioFormatPcm16,
		OutputAudioFormat:       types.AudioFormatPcm16,
		TurnDetection:           s.TurnDetection,
		InputAudioTranscription: s.InputAudioTranscription,
		// TODO: Should be constructed from Functions?
		Tools: []types.Tool{},
		// TODO: ToolChoice
		// TODO: Temperature
		// TODO: MaxOutputTokens
		// TODO: InputAudioNoiseReduction
	}
}

// TODO: Update to tools?
// FunctionCall represents a function call initiated by the model
type FunctionCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// Conversation represents a conversation with a list of items
type Conversation struct {
	ID    string
	Items []*types.MessageItem
	Lock  sync.Mutex
}

func (c *Conversation) ToServer() types.Conversation {
	return types.Conversation{
		ID:     c.ID,
		Object: "realtime.conversation",
	}
}

// Item represents a message, function_call, or function_call_output
type Item struct {
	ID           string                `json:"id"`
	Object       string                `json:"object"`
	Type         string                `json:"type"` // "message", "function_call", "function_call_output"
	Status       string                `json:"status"`
	Role         string                `json:"role"`
	Content      []ConversationContent `json:"content,omitempty"`
	FunctionCall *FunctionCall         `json:"function_call,omitempty"`
}

// ConversationContent represents the content of an item
type ConversationContent struct {
	Type  string `json:"type"` // "input_text", "input_audio", "text", "audio", etc.
	Audio string `json:"audio,omitempty"`
	Text  string `json:"text,omitempty"`
	// Additional fields as needed
}

// Define the structures for incoming messages
type IncomingMessage struct {
	Type     types.ClientEventType `json:"type"`
	Session  json.RawMessage       `json:"session,omitempty"`
	Item     json.RawMessage       `json:"item,omitempty"`
	Audio    string                `json:"audio,omitempty"`
	Response json.RawMessage       `json:"response,omitempty"`
	Error    *ErrorMessage         `json:"error,omitempty"`
	// Other fields as needed
}

// ErrorMessage represents an error message sent to the client
type ErrorMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
	EventID string `json:"event_id,omitempty"`
}

// Define a structure for outgoing messages
type OutgoingMessage struct {
	Type         string        `json:"type"`
	Session      *Session      `json:"session,omitempty"`
	Conversation *Conversation `json:"conversation,omitempty"`
	Item         *Item         `json:"item,omitempty"`
	Content      string        `json:"content,omitempty"`
	Audio        string        `json:"audio,omitempty"`
	Error        *ErrorMessage `json:"error,omitempty"`
}

// Map to store sessions (in-memory)
var sessions = make(map[string]*Session)
var sessionLock sync.Mutex

// TODO: implement interface as we start to define usages
type Model interface {
	VAD(ctx context.Context, in *proto.VADRequest, opts ...grpc.CallOption) (*proto.VADResponse, error)
	Transcribe(ctx context.Context, in *proto.TranscriptRequest, opts ...grpc.CallOption) (*proto.TranscriptResult, error)
	Predict(ctx context.Context, in *proto.PredictOptions, opts ...grpc.CallOption) (*proto.Reply, error)
	PredictStream(ctx context.Context, in *proto.PredictOptions, f func(*proto.Reply), opts ...grpc.CallOption) error
}

// TODO: Implement ephemeral keys to allow these endpoints to be used
func RealtimeSessions(application *application.Application) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		return ctx.SendStatus(501)
	}
}

func RealtimeTranscriptionSession(application *application.Application) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		return ctx.SendStatus(501)
	}
}

func Realtime(application *application.Application) fiber.Handler {
	return websocket.New(registerRealtime(application))
}

func registerRealtime(application *application.Application) func(c *websocket.Conn) {
	return func(c *websocket.Conn) {

		evaluator := application.TemplatesEvaluator()
		log.Debug().Msgf("WebSocket connection established with '%s'", c.RemoteAddr().String())

		model := c.Query("model", "gpt-4o")

		intent := c.Query("intent")
		if intent != "transcription" {
			sendNotImplemented(c, "Only transcription mode is supported which requires the intent=transcription parameter")
		}

		log.Debug().Msgf("Realtime params: model=%s, intent=%s", model, intent)

		sessionID := generateSessionID()
		session := &Session{
			ID:                sessionID,
			TranscriptionOnly: true,
			Model:             model,   // default model
			Voice:             "alloy", // default voice
			TurnDetection: &types.ServerTurnDetection{
				Type: types.ServerTurnDetectionTypeServerVad,
				TurnDetectionParams: types.TurnDetectionParams{
					// TODO: Need some way to pass this to the backend
					Threshold: 0.5,
					// TODO: This is ignored and the amount of padding is random at present
					PrefixPaddingMs:   30,
					SilenceDurationMs: 500,
					CreateResponse:    func() *bool { t := true; return &t }(),
				},
			},
			InputAudioTranscription: &types.InputAudioTranscription{
				Model: "whisper-1",
			},
			Conversations: make(map[string]*Conversation),
		}

		// Create a default conversation
		conversationID := generateConversationID()
		conversation := &Conversation{
			ID:    conversationID,
			Items: []*types.MessageItem{},
		}
		session.Conversations[conversationID] = conversation
		session.DefaultConversationID = conversationID

		// TODO: The API has no way to configure the VAD model or other models that make up a pipeline to fake any-to-any
		//       So possibly we could have a way to configure a composite model that can be used in situations where any-to-any is expected
		pipeline := config.Pipeline{
			VAD:           vadModel,
			Transcription: session.InputAudioTranscription.Model,
		}

		m, cfg, err := newTranscriptionOnlyModel(
			&pipeline,
			application.ModelConfigLoader(),
			application.ModelLoader(),
			application.ApplicationConfig(),
		)
		if err != nil {
			log.Error().Msgf("failed to load model: %s", err.Error())
			sendError(c, "model_load_error", "Failed to load model", "", "")
			return
		}
		session.ModelInterface = m

		// Store the session
		sessionLock.Lock()
		sessions[sessionID] = session
		sessionLock.Unlock()

		sendEvent(c, types.TranscriptionSessionCreatedEvent{
			ServerEventBase: types.ServerEventBase{
				EventID: "event_TODO",
				Type:    types.ServerEventTypeTranscriptionSessionCreated,
			},
			Session: session.ToServer(),
		})

		var (
			// mt   int
			msg  []byte
			wg   sync.WaitGroup
			done = make(chan struct{})
		)

		vadServerStarted := true
		wg.Add(1)
		go func() {
			defer wg.Done()
			conversation := session.Conversations[session.DefaultConversationID]
			handleVAD(cfg, evaluator, session, conversation, c, done)
		}()

		for {
			if _, msg, err = c.ReadMessage(); err != nil {
				log.Error().Msgf("read: %s", err.Error())
				break
			}

			// Parse the incoming message
			var incomingMsg IncomingMessage
			if err := json.Unmarshal(msg, &incomingMsg); err != nil {
				log.Error().Msgf("invalid json: %s", err.Error())
				sendError(c, "invalid_json", "Invalid JSON format", "", "")
				continue
			}

			var sessionUpdate types.ClientSession
			switch incomingMsg.Type {
			case types.ClientEventTypeTranscriptionSessionUpdate:
				log.Debug().Msgf("recv: %s", msg)

				if err := json.Unmarshal(incomingMsg.Session, &sessionUpdate); err != nil {
					log.Error().Msgf("failed to unmarshal 'transcription_session.update': %s", err.Error())
					sendError(c, "invalid_session_update", "Invalid session update format", "", "")
					continue
				}
				if err := updateTransSession(
					session,
					&sessionUpdate,
					application.ModelConfigLoader(),
					application.ModelLoader(),
					application.ApplicationConfig(),
				); err != nil {
					log.Error().Msgf("failed to update session: %s", err.Error())
					sendError(c, "session_update_error", "Failed to update session", "", "")
					continue
				}

				sendEvent(c, types.SessionUpdatedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
						Type:    types.ServerEventTypeTranscriptionSessionUpdated,
					},
					Session: session.ToServer(),
				})

			case types.ClientEventTypeSessionUpdate:
				log.Debug().Msgf("recv: %s", msg)

				// Update session configurations
				if err := json.Unmarshal(incomingMsg.Session, &sessionUpdate); err != nil {
					log.Error().Msgf("failed to unmarshal 'session.update': %s", err.Error())
					sendError(c, "invalid_session_update", "Invalid session update format", "", "")
					continue
				}
				if err := updateSession(
					session,
					&sessionUpdate,
					application.ModelConfigLoader(),
					application.ModelLoader(),
					application.ApplicationConfig(),
				); err != nil {
					log.Error().Msgf("failed to update session: %s", err.Error())
					sendError(c, "session_update_error", "Failed to update session", "", "")
					continue
				}

				sendEvent(c, types.SessionUpdatedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
						Type:    types.ServerEventTypeSessionUpdated,
					},
					Session: session.ToServer(),
				})

				if session.TurnDetection.Type == types.ServerTurnDetectionTypeServerVad && !vadServerStarted {
					log.Debug().Msg("Starting VAD goroutine...")
					wg.Add(1)
					go func() {
						defer wg.Done()
						conversation := session.Conversations[session.DefaultConversationID]
						handleVAD(cfg, evaluator, session, conversation, c, done)
					}()
					vadServerStarted = true
				} else if session.TurnDetection.Type != types.ServerTurnDetectionTypeServerVad && vadServerStarted {
					log.Debug().Msg("Stopping VAD goroutine...")

					wg.Add(-1)
					go func() {
						done <- struct{}{}
					}()
					vadServerStarted = false
				}
			case types.ClientEventTypeInputAudioBufferAppend:
				// Handle 'input_audio_buffer.append'
				if incomingMsg.Audio == "" {
					log.Error().Msg("Audio data is missing in 'input_audio_buffer.append'")
					sendError(c, "missing_audio_data", "Audio data is missing", "", "")
					continue
				}

				// Decode base64 audio data
				decodedAudio, err := base64.StdEncoding.DecodeString(incomingMsg.Audio)
				if err != nil {
					log.Error().Msgf("failed to decode audio data: %s", err.Error())
					sendError(c, "invalid_audio_data", "Failed to decode audio data", "", "")
					continue
				}

				// Append to InputAudioBuffer
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = append(session.InputAudioBuffer, decodedAudio...)
				session.AudioBufferLock.Unlock()

			case types.ClientEventTypeInputAudioBufferCommit:
				log.Debug().Msgf("recv: %s", msg)

				// TODO: Trigger transcription.
				// TODO: Ignore this if VAD enabled or interrupt VAD?

				if session.TranscriptionOnly {
					continue
				}

				// Commit the audio buffer to the conversation as a new item
				item := &types.MessageItem{
					ID:     generateItemID(),
					Type:   "message",
					Status: "completed",
					Role:   "user",
					Content: []types.MessageContentPart{
						{
							Type:  "input_audio",
							Audio: base64.StdEncoding.EncodeToString(session.InputAudioBuffer),
						},
					},
				}

				// Add item to conversation
				conversation.Lock.Lock()
				conversation.Items = append(conversation.Items, item)
				conversation.Lock.Unlock()

				// Reset InputAudioBuffer
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = nil
				session.AudioBufferLock.Unlock()

				// Send item.created event
				sendEvent(c, types.ConversationItemCreatedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
						Type:    "conversation.item.created",
					},
					Item: types.ResponseMessageItem{
						Object:      "realtime.item",
						MessageItem: *item,
					},
				})

			case types.ClientEventTypeConversationItemCreate:
				log.Debug().Msgf("recv: %s", msg)

				// Handle creating new conversation items
				var item types.ConversationItemCreateEvent
				if err := json.Unmarshal(incomingMsg.Item, &item); err != nil {
					log.Error().Msgf("failed to unmarshal 'conversation.item.create': %s", err.Error())
					sendError(c, "invalid_item", "Invalid item format", "", "")
					continue
				}

				sendNotImplemented(c, "conversation.item.create")

				// Generate item ID and set status
				// item.ID = generateItemID()
				// item.Object = "realtime.item"
				// item.Status = "completed"
				//
				// // Add item to conversation
				// conversation.Lock.Lock()
				// conversation.Items = append(conversation.Items, &item)
				// conversation.Lock.Unlock()
				//
				// // Send item.created event
				// sendEvent(c, OutgoingMessage{
				// 	Type: "conversation.item.created",
				// 	Item: &item,
				// })

			case types.ClientEventTypeConversationItemDelete:
				sendError(c, "not_implemented", "Deleting items not implemented", "", "event_TODO")

			case types.ClientEventTypeResponseCreate:
				// Handle generating a response
				var responseCreate types.ResponseCreateEvent
				if len(incomingMsg.Response) > 0 {
					if err := json.Unmarshal(incomingMsg.Response, &responseCreate); err != nil {
						log.Error().Msgf("failed to unmarshal 'response.create' response object: %s", err.Error())
						sendError(c, "invalid_response_create", "Invalid response create format", "", "")
						continue
					}
				}

				// Update session functions if provided
				if len(responseCreate.Response.Tools) > 0 {
					// TODO: Tools -> Functions
				}

				sendNotImplemented(c, "response.create")

				// TODO: Generate a response based on the conversation history
				// wg.Add(1)
				// go func() {
				// 	defer wg.Done()
				// 	generateResponse(cfg, evaluator, session, conversation, responseCreate, c, mt)
				// }()

			case types.ClientEventTypeResponseCancel:
				log.Printf("recv: %s", msg)

				// Handle cancellation of ongoing responses
				// Implement cancellation logic as needed
				sendNotImplemented(c, "response.cancel")

			default:
				log.Error().Msgf("unknown message type: %s", incomingMsg.Type)
				sendError(c, "unknown_message_type", fmt.Sprintf("Unknown message type: %s", incomingMsg.Type), "", "")
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
func sendEvent(c *websocket.Conn, event types.ServerEvent) {
	eventBytes, err := json.Marshal(event)
	if err != nil {
		log.Error().Msgf("failed to marshal event: %s", err.Error())
		return
	}
	if err = c.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		log.Error().Msgf("write: %s", err.Error())
	}
}

// Helper function to send errors to the client
func sendError(c *websocket.Conn, code, message, param, eventID string) {
	errorEvent := types.ErrorEvent{
		ServerEventBase: types.ServerEventBase{
			Type:    types.ServerEventTypeError,
			EventID: eventID,
		},
		Error: types.Error{
			Type:    "invalid_request_error",
			Code:    code,
			Message: message,
			EventID: eventID,
		},
	}

	sendEvent(c, errorEvent)
}

func sendNotImplemented(c *websocket.Conn, message string) {
	sendError(c, "not_implemented", message, "", "event_TODO")
}

func updateTransSession(session *Session, update *types.ClientSession, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) error {
	sessionLock.Lock()
	defer sessionLock.Unlock()

	trUpd := update.InputAudioTranscription
	trCur := session.InputAudioTranscription

	if trUpd != nil && trUpd.Model != "" && trUpd.Model != trCur.Model {
		pipeline := config.Pipeline{
			VAD:           vadModel,
			Transcription: trUpd.Model,
		}

		m, _, err := newTranscriptionOnlyModel(&pipeline, cl, ml, appConfig)
		if err != nil {
			return err
		}

		session.ModelInterface = m
	}

	if update.TurnDetection != nil && update.TurnDetection.Type != "" {
		session.TurnDetection.Type = types.ServerTurnDetectionType(update.TurnDetection.Type)
		session.TurnDetection.TurnDetectionParams = update.TurnDetection.TurnDetectionParams
	}

	return nil
}

// Function to update session configurations
func updateSession(session *Session, update *types.ClientSession, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) error {
	sessionLock.Lock()
	defer sessionLock.Unlock()

	if update.Model != "" {
		pipeline := config.Pipeline{
			LLM: update.Model,
			// TODO: Setup pipeline by configuring STT and TTS models
		}
		m, err := newModel(&pipeline, cl, ml, appConfig)
		if err != nil {
			return err
		}
		session.ModelInterface = m
		session.Model = update.Model
	}

	if update.Voice != "" {
		session.Voice = update.Voice
	}
	if update.TurnDetection != nil && update.TurnDetection.Type != "" {
		session.TurnDetection.Type = types.ServerTurnDetectionType(update.TurnDetection.Type)
		session.TurnDetection.TurnDetectionParams = update.TurnDetection.TurnDetectionParams
	}
	// TODO: We should actually check if the field was present in the JSON; empty string means clear the settings
	if update.Instructions != "" {
		session.Instructions = update.Instructions
	}
	if update.Tools != nil {
		return fmt.Errorf("Haven't implemented tools")
	}

	session.InputAudioTranscription = update.InputAudioTranscription

	return nil
}

// handleVAD is a goroutine that listens for audio data from the client,
// runs VAD on the audio data, and commits utterances to the conversation
func handleVAD(cfg *config.ModelConfig, evaluator *templates.Evaluator, session *Session, conv *Conversation, c *websocket.Conn, done chan struct{}) {
	vadContext, cancel := context.WithCancel(context.Background())
	go func() {
		<-done
		cancel()
	}()

	silenceThreshold := float64(session.TurnDetection.SilenceDurationMs) / 1000
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
			if len(aints) == 0 || len(aints) < int(silenceThreshold)*remoteSampleRate {
				continue
			}

			// Resample from 24kHz to 16kHz
			aints = sound.ResampleInt16(aints, remoteSampleRate, localSampleRate)

			segments, err := runVAD(vadContext, session, aints)
			if err != nil {
				if err.Error() == "unexpected speech end" {
					log.Debug().Msg("VAD cancelled")
					continue
				}
				log.Error().Msgf("failed to process audio: %s", err.Error())
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
				log.Debug().Msgf("Detected silence for a while, clearing audio buffer")

				sendEvent(c, types.InputAudioBufferClearedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
						Type:    types.ServerEventTypeInputAudioBufferCleared,
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
						Type:    types.ServerEventTypeInputAudioBufferSpeechStarted,
					},
					AudioStartMs: time.Now().Sub(startTime).Milliseconds(),
				})
				speechStarted = true
			}

			// Segment still in progress when audio ended
			segEndTime := segments[len(segments)-1].GetEnd()
			if segEndTime == 0 {
				continue
			}

			if float32(audioLength)-segEndTime > float32(silenceThreshold) {
				log.Debug().Msgf("Detected end of speech segment")
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = nil
				session.AudioBufferLock.Unlock()

				sendEvent(c, types.InputAudioBufferSpeechStoppedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
						Type:    types.ServerEventTypeInputAudioBufferSpeechStopped,
					},
					AudioEndMs: time.Now().Sub(startTime).Milliseconds(),
				})
				speechStarted = false

				sendEvent(c, types.InputAudioBufferCommittedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
						Type:    types.ServerEventTypeInputAudioBufferCommitted,
					},
					ItemID:         generateItemID(),
					PreviousItemID: "TODO",
				})

				abytes := sound.Int16toBytesLE(aints)
				// TODO: Remove prefix silence that is is over TurnDetectionParams.PrefixPaddingMs
				go commitUtterance(vadContext, abytes, cfg, evaluator, session, conv, c)
			}
		}
	}
}

func commitUtterance(ctx context.Context, utt []byte, cfg *config.ModelConfig, evaluator *templates.Evaluator, session *Session, conv *Conversation, c *websocket.Conn) {
	if len(utt) == 0 {
		return
	}

	// TODO: If we have a real any-to-any model then transcription is optional

	f, err := os.CreateTemp("", "realtime-audio-chunk-*.wav")
	if err != nil {
		log.Error().Msgf("failed to create temp file: %s", err.Error())
		return
	}
	defer f.Close()
	defer os.Remove(f.Name())
	log.Debug().Msgf("Writing to %s\n", f.Name())

	hdr := laudio.NewWAVHeader(uint32(len(utt)))
	if err := hdr.Write(f); err != nil {
		log.Error().Msgf("Failed to write WAV header: %s", err.Error())
		return
	}

	if _, err := f.Write(utt); err != nil {
		log.Error().Msgf("Failed to write audio data: %s", err.Error())
		return
	}

	f.Sync()

	if session.InputAudioTranscription != nil {
		tr, err := session.ModelInterface.Transcribe(ctx, &proto.TranscriptRequest{
			Dst:       f.Name(),
			Language:  session.InputAudioTranscription.Language,
			Translate: false,
			Threads:   uint32(*cfg.Threads),
		})
		if err != nil {
			sendError(c, "transcription_failed", err.Error(), "", "event_TODO")
		}

		sendEvent(c, types.ResponseAudioTranscriptDoneEvent{
			ServerEventBase: types.ServerEventBase{
				Type:    types.ServerEventTypeResponseAudioTranscriptDone,
				EventID: "event_TODO",
			},

			ItemID:       generateItemID(),
			ResponseID:   "resp_TODO",
			OutputIndex:  0,
			ContentIndex: 0,
			Transcript:   tr.GetText(),
		})
		// TODO: Update the prompt with transcription result?
	}

	if !session.TranscriptionOnly {
		sendNotImplemented(c, "Commiting items to the conversation not implemented")
	}

	// TODO: Commit the audio and/or transcribed text to the conversation
	// Commit logic: create item, broadcast item.created, etc.
	// item := &Item{
	// 	ID:     generateItemID(),
	// 	Object: "realtime.item",
	// 	Type:   "message",
	// 	Status: "completed",
	// 	Role:   "user",
	// 	Content: []ConversationContent{
	// 		{
	// 			Type:  "input_audio",
	// 			Audio: base64.StdEncoding.EncodeToString(utt),
	// 		},
	// 	},
	// }
	// conv.Lock.Lock()
	// conv.Items = append(conv.Items, item)
	// conv.Lock.Unlock()
	//
	//
	// sendEvent(c, OutgoingMessage{
	// 	Type: "conversation.item.created",
	// 	Item: item,
	// })
	//
	//
	// // trigger the response generation
	// generateResponse(cfg, evaluator, session, conv, ResponseCreate{}, c, websocket.TextMessage)
}

func runVAD(ctx context.Context, session *Session, adata []int16) ([]*proto.VADSegment, error) {
	soundIntBuffer := &audio.IntBuffer{
		Format:         &audio.Format{SampleRate: localSampleRate, NumChannels: 1},
		SourceBitDepth: 16,
		Data:           sound.ConvertInt16ToInt(adata),
	}

	float32Data := soundIntBuffer.AsFloat32Buffer().Data

	resp, err := session.ModelInterface.VAD(ctx, &proto.VADRequest{
		Audio: float32Data,
	})
	if err != nil {
		return nil, err
	}

	// If resp.Segments is empty => no speech
	return resp.Segments, nil
}

// TODO: Below needed for normal mode instead of transcription only
// Function to generate a response based on the conversation
// func generateResponse(config *config.ModelConfig, evaluator *templates.Evaluator, session *Session, conversation *Conversation, responseCreate ResponseCreate, c *websocket.Conn, mt int) {
//
// 	log.Debug().Msg("Generating realtime response...")
//
// 	// Compile the conversation history
// 	conversation.Lock.Lock()
// 	var conversationHistory []schema.Message
// 	var latestUserAudio string
// 	for _, item := range conversation.Items {
// 		for _, content := range item.Content {
// 			switch content.Type {
// 			case "input_text", "text":
// 				conversationHistory = append(conversationHistory, schema.Message{
// 					Role:          string(item.Role),
// 					StringContent: content.Text,
// 					Content:       content.Text,
// 				})
// 			case "input_audio":
// 				// We do not to turn to text here the audio result.
// 				// When generating it later on from the LLM,
// 				// we will also generate text and return it and store it in the conversation
// 				// Here we just want to get the user audio if there is any as a new input for the conversation.
// 				if item.Role == "user" {
// 					latestUserAudio = content.Audio
// 				}
// 			}
// 		}
// 	}
//
// 	conversation.Lock.Unlock()
//
// 	var generatedText string
// 	var generatedAudio []byte
// 	var functionCall *FunctionCall
// 	var err error
//
// 	if latestUserAudio != "" {
// 		// Process the latest user audio input
// 		decodedAudio, err := base64.StdEncoding.DecodeString(latestUserAudio)
// 		if err != nil {
// 			log.Error().Msgf("failed to decode latest user audio: %s", err.Error())
// 			sendError(c, "invalid_audio_data", "Failed to decode audio data", "", "")
// 			return
// 		}
//
// 		// Process the audio input and generate a response
// 		generatedText, generatedAudio, functionCall, err = processAudioResponse(session, decodedAudio)
// 		if err != nil {
// 			log.Error().Msgf("failed to process audio response: %s", err.Error())
// 			sendError(c, "processing_error", "Failed to generate audio response", "", "")
// 			return
// 		}
// 	} else {
//
// 		if session.Instructions != "" {
// 			conversationHistory = append([]schema.Message{{
// 				Role:          "system",
// 				StringContent: session.Instructions,
// 				Content:       session.Instructions,
// 			}}, conversationHistory...)
// 		}
//
// 		funcs := session.Functions
// 		shouldUseFn := len(funcs) > 0 && config.ShouldUseFunctions()
//
// 		// Allow the user to set custom actions via config file
// 		// to be "embedded" in each model
// 		noActionName := "answer"
// 		noActionDescription := "use this action to answer without performing any action"
//
// 		if config.FunctionsConfig.NoActionFunctionName != "" {
// 			noActionName = config.FunctionsConfig.NoActionFunctionName
// 		}
// 		if config.FunctionsConfig.NoActionDescriptionName != "" {
// 			noActionDescription = config.FunctionsConfig.NoActionDescriptionName
// 		}
//
// 		if (!config.FunctionsConfig.GrammarConfig.NoGrammar) && shouldUseFn {
// 			noActionGrammar := functions.Function{
// 				Name:        noActionName,
// 				Description: noActionDescription,
// 				Parameters: map[string]interface{}{
// 					"properties": map[string]interface{}{
// 						"message": map[string]interface{}{
// 							"type":        "string",
// 							"description": "The message to reply the user with",
// 						}},
// 				},
// 			}
//
// 			// Append the no action function
// 			if !config.FunctionsConfig.DisableNoAction {
// 				funcs = append(funcs, noActionGrammar)
// 			}
//
// 			// Update input grammar
// 			jsStruct := funcs.ToJSONStructure(config.FunctionsConfig.FunctionNameKey, config.FunctionsConfig.FunctionNameKey)
// 			g, err := jsStruct.Grammar(config.FunctionsConfig.GrammarOptions()...)
// 			if err == nil {
// 				config.Grammar = g
// 			}
// 		}
//
// 		// Generate a response based on text conversation history
// 		prompt := evaluator.TemplateMessages(conversationHistory, config, funcs, shouldUseFn)
//
// 		generatedText, functionCall, err = processTextResponse(config, session, prompt)
// 		if err != nil {
// 			log.Error().Msgf("failed to process text response: %s", err.Error())
// 			sendError(c, "processing_error", "Failed to generate text response", "", "")
// 			return
// 		}
// 		log.Debug().Any("text", generatedText).Msg("Generated text response")
// 	}
//
// 	if functionCall != nil {
// 		// The model wants to call a function
// 		// Create a function_call item and send it to the client
// 		item := &Item{
// 			ID:           generateItemID(),
// 			Object:       "realtime.item",
// 			Type:         "function_call",
// 			Status:       "completed",
// 			Role:         "assistant",
// 			FunctionCall: functionCall,
// 		}
//
// 		// Add item to conversation
// 		conversation.Lock.Lock()
// 		conversation.Items = append(conversation.Items, item)
// 		conversation.Lock.Unlock()
//
// 		// Send item.created event
// 		sendEvent(c, OutgoingMessage{
// 			Type: "conversation.item.created",
// 			Item: item,
// 		})
//
// 		// Optionally, you can generate a message to the user indicating the function call
// 		// For now, we'll assume the client handles the function call and may trigger another response
//
// 	} else {
// 		// Send response.stream messages
// 		if generatedAudio != nil {
// 			// If generatedAudio is available, send it as audio
// 			encodedAudio := base64.StdEncoding.EncodeToString(generatedAudio)
// 			outgoingMsg := OutgoingMessage{
// 				Type:  "response.stream",
// 				Audio: encodedAudio,
// 			}
// 			sendEvent(c, outgoingMsg)
// 		} else {
// 			// Send text response (could be streamed in chunks)
// 			chunks := splitResponseIntoChunks(generatedText)
// 			for _, chunk := range chunks {
// 				outgoingMsg := OutgoingMessage{
// 					Type:    "response.stream",
// 					Content: chunk,
// 				}
// 				sendEvent(c, outgoingMsg)
// 			}
// 		}
//
// 		// Send response.done message
// 		sendEvent(c, OutgoingMessage{
// 			Type: "response.done",
// 		})
//
// 		// Add the assistant's response to the conversation
// 		content := []ConversationContent{}
// 		if generatedAudio != nil {
// 			content = append(content, ConversationContent{
// 				Type:  "audio",
// 				Audio: base64.StdEncoding.EncodeToString(generatedAudio),
// 			})
// 			// Optionally include a text transcript
// 			if generatedText != "" {
// 				content = append(content, ConversationContent{
// 					Type: "text",
// 					Text: generatedText,
// 				})
// 			}
// 		} else {
// 			content = append(content, ConversationContent{
// 				Type: "text",
// 				Text: generatedText,
// 			})
// 		}
//
// 		item := &Item{
// 			ID:      generateItemID(),
// 			Object:  "realtime.item",
// 			Type:    "message",
// 			Status:  "completed",
// 			Role:    "assistant",
// 			Content: content,
// 		}
//
// 		// Add item to conversation
// 		conversation.Lock.Lock()
// 		conversation.Items = append(conversation.Items, item)
// 		conversation.Lock.Unlock()
//
// 		// Send item.created event
// 		sendEvent(c, OutgoingMessage{
// 			Type: "conversation.item.created",
// 			Item: item,
// 		})
//
// 		log.Debug().Any("item", item).Msg("Realtime response sent")
// 	}
// }

// Function to process text response and detect function calls
func processTextResponse(config *config.ModelConfig, session *Session, prompt string) (string, *FunctionCall, error) {

	// Placeholder implementation
	// Replace this with actual model inference logic using session.Model and prompt
	// For example, the model might return a special token or JSON indicating a function call

	/*
		predFunc, err := backend.ModelInference(context.Background(), prompt, input.Messages, images, videos, audios, ml, *config, o, nil)

		result, tokenUsage, err := ComputeChoices(input, prompt, config, startupOptions, ml, func(s string, c *[]schema.Choice) {
			if !shouldUseFn {
				// no function is called, just reply and use stop as finish reason
				*c = append(*c, schema.Choice{FinishReason: "stop", Index: 0, Message: &schema.Message{Role: "assistant", Content: &s}})
				return
			}

			textContentToReturn = functions.ParseTextContent(s, config.FunctionsConfig)
			s = functions.CleanupLLMResult(s, config.FunctionsConfig)
			results := functions.ParseFunctionCall(s, config.FunctionsConfig)
			log.Debug().Msgf("Text content to return: %s", textContentToReturn)
			noActionsToRun := len(results) > 0 && results[0].Name == noActionName || len(results) == 0

			switch {
			case noActionsToRun:
				result, err := handleQuestion(config, input, ml, startupOptions, results, s, predInput)
				if err != nil {
					log.Error().Err(err).Msg("error handling question")
					return
				}
				*c = append(*c, schema.Choice{
					Message: &schema.Message{Role: "assistant", Content: &result}})
			default:
				toolChoice := schema.Choice{
					Message: &schema.Message{
						Role: "assistant",
					},
				}

				if len(input.Tools) > 0 {
					toolChoice.FinishReason = "tool_calls"
				}

				for _, ss := range results {
					name, args := ss.Name, ss.Arguments
					if len(input.Tools) > 0 {
						// If we are using tools, we condense the function calls into
						// a single response choice with all the tools
						toolChoice.Message.Content = textContentToReturn
						toolChoice.Message.ToolCalls = append(toolChoice.Message.ToolCalls,
							schema.ToolCall{
								ID:   id,
								Type: "function",
								FunctionCall: schema.FunctionCall{
									Name:      name,
									Arguments: args,
								},
							},
						)
					} else {
						// otherwise we return more choices directly
						*c = append(*c, schema.Choice{
							FinishReason: "function_call",
							Message: &schema.Message{
								Role:    "assistant",
								Content: &textContentToReturn,
								FunctionCall: map[string]interface{}{
									"name":      name,
									"arguments": args,
								},
							},
						})
					}
				}

				if len(input.Tools) > 0 {
					// we need to append our result if we are using tools
					*c = append(*c, toolChoice)
				}
			}

		}, nil)
		if err != nil {
			return err
		}

		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "chat.completion",
			Usage: schema.OpenAIUsage{
				PromptTokens:     tokenUsage.Prompt,
				CompletionTokens: tokenUsage.Completion,
				TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
			},
		}
		respData, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", respData)

		// Return the prediction in the response body
		return c.JSON(resp)

	*/

	// TODO: use session.ModelInterface...
	// Simulate a function call
	if strings.Contains(prompt, "weather") {
		functionCall := &FunctionCall{
			Name: "get_weather",
			Arguments: map[string]interface{}{
				"location": "New York",
				"scale":    "celsius",
			},
		}
		return "", functionCall, nil
	}

	// Otherwise, return a normal text response
	return "This is a generated response based on the conversation.", nil, nil
}

// Function to process audio response and detect function calls
func processAudioResponse(session *Session, audioData []byte) (string, []byte, *FunctionCall, error) {
	// TODO: Do the below or use an any-to-any model like Qwen Omni
	// Implement the actual model inference logic using session.Model and audioData
	// For example:
	// 1. Transcribe the audio to text
	// 2. Generate a response based on the transcribed text
	// 3. Check if the model wants to call a function
	// 4. Convert the response text to speech (audio)
	//
	// Placeholder implementation:

	// TODO: template eventual messages, like chat.go
	reply, err := session.ModelInterface.Predict(context.Background(), &proto.PredictOptions{
		Prompt: "What's the weather in New York?",
	})

	if err != nil {
		return "", nil, nil, err
	}

	generatedAudio := reply.Audio

	transcribedText := "What's the weather in New York?"
	var functionCall *FunctionCall

	// Simulate a function call
	if strings.Contains(transcribedText, "weather") {
		functionCall = &FunctionCall{
			Name: "get_weather",
			Arguments: map[string]interface{}{
				"location": "New York",
				"scale":    "celsius",
			},
		}
		return "", nil, functionCall, nil
	}

	// Generate a response
	generatedText := "This is a response to your speech input."

	return generatedText, generatedAudio, nil, nil
}

// Function to split the response into chunks (for streaming)
func splitResponseIntoChunks(response string) []string {
	// Split the response into chunks of fixed size
	chunkSize := 50 // characters per chunk
	var chunks []string
	for len(response) > 0 {
		if len(response) > chunkSize {
			chunks = append(chunks, response[:chunkSize])
			response = response[chunkSize:]
		} else {
			chunks = append(chunks, response)
			break
		}
	}
	return chunks
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

// Structures for 'response.create' messages
type ResponseCreate struct {
	Modalities   []string            `json:"modalities,omitempty"`
	Instructions string              `json:"instructions,omitempty"`
	Functions    functions.Functions `json:"functions,omitempty"`
	// Other fields as needed
}
