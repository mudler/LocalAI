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

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/sound"

	"google.golang.org/grpc"

	"github.com/mudler/xlog"
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
		Tools:                   []types.Tool{},
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

// Map to store sessions (in-memory)
var sessions = make(map[string]*Session)
var sessionLock sync.Mutex

// TODO: implement interface as we start to define usages
type Model interface {
	VAD(ctx context.Context, in *proto.VADRequest, opts ...grpc.CallOption) (*proto.VADResponse, error)
	Transcribe(ctx context.Context, in *proto.TranscriptRequest, opts ...grpc.CallOption) (*proto.TranscriptResult, error)
	Predict(ctx context.Context, in *proto.PredictOptions, opts ...grpc.CallOption) (*proto.Reply, error)
	PredictStream(ctx context.Context, in *proto.PredictOptions, f func(*proto.Reply), opts ...grpc.CallOption) error
	TTS(ctx context.Context, in *proto.TTSRequest, opts ...grpc.CallOption) (*proto.Result, error)
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
		if model == "" {
			model = "gpt-4o"
		}
		intent := c.QueryParam("intent")

		registerRealtime(application, model, intent)(ws)
		return nil
	}
}

func registerRealtime(application *application.Application, model, intent string) func(c *websocket.Conn) {
	return func(c *websocket.Conn) {

		evaluator := application.TemplatesEvaluator()
		xlog.Debug("WebSocket connection established", "address", c.RemoteAddr().String())
		if intent != "transcription" {
			sendNotImplemented(c, "Only transcription mode is supported which requires the intent=transcription parameter")
		}

		xlog.Debug("Realtime params", "model", model, "intent", intent)

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
			xlog.Error("failed to load model", "error", err)
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

		vadServerStarted := false
		toggleVAD := func() {
			if session.TurnDetection.Type == types.ServerTurnDetectionTypeServerVad && !vadServerStarted {
				xlog.Debug("Starting VAD goroutine...")
				wg.Add(1)
				go func() {
					defer wg.Done()
					conversation := session.Conversations[session.DefaultConversationID]
					handleVAD(cfg, evaluator, session, conversation, c, done)
				}()
				vadServerStarted = true
			} else if session.TurnDetection.Type != types.ServerTurnDetectionTypeServerVad && vadServerStarted {
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
			var incomingMsg IncomingMessage
			if err := json.Unmarshal(msg, &incomingMsg); err != nil {
				xlog.Error("invalid json", "error", err)
				sendError(c, "invalid_json", "Invalid JSON format", "", "")
				continue
			}

			var sessionUpdate types.ClientSession
			switch incomingMsg.Type {
			case types.ClientEventTypeTranscriptionSessionUpdate:
				xlog.Debug("recv", "message", string(msg))

				if err := json.Unmarshal(incomingMsg.Session, &sessionUpdate); err != nil {
					xlog.Error("failed to unmarshal 'transcription_session.update'", "error", err)
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
					xlog.Error("failed to update session", "error", err)
					sendError(c, "session_update_error", "Failed to update session", "", "")
					continue
				}

				toggleVAD()

				sendEvent(c, types.SessionUpdatedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
						Type:    types.ServerEventTypeTranscriptionSessionUpdated,
					},
					Session: session.ToServer(),
				})

			case types.ClientEventTypeSessionUpdate:
				xlog.Debug("recv", "message", string(msg))

				// Update session configurations
				if err := json.Unmarshal(incomingMsg.Session, &sessionUpdate); err != nil {
					xlog.Error("failed to unmarshal 'session.update'", "error", err)
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
					xlog.Error("failed to update session", "error", err)
					sendError(c, "session_update_error", "Failed to update session", "", "")
					continue
				}

				toggleVAD()

				sendEvent(c, types.SessionUpdatedEvent{
					ServerEventBase: types.ServerEventBase{
						EventID: "event_TODO",
						Type:    types.ServerEventTypeSessionUpdated,
					},
					Session: session.ToServer(),
				})

			case types.ClientEventTypeInputAudioBufferAppend:
				// Handle 'input_audio_buffer.append'
				if incomingMsg.Audio == "" {
					xlog.Error("Audio data is missing in 'input_audio_buffer.append'")
					sendError(c, "missing_audio_data", "Audio data is missing", "", "")
					continue
				}

				// Decode base64 audio data
				decodedAudio, err := base64.StdEncoding.DecodeString(incomingMsg.Audio)
				if err != nil {
					xlog.Error("failed to decode audio data", "error", err)
					sendError(c, "invalid_audio_data", "Failed to decode audio data", "", "")
					continue
				}

				// Append to InputAudioBuffer
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = append(session.InputAudioBuffer, decodedAudio...)
				session.AudioBufferLock.Unlock()

			case types.ClientEventTypeInputAudioBufferCommit:
				xlog.Debug("recv", "message", string(msg))

				sessionLock.Lock()
				td := session.TurnDetection.Type
				sessionLock.Unlock()

				// TODO: At the least need to check locking and timer state in the VAD Go routine before allowing this
				if td == types.ServerTurnDetectionTypeServerVad {
					sendNotImplemented(c, "input_audio_buffer.commit in conjunction with VAD")
					continue
				}

				session.AudioBufferLock.Lock()
				allAudio := make([]byte, len(session.InputAudioBuffer))
				copy(allAudio, session.InputAudioBuffer)
				session.InputAudioBuffer = nil
				session.AudioBufferLock.Unlock()

				go commitUtterance(context.TODO(), allAudio, cfg, evaluator, session, conversation, c)

			case types.ClientEventTypeConversationItemCreate:
				xlog.Debug("recv", "message", string(msg))

				// Handle creating new conversation items
				var item types.ConversationItemCreateEvent
				if err := json.Unmarshal(incomingMsg.Item, &item); err != nil {
					xlog.Error("failed to unmarshal 'conversation.item.create'", "error", err)
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
						xlog.Error("failed to unmarshal 'response.create' response object", "error", err)
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
				xlog.Debug("recv", "message", string(msg))

				// Handle cancellation of ongoing responses
				// Implement cancellation logic as needed
				sendNotImplemented(c, "response.cancel")

			default:
				xlog.Error("unknown message type", "type", incomingMsg.Type)
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
		xlog.Error("failed to marshal event", "error", err)
		return
	}
	if err = c.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		xlog.Error("write error", "error", err)
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

	session.TranscriptionOnly = true

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

	if trUpd != nil {
		trCur.Language = trUpd.Language
		trCur.Prompt = trUpd.Prompt
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

	session.TranscriptionOnly = false

	if update.Model != "" {
		pipeline := config.Pipeline{
			VAD:           vadModel,
			LLM:           update.Model,
			Transcription: "whisper-1",
			TTS:           "tts-1",
			// TODO: Allow user to configure transcription and audio gen models
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
					AudioStartMs: time.Since(startTime).Milliseconds(),
				})
				speechStarted = true
			}

			// Segment still in progress when audio ended
			segEndTime := segments[len(segments)-1].GetEnd()
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
						Type:    types.ServerEventTypeInputAudioBufferSpeechStopped,
					},
					AudioEndMs: time.Since(startTime).Milliseconds(),
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
		tr, err := session.ModelInterface.Transcribe(ctx, &proto.TranscriptRequest{
			Dst:       f.Name(),
			Language:  session.InputAudioTranscription.Language,
			Translate: false,
			Threads:   uint32(*cfg.Threads),
			Prompt:    session.InputAudioTranscription.Prompt,
		})
		if err != nil {
			sendError(c, "transcription_failed", err.Error(), "", "event_TODO")
		}

		transcript = tr.GetText()
		sendEvent(c, types.ResponseAudioTranscriptDoneEvent{
			ServerEventBase: types.ServerEventBase{
				Type:    types.ServerEventTypeResponseAudioTranscriptDone,
				EventID: "event_TODO",
			},

			ItemID:       generateItemID(),
			ResponseID:   "resp_TODO",
			OutputIndex:  0,
			ContentIndex: 0,
			Transcript:   transcript,
		})
	} else {
		sendNotImplemented(c, "any-to-any models")
		return
	}

	if !session.TranscriptionOnly {
		generateResponse(cfg, evaluator, session, utt, transcript, conv, ResponseCreate{}, c, websocket.TextMessage)
	}
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

// Function to generate a response based on the conversation
func generateResponse(config *config.ModelConfig, evaluator *templates.Evaluator, session *Session, utt []byte, transcript string, conv *Conversation, responseCreate ResponseCreate, c *websocket.Conn, mt int) {
	xlog.Debug("Generating realtime response...")

	item := types.MessageItem{
		ID:     generateItemID(),
		Type:   "message",
		Status: "completed",
		Role:   "user",
		Content: []types.MessageContentPart{
			{
				Type:       types.MessageContentTypeInputAudio,
				Audio:      base64.StdEncoding.EncodeToString(utt),
				Transcript: transcript,
			},
		},
	}
	conv.Lock.Lock()
	conv.Items = append(conv.Items, &item)
	conv.Lock.Unlock()

	sendEvent(c, types.ConversationItemAddedEvent{
		ServerEventBase: types.ServerEventBase{
			Type: types.ServerEventTypeConversationItemAdded,
		},
		Item: item,
	})

	// Compile the conversation history
	conv.Lock.Lock()
	var conversationHistory schema.Messages
	for _, item := range conv.Items {
		for _, content := range item.Content {
			switch content.Type {
			case types.MessageContentTypeInputText, types.MessageContentTypeOutputText:
				conversationHistory = append(conversationHistory, schema.Message{
					Role:          string(item.Role),
					StringContent: content.Text,
					Content:       content.Text,
				})
			case types.MessageContentTypeInputAudio, types.MessageContentTypeOutputAudio:
				conversationHistory = append(conversationHistory, schema.Message{
					Role:          string(item.Role),
					StringContent: content.Transcript,
					Content:       content.Transcript,
					StringAudios:  []string{content.Audio},
				})
			}
		}
	}
	conv.Lock.Unlock()

	item = types.MessageItem{
		ID:     generateItemID(),
		Type:   types.MessageItemTypeMessage,
		Status: types.ItemStatusInProgress,
		Role:   types.MessageRoleAssistant,
	}

	sendEvent(c, types.ConversationItemAddedEvent{
		ServerEventBase: types.ServerEventBase{
			Type: types.ServerEventTypeConversationItemAdded,
		},
		Item: item,
	})

	conv.Lock.Lock()
	conv.Items = append(conv.Items, &item)
	conv.Lock.Unlock()
	// XXX: And from now item must be accessed with conv.Lock held

	input := schema.OpenAIRequest{
		Messages: conversationHistory,
	}

	// TODO: This logic is shared with llm.go and the chat API. We probably want to refactor it
	var protoMessages []*proto.Message
	var predInput string
	if !config.TemplateConfig.UseTokenizerTemplate {
		predInput = evaluator.TemplateMessages(input, input.Messages, config, []functions.Function{}, false)

		xlog.Debug("Prompt (after templating)", "prompt", predInput)
		if config.Grammar != "" {
			xlog.Debug("Grammar", "grammar", config.Grammar)
		}

		protoMessages = conversationHistory.ToProto()
	}

	opts := proto.PredictOptions{}
	opts.Prompt = predInput
	opts.Messages = protoMessages
	opts.UseTokenizerTemplate = config.TemplateConfig.UseTokenizerTemplate

	reply, err := session.ModelInterface.Predict(context.TODO(), &opts)
	if err != nil {
		sendError(c, "inference_failed", fmt.Sprintf("backend error: %v", err), "", item.ID)
		return
	}

	response := string(reply.Message)
	if config.TemplateConfig.ReplyPrefix != "" {
		response = config.TemplateConfig.ReplyPrefix + response
	}

	// For some reason we don't send the audio in the ItemDone event according to OpenAI. The user must request it with conversation.item.retrieve.
	// This almost makes sense when not using a real any-to-any model because we can send the transcript before the audio is ready, but it's not clear
	// why the user should request the audio? If it is to save bandwidth then we shouldn't send the user's own audio back to them and besides the common case
	// is to want the generated audio
	conv.Lock.Lock()
	item.Status = types.ItemStatusCompleted
	item.Content = []types.MessageContentPart{
		{
			Type: types.MessageContentTypeOutputAudio,
			Transcript: response,
		},
	}
	conv.Lock.Unlock()

	f, err := os.CreateTemp("", "realtime-tts-*.wav")
	if err != nil {
		xlog.Error("failed to create temp file for TTS", "error", err)
		sendError(c, "tts_error", "Failed to create temp file for TTS", "", item.ID)
		return
	}
	defer os.Remove(f.Name())

	modelWrapped, ok := session.ModelInterface.(*wrappedModel)
	if !ok {
		xlog.Error("model is not wrappedModel")
		sendError(c, "model_error", "Model is not wrappedModel", "", item.ID)
		return
	}

	ttsReq := &proto.TTSRequest{
		Text:  response,
		Voice: session.Voice,
		Model: modelWrapped.TTSConfig.Model,
		Dst:   f.Name(),
	}

	res, err := session.ModelInterface.TTS(context.TODO(), ttsReq)
	if err != nil {
		xlog.Error("TTS failed", "error", err)
		sendError(c, "tts_error", fmt.Sprintf("TTS generation failed: %v", err), "", item.ID)
		return
	}
	if !res.Success {
		xlog.Error("TTS failed", "message", res.Message)
		sendError(c, "tts_error", fmt.Sprintf("TTS generation failed: %s", res.Message), "", item.ID)
		return
	}

	audioBytes, err := os.ReadFile(f.Name())
	if err != nil {
		xlog.Error("failed to read TTS file", "error", err)
		sendError(c, "tts_error", fmt.Sprintf("Failed to read TTS audio: %v", err), "", item.ID)
		return
	}

	conv.Lock.Lock()
	item.Content[0].Audio = base64.StdEncoding.EncodeToString(audioBytes)
	conv.Lock.Unlock()

	// TODO: should we just send the audio now despite the OpenAI docs?
	// TODO: save messages in a KV store with eviction for later retrieval with conversation.item.retrieve
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
