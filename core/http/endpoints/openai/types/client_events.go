package types

import "encoding/json"

// ClientEventType is the type of client event. See https://platform.openai.com/docs/guides/realtime/client-events
type ClientEventType string

const (
	ClientEventTypeSessionUpdate            ClientEventType = "session.update"
	ClientEventTypeInputAudioBufferAppend   ClientEventType = "input_audio_buffer.append"
	ClientEventTypeInputAudioBufferCommit   ClientEventType = "input_audio_buffer.commit"
	ClientEventTypeInputAudioBufferClear    ClientEventType = "input_audio_buffer.clear"
	ClientEventTypeConversationItemCreate   ClientEventType = "conversation.item.create"
	ClientEventTypeConversationItemRetrieve ClientEventType = "conversation.item.retrieve"
	ClientEventTypeConversationItemTruncate ClientEventType = "conversation.item.truncate"
	ClientEventTypeConversationItemDelete   ClientEventType = "conversation.item.delete"
	ClientEventTypeResponseCreate           ClientEventType = "response.create"
	ClientEventTypeResponseCancel           ClientEventType = "response.cancel"
	ClientEventTypeOutputAudioBufferClear   ClientEventType = "output_audio_buffer.clear"
)

// ClientEvent is the interface for client event.
type ClientEvent interface {
	ClientEventType() ClientEventType
}

// EventBase is the base struct for all client events.
type EventBase struct {
	Type string `json:"type"`
	// Optional client-generated ID used to identify this event.
	EventID string `json:"event_id,omitempty"`
}

// Send this event to update the session’s configuration. The client may send this event at any time to update any field except for voice and model. voice can be updated only if there have been no other audio outputs yet.
//
// When the server receives a session.update, it will respond with a session.updated event showing the full, effective configuration. Only the fields that are present in the session.update are updated. To clear a field like instructions, pass an empty string. To clear a field like tools, pass an empty array. To clear a field like turn_detection, pass null.//
//
// See https://platform.openai.com/docs/api-reference/realtime-client-events/session/update
type SessionUpdateEvent struct {
	EventBase
	// Session configuration to update.
	Session SessionUnion `json:"session"`
}

func (m SessionUpdateEvent) ClientEventType() ClientEventType {
	return ClientEventTypeSessionUpdate
}

func (m SessionUpdateEvent) MarshalJSON() ([]byte, error) {
	type typeAlias SessionUpdateEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

type NoiseReductionType string

const (
	NoiseReductionNearField NoiseReductionType = "near_field"
	NoiseReductionFarField  NoiseReductionType = "far_field"
)

// Send this event to append audio bytes to the input audio buffer. The audio buffer is temporary storage you can write to and later commit. A "commit" will create a new user message item in the conversation history from the buffer content and clear the buffer. Input audio transcription (if enabled) will be generated when the buffer is committed.
//
// If VAD is enabled the audio buffer is used to detect speech and the server will decide when to commit. When Server VAD is disabled, you must commit the audio buffer manually. Input audio noise reduction operates on writes to the audio buffer.
//
// The client may choose how much audio to place in each event up to a maximum of 15 MiB, for example streaming smaller chunks from the client may allow the VAD to be more responsive. Unlike most other client events, the server will not send a confirmation response to this event.
//
// See https://platform.openai.com/docs/api-reference/realtime-client-events/input_audio_buffer/append
type InputAudioBufferAppendEvent struct {
	EventBase
	Audio string `json:"audio"` // Base64-encoded audio bytes.
}

func (m InputAudioBufferAppendEvent) ClientEventType() ClientEventType {
	return ClientEventTypeInputAudioBufferAppend
}

func (m InputAudioBufferAppendEvent) MarshalJSON() ([]byte, error) {
	type typeAlias InputAudioBufferAppendEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

// Send this event to commit the user input audio buffer, which will create a new user message item in the conversation. This event will produce an error if the input audio buffer is empty. When in Server VAD mode, the client does not need to send this event, the server will commit the audio buffer automatically.
//
// Committing the input audio buffer will trigger input audio transcription (if enabled in session configuration), but it will not create a response from the model. The server will respond with an input_audio_buffer.committed event.
//
// See https://platform.openai.com/docs/api-reference/realtime-client-events/input_audio_buffer/commit
type InputAudioBufferCommitEvent struct {
	EventBase
}

func (m InputAudioBufferCommitEvent) ClientEventType() ClientEventType {
	return ClientEventTypeInputAudioBufferCommit
}

func (m InputAudioBufferCommitEvent) MarshalJSON() ([]byte, error) {
	type typeAlias InputAudioBufferCommitEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

// Send this event to clear the audio bytes in the buffer. The server will respond with an input_audio_buffer.cleared event.
//
// See https://platform.openai.com/docs/api-reference/realtime-client-events/input_audio_buffer/clear
type InputAudioBufferClearEvent struct {
	EventBase
}

func (m InputAudioBufferClearEvent) ClientEventType() ClientEventType {
	return ClientEventTypeInputAudioBufferClear
}

func (m InputAudioBufferClearEvent) MarshalJSON() ([]byte, error) {
	type typeAlias InputAudioBufferClearEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

// Send this event to clear the audio bytes in the buffer. The server will respond with an input_audio_buffer.cleared event.
//
// See https://platform.openai.com/docs/api-reference/realtime-client-events/output_audio_buffer/clear

type OutputAudioBufferClearEvent struct {
	EventBase
}

func (m OutputAudioBufferClearEvent) ClientEventType() ClientEventType {
	return ClientEventTypeOutputAudioBufferClear
}

func (m OutputAudioBufferClearEvent) MarshalJSON() ([]byte, error) {
	type typeAlias OutputAudioBufferClearEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

// Add a new Item to the Conversation's context, including messages, function calls, and function call responses. This event can be used both to populate a "history" of the conversation and to add new items mid-stream, but has the current limitation that it cannot populate assistant audio messages.
//
// If successful, the server will respond with a conversation.item.created event, otherwise an error event will be sent.
//
// See https://platform.openai.com/docs/api-reference/realtime-client-events/conversation/item/create
type ConversationItemCreateEvent struct {
	EventBase
	// The ID of the preceding item after which the new item will be inserted.
	PreviousItemID string `json:"previous_item_id,omitempty"`
	// The item to add to the conversation.
	Item MessageItemUnion `json:"item"`
}

func (m ConversationItemCreateEvent) ClientEventType() ClientEventType {
	return ClientEventTypeConversationItemCreate
}

func (m ConversationItemCreateEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemCreateEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

// Send this event when you want to retrieve the server's representation of a specific item in the conversation history. This is useful, for example, to inspect user audio after noise cancellation and VAD. The server will respond with a conversation.item.retrieved event, unless the item does not exist in the conversation history, in which case the server will respond with an error.
//
// See https://platform.openai.com/docs/api-reference/realtime-client-events/conversation/item/retrieve
type ConversationItemRetrieveEvent struct {
	EventBase
	// The ID of the item to retrieve.
	ItemID string `json:"item_id"`
}

func (m ConversationItemRetrieveEvent) ClientEventType() ClientEventType {
	return ClientEventTypeConversationItemRetrieve
}

func (m ConversationItemRetrieveEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemRetrieveEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

// Send this event to truncate a previous assistant message’s audio. The server will produce audio faster than realtime, so this event is useful when the user interrupts to truncate audio that has already been sent to the client but not yet played. This will synchronize the server's understanding of the audio with the client's playback.
//
// Truncating audio will delete the server-side text transcript to ensure there is not text in the context that hasn't been heard by the user.
//
// If successful, the server will respond with a conversation.item.truncated event.
//
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
	type typeAlias ConversationItemTruncateEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

// Send this event when you want to remove any item from the conversation history. The server will respond with a conversation.item.deleted event, unless the item does not exist in the conversation history, in which case the server will respond with an error.
//
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
	type typeAlias ConversationItemDeleteEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

// This event instructs the server to create a Response, which means triggering model inference. When in Server VAD mode, the server will create Responses automatically.
//
// A Response will include at least one Item, and may have two, in which case the second will be a function call. These Items will be appended to the conversation history by default.
//
// The server will respond with a response.created event, events for Items and content created, and finally a response.done event to indicate the Response is complete.
//
// The response.create event includes inference configuration like instructions and tools. If these are set, they will override the Session's configuration for this Response only.
//
// Responses can be created out-of-band of the default Conversation, meaning that they can have arbitrary input, and it's possible to disable writing the output to the Conversation. Only one Response can write to the default Conversation at a time, but otherwise multiple Responses can be created in parallel. The metadata field is a good way to disambiguate multiple simultaneous Responses.
//
// Clients can set conversation to none to create a Response that does not write to the default Conversation. Arbitrary input can be provided with the input field, which is an array accepting raw Items and references to existing Items.
//
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
	type typeAlias ResponseCreateEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

// Send this event to cancel an in-progress response. The server will respond with a response.done event with a status of response.status=cancelled. If there is no response to cancel, the server will respond with an error. It's safe to call response.cancel even if no response is in progress, an error will be returned the session will remain unaffected.
//
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
	type typeAlias ResponseCancelEvent
	type typeWrapper struct {
		typeAlias
		Type ClientEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ClientEventType(),
	}
	return json.Marshal(shadow)
}

type ClientEventInterface interface {
	SessionUpdateEvent |
		InputAudioBufferAppendEvent |
		InputAudioBufferCommitEvent |
		InputAudioBufferClearEvent |
		OutputAudioBufferClearEvent |
		ConversationItemCreateEvent |
		ConversationItemRetrieveEvent |
		ConversationItemTruncateEvent |
		ConversationItemDeleteEvent |
		ResponseCreateEvent |
		ResponseCancelEvent
}

func unmarshalClientEvent[T ClientEventInterface](data []byte) (T, error) {
	var t T
	err := json.Unmarshal(data, &t)
	if err != nil {
		return t, err
	}
	return t, nil
}

// UnmarshalClientEvent unmarshals the client event from the given JSON data.
func UnmarshalClientEvent(data []byte) (ClientEvent, error) {
	var eventType struct {
		Type ClientEventType `json:"type"`
	}
	err := json.Unmarshal(data, &eventType)
	if err != nil {
		return nil, err
	}

	switch eventType.Type {
	case ClientEventTypeSessionUpdate:
		return unmarshalClientEvent[SessionUpdateEvent](data)
	case ClientEventTypeInputAudioBufferAppend:
		return unmarshalClientEvent[InputAudioBufferAppendEvent](data)
	case ClientEventTypeInputAudioBufferCommit:
		return unmarshalClientEvent[InputAudioBufferCommitEvent](data)
	case ClientEventTypeInputAudioBufferClear:
		return unmarshalClientEvent[InputAudioBufferClearEvent](data)
	case ClientEventTypeOutputAudioBufferClear:
		return unmarshalClientEvent[OutputAudioBufferClearEvent](data)
	case ClientEventTypeConversationItemCreate:
		return unmarshalClientEvent[ConversationItemCreateEvent](data)
	case ClientEventTypeConversationItemRetrieve:
		return unmarshalClientEvent[ConversationItemRetrieveEvent](data)
	case ClientEventTypeConversationItemTruncate:
		return unmarshalClientEvent[ConversationItemTruncateEvent](data)
	case ClientEventTypeConversationItemDelete:
		return unmarshalClientEvent[ConversationItemDeleteEvent](data)
	case ClientEventTypeResponseCreate:
		return unmarshalClientEvent[ResponseCreateEvent](data)
	case ClientEventTypeResponseCancel:
		return unmarshalClientEvent[ResponseCancelEvent](data)
	default:
		// We should probably return a generic event or error here, but for now just nil.
		// Or maybe a "UnknownEvent" struct?
		// For now matching the existing pattern
		return nil, nil
	}
}
