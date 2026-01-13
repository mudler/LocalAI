package types

import (
	"encoding/json"
	"fmt"
)

type ServerEventType string

const (
	ServerEventTypeError                                            ServerEventType = "error"
	ServerEventTypeSessionCreated                                   ServerEventType = "session.created"
	ServerEventTypeSessionUpdated                                   ServerEventType = "session.updated"
	ServerEventTypeConversationItemAdded                            ServerEventType = "conversation.item.added"
	ServerEventTypeConversationItemDone                             ServerEventType = "conversation.item.done"
	ServerEventTypeConversationItemRetrieved                        ServerEventType = "conversation.item.retrieved"
	ServerEventTypeConversationItemInputAudioTranscriptionCompleted ServerEventType = "conversation.item.input_audio_transcription.completed"
	ServerEventTypeConversationItemInputAudioTranscriptionDelta     ServerEventType = "conversation.item.input_audio_transcription.delta"
	ServerEventTypeConversationItemInputAudioTranscriptionSegment   ServerEventType = "conversation.item.input_audio_transcription.segment"
	ServerEventTypeConversationItemInputAudioTranscriptionFailed    ServerEventType = "conversation.item.input_audio_transcription.failed"
	ServerEventTypeConversationItemTruncated                        ServerEventType = "conversation.item.truncated"
	ServerEventTypeConversationItemDeleted                          ServerEventType = "conversation.item.deleted"
	ServerEventTypeInputAudioBufferCommitted                        ServerEventType = "input_audio_buffer.committed"
	ServerEventTypeInputAudioBufferCleared                          ServerEventType = "input_audio_buffer.cleared"
	ServerEventTypeInputAudioBufferSpeechStarted                    ServerEventType = "input_audio_buffer.speech_started"
	ServerEventTypeInputAudioBufferSpeechStopped                    ServerEventType = "input_audio_buffer.speech_stopped"
	ServerEventTypeInputAudioBufferTimeoutTriggered                 ServerEventType = "input_audio_buffer.timeout_triggered"
	ServerEventTypeResponseCreated                                  ServerEventType = "response.created"
	ServerEventTypeResponseDone                                     ServerEventType = "response.done"
	ServerEventTypeResponseOutputItemAdded                          ServerEventType = "response.output_item.added"
	ServerEventTypeResponseOutputItemDone                           ServerEventType = "response.output_item.done"
	ServerEventTypeResponseContentPartAdded                         ServerEventType = "response.content_part.added"
	ServerEventTypeResponseContentPartDone                          ServerEventType = "response.content_part.done"
	ServerEventTypeResponseOutputTextDelta                          ServerEventType = "response.output_text.delta"
	ServerEventTypeResponseOutputTextDone                           ServerEventType = "response.output_text.done"
	ServerEventTypeResponseOutputAudioTranscriptDelta               ServerEventType = "response.output_audio_transcript.delta"
	ServerEventTypeResponseOutputAudioTranscriptDone                ServerEventType = "response.output_audio_transcript.done"
	ServerEventTypeResponseOutputAudioDelta                         ServerEventType = "response.output_audio.delta"
	ServerEventTypeResponseOutputAudioDone                          ServerEventType = "response.output_audio.done"
	ServerEventTypeResponseFunctionCallArgumentsDelta               ServerEventType = "response.function_call_arguments.delta"
	ServerEventTypeResponseFunctionCallArgumentsDone                ServerEventType = "response.function_call_arguments.done"
	ServerEventTypeResponseMcpCallArgumentsDelta                    ServerEventType = "response.mcp_call_arguments.delta"
	ServerEventTypeResponseMcpCallArgumentsDone                     ServerEventType = "response.mcp_call_arguments.done"
	ServerEventTypeResponseMcpCallInProgress                        ServerEventType = "response.mcp_call.in_progress"
	ServerEventTypeResponseMcpCallCompleted                         ServerEventType = "response.mcp_call.completed"
	ServerEventTypeResponseMcpCallFailed                            ServerEventType = "response.mcp_call.failed"
	ServerEventTypeMcpListToolsInProgress                           ServerEventType = "mcp_list_tools.in_progress"
	ServerEventTypeMcpListToolsCompleted                            ServerEventType = "mcp_list_tools.completed"
	ServerEventTypeMcpListToolsFailed                               ServerEventType = "mcp_list_tools.failed"
	ServerEventTypeRateLimitsUpdated                                ServerEventType = "rate_limits.updated"
)

// ServerEvent is the interface for server events.
type ServerEvent interface {
	ServerEventType() ServerEventType
}

// ServerEventBase is the base struct for all server events.
type ServerEventBase struct {
	EventID string `json:"event_id,omitempty"`
}

// Returned when an error occurs, which could be a client problem or a server problem. Most errors are recoverable and the session will stay open, we recommend to implementors to monitor and log error messages by default.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/error
type ErrorEvent struct {
	ServerEventBase
	// Details of the error.
	Error Error `json:"error"`
}

func (m ErrorEvent) ServerEventType() ServerEventType {
	return ServerEventTypeError
}

func (m ErrorEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ErrorEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when a Session is created. Emitted automatically when a new connection is established as the first server event. This event will contain the default Session configuration.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/session/created
type SessionCreatedEvent struct {
	ServerEventBase
	// The session resource.
	Session SessionUnion `json:"session"`
}

func (m SessionCreatedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeSessionCreated
}

func (m SessionCreatedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias SessionCreatedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when a session is updated with a `session.update` event, unless there is an error.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/session/updated
type SessionUpdatedEvent struct {
	ServerEventBase
	// The updated session resource.
	Session SessionUnion `json:"session"`
}

func (m SessionUpdatedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeSessionUpdated
}

func (m SessionUpdatedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias SessionUpdatedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when an input audio buffer is committed, either by the client or automatically in server VAD mode.
//
// The `item_id` property is the ID of the user message item that will be created, thus a `conversation.item.created` event will also be sent to the client.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/input_audio_buffer/committed
type InputAudioBufferCommittedEvent struct {
	ServerEventBase
	// The ID of the preceding item after which the new item will be inserted.
	PreviousItemID string `json:"previous_item_id,omitempty"`
	// The ID of the user message item that will be created.
	ItemID string `json:"item_id"`
}

func (m InputAudioBufferCommittedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeInputAudioBufferCommitted
}

func (m InputAudioBufferCommittedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias InputAudioBufferCommittedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the input audio buffer is cleared by the client with a `input_audio_buffer.clear` event.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/input_audio_buffer/cleared
type InputAudioBufferClearedEvent struct {
	ServerEventBase
}

func (m InputAudioBufferClearedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeInputAudioBufferCleared
}

func (m InputAudioBufferClearedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias InputAudioBufferClearedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Sent by the server when in `server_vad` mode to indicate that speech has been detected in the audio buffer.
//
// This can happen any time audio is added to the buffer (unless speech is already detected). The client may want to use this event to interrupt audio playback or provide visual feedback to the user.
//
// The client should expect to receive a `input_audio_buffer.speech_stopped` event when speech stops.
//
// The `item_id` property is the ID of the user message item that will be created when speech stops and will also be included in the `input_audio_buffer.speech_stopped` event (unless the client manually commits the audio buffer during VAD activation).
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/input_audio_buffer/speech_started
type InputAudioBufferSpeechStartedEvent struct {
	ServerEventBase
	// Milliseconds since the session started when speech was detected.
	AudioStartMs int64 `json:"audio_start_ms"`
	// The ID of the user message item that will be created when speech stops.
	ItemID string `json:"item_id"`
}

func (m InputAudioBufferSpeechStartedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeInputAudioBufferSpeechStarted
}

func (m InputAudioBufferSpeechStartedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias InputAudioBufferSpeechStartedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned in `server_vad` mode when the server detects the end of speech in the audio buffer.
//
// The server will also send an `conversation.item.created` event with the user message item that is created from the audio buffer.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/input_audio_buffer/speech_stopped
type InputAudioBufferSpeechStoppedEvent struct {
	ServerEventBase
	// Milliseconds since the session started when speech stopped.
	AudioEndMs int64 `json:"audio_end_ms"`
	// The ID of the user message item that will be created.
	ItemID string `json:"item_id"`
}

func (m InputAudioBufferSpeechStoppedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeInputAudioBufferSpeechStopped
}

func (m InputAudioBufferSpeechStoppedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias InputAudioBufferSpeechStoppedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the Server VAD timeout is triggered for the input audio buffer.
//
// This is configured with `idle_timeout_ms` in the `turn_detection` settings of the session, and it indicates that there hasn't been any speech detected for the configured duration.
//
// The `audio_start_ms` and `audio_end_ms` fields indicate the segment of audio after the last model response up to the triggering time, as an offset from the beginning of audio written to the input audio buffer.
//
// This means it demarcates the segment of audio that was silent and the difference between the start and end values will roughly match the configured timeout.
//
// The empty audio will be committed to the conversation as an `input_audio` item (there will be a `input_audio_buffer.committed` event) and a model response will be generated.
//
// There may be speech that didn't trigger VAD but is still detected by the model, so the model may respond with something relevant to the conversation or a prompt to continue speaking.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/input_audio_buffer/timeout_triggered
type InputAudioBufferTimeoutTriggeredEvent struct {
	ServerEventBase
	// Milliseconds since the session started when speech started.
	AudioStartMs int64 `json:"audio_start_ms"`
	// Milliseconds since the session started when speech stopped.
	AudioEndMs int64 `json:"audio_end_ms"`
	// The ID of the user message item that will be created.
	ItemID string `json:"item_id"`
}

func (m InputAudioBufferTimeoutTriggeredEvent) ServerEventType() ServerEventType {
	return ServerEventTypeInputAudioBufferTimeoutTriggered
}

func (m InputAudioBufferTimeoutTriggeredEvent) MarshalJSON() ([]byte, error) {
	type typeAlias InputAudioBufferTimeoutTriggeredEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Sent by the server when an Item is added to the default Conversation.
//
// This can happen in several cases:
//
// - When the client sends a `conversation.item.create` event.
//
// - When the input audio buffer is committed. In this case the item will be a user message containing the audio from the buffer.
//
// - When the model is generating a Response. In this case the `conversation.item.added` event will be sent when the model starts generating a specific Item, and thus it will not yet have any content (and `status` will be `in_progress`).
//
// The event will include the full content of the Item (except when model is generating a Response) except for audio data, which can be retrieved separately with a `conversation.item.retrieve` event if necessary.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/item/added
type ConversationItemAddedEvent struct {
	ServerEventBase
	// The ID of the preceding item after which the new item will be inserted.
	PreviousItemID string `json:"previous_item_id,omitempty"`

	// The item that was added.
	Item MessageItemUnion `json:"item"`
}

func (m ConversationItemAddedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeConversationItemAdded
}

func (m ConversationItemAddedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemAddedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when a conversation item is finalized.
//
// The event will include the full content of the Item except for audio data, which can be retrieved separately with a `conversation.item.retrieve` event if needed.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/item/done
type ConversationItemDoneEvent struct {
	ServerEventBase
	// The ID of the preceding item after which the item appears.
	PreviousItemID string `json:"previous_item_id,omitempty"`

	// The completed item.
	Item MessageItemUnion `json:"item"`
}

func (m ConversationItemDoneEvent) ServerEventType() ServerEventType {
	return ServerEventTypeConversationItemDone
}

func (m ConversationItemDoneEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemDoneEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when a conversation item is retrieved with `conversation.item.retrieve`. This is provided as a way to fetch the server's representation of an item, for example to get access to the post-processed audio data after noise cancellation and VAD. It includes the full content of the Item, including audio data.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/item/retrieved
type ConversationItemRetrievedEvent struct {
	ServerEventBase
	// The item that was retrieved.
	Item MessageItemUnion `json:"item"`
}

func (m ConversationItemRetrievedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeConversationItemRetrieved
}

func (m ConversationItemRetrievedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemRetrievedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

type Logprobs struct {
	// Raw byte sequence corresponding to the token (if applicable).
	Bytes []byte `json:"bytes,omitempty"`

	// Log probability of the token or segment.
	Logprob float64 `json:"logprob,omitempty"`

	// The decoded token text.
	Token string `json:"token,omitempty"`
}

// This event is the output of audio transcription for user audio written to the user audio buffer. Transcription begins when the input audio buffer is committed by the client or server (in `server_vad` mode). Transcription runs asynchronously with Response creation, so this event may come before or after the Response events.

// Realtime API models accept audio natively, and thus input transcription is a separate process run on a separate ASR (Automatic Speech Recognition) model. The transcript may diverge somewhat from the model's interpretation, and should be treated as a rough guide.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/item/input_audio_transcription/completed
type ConversationItemInputAudioTranscriptionCompletedEvent struct {
	ServerEventBase
	// The ID of the item.
	ItemID string `json:"item_id"`

	// The index of the content part in the item's content array.
	ContentIndex int `json:"content_index"`

	// The final transcript of the audio.
	Transcript string `json:"transcript"`

	// Log probability information for the transcription, if available.
	Logprobs []Logprobs `json:"logprobs,omitempty"`

	// Usage information for the transcription, if available.
	Usage *UsageUnion `json:"usage,omitempty"`
}

func (m ConversationItemInputAudioTranscriptionCompletedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeConversationItemInputAudioTranscriptionCompleted
}

func (m ConversationItemInputAudioTranscriptionCompletedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemInputAudioTranscriptionCompletedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the text value of an input audio transcription content part is updated with incremental transcription results.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/item/input_audio_transcription/delta
type ConversationItemInputAudioTranscriptionDeltaEvent struct {
	ServerEventBase
	// The ID of the item.
	ItemID string `json:"item_id"`

	// The index of the content part in the item's content array.
	ContentIndex int `json:"content_index"`

	// The transcript delta.
	Delta string `json:"delta"`

	// Log probability updates for the delta, if available.
	Logprobs []Logprobs `json:"logprobs,omitempty"`
}

func (m ConversationItemInputAudioTranscriptionDeltaEvent) ServerEventType() ServerEventType {
	return ServerEventTypeConversationItemInputAudioTranscriptionDelta
}

func (m ConversationItemInputAudioTranscriptionDeltaEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemInputAudioTranscriptionDeltaEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when an input audio transcription segment is identified for an item.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/item/input_audio_transcription/segment
type ConversationItemInputAudioTranscriptionSegmentEvent struct {
	ServerEventBase
	// The ID of the item.
	ItemID string `json:"item_id"`

	// The index of the content part in the item's content array.
	ContentIndex int `json:"content_index"`

	// Log probability information for the segment, if available.
	Logprobs []Logprobs `json:"logprobs,omitempty"`

	// The unique ID of the transcript segment.
	ID string `json:"id,omitempty"`

	// The speaker label for the segment, if available.
	Speaker string `json:"speaker,omitempty"`

	// The start time of the segment in seconds.
	Start float64 `json:"start,omitempty"`

	// The end time of the segment in seconds.
	End float64 `json:"end,omitempty"`

	// The text content of the segment.
	Text string `json:"text,omitempty"`
}

func (m ConversationItemInputAudioTranscriptionSegmentEvent) ServerEventType() ServerEventType {
	return ServerEventTypeConversationItemInputAudioTranscriptionSegment
}

func (m ConversationItemInputAudioTranscriptionSegmentEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemInputAudioTranscriptionSegmentEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when input audio transcription is configured, and a transcription request for a user message failed. These events are separate from other error events so that the client can identify the related Item.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/item/input_audio_transcription/failed
type ConversationItemInputAudioTranscriptionFailedEvent struct {
	ServerEventBase
	// The ID of the item.
	ItemID string `json:"item_id"`

	// The index of the content part in the item's content array.
	ContentIndex int `json:"content_index"`

	// Details of the failure.
	Error Error `json:"error"`
}

func (m ConversationItemInputAudioTranscriptionFailedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeConversationItemInputAudioTranscriptionFailed
}

func (m ConversationItemInputAudioTranscriptionFailedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemInputAudioTranscriptionFailedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when an earlier assistant audio message item is truncated by the client with a `conversation.item.truncate` event. This event is used to synchronize the server's understanding of the audio with the client's playback.
//
// This action will truncate the audio and remove the server-side text transcript to ensure there is no text in the context that hasn't been heard by the user.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/item/truncated
type ConversationItemTruncatedEvent struct {
	ServerEventBase
	// The ID of the assistant message item that was truncated.
	ItemID string `json:"item_id"`

	// The index of the content part that was truncated.
	ContentIndex int `json:"content_index"`

	// The duration up to which the audio was truncated, in milliseconds.
	AudioEndMs int `json:"audio_end_ms"`
}

func (m ConversationItemTruncatedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeConversationItemTruncated
}

func (m ConversationItemTruncatedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemTruncatedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when an item in the conversation is deleted by the client with a `conversation.item.delete` event. This event is used to synchronize the server's understanding of the conversation history with the client's view.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/conversation/item/deleted
type ConversationItemDeletedEvent struct {
	ServerEventBase
	// The ID of the item that was deleted.
	ItemID string `json:"item_id"`
}

func (m ConversationItemDeletedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeConversationItemDeleted
}

func (m ConversationItemDeletedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ConversationItemDeletedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when a new Response is created. The first event of response creation, where the response is in an initial state of in_progress.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/created
type ResponseCreatedEvent struct {
	ServerEventBase
	// The response resource.
	Response Response `json:"response"`
}

func (m ResponseCreatedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseCreated
}

func (m ResponseCreatedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseCreatedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when a Response is done streaming. Always emitted, no matter the final state. The Response object included in the response.done event will include all output Items in the Response but will omit the raw audio data.
//
// Clients should check the status field of the Response to determine if it was successful (completed) or if there was another outcome: cancelled, failed, or incomplete.
//
// A response will contain all output items that were generated during the response, excluding any audio content.
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/done
type ResponseDoneEvent struct {
	ServerEventBase
	// The response resource.
	Response Response `json:"response"`
}

func (m ResponseDoneEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseDone
}

func (m ResponseDoneEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseDoneEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when a new Item is created during Response generation.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_item/added
type ResponseOutputItemAddedEvent struct {
	ServerEventBase
	// The ID of the response to which the item belongs.
	ResponseID string `json:"response_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The item that was added.
	Item MessageItemUnion `json:"item"`
}

func (m ResponseOutputItemAddedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseOutputItemAdded
}

func (m ResponseOutputItemAddedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseOutputItemAddedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when an Item is done streaming. Also emitted when a Response is interrupted, incomplete, or cancelled.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_item/done
type ResponseOutputItemDoneEvent struct {
	ServerEventBase
	// The ID of the response to which the item belongs.
	ResponseID string `json:"response_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The completed item.
	Item MessageItemUnion `json:"item"`
}

func (m ResponseOutputItemDoneEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseOutputItemDone
}

func (m ResponseOutputItemDoneEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseOutputItemDoneEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when a new content part is added to an assistant message item during response generation.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/content_part/added
type ResponseContentPartAddedEvent struct {
	ServerEventBase
	ResponseID   string               `json:"response_id"`
	ItemID       string               `json:"item_id"`
	OutputIndex  int                  `json:"output_index"`
	ContentIndex int                  `json:"content_index"`
	Part         MessageContentOutput `json:"part"`
}

func (m ResponseContentPartAddedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseContentPartAdded
}

func (m ResponseContentPartAddedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseContentPartAddedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when a content part is done streaming in an assistant message item. Also emitted when a Response is interrupted, incomplete, or cancelled.
//
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
	Part MessageContentOutput `json:"part"`
}

func (m ResponseContentPartDoneEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseContentPartDone
}

func (m ResponseContentPartDoneEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseContentPartDoneEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the text value of an "output_text" content part is updated.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_text/delta
type ResponseOutputTextDeltaEvent struct {
	ServerEventBase
	ResponseID   string `json:"response_id"`
	ItemID       string `json:"item_id"`
	OutputIndex  int    `json:"output_index"`
	ContentIndex int    `json:"content_index"`
	Delta        string `json:"delta"`
}

func (m ResponseOutputTextDeltaEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseOutputTextDelta
}

func (m ResponseOutputTextDeltaEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseOutputTextDeltaEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the text value of an "output_text" content part is done streaming. Also emitted when a Response is interrupted, incomplete, or cancelled.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_text/done
type ResponseOutputTextDoneEvent struct {
	ServerEventBase
	ResponseID   string `json:"response_id"`
	ItemID       string `json:"item_id"`
	OutputIndex  int    `json:"output_index"`
	ContentIndex int    `json:"content_index"`
	Text         string `json:"text"`
}

func (m ResponseOutputTextDoneEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseOutputTextDone
}

func (m ResponseOutputTextDoneEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseOutputTextDoneEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the model-generated transcription of audio output is updated.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_audio_transcript/delta
type ResponseOutputAudioTranscriptDeltaEvent struct {
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

func (m ResponseOutputAudioTranscriptDeltaEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseOutputAudioTranscriptDelta
}

func (m ResponseOutputAudioTranscriptDeltaEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseOutputAudioTranscriptDeltaEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the model-generated transcription of audio output is done streaming. Also emitted when a Response is interrupted, incomplete, or cancelled.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_audio_transcript/done
type ResponseOutputAudioTranscriptDoneEvent struct {
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

func (m ResponseOutputAudioTranscriptDoneEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseOutputAudioTranscriptDone
}

func (m ResponseOutputAudioTranscriptDoneEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseOutputAudioTranscriptDoneEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the model-generated audio is updated.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_audio/delta
type ResponseOutputAudioDeltaEvent struct {
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

func (m ResponseOutputAudioDeltaEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseOutputAudioDelta
}

func (m ResponseOutputAudioDeltaEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseOutputAudioDeltaEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the model-generated audio is done. Also emitted when a Response is interrupted, incomplete, or cancelled.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/output_audio/done
type ResponseOutputAudioDoneEvent struct {
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

func (m ResponseOutputAudioDoneEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseOutputAudioDone
}

func (m ResponseOutputAudioDoneEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseOutputAudioDoneEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the model-generated function call arguments are updated.
//
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

func (m ResponseFunctionCallArgumentsDeltaEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseFunctionCallArgumentsDelta
}

func (m ResponseFunctionCallArgumentsDeltaEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseFunctionCallArgumentsDeltaEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when the model-generated function call arguments are done streaming. Also emitted when a Response is interrupted, incomplete, or cancelled.
//
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

func (m ResponseFunctionCallArgumentsDoneEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseFunctionCallArgumentsDone
}

func (m ResponseFunctionCallArgumentsDoneEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseFunctionCallArgumentsDoneEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when MCP tool call arguments are updated during response generation.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/mcp_call_arguments/delta
type ResponseMcpCallArgumentsDeltaEvent struct {
	ServerEventBase
	// The ID of the response.
	ResponseID string `json:"response_id"`
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The arguments delta as a JSON string.
	Delta string `json:"delta"`
	// Obfuscation
	Obfuscation string `json:"obfuscation"`
}

func (m ResponseMcpCallArgumentsDeltaEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseMcpCallArgumentsDelta
}

func (m ResponseMcpCallArgumentsDeltaEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseMcpCallArgumentsDeltaEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when MCP tool call arguments are finalized during response generation.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/mcp_call_arguments/done
type ResponseMcpCallArgumentsDoneEvent struct {
	ServerEventBase
	// The ID of the response.
	ResponseID string `json:"response_id"`
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
	// The final arguments as a JSON string.
	Arguments string `json:"arguments"`
}

func (m ResponseMcpCallArgumentsDoneEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseMcpCallArgumentsDone
}

func (m ResponseMcpCallArgumentsDoneEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseMcpCallArgumentsDoneEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when an MCP tool call has started and is in progress.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/mcp_call/in_progress
type ResponseMcpCallInProgressEvent struct {
	ServerEventBase
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
}

func (m ResponseMcpCallInProgressEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseMcpCallInProgress
}

func (m ResponseMcpCallInProgressEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseMcpCallInProgressEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when an MCP tool call has completed successfully.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/mcp_call/completed
type ResponseMcpCallCompletedEvent struct {
	ServerEventBase
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
}

func (m ResponseMcpCallCompletedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseMcpCallCompleted
}

func (m ResponseMcpCallCompletedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseMcpCallCompletedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when an MCP tool call has failed.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/response/mcp_call/failed
type ResponseMcpCallFailedEvent struct {
	ServerEventBase
	// The ID of the item.
	ItemID string `json:"item_id"`
	// The index of the output item in the response.
	OutputIndex int `json:"output_index"`
}

func (m ResponseMcpCallFailedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeResponseMcpCallFailed
}

func (m ResponseMcpCallFailedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ResponseMcpCallFailedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when listing MCP tools is in progress for an item.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/mcp_list_tools/in_progress
type McpListToolsInProgressEvent struct {
	ServerEventBase
	// The ID of the MCP list tools item.
	ItemID string `json:"item_id"`
}

func (m McpListToolsInProgressEvent) ServerEventType() ServerEventType {
	return ServerEventTypeMcpListToolsInProgress
}

func (m McpListToolsInProgressEvent) MarshalJSON() ([]byte, error) {
	type typeAlias McpListToolsInProgressEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when listing MCP tools has completed for an item.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/mcp_list_tools/completed
type McpListToolsCompletedEvent struct {
	ServerEventBase
	// The ID of the MCP list tools item.
	ItemID string `json:"item_id"`
}

func (m McpListToolsCompletedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeMcpListToolsCompleted
}

func (m McpListToolsCompletedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias McpListToolsCompletedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Returned when listing MCP tools has failed for an item.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/mcp_list_tools/failed
type McpListToolsFailedEvent struct {
	ServerEventBase
	// The ID of the MCP list tools item.
	ItemID string `json:"item_id"`
}

func (m McpListToolsFailedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeMcpListToolsFailed
}

func (m McpListToolsFailedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias McpListToolsFailedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

// Emitted at the beginning of a Response to indicate the updated rate limits. When a Response is created some tokens will be "reserved" for the output tokens, the rate limits shown here reflect that reservation, which is then adjusted accordingly once the Response is completed.
//
// See https://platform.openai.com/docs/api-reference/realtime-server-events/rate_limits/updated
type RateLimitsUpdatedEvent struct {
	ServerEventBase
	// List of rate limit information.
	RateLimits []RateLimit `json:"rate_limits"`
}

func (m RateLimitsUpdatedEvent) ServerEventType() ServerEventType {
	return ServerEventTypeRateLimitsUpdated
}

func (m RateLimitsUpdatedEvent) MarshalJSON() ([]byte, error) {
	type typeAlias RateLimitsUpdatedEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}

type ServerEventInterface interface {
	ErrorEvent |
		SessionCreatedEvent |
		SessionUpdatedEvent |
		ConversationItemAddedEvent |
		ConversationItemDoneEvent |
		ConversationItemRetrievedEvent |
		ConversationItemInputAudioTranscriptionCompletedEvent |
		ConversationItemInputAudioTranscriptionDeltaEvent |
		ConversationItemInputAudioTranscriptionSegmentEvent |
		ConversationItemInputAudioTranscriptionFailedEvent |
		ConversationItemTruncatedEvent |
		ConversationItemDeletedEvent |
		InputAudioBufferCommittedEvent |
		InputAudioBufferClearedEvent |
		InputAudioBufferSpeechStartedEvent |
		InputAudioBufferSpeechStoppedEvent |
		InputAudioBufferTimeoutTriggeredEvent |
		ResponseCreatedEvent |
		ResponseDoneEvent |
		ResponseOutputItemAddedEvent |
		ResponseOutputItemDoneEvent |
		ResponseContentPartAddedEvent |
		ResponseContentPartDoneEvent |
		ResponseOutputTextDeltaEvent |
		ResponseOutputTextDoneEvent |
		ResponseOutputAudioTranscriptDeltaEvent |
		ResponseOutputAudioTranscriptDoneEvent |
		ResponseOutputAudioDeltaEvent |
		ResponseOutputAudioDoneEvent |
		ResponseFunctionCallArgumentsDeltaEvent |
		ResponseFunctionCallArgumentsDoneEvent |
		ResponseMcpCallArgumentsDeltaEvent |
		ResponseMcpCallArgumentsDoneEvent |
		ResponseMcpCallInProgressEvent |
		ResponseMcpCallCompletedEvent |
		ResponseMcpCallFailedEvent |
		McpListToolsInProgressEvent |
		McpListToolsCompletedEvent |
		McpListToolsFailedEvent |
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
func UnmarshalServerEvent(data []byte) (ServerEvent, error) { //nolint:funlen,cyclop,gocyclo // TODO: optimize
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

	case ServerEventTypeConversationItemAdded:
		return unmarshalServerEvent[ConversationItemAddedEvent](data)

	case ServerEventTypeConversationItemDone:
		return unmarshalServerEvent[ConversationItemDoneEvent](data)

	case ServerEventTypeConversationItemRetrieved:
		return unmarshalServerEvent[ConversationItemRetrievedEvent](data)

	case ServerEventTypeConversationItemInputAudioTranscriptionCompleted:
		return unmarshalServerEvent[ConversationItemInputAudioTranscriptionCompletedEvent](data)

	case ServerEventTypeConversationItemInputAudioTranscriptionDelta:
		return unmarshalServerEvent[ConversationItemInputAudioTranscriptionDeltaEvent](data)

	case ServerEventTypeConversationItemInputAudioTranscriptionSegment:
		return unmarshalServerEvent[ConversationItemInputAudioTranscriptionSegmentEvent](data)

	case ServerEventTypeConversationItemInputAudioTranscriptionFailed:
		return unmarshalServerEvent[ConversationItemInputAudioTranscriptionFailedEvent](data)

	case ServerEventTypeConversationItemTruncated:
		return unmarshalServerEvent[ConversationItemTruncatedEvent](data)

	case ServerEventTypeConversationItemDeleted:
		return unmarshalServerEvent[ConversationItemDeletedEvent](data)

	case ServerEventTypeInputAudioBufferCommitted:
		return unmarshalServerEvent[InputAudioBufferCommittedEvent](data)

	case ServerEventTypeInputAudioBufferCleared:
		return unmarshalServerEvent[InputAudioBufferClearedEvent](data)

	case ServerEventTypeInputAudioBufferSpeechStarted:
		return unmarshalServerEvent[InputAudioBufferSpeechStartedEvent](data)

	case ServerEventTypeInputAudioBufferSpeechStopped:
		return unmarshalServerEvent[InputAudioBufferSpeechStoppedEvent](data)

	case ServerEventTypeInputAudioBufferTimeoutTriggered:
		return unmarshalServerEvent[InputAudioBufferTimeoutTriggeredEvent](data)

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

	case ServerEventTypeResponseOutputTextDelta:
		return unmarshalServerEvent[ResponseOutputTextDeltaEvent](data)

	case ServerEventTypeResponseOutputTextDone:
		return unmarshalServerEvent[ResponseOutputTextDoneEvent](data)

	case ServerEventTypeResponseOutputAudioTranscriptDelta:
		return unmarshalServerEvent[ResponseOutputAudioTranscriptDeltaEvent](data)

	case ServerEventTypeResponseOutputAudioTranscriptDone:
		return unmarshalServerEvent[ResponseOutputAudioTranscriptDoneEvent](data)

	case ServerEventTypeResponseOutputAudioDelta:
		return unmarshalServerEvent[ResponseOutputAudioDeltaEvent](data)

	case ServerEventTypeResponseOutputAudioDone:
		return unmarshalServerEvent[ResponseOutputAudioDoneEvent](data)

	case ServerEventTypeResponseFunctionCallArgumentsDelta:
		return unmarshalServerEvent[ResponseFunctionCallArgumentsDeltaEvent](data)

	case ServerEventTypeResponseFunctionCallArgumentsDone:
		return unmarshalServerEvent[ResponseFunctionCallArgumentsDoneEvent](data)

	case ServerEventTypeResponseMcpCallArgumentsDelta:
		return unmarshalServerEvent[ResponseMcpCallArgumentsDeltaEvent](data)

	case ServerEventTypeResponseMcpCallArgumentsDone:
		return unmarshalServerEvent[ResponseMcpCallArgumentsDoneEvent](data)

	case ServerEventTypeResponseMcpCallInProgress:
		return unmarshalServerEvent[ResponseMcpCallInProgressEvent](data)

	case ServerEventTypeResponseMcpCallCompleted:
		return unmarshalServerEvent[ResponseMcpCallCompletedEvent](data)

	case ServerEventTypeResponseMcpCallFailed:
		return unmarshalServerEvent[ResponseMcpCallFailedEvent](data)

	case ServerEventTypeMcpListToolsInProgress:
		return unmarshalServerEvent[McpListToolsInProgressEvent](data)

	case ServerEventTypeMcpListToolsCompleted:
		return unmarshalServerEvent[McpListToolsCompletedEvent](data)

	case ServerEventTypeMcpListToolsFailed:
		return unmarshalServerEvent[McpListToolsFailedEvent](data)

	case ServerEventTypeRateLimitsUpdated:
		return unmarshalServerEvent[RateLimitsUpdatedEvent](data)

	default:
		// This should never happen.
		return nil, fmt.Errorf("unknown server event type: %s", eventType.Type)
	}
}
