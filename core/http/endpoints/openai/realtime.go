package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-audio/wav"

	"github.com/go-audio/audio"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/sound"
	"github.com/mudler/LocalAI/pkg/templates"

	"google.golang.org/grpc"

	"github.com/rs/zerolog/log"
)

// A model can be "emulated" that is: transcribe audio to text -> feed text to the LLM -> generate audio as result
// If the model support instead audio-to-audio, we will use the specific gRPC calls instead

// Session represents a single WebSocket connection and its state
type Session struct {
	ID                    string
	Model                 string
	Voice                 string
	TurnDetection         *TurnDetection `json:"turn_detection"` // "server_vad" or "none"
	Functions             functions.Functions
	Conversations         map[string]*Conversation
	InputAudioBuffer      []byte
	AudioBufferLock       sync.Mutex
	Instructions          string
	DefaultConversationID string
	ModelInterface        Model
}

type TurnDetection struct {
	Type string `json:"type"`
}

// FunctionCall represents a function call initiated by the model
type FunctionCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// Conversation represents a conversation with a list of items
type Conversation struct {
	ID    string
	Items []*Item
	Lock  sync.Mutex
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
	Type     string          `json:"type"`
	Session  json.RawMessage `json:"session,omitempty"`
	Item     json.RawMessage `json:"item,omitempty"`
	Audio    string          `json:"audio,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
	Error    *ErrorMessage   `json:"error,omitempty"`
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
	Predict(ctx context.Context, in *proto.PredictOptions, opts ...grpc.CallOption) (*proto.Reply, error)
	PredictStream(ctx context.Context, in *proto.PredictOptions, f func(*proto.Reply), opts ...grpc.CallOption) error
}

func Realtime(application *application.Application) fiber.Handler {
	return websocket.New(registerRealtime(application))
}

func registerRealtime(application *application.Application) func(c *websocket.Conn) {
	return func(c *websocket.Conn) {

		evaluator := application.TemplatesEvaluator()
		log.Debug().Msgf("WebSocket connection established with '%s'", c.RemoteAddr().String())

		model := c.Params("model")
		if model == "" {
			model = "gpt-4o"
		}

		log.Info().Msgf("New session with model: %s", model)

		sessionID := generateSessionID()
		session := &Session{
			ID:            sessionID,
			Model:         model,   // default model
			Voice:         "alloy", // default voice
			TurnDetection: &TurnDetection{Type: "none"},
			Conversations: make(map[string]*Conversation),
		}

		// Create a default conversation
		conversationID := generateConversationID()
		conversation := &Conversation{
			ID:    conversationID,
			Items: []*Item{},
		}
		session.Conversations[conversationID] = conversation
		session.DefaultConversationID = conversationID

		cfg, err := application.BackendLoader().LoadBackendConfigFileByName(model, application.ModelLoader().ModelPath)
		if err != nil {
			log.Error().Msgf("failed to load model (no config): %s", err.Error())
			sendError(c, "model_load_error", "Failed to load model (no config)", "", "")
			return
		}

		m, err := newModel(
			cfg,
			application.BackendLoader(),
			application.ModelLoader(),
			application.ApplicationConfig(),
			model,
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

		// Send session.created and conversation.created events to the client
		sendEvent(c, OutgoingMessage{
			Type:    "session.created",
			Session: session,
		})
		sendEvent(c, OutgoingMessage{
			Type:         "conversation.created",
			Conversation: conversation,
		})

		var (
			mt   int
			msg  []byte
			wg   sync.WaitGroup
			done = make(chan struct{})
		)

		var vadServerStarted bool

		for {
			if mt, msg, err = c.ReadMessage(); err != nil {
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

			switch incomingMsg.Type {
			case "session.update":
				log.Printf("recv: %s", msg)

				// Update session configurations
				var sessionUpdate Session
				if err := json.Unmarshal(incomingMsg.Session, &sessionUpdate); err != nil {
					log.Error().Msgf("failed to unmarshal 'session.update': %s", err.Error())
					sendError(c, "invalid_session_update", "Invalid session update format", "", "")
					continue
				}
				if err := updateSession(
					session,
					&sessionUpdate,
					application.BackendLoader(),
					application.ModelLoader(),
					application.ApplicationConfig(),
				); err != nil {
					log.Error().Msgf("failed to update session: %s", err.Error())
					sendError(c, "session_update_error", "Failed to update session", "", "")
					continue
				}

				// Acknowledge the session update
				sendEvent(c, OutgoingMessage{
					Type:    "session.updated",
					Session: session,
				})

				if session.TurnDetection.Type == "server_vad" && !vadServerStarted {
					log.Debug().Msg("Starting VAD goroutine...")
					wg.Add(1)
					go func() {
						defer wg.Done()
						conversation := session.Conversations[session.DefaultConversationID]
						handleVAD(cfg, evaluator, session, conversation, c, done)
					}()
					vadServerStarted = true
				} else if vadServerStarted {
					log.Debug().Msg("Stopping VAD goroutine...")

					wg.Add(-1)
					go func() {
						done <- struct{}{}
					}()
					vadServerStarted = false
				}
			case "input_audio_buffer.append":
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

			case "input_audio_buffer.commit":
				log.Printf("recv: %s", msg)

				// Commit the audio buffer to the conversation as a new item
				item := &Item{
					ID:     generateItemID(),
					Object: "realtime.item",
					Type:   "message",
					Status: "completed",
					Role:   "user",
					Content: []ConversationContent{
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
				sendEvent(c, OutgoingMessage{
					Type: "conversation.item.created",
					Item: item,
				})

			case "conversation.item.create":
				log.Printf("recv: %s", msg)

				// Handle creating new conversation items
				var item Item
				if err := json.Unmarshal(incomingMsg.Item, &item); err != nil {
					log.Error().Msgf("failed to unmarshal 'conversation.item.create': %s", err.Error())
					sendError(c, "invalid_item", "Invalid item format", "", "")
					continue
				}

				// Generate item ID and set status
				item.ID = generateItemID()
				item.Object = "realtime.item"
				item.Status = "completed"

				// Add item to conversation
				conversation.Lock.Lock()
				conversation.Items = append(conversation.Items, &item)
				conversation.Lock.Unlock()

				// Send item.created event
				sendEvent(c, OutgoingMessage{
					Type: "conversation.item.created",
					Item: &item,
				})

			case "conversation.item.delete":
				log.Printf("recv: %s", msg)

				// Handle deleting conversation items
				// Implement deletion logic as needed

			case "response.create":
				log.Printf("recv: %s", msg)

				// Handle generating a response
				var responseCreate ResponseCreate
				if len(incomingMsg.Response) > 0 {
					if err := json.Unmarshal(incomingMsg.Response, &responseCreate); err != nil {
						log.Error().Msgf("failed to unmarshal 'response.create' response object: %s", err.Error())
						sendError(c, "invalid_response_create", "Invalid response create format", "", "")
						continue
					}
				}

				// Update session functions if provided
				if len(responseCreate.Functions) > 0 {
					session.Functions = responseCreate.Functions
				}

				// Generate a response based on the conversation history
				wg.Add(1)
				go func() {
					defer wg.Done()
					generateResponse(cfg, evaluator, session, conversation, responseCreate, c, mt)
				}()

			case "conversation.item.update":
				log.Printf("recv: %s", msg)

				// Handle function_call_output from the client
				var item Item
				if err := json.Unmarshal(incomingMsg.Item, &item); err != nil {
					log.Error().Msgf("failed to unmarshal 'conversation.item.update': %s", err.Error())
					sendError(c, "invalid_item_update", "Invalid item update format", "", "")
					continue
				}

				// Add the function_call_output item to the conversation
				item.ID = generateItemID()
				item.Object = "realtime.item"
				item.Status = "completed"

				conversation.Lock.Lock()
				conversation.Items = append(conversation.Items, &item)
				conversation.Lock.Unlock()

				// Send item.updated event
				sendEvent(c, OutgoingMessage{
					Type: "conversation.item.updated",
					Item: &item,
				})

			case "response.cancel":
				log.Printf("recv: %s", msg)

				// Handle cancellation of ongoing responses
				// Implement cancellation logic as needed

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
func sendEvent(c *websocket.Conn, event OutgoingMessage) {
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
	errorEvent := OutgoingMessage{
		Type: "error",
		Error: &ErrorMessage{
			Type:    "error",
			Code:    code,
			Message: message,
			Param:   param,
			EventID: eventID,
		},
	}
	sendEvent(c, errorEvent)
}

// Function to update session configurations
func updateSession(session *Session, update *Session, cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) error {
	sessionLock.Lock()
	defer sessionLock.Unlock()

	if update.Model != "" {
		cfg, err := cl.LoadBackendConfigFileByName(update.Model, ml.ModelPath)
		if err != nil {
			return err
		}

		m, err := newModel(cfg, cl, ml, appConfig, update.Model)
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
		session.TurnDetection.Type = update.TurnDetection.Type
	}
	if update.Instructions != "" {
		session.Instructions = update.Instructions
	}
	if update.Functions != nil {
		session.Functions = update.Functions
	}

	return nil
}

const (
	sendToVADDelay   = 2 * time.Second
	silenceThreshold = 2 * time.Second
)

// handleVAD is a goroutine that listens for audio data from the client,
// runs VAD on the audio data, and commits utterances to the conversation
func handleVAD(cfg *config.BackendConfig, evaluator *templates.Evaluator, session *Session, conv *Conversation, c *websocket.Conn, done chan struct{}) {
	vadContext, cancel := context.WithCancel(context.Background())
	go func() {
		<-done
		cancel()
	}()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastSegmentCount int
		timeOfLastNewSeg time.Time
		speaking         bool
	)

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			// 1) Copy the entire buffer
			session.AudioBufferLock.Lock()
			allAudio := make([]byte, len(session.InputAudioBuffer))
			copy(allAudio, session.InputAudioBuffer)
			session.AudioBufferLock.Unlock()

			// 2) If there's no audio at all, or just too small samples, just continue
			if len(allAudio) == 0 || len(allAudio) < 32000 {
				continue
			}

			// 3) Run VAD on the entire audio so far
			segments, err := runVAD(vadContext, session, allAudio)
			if err != nil {
				if err.Error() == "unexpected speech end" {
					log.Debug().Msg("VAD cancelled")
					continue
				}
				log.Error().Msgf("failed to process audio: %s", err.Error())
				sendError(c, "processing_error", "Failed to process audio: "+err.Error(), "", "")
				// handle or log error, continue
				continue
			}

			segCount := len(segments)

			if len(segments) == 0 && !speaking && time.Since(timeOfLastNewSeg) > silenceThreshold {
				// no speech detected, and we haven't seen a new segment in > 1s
				// clean up input
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = nil
				session.AudioBufferLock.Unlock()
				log.Debug().Msgf("Detected silence for a while, clearing audio buffer")
				continue
			}

			// 4) If we see more segments than before => "new speech"
			if segCount > lastSegmentCount {
				speaking = true
				lastSegmentCount = segCount
				timeOfLastNewSeg = time.Now()
				log.Debug().Msgf("Detected new speech segment")
			}

			// 5) If speaking, but we haven't seen a new segment in > 1s => finalize
			if speaking && time.Since(timeOfLastNewSeg) > sendToVADDelay {
				log.Debug().Msgf("Detected end of speech segment")
				session.AudioBufferLock.Lock()
				session.InputAudioBuffer = nil
				session.AudioBufferLock.Unlock()
				// user has presumably stopped talking
				commitUtterance(allAudio, cfg, evaluator, session, conv, c)
				// reset state
				speaking = false
				lastSegmentCount = 0
			}
		}
	}
}

func commitUtterance(utt []byte, cfg *config.BackendConfig, evaluator *templates.Evaluator, session *Session, conv *Conversation, c *websocket.Conn) {
	if len(utt) == 0 {
		return
	}
	// Commit logic: create item, broadcast item.created, etc.
	item := &Item{
		ID:     generateItemID(),
		Object: "realtime.item",
		Type:   "message",
		Status: "completed",
		Role:   "user",
		Content: []ConversationContent{
			{
				Type:  "input_audio",
				Audio: base64.StdEncoding.EncodeToString(utt),
			},
		},
	}
	conv.Lock.Lock()
	conv.Items = append(conv.Items, item)
	conv.Lock.Unlock()

	sendEvent(c, OutgoingMessage{
		Type: "conversation.item.created",
		Item: item,
	})

	// save chunk to disk
	f, err := os.CreateTemp("", "audio-*.wav")
	if err != nil {
		log.Error().Msgf("failed to create temp file: %s", err.Error())
		return
	}
	defer f.Close()
	//defer os.Remove(f.Name())
	log.Debug().Msgf("Writing to %s\n", f.Name())

	f.Write(utt)
	f.Sync()

	// trigger the response generation
	generateResponse(cfg, evaluator, session, conv, ResponseCreate{}, c, websocket.TextMessage)
}

// runVAD is a helper that calls the model's VAD method, returning
// true if it detects speech, false if it detects silence
func runVAD(ctx context.Context, session *Session, chunk []byte) ([]*proto.VADSegment, error) {

	adata := sound.BytesToInt16sLE(chunk)

	// Resample from 24kHz to 16kHz
	adata = sound.ResampleInt16(adata, 24000, 16000)

	dec := wav.NewDecoder(bytes.NewReader(chunk))
	dur, err := dec.Duration()
	if err != nil {
		fmt.Printf("failed to get duration: %s\n", err)
	}
	fmt.Printf("duration: %s\n", dur)

	soundIntBuffer := &audio.IntBuffer{
		Format: &audio.Format{SampleRate: 16000, NumChannels: 1},
	}
	soundIntBuffer.Data = sound.ConvertInt16ToInt(adata)

	/* if len(adata) < 16000 {
		log.Debug().Msgf("audio length too small %d", len(session.InputAudioBuffer))
		session.AudioBufferLock.Unlock()
		continue
	} */
	float32Data := soundIntBuffer.AsFloat32Buffer().Data

	resp, err := session.ModelInterface.VAD(ctx, &proto.VADRequest{
		Audio: float32Data,
	})
	if err != nil {
		return nil, err
	}

	// TODO: testing wav decoding
	// dec := wav.NewDecoder(bytes.NewReader(session.InputAudioBuffer))
	// buf, err := dec.FullPCMBuffer()
	// if err != nil {
	// 	//log.Error().Msgf("failed to process audio: %s", err.Error())
	// 	sendError(c, "processing_error", "Failed to process audio: "+err.Error(), "", "")
	// 	session.AudioBufferLock.Unlock()
	// 	continue
	// }

	//float32Data = buf.AsFloat32Buffer().Data

	// If resp.Segments is empty => no speech
	return resp.Segments, nil
}

// Function to generate a response based on the conversation
func generateResponse(config *config.BackendConfig, evaluator *templates.Evaluator, session *Session, conversation *Conversation, responseCreate ResponseCreate, c *websocket.Conn, mt int) {

	log.Debug().Msg("Generating realtime response...")

	// Compile the conversation history
	conversation.Lock.Lock()
	var conversationHistory []schema.Message
	var latestUserAudio string
	for _, item := range conversation.Items {
		for _, content := range item.Content {
			switch content.Type {
			case "input_text", "text":
				conversationHistory = append(conversationHistory, schema.Message{
					Role:          item.Role,
					StringContent: content.Text,
					Content:       content.Text,
				})
			case "input_audio":
				// We do not to turn to text here the audio result.
				// When generating it later on from the LLM,
				// we will also generate text and return it and store it in the conversation
				// Here we just want to get the user audio if there is any as a new input for the conversation.
				if item.Role == "user" {
					latestUserAudio = content.Audio
				}
			}
		}
	}

	conversation.Lock.Unlock()

	var generatedText string
	var generatedAudio []byte
	var functionCall *FunctionCall
	var err error

	if latestUserAudio != "" {
		// Process the latest user audio input
		decodedAudio, err := base64.StdEncoding.DecodeString(latestUserAudio)
		if err != nil {
			log.Error().Msgf("failed to decode latest user audio: %s", err.Error())
			sendError(c, "invalid_audio_data", "Failed to decode audio data", "", "")
			return
		}

		// Process the audio input and generate a response
		generatedText, generatedAudio, functionCall, err = processAudioResponse(session, decodedAudio)
		if err != nil {
			log.Error().Msgf("failed to process audio response: %s", err.Error())
			sendError(c, "processing_error", "Failed to generate audio response", "", "")
			return
		}
	} else {

		if session.Instructions != "" {
			conversationHistory = append([]schema.Message{{
				Role:          "system",
				StringContent: session.Instructions,
				Content:       session.Instructions,
			}}, conversationHistory...)
		}

		funcs := session.Functions
		shouldUseFn := len(funcs) > 0 && config.ShouldUseFunctions()

		// Allow the user to set custom actions via config file
		// to be "embedded" in each model
		noActionName := "answer"
		noActionDescription := "use this action to answer without performing any action"

		if config.FunctionsConfig.NoActionFunctionName != "" {
			noActionName = config.FunctionsConfig.NoActionFunctionName
		}
		if config.FunctionsConfig.NoActionDescriptionName != "" {
			noActionDescription = config.FunctionsConfig.NoActionDescriptionName
		}

		if (!config.FunctionsConfig.GrammarConfig.NoGrammar) && shouldUseFn {
			noActionGrammar := functions.Function{
				Name:        noActionName,
				Description: noActionDescription,
				Parameters: map[string]interface{}{
					"properties": map[string]interface{}{
						"message": map[string]interface{}{
							"type":        "string",
							"description": "The message to reply the user with",
						}},
				},
			}

			// Append the no action function
			if !config.FunctionsConfig.DisableNoAction {
				funcs = append(funcs, noActionGrammar)
			}

			// Update input grammar
			jsStruct := funcs.ToJSONStructure(config.FunctionsConfig.FunctionNameKey, config.FunctionsConfig.FunctionNameKey)
			g, err := jsStruct.Grammar(config.FunctionsConfig.GrammarOptions()...)
			if err == nil {
				config.Grammar = g
			}
		}

		// Generate a response based on text conversation history
		prompt := evaluator.TemplateMessages(conversationHistory, config, funcs, shouldUseFn)

		generatedText, functionCall, err = processTextResponse(config, session, prompt)
		if err != nil {
			log.Error().Msgf("failed to process text response: %s", err.Error())
			sendError(c, "processing_error", "Failed to generate text response", "", "")
			return
		}
		log.Debug().Any("text", generatedText).Msg("Generated text response")
	}

	if functionCall != nil {
		// The model wants to call a function
		// Create a function_call item and send it to the client
		item := &Item{
			ID:           generateItemID(),
			Object:       "realtime.item",
			Type:         "function_call",
			Status:       "completed",
			Role:         "assistant",
			FunctionCall: functionCall,
		}

		// Add item to conversation
		conversation.Lock.Lock()
		conversation.Items = append(conversation.Items, item)
		conversation.Lock.Unlock()

		// Send item.created event
		sendEvent(c, OutgoingMessage{
			Type: "conversation.item.created",
			Item: item,
		})

		// Optionally, you can generate a message to the user indicating the function call
		// For now, we'll assume the client handles the function call and may trigger another response

	} else {
		// Send response.stream messages
		if generatedAudio != nil {
			// If generatedAudio is available, send it as audio
			encodedAudio := base64.StdEncoding.EncodeToString(generatedAudio)
			outgoingMsg := OutgoingMessage{
				Type:  "response.stream",
				Audio: encodedAudio,
			}
			sendEvent(c, outgoingMsg)
		} else {
			// Send text response (could be streamed in chunks)
			chunks := splitResponseIntoChunks(generatedText)
			for _, chunk := range chunks {
				outgoingMsg := OutgoingMessage{
					Type:    "response.stream",
					Content: chunk,
				}
				sendEvent(c, outgoingMsg)
			}
		}

		// Send response.done message
		sendEvent(c, OutgoingMessage{
			Type: "response.done",
		})

		// Add the assistant's response to the conversation
		content := []ConversationContent{}
		if generatedAudio != nil {
			content = append(content, ConversationContent{
				Type:  "audio",
				Audio: base64.StdEncoding.EncodeToString(generatedAudio),
			})
			// Optionally include a text transcript
			if generatedText != "" {
				content = append(content, ConversationContent{
					Type: "text",
					Text: generatedText,
				})
			}
		} else {
			content = append(content, ConversationContent{
				Type: "text",
				Text: generatedText,
			})
		}

		item := &Item{
			ID:      generateItemID(),
			Object:  "realtime.item",
			Type:    "message",
			Status:  "completed",
			Role:    "assistant",
			Content: content,
		}

		// Add item to conversation
		conversation.Lock.Lock()
		conversation.Items = append(conversation.Items, item)
		conversation.Lock.Unlock()

		// Send item.created event
		sendEvent(c, OutgoingMessage{
			Type: "conversation.item.created",
			Item: item,
		})

		log.Debug().Any("item", item).Msg("Realtime response sent")
	}
}

// Function to process text response and detect function calls
func processTextResponse(config *config.BackendConfig, session *Session, prompt string) (string, *FunctionCall, error) {

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

/*
func RegisterRealtime(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, firstModel bool) func(c *websocket.Conn) {
	return func(c *websocket.Conn) {
		modelFile, input, err := readRequest(c, cl, ml, appConfig, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		var (
			mt  int
			msg []byte
			err error
		)
		for {
			if mt, msg, err = c.ReadMessage(); err != nil {
				log.Error().Msgf("read: %s", err.Error())
				break
			}
			log.Printf("recv: %s", msg)

			if err = c.WriteMessage(mt, msg); err != nil {
				log.Error().Msgf("write: %s", err.Error())
				break
			}
		}
	}
}

*/
