package types

// Most of this file was coppied from https://github.com/WqyJh/go-openai-realtime
// Copyright (c) 2024 Qiying Wang MIT License

import (
	"encoding/json"
	"fmt"
	"math"
)

const (
	// Inf is the maximum value for an IntOrInf.
	Inf IntOrInf = math.MaxInt
)

// IntOrInf is a type that can be either an int or "inf".
type IntOrInf int

// IsInf returns true if the value is "inf".
func (m IntOrInf) IsInf() bool {
	return m == Inf
}

// MarshalJSON marshals the IntOrInf to JSON.
func (m IntOrInf) MarshalJSON() ([]byte, error) {
	if m == Inf {
		return []byte("\"inf\""), nil
	}
	return json.Marshal(int(m))
}

// UnmarshalJSON unmarshals the IntOrInf from JSON.
func (m *IntOrInf) UnmarshalJSON(data []byte) error {
	if string(data) == "\"inf\"" {
		*m = Inf
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, (*int)(m))
}

type AudioFormat string

const (
	AudioFormatPcm16    AudioFormat = "pcm16"
	AudioFormatG711Ulaw AudioFormat = "g711_ulaw"
	AudioFormatG711Alaw AudioFormat = "g711_alaw"
)

type Modality string

const (
	ModalityText  Modality = "text"
	ModalityAudio Modality = "audio"
)

type ClientTurnDetectionType string

const (
	ClientTurnDetectionTypeServerVad ClientTurnDetectionType = "server_vad"
)

type ServerTurnDetectionType string

const (
	ServerTurnDetectionTypeNone      ServerTurnDetectionType = "none"
	ServerTurnDetectionTypeServerVad ServerTurnDetectionType = "server_vad"
)

type TurnDetectionType string

const (
	// TurnDetectionTypeNone means turn detection is disabled.
	// This can only be used in ServerSession, not in ClientSession.
	// If you want to disable turn detection, you should send SessionUpdateEvent with TurnDetection set to nil.
	TurnDetectionTypeNone TurnDetectionType = "none"
	// TurnDetectionTypeServerVad use server-side VAD to detect turn.
	// This is default value for newly created session.
	TurnDetectionTypeServerVad TurnDetectionType = "server_vad"
)

type TurnDetectionParams struct {
	// Activation threshold for VAD.
	Threshold float64 `json:"threshold,omitempty"`
	// Audio included before speech starts (in milliseconds).
	PrefixPaddingMs int `json:"prefix_padding_ms,omitempty"`
	// Duration of silence to detect speech stop (in milliseconds).
	SilenceDurationMs int `json:"silence_duration_ms,omitempty"`
	// Whether or not to automatically generate a response when VAD is enabled. true by default.
	CreateResponse *bool `json:"create_response,omitempty"`
}

type ClientTurnDetection struct {
	// Type of turn detection, only "server_vad" is currently supported.
	Type ClientTurnDetectionType `json:"type"`

	TurnDetectionParams
}

type ServerTurnDetection struct {
	// The type of turn detection ("server_vad" or "none").
	Type ServerTurnDetectionType `json:"type"`

	TurnDetectionParams
}

type ToolType string

const (
	ToolTypeFunction ToolType = "function"
)

type ToolChoiceInterface interface {
	ToolChoice()
}

type ToolChoiceString string

func (ToolChoiceString) ToolChoice() {}

const (
	ToolChoiceAuto     ToolChoiceString = "auto"
	ToolChoiceNone     ToolChoiceString = "none"
	ToolChoiceRequired ToolChoiceString = "required"
)

type ToolChoice struct {
	Type     ToolType     `json:"type"`
	Function ToolFunction `json:"function,omitempty"`
}

func (t ToolChoice) ToolChoice() {}

type ToolFunction struct {
	Name string `json:"name"`
}

type MessageRole string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleUser      MessageRole = "user"
)

type InputAudioTranscription struct {
	// The model used for transcription.
	Model    string `json:"model"`
	Language string `json:"language,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
}

type Tool struct {
	Type        ToolType `json:"type"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Parameters  any      `json:"parameters"`
}

type MessageItemType string

const (
	MessageItemTypeMessage            MessageItemType = "message"
	MessageItemTypeFunctionCall       MessageItemType = "function_call"
	MessageItemTypeFunctionCallOutput MessageItemType = "function_call_output"
)

type MessageContentType string

const (
	MessageContentTypeText       MessageContentType = "text"
	MessageContentTypeAudio      MessageContentType = "audio"
	MessageContentTypeTranscript MessageContentType = "transcript"
	MessageContentTypeInputText  MessageContentType = "input_text"
	MessageContentTypeInputAudio MessageContentType = "input_audio"
)

type MessageContentPart struct {
	// The content type.
	Type MessageContentType `json:"type"`
	// The text content. Validated if type is text.
	Text string `json:"text,omitempty"`
	// Base64-encoded audio data. Validated if type is audio.
	Audio string `json:"audio,omitempty"`
	// The transcript of the audio. Validated if type is transcript.
	Transcript string `json:"transcript,omitempty"`
}

type MessageItem struct {
	// The unique ID of the item.
	ID string `json:"id,omitempty"`
	// The type of the item ("message", "function_call", "function_call_output").
	Type MessageItemType `json:"type"`
	// The final status of the item.
	Status ItemStatus `json:"status,omitempty"`
	// The role associated with the item.
	Role MessageRole `json:"role,omitempty"`
	// The content of the item.
	Content []MessageContentPart `json:"content,omitempty"`
	// The ID of the function call, if the item is a function call.
	CallID string `json:"call_id,omitempty"`
	// The name of the function, if the item is a function call.
	Name string `json:"name,omitempty"`
	// The arguments of the function, if the item is a function call.
	Arguments string `json:"arguments,omitempty"`
	// The output of the function, if the item is a function call output.
	Output string `json:"output,omitempty"`
}

type ResponseMessageItem struct {
	MessageItem
	// The object type, must be "realtime.item".
	Object string `json:"object,omitempty"`
}

type Error struct {
	// The type of error (e.g., "invalid_request_error", "server_error").
	Message string `json:"message,omitempty"`
	// Error code, if any.
	Type string `json:"type,omitempty"`
	// A human-readable error message.
	Code string `json:"code,omitempty"`
	// Parameter related to the error, if any.
	Param string `json:"param,omitempty"`
	// The event_id of the client event that caused the error, if applicable.
	EventID string `json:"event_id,omitempty"`
}

// ServerToolChoice is a type that can be used to choose a tool response from the server.
type ServerToolChoice struct {
	String   ToolChoiceString
	Function ToolChoice
}

// UnmarshalJSON is a custom unmarshaler for ServerToolChoice.
func (m *ServerToolChoice) UnmarshalJSON(data []byte) error {
	err := json.Unmarshal(data, &m.Function)
	if err != nil {
		if data[0] == '"' {
			data = data[1:]
		}
		if data[len(data)-1] == '"' {
			data = data[:len(data)-1]
		}
		m.String = ToolChoiceString(data)
		m.Function = ToolChoice{}
		return nil
	}
	return nil
}

// IsFunction returns true if the tool choice is a function call.
func (m *ServerToolChoice) IsFunction() bool {
	return m.Function.Type == ToolTypeFunction
}

// Get returns the ToolChoiceInterface based on the type of tool choice.
func (m ServerToolChoice) Get() ToolChoiceInterface {
	if m.IsFunction() {
		return m.Function
	}
	return m.String
}

type ServerSession struct {
	// The unique ID of the session.
	ID string `json:"id"`
	// The object type, must be "realtime.session".
	Object string `json:"object"`
	// The default model used for this session.
	Model string `json:"model"`
	// The set of modalities the model can respond with.
	Modalities []Modality `json:"modalities,omitempty"`
	// The default system instructions.
	Instructions string `json:"instructions,omitempty"`
	// The voice the model uses to respond - one of alloy, echo, or shimmer.
	Voice string `json:"voice,omitempty"`
	// The format of input audio.
	InputAudioFormat AudioFormat `json:"input_audio_format,omitempty"`
	// The format of output audio.
	OutputAudioFormat AudioFormat `json:"output_audio_format,omitempty"`
	// Configuration for input audio transcription.
	InputAudioTranscription *InputAudioTranscription `json:"input_audio_transcription,omitempty"`
	// Configuration for turn detection.
	TurnDetection *ServerTurnDetection `json:"turn_detection,omitempty"`
	// Tools (functions) available to the model.
	Tools []Tool `json:"tools,omitempty"`
	// How the model chooses tools.
	ToolChoice ServerToolChoice `json:"tool_choice,omitempty"`
	// Sampling temperature.
	Temperature *float32 `json:"temperature,omitempty"`
	// Maximum number of output tokens.
	MaxOutputTokens IntOrInf `json:"max_response_output_tokens,omitempty"`
}

type ItemStatus string

const (
	ItemStatusInProgress ItemStatus = "in_progress"
	ItemStatusCompleted  ItemStatus = "completed"
	ItemStatusIncomplete ItemStatus = "incomplete"
)

type Conversation struct {
	// The unique ID of the conversation.
	ID string `json:"id"`
	// The object type, must be "realtime.conversation".
	Object string `json:"object"`
}

type ResponseStatus string

const (
	ResponseStatusInProgress ResponseStatus = "in_progress"
	ResponseStatusCompleted  ResponseStatus = "completed"
	ResponseStatusCancelled  ResponseStatus = "cancelled"
	ResponseStatusIncomplete ResponseStatus = "incomplete"
	ResponseStatusFailed     ResponseStatus = "failed"
)

type CachedTokensDetails struct {
	TextTokens  int `json:"text_tokens"`
	AudioTokens int `json:"audio_tokens"`
}

type InputTokenDetails struct {
	CachedTokens        int                 `json:"cached_tokens"`
	TextTokens          int                 `json:"text_tokens"`
	AudioTokens         int                 `json:"audio_tokens"`
	CachedTokensDetails CachedTokensDetails `json:"cached_tokens_details,omitempty"`
}

type OutputTokenDetails struct {
	TextTokens  int `json:"text_tokens"`
	AudioTokens int `json:"audio_tokens"`
}

type Usage struct {
	TotalTokens  int `json:"total_tokens"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// Input token details.
	InputTokenDetails InputTokenDetails `json:"input_token_details,omitempty"`
	// Output token details.
	OutputTokenDetails OutputTokenDetails `json:"output_token_details,omitempty"`
}

type Response struct {
	// The unique ID of the response.
	ID string `json:"id"`
	// The object type, must be "realtime.response".
	Object string `json:"object"`
	// The status of the response.
	Status ResponseStatus `json:"status"`
	// Additional details about the status.
	StatusDetails any `json:"status_details,omitempty"`
	// The list of output items generated by the response.
	Output []ResponseMessageItem `json:"output"`
	// Usage statistics for the response.
	Usage *Usage `json:"usage,omitempty"`
}

type RateLimit struct {
	// The name of the rate limit ("requests", "tokens", "input_tokens", "output_tokens").
	Name string `json:"name"`
	// The maximum allowed value for the rate limit.
	Limit int `json:"limit"`
	// The remaining value before the limit is reached.
	Remaining int `json:"remaining"`
	// Seconds until the rate limit resets.
	ResetSeconds float64 `json:"reset_seconds"`
}

// ClientEventType is the type of client event. See https://platform.openai.com/docs/guides/realtime/client-events
type ClientEventType string

const (
	ClientEventTypeSessionUpdate              ClientEventType = "session.update"
	ClientEventTypeTranscriptionSessionUpdate ClientEventType = "transcription_session.update"
	ClientEventTypeInputAudioBufferAppend     ClientEventType = "input_audio_buffer.append"
	ClientEventTypeInputAudioBufferCommit     ClientEventType = "input_audio_buffer.commit"
	ClientEventTypeInputAudioBufferClear      ClientEventType = "input_audio_buffer.clear"
	ClientEventTypeConversationItemCreate     ClientEventType = "conversation.item.create"
	ClientEventTypeConversationItemTruncate   ClientEventType = "conversation.item.truncate"
	ClientEventTypeConversationItemDelete     ClientEventType = "conversation.item.delete"
	ClientEventTypeResponseCreate             ClientEventType = "response.create"
	ClientEventTypeResponseCancel             ClientEventType = "response.cancel"
)

// ClientEvent is the interface for client event.
type ClientEvent interface {
	ClientEventType() ClientEventType
}

// EventBase is the base struct for all client events.
type EventBase struct {
	// Optional client-generated ID used to identify this event.
	EventID string `json:"event_id,omitempty"`
}

type ClientSession struct {
	Model string `json:"model,omitempty"`
	// The set of modalities the model can respond with. To disable audio, set this to ["text"].
	Modalities []Modality `json:"modalities,omitempty"`
	// The default system instructions prepended to model calls.
	Instructions string `json:"instructions,omitempty"`
	// The voice the model uses to respond - one of alloy, echo, or shimmer. Cannot be changed once the model has responded with audio at least once.
	Voice string `json:"voice,omitempty"`
	// The format of input audio. Options are "pcm16", "g711_ulaw", or "g711_alaw".
	InputAudioFormat AudioFormat `json:"input_audio_format,omitempty"`
	// The format of output audio. Options are "pcm16", "g711_ulaw", or "g711_alaw".
	OutputAudioFormat AudioFormat `json:"output_audio_format,omitempty"`
	// Configuration for input audio transcription. Can be set to `nil` to turn off.
	InputAudioTranscription *InputAudioTranscription `json:"input_audio_transcription,omitempty"`
	// Configuration for turn detection. Can be set to `nil` to turn off.
	TurnDetection *ClientTurnDetection `json:"turn_detection"`
	// Tools (functions) available to the model.
	Tools []Tool `json:"tools,omitempty"`
	// How the model chooses tools. Options are "auto", "none", "required", or specify a function.
	ToolChoice ToolChoiceInterface `json:"tool_choice,omitempty"`
	// Sampling temperature for the model.
	Temperature *float32 `json:"temperature,omitempty"`
	// Maximum number of output tokens for a single assistant response, inclusive of tool calls. Provide an integer between 1 and 4096 to limit output tokens, or "inf" for the maximum available tokens for a given model. Defaults to "inf".
	MaxOutputTokens IntOrInf `json:"max_response_output_tokens,omitempty"`
}

type CreateSessionRequest struct {
	ClientSession

	// The Realtime model used for this session.
	Model string `json:"model,omitempty"`
}

type ClientSecret struct {
	// Ephemeral key usable in client environments to authenticate connections to the Realtime API. Use this in client-side environments rather than a standard API token, which should only be used server-side.
	Value string `json:"value"`
	// Timestamp for when the token expires. Currently, all tokens expire after one minute.
	ExpiresAt int64 `json:"expires_at"`
}

type CreateSessionResponse struct {
	ServerSession

	// Ephemeral key returned by the API.
	ClientSecret ClientSecret `json:"client_secret"`
}

// SessionUpdateEvent is the event for session update.
// Send this event to update the session’s default configuration.
// See https://platform.openai.com/docs/api-reference/realtime-client-events/session/update
type SessionUpdateEvent struct {
	EventBase
	// Session configuration to update.
	Session ClientSession `json:"session"`
}

func (m SessionUpdateEvent) ClientEventType() ClientEventType {
	return ClientEventTypeSessionUpdate
}

func (m SessionUpdateEvent) MarshalJSON() ([]byte, error) {
	type sessionUpdateEvent SessionUpdateEvent
	v := struct {
		*sessionUpdateEvent
		Type ClientEventType `json:"type"`
	}{
		sessionUpdateEvent: (*sessionUpdateEvent)(&m),
		Type:               m.ClientEventType(),
	}
	return json.Marshal(v)
}

// InputAudioBufferAppendEvent is the event for input audio buffer append.
// Send this event to append audio bytes to the input audio buffer.
// See https://platform.openai.com/docs/api-reference/realtime-client-events/input_audio_buffer/append
type InputAudioBufferAppendEvent struct {
	EventBase
	Audio string `json:"audio"` // Base64-encoded audio bytes.
}

func (m InputAudioBufferAppendEvent) ClientEventType() ClientEventType {
	return ClientEventTypeInputAudioBufferAppend
}

func (m InputAudioBufferAppendEvent) MarshalJSON() ([]byte, error) {
	type inputAudioBufferAppendEvent InputAudioBufferAppendEvent
	v := struct {
		*inputAudioBufferAppendEvent
		Type ClientEventType `json:"type"`
	}{
		inputAudioBufferAppendEvent: (*inputAudioBufferAppendEvent)(&m),
		Type:                        m.ClientEventType(),
	}
	return json.Marshal(v)
}

// InputAudioBufferCommitEvent is the event for input audio buffer commit.
// Send this event to commit audio bytes to a user message.
// See https://platform.openai.com/docs/api-reference/realtime-client-events/input_audio_buffer/commit
type InputAudioBufferCommitEvent struct {
	EventBase
}

func (m InputAudioBufferCommitEvent) ClientEventType() ClientEventType {
	return ClientEventTypeInputAudioBufferCommit
}

func (m InputAudioBufferCommitEvent) MarshalJSON() ([]byte, error) {
	type inputAudioBufferCommitEvent InputAudioBufferCommitEvent
	v := struct {
		*inputAudioBufferCommitEvent
		Type ClientEventType `json:"type"`
	}{
		inputAudioBufferCommitEvent: (*inputAudioBufferCommitEvent)(&m),
		Type:                        m.ClientEventType(),
	}
	return json.Marshal(v)
}

// InputAudioBufferClearEvent is the event for input audio buffer clear.
// Send this event to clear the audio bytes in the buffer.
// See https://platform.openai.com/docs/api-reference/realtime-client-events/input_audio_buffer/clear
type InputAudioBufferClearEvent struct {
	EventBase
}

func (m InputAudioBufferClearEvent) ClientEventType() ClientEventType {
	return ClientEventTypeInputAudioBufferClear
}

func (m InputAudioBufferClearEvent) MarshalJSON() ([]byte, error) {
	type inputAudioBufferClearEvent InputAudioBufferClearEvent
	v := struct {
		*inputAudioBufferClearEvent
		Type ClientEventType `json:"type"`
	}{
		inputAudioBufferClearEvent: (*inputAudioBufferClearEvent)(&m),
		Type:                       m.ClientEventType(),
	}
	return json.Marshal(v)
}

// ConversationItemCreateEvent is the event for conversation item create.
// Send this event when adding an item to the conversation.
// See https://platform.openai.com/docs/api-reference/realtime-client-events/conversation/item/create
type ConversationItemCreateEvent struct {
	EventBase
	// The ID of the preceding item after which the new item will be inserted.
	PreviousItemID string `json:"previous_item_id,omitempty"`
	// The item to add to the conversation.
	Item MessageItem `json:"item"`
}

func (m ConversationItemCreateEvent) ClientEventType() ClientEventType {
	return ClientEventTypeConversationItemCreate
}

func (m ConversationItemCreateEvent) MarshalJSON() ([]byte, error) {
	type conversationItemCreateEvent ConversationItemCreateEvent
	v := struct {
		*conversationItemCreateEvent
		Type ClientEventType `json:"type"`
	}{
		conversationItemCreateEvent: (*conversationItemCreateEvent)(&m),
		Type:                        m.ClientEventType(),
	}
	return json.Marshal(v)
}

// ConversationItemTruncateEvent is the event for conversation item truncate.
// Send this event when you want to truncate a previous assistant message’s audio.
// See https://platform.openai.com/docs/api-reference/realtime-client-events/conversation/item/truncate
type ConversationItemTruncateEvent struct {
	EventBase
	// The ID of the assistant message item to truncate.
	ItemID string `json:"item_id"`
	// The index of the content part to truncate.
	ContentIndex int `json:"content_index"`
	// Inclusive duration up to which audio is truncated, in milliseconds.
	AudioEndMs int `json:"audio_end_ms"`
}

func (m ConversationItemTruncateEvent) ClientEventType() ClientEventType {
	return ClientEventTypeConversationItemTruncate
}

func (m ConversationItemTruncateEvent) MarshalJSON() ([]byte, error) {
	type conversationItemTruncateEvent ConversationItemTruncateEvent
	v := struct {
		*conversationItemTruncateEvent
		Type ClientEventType `json:"type"`
	}{
		conversationItemTruncateEvent: (*conversationItemTruncateEvent)(&m),
		Type:                          m.ClientEventType(),
	}
	return json.Marshal(v)
}

// ConversationItemDeleteEvent is the event for conversation item delete.
// Send this event when you want to remove any item from the conversation history.
// See https://platform.openai.com/docs/api-reference/realtime-client-events/conversation/item/delete
type ConversationItemDeleteEvent struct {
	EventBase
	// The ID of the item to delete.
	ItemID string `json:"item_id"`
}

func (m ConversationItemDeleteEvent) ClientEventType() ClientEventType {
	return ClientEventTypeConversationItemDelete
}

func (m ConversationItemDeleteEvent) MarshalJSON() ([]byte, error) {
	type conversationItemDeleteEvent ConversationItemDeleteEvent
	v := struct {
		*conversationItemDeleteEvent
		Type ClientEventType `json:"type"`
	}{
		conversationItemDeleteEvent: (*conversationItemDeleteEvent)(&m),
		Type:                        m.ClientEventType(),
	}
	return json.Marshal(v)
}

type ResponseCreateParams struct {
	// The modalities for the response.
	Modalities []Modality `json:"modalities,omitempty"`
	// Instructions for the model.
	Instructions string `json:"instructions,omitempty"`
	// The voice the model uses to respond - one of alloy, echo, or shimmer.
	Voice string `json:"voice,omitempty"`
	// The format of output audio.
	OutputAudioFormat AudioFormat `json:"output_audio_format,omitempty"`
	// Tools (functions) available to the model.
	Tools []Tool `json:"tools,omitempty"`
	// How the model chooses tools.
	ToolChoice ToolChoiceInterface `json:"tool_choice,omitempty"`
	// Sampling temperature.
	Temperature *float32 `json:"temperature,omitempty"`
	// Maximum number of output tokens for a single assistant response, inclusive of tool calls. Provide an integer between 1 and 4096 to limit output tokens, or "inf" for the maximum available tokens for a given model. Defaults to "inf".
	MaxOutputTokens IntOrInf `json:"max_output_tokens,omitempty"`
}

// ResponseCreateEvent is the event for response create.
// Send this event to trigger a response generation.
// See https://platform.openai.com/docs/api-reference/realtime-client-events/response/create
type ResponseCreateEvent struct {
	EventBase
	// Configuration for the response.
	Response ResponseCreateParams `json:"response"`
}

func (m ResponseCreateEvent) ClientEventType() ClientEventType {
	return ClientEventTypeResponseCreate
}

func (m ResponseCreateEvent) MarshalJSON() ([]byte, error) {
	type responseCreateEvent ResponseCreateEvent
	v := struct {
		*responseCreateEvent
		Type ClientEventType `json:"type"`
	}{
		responseCreateEvent: (*responseCreateEvent)(&m),
		Type:                m.ClientEventType(),
	}
	return json.Marshal(v)
}

// ResponseCancelEvent is the event for response cancel.
// Send this event to cancel an in-progress response.
// See https://platform.openai.com/docs/api-reference/realtime-client-events/response/cancel
type ResponseCancelEvent struct {
	EventBase
	// A specific response ID to cancel - if not provided, will cancel an in-progress response in the default conversation.
	ResponseID string `json:"response_id,omitempty"`
}

func (m ResponseCancelEvent) ClientEventType() ClientEventType {
	return ClientEventTypeResponseCancel
}

func (m ResponseCancelEvent) MarshalJSON() ([]byte, error) {
	type responseCancelEvent ResponseCancelEvent
	v := struct {
		*responseCancelEvent
		Type ClientEventType `json:"type"`
	}{
		responseCancelEvent: (*responseCancelEvent)(&m),
		Type:                m.ClientEventType(),
	}
	return json.Marshal(v)
}

// MarshalClientEvent marshals the client event to JSON.
func MarshalClientEvent(event ClientEvent) ([]byte, error) {
	return json.Marshal(event)
}

type ServerEventType string

const (
	ServerEventTypeError                                            ServerEventType = "error"
	ServerEventTypeSessionCreated                                   ServerEventType = "session.created"
	ServerEventTypeSessionUpdated                                   ServerEventType = "session.updated"
	ServerEventTypeTranscriptionSessionCreated                      ServerEventType = "transcription_session.created"
	ServerEventTypeTranscriptionSessionUpdated                      ServerEventType = "transcription_session.updated"
	ServerEventTypeConversationCreated                              ServerEventType = "conversation.created"
	ServerEventTypeInputAudioBufferCommitted                        ServerEventType = "input_audio_buffer.committed"
	ServerEventTypeInputAudioBufferCleared                          ServerEventType = "input_audio_buffer.cleared"
	ServerEventTypeInputAudioBufferSpeechStarted                    ServerEventType = "input_audio_buffer.speech_started"
	ServerEventTypeInputAudioBufferSpeechStopped                    ServerEventType = "input_audio_buffer.speech_stopped"
	ServerEventTypeConversationItemCreated                          ServerEventType = "conversation.item.created"
	ServerEventTypeConversationItemInputAudioTranscriptionCompleted ServerEventType = "conversation.item.input_audio_transcription.completed"
	ServerEventTypeConversationItemInputAudioTranscriptionFailed    ServerEventType = "conversation.item.input_audio_transcription.failed"
	ServerEventTypeConversationItemTruncated                        ServerEventType = "conversation.item.truncated"
	ServerEventTypeConversationItemDeleted                          ServerEventType = "conversation.item.deleted"
	ServerEventTypeResponseCreated                                  ServerEventType = "response.created"
	ServerEventTypeResponseDone                                     ServerEventType = "response.done"
	ServerEventTypeResponseOutputItemAdded                          ServerEventType = "response.output_item.added"
	ServerEventTypeResponseOutputItemDone                           ServerEventType = "response.output_item.done"
	ServerEventTypeResponseContentPartAdded                         ServerEventType = "response.content_part.added"
	ServerEventTypeResponseContentPartDone                          ServerEventType = "response.content_part.done"
	ServerEventTypeResponseTextDelta                                ServerEventType = "response.text.delta"
	ServerEventTypeResponseTextDone                                 ServerEventType = "response.text.done"
	ServerEventTypeResponseAudioTranscriptDelta                     ServerEventType = "response.audio_transcript.delta"
	ServerEventTypeResponseAudioTranscriptDone                      ServerEventType = "response.audio_transcript.done"
	ServerEventTypeResponseAudioDelta                               ServerEventType = "response.audio.delta"
	ServerEventTypeResponseAudioDone                                ServerEventType = "response.audio.done"
	ServerEventTypeResponseFunctionCallArgumentsDelta               ServerEventType = "response.function_call_arguments.delta"
	ServerEventTypeResponseFunctionCallArgumentsDone                ServerEventType = "response.function_call_arguments.done"
	ServerEventTypeRateLimitsUpdated                                ServerEventType = "rate_limits.updated"
)

// ServerEvent is the interface for server events.
type ServerEvent interface {
	ServerEventType() ServerEventType
}

// ServerEventBase is the base struct for all server events.
type ServerEventBase struct {
	// The unique ID of the server event.
	EventID string `json:"event_id,omitempty"`
	// The type of the server event.
	Type ServerEventType `json:"type"`
}

func (m ServerEventBase) ServerEventType() ServerEventType {
	return m.Type
}

// ErrorEvent is the event for error.
// Returned when an error occurs.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/error
type ErrorEvent struct {
	ServerEventBase
	// Details of the error.
	Error Error `json:"error"`
}

// SessionCreatedEvent is the event for session created.
// Returned when a session is created. Emitted automatically when a new connection is established.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/session/created
type SessionCreatedEvent struct {
	ServerEventBase
	// The session resource.
	Session ServerSession `json:"session"`
}

// TranscriptionSessionCreatedEvent is the event for session created.
// Returned when a transcription session is created.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/session/created
type TranscriptionSessionCreatedEvent struct {
  ServerEventBase
  // The transcription session resource.
  Session ServerSession `json:"session"`
}

// SessionUpdatedEvent is the event for session updated.
// Returned when a session is updated.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/session/updated
type SessionUpdatedEvent struct {
	ServerEventBase
	// The updated session resource.
	Session ServerSession `json:"session"`
}

// ConversationCreatedEvent is the event for conversation created.
// Returned when a conversation is created. Emitted right after session creation.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/created
type ConversationCreatedEvent struct {
	ServerEventBase
	// The conversation resource.
	Conversation Conversation `json:"conversation"`
}

// InputAudioBufferCommittedEvent is the event for input audio buffer committed.
// Returned when an input audio buffer is committed, either by the client or automatically in server VAD mode.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/input_audio_buffer/committed
type InputAudioBufferCommittedEvent struct {
	ServerEventBase
	// The ID of the preceding item after which the new item will be inserted.
	PreviousItemID string `json:"previous_item_id,omitempty"`
	// The ID of the user message item that will be created.
	ItemID string `json:"item_id"`
}

// InputAudioBufferClearedEvent is the event for input audio buffer cleared.
// Returned when the input audio buffer is cleared by the client.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/input_audio_buffer/cleared
type InputAudioBufferClearedEvent struct {
	ServerEventBase
}

// InputAudioBufferSpeechStartedEvent is the event for input audio buffer speech started.
// Returned in server turn detection mode when speech is detected.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/input_audio_buffer/speech_started
type InputAudioBufferSpeechStartedEvent struct {
	ServerEventBase
	// Milliseconds since the session started when speech was detected.
	AudioStartMs int64 `json:"audio_start_ms"`
	// The ID of the user message item that will be created when speech stops.
	ItemID string `json:"item_id"`
}

// InputAudioBufferSpeechStoppedEvent is the event for input audio buffer speech stopped.
// Returned in server turn detection mode when speech stops.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/input_audio_buffer/speech_stopped
type InputAudioBufferSpeechStoppedEvent struct {
	ServerEventBase
	// Milliseconds since the session started when speech stopped.
	AudioEndMs int64 `json:"audio_end_ms"`
	// The ID of the user message item that will be created.
	ItemID string `json:"item_id"`
}

type ConversationItemCreatedEvent struct {
	ServerEventBase
	PreviousItemID string              `json:"previous_item_id,omitempty"`
	Item           ResponseMessageItem `json:"item"`
}

type ConversationItemInputAudioTranscriptionCompletedEvent struct {
	ServerEventBase
	ItemID       string `json:"item_id"`
	ContentIndex int    `json:"content_index"`
	Transcript   string `json:"transcript"`
}

type ConversationItemInputAudioTranscriptionFailedEvent struct {
	ServerEventBase
	ItemID       string `json:"item_id"`
	ContentIndex int    `json:"content_index"`
	Error        Error  `json:"error"`
}

type ConversationItemTruncatedEvent struct {
	ServerEventBase
	ItemID       string `json:"item_id"`       // The ID of the assistant message item that was truncated.
	ContentIndex int    `json:"content_index"` // The index of the content part that was truncated.
	AudioEndMs   int    `json:"audio_end_ms"`  // The duration up to which the audio was truncated, in milliseconds.
}

type ConversationItemDeletedEvent struct {
	ServerEventBase
	ItemID string `json:"item_id"` // The ID of the item that was deleted.
}

// ResponseCreatedEvent is the event for response created.
// Returned when a new Response is created. The first event of response creation, where the response is in an initial state of "in_progress".
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/created
type ResponseCreatedEvent struct {
	ServerEventBase
	// The response resource.
	Response Response `json:"response"`
}

// ResponseDoneEvent is the event for response done.
// Returned when a Response is done streaming. Always emitted, no matter the final state.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/done
type ResponseDoneEvent struct {
	ServerEventBase
	// The response resource.
	Response Response `json:"response"`
}

// ResponseOutputItemAddedEvent is the event for response output item added.
// Returned when a new Item is created during response generation.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_item/added
type ResponseOutputItemAddedEvent struct {
	ServerEventBase
	// The ID of the response to which the item belongs.
	ResponseID string `json:"response_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The item that was added.
	Item ResponseMessageItem `json:"item"`
}

// ResponseOutputItemDoneEvent is the event for response output item done.
// Returned when an Item is done streaming. Also emitted when a Response is interrupted, incomplete, or cancelled.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_item/done
type ResponseOutputItemDoneEvent struct {
	ServerEventBase
	// The ID of the response to which the item belongs.
	ResponseID string `json:"response_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The completed item.
	Item ResponseMessageItem `json:"item"`
}

// ResponseContentPartAddedEvent is the event for response content part added.
// Returned when a new content part is added to an assistant message item during response generation.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/content_part/added
type ResponseContentPartAddedEvent struct {
	ServerEventBase
	ResponseID   string             `json:"response_id"`
	ItemID       string             `json:"item_id"`
	OutputIndex  int                `json:"output_index"`
	ContentIndex int                `json:"content_index"`
	Part         MessageContentPart `json:"part"`
}

// ResponseContentPartDoneEvent is the event for response content part done.
// Returned when a content part is done streaming in an assistant message item. Also emitted when a Response is interrupted, incomplete, or cancelled.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/content_part/done
type ResponseContentPartDoneEvent struct {
	ServerEventBase
	// The ID of the response.
	ResponseID string `json:"response_id"`
	// The ID of the item to which the content part was added.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The index of the content part in the item's content array.
	ContentIndex int `json:"content_index"`
	// The content part that was added.
	Part MessageContentPart `json:"part"`
}

// ResponseTextDeltaEvent is the event for response text delta.
// Returned when the text value of a "text" content part is updated.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/text/delta
type ResponseTextDeltaEvent struct {
	ServerEventBase
	ResponseID   string `json:"response_id"`
	ItemID       string `json:"item_id"`
	OutputIndex  int    `json:"output_index"`
	ContentIndex int    `json:"content_index"`
	Delta        string `json:"delta"`
}

// ResponseTextDoneEvent is the event for response text done.
// Returned when the text value of a "text" content part is done streaming. Also emitted when a Response is interrupted, incomplete, or cancelled.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/text/done
type ResponseTextDoneEvent struct {
	ServerEventBase
	ResponseID   string `json:"response_id"`
	ItemID       string `json:"item_id"`
	OutputIndex  int    `json:"output_index"`
	ContentIndex int    `json:"content_index"`
	Text         string `json:"text"`
}

// ResponseAudioTranscriptDeltaEvent is the event for response audio transcript delta.
// Returned when the model-generated transcription of audio output is updated.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/audio_transcript/delta
type ResponseAudioTranscriptDeltaEvent struct {
	ServerEventBase
	// The ID of the response.
	ResponseID string `json:"response_id"`
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The index of the content part in the item's content array.
	ContentIndex int `json:"content_index"`
	// The transcript delta.
	Delta string `json:"delta"`
}

// ResponseAudioTranscriptDoneEvent is the event for response audio transcript done.
// Returned when the model-generated transcription of audio output is done streaming. Also emitted when a Response is interrupted, incomplete, or cancelled.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/audio_transcript/done
type ResponseAudioTranscriptDoneEvent struct {
	ServerEventBase
	// The ID of the response.
	ResponseID string `json:"response_id"`
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The index of the content part in the item's content array.
	ContentIndex int `json:"content_index"`
	// The final transcript of the audio.
	Transcript string `json:"transcript"`
}

// ResponseAudioDeltaEvent is the event for response audio delta.
// Returned when the model-generated audio is updated.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/audio/delta
type ResponseAudioDeltaEvent struct {
	ServerEventBase
	// The ID of the response.
	ResponseID string `json:"response_id"`
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The index of the content part in the item's content array.
	ContentIndex int `json:"content_index"`
	// Base64-encoded audio data delta.
	Delta string `json:"delta"`
}

// ResponseAudioDoneEvent is the event for response audio done.
// Returned when the model-generated audio is done. Also emitted when a Response is interrupted, incomplete, or cancelled.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/audio/done
type ResponseAudioDoneEvent struct {
	ServerEventBase
	// The ID of the response.
	ResponseID string `json:"response_id"`
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The index of the content part in the item's content array.
	ContentIndex int `json:"content_index"`
}

// ResponseFunctionCallArgumentsDeltaEvent is the event for response function call arguments delta.
// Returned when the model-generated function call arguments are updated.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/function_call_arguments/delta
type ResponseFunctionCallArgumentsDeltaEvent struct {
	ServerEventBase
	// The ID of the response.
	ResponseID string `json:"response_id"`
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The ID of the function call.
	CallID string `json:"call_id"`
	// The arguments delta as a JSON string.
	Delta string `json:"delta"`
}

// ResponseFunctionCallArgumentsDoneEvent is the event for response function call arguments done.
// Returned when the model-generated function call arguments are done streaming. Also emitted when a Response is interrupted, incomplete, or cancelled.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/function_call_arguments/done
type ResponseFunctionCallArgumentsDoneEvent struct {
	ServerEventBase
	// The ID of the response.
	ResponseID string `json:"response_id"`
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The ID of the function call.
	CallID string `json:"call_id"`
	// The final arguments as a JSON string.
	Arguments string `json:"arguments"`
	// The name of the function. Not shown in API reference but present in the actual event.
	Name string `json:"name"`
}

// RateLimitsUpdatedEvent is the event for rate limits updated.
// Emitted after every "response.done" event to indicate the updated rate limits.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/rate_limits/updated
type RateLimitsUpdatedEvent struct {
	ServerEventBase
	// List of rate limit information.
	RateLimits []RateLimit `json:"rate_limits"`
}

type ServerEventInterface interface {
	ErrorEvent |
		SessionCreatedEvent |
		SessionUpdatedEvent |
		ConversationCreatedEvent |
		InputAudioBufferCommittedEvent |
		InputAudioBufferClearedEvent |
		InputAudioBufferSpeechStartedEvent |
		InputAudioBufferSpeechStoppedEvent |
		ConversationItemCreatedEvent |
		ConversationItemInputAudioTranscriptionCompletedEvent |
		ConversationItemInputAudioTranscriptionFailedEvent |
		ConversationItemTruncatedEvent |
		ConversationItemDeletedEvent |
		ResponseCreatedEvent |
		ResponseDoneEvent |
		ResponseOutputItemAddedEvent |
		ResponseOutputItemDoneEvent |
		ResponseContentPartAddedEvent |
		ResponseContentPartDoneEvent |
		ResponseTextDeltaEvent |
		ResponseTextDoneEvent |
		ResponseAudioTranscriptDeltaEvent |
		ResponseAudioTranscriptDoneEvent |
		ResponseAudioDeltaEvent |
		ResponseAudioDoneEvent |
		ResponseFunctionCallArgumentsDeltaEvent |
		ResponseFunctionCallArgumentsDoneEvent |
		RateLimitsUpdatedEvent
}

func unmarshalServerEvent[T ServerEventInterface](data []byte) (T, error) {
	var t T
	err := json.Unmarshal(data, &t)
	if err != nil {
		return t, err
	}
	return t, nil
}

// UnmarshalServerEvent unmarshals the server event from the given JSON data.
func UnmarshalServerEvent(data []byte) (ServerEvent, error) { //nolint:funlen,cyclop // TODO: optimize
	var eventType struct {
		Type ServerEventType `json:"type"`
	}
	err := json.Unmarshal(data, &eventType)
	if err != nil {
		return nil, err
	}
	switch eventType.Type {
	case ServerEventTypeError:
		return unmarshalServerEvent[ErrorEvent](data)
	case ServerEventTypeSessionCreated:
		return unmarshalServerEvent[SessionCreatedEvent](data)
	case ServerEventTypeSessionUpdated:
		return unmarshalServerEvent[SessionUpdatedEvent](data)
	case ServerEventTypeConversationCreated:
		return unmarshalServerEvent[ConversationCreatedEvent](data)
	case ServerEventTypeInputAudioBufferCommitted:
		return unmarshalServerEvent[InputAudioBufferCommittedEvent](data)
	case ServerEventTypeInputAudioBufferCleared:
		return unmarshalServerEvent[InputAudioBufferClearedEvent](data)
	case ServerEventTypeInputAudioBufferSpeechStarted:
		return unmarshalServerEvent[InputAudioBufferSpeechStartedEvent](data)
	case ServerEventTypeInputAudioBufferSpeechStopped:
		return unmarshalServerEvent[InputAudioBufferSpeechStoppedEvent](data)
	case ServerEventTypeConversationItemCreated:
		return unmarshalServerEvent[ConversationItemCreatedEvent](data)
	case ServerEventTypeConversationItemInputAudioTranscriptionCompleted:
		return unmarshalServerEvent[ConversationItemInputAudioTranscriptionCompletedEvent](data)
	case ServerEventTypeConversationItemInputAudioTranscriptionFailed:
		return unmarshalServerEvent[ConversationItemInputAudioTranscriptionFailedEvent](data)
	case ServerEventTypeConversationItemTruncated:
		return unmarshalServerEvent[ConversationItemTruncatedEvent](data)
	case ServerEventTypeConversationItemDeleted:
		return unmarshalServerEvent[ConversationItemDeletedEvent](data)
	case ServerEventTypeResponseCreated:
		return unmarshalServerEvent[ResponseCreatedEvent](data)
	case ServerEventTypeResponseDone:
		return unmarshalServerEvent[ResponseDoneEvent](data)
	case ServerEventTypeResponseOutputItemAdded:
		return unmarshalServerEvent[ResponseOutputItemAddedEvent](data)
	case ServerEventTypeResponseOutputItemDone:
		return unmarshalServerEvent[ResponseOutputItemDoneEvent](data)
	case ServerEventTypeResponseContentPartAdded:
		return unmarshalServerEvent[ResponseContentPartAddedEvent](data)
	case ServerEventTypeResponseContentPartDone:
		return unmarshalServerEvent[ResponseContentPartDoneEvent](data)
	case ServerEventTypeResponseTextDelta:
		return unmarshalServerEvent[ResponseTextDeltaEvent](data)
	case ServerEventTypeResponseTextDone:
		return unmarshalServerEvent[ResponseTextDoneEvent](data)
	case ServerEventTypeResponseAudioTranscriptDelta:
		return unmarshalServerEvent[ResponseAudioTranscriptDeltaEvent](data)
	case ServerEventTypeResponseAudioTranscriptDone:
		return unmarshalServerEvent[ResponseAudioTranscriptDoneEvent](data)
	case ServerEventTypeResponseAudioDelta:
		return unmarshalServerEvent[ResponseAudioDeltaEvent](data)
	case ServerEventTypeResponseAudioDone:
		return unmarshalServerEvent[ResponseAudioDoneEvent](data)
	case ServerEventTypeResponseFunctionCallArgumentsDelta:
		return unmarshalServerEvent[ResponseFunctionCallArgumentsDeltaEvent](data)
	case ServerEventTypeResponseFunctionCallArgumentsDone:
		return unmarshalServerEvent[ResponseFunctionCallArgumentsDoneEvent](data)
	case ServerEventTypeRateLimitsUpdated:
		return unmarshalServerEvent[RateLimitsUpdatedEvent](data)
	default:
		// This should never happen.
		return nil, fmt.Errorf("unknown server event type: %s", eventType.Type)
	}
}
