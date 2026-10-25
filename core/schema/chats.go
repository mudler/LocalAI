package schema

import "time"

// ChatsCreateRequest starts a chat.
type ChatsCreateRequest struct {
	// Title is optional; the conversation is named after its first message otherwise.
	Title string `json:"title,omitempty"`
	// Model is the model the conversation talks to. It cannot change later.
	Model string `json:"model"`
}

// ChatsConversation is a chat and the state of its turn.
type ChatsConversation struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Model    string    `json:"model"`
	Kind     string    `json:"kind"` // chat or subagent
	ParentID string    `json:"parent_id,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	// Running is true while a turn runs. LastTurn is how the last finished turn
	// ended: done, stopped, failed or interrupted.
	Running  bool   `json:"running"`
	LastTurn string `json:"last_turn,omitempty"`
}

// ChatsConversationList is the response of the list endpoint.
type ChatsConversationList struct {
	Conversations []ChatsConversation `json:"conversations"`
}

// ChatsConversationDetail is a conversation with the messages waiting for the model.
type ChatsConversationDetail struct {
	ChatsConversation
	Pending []ChatsPending `json:"pending"`
}

// ChatsSendRequest is a message to a conversation.
type ChatsSendRequest struct {
	Text string `json:"text"`
}

// ChatsSendResponse says what happened to a message. With a turn running the
// message waits: Started is false and PendingID names it.
type ChatsSendResponse struct {
	Started   bool   `json:"started"`
	PendingID string `json:"pending_id,omitempty"`
}

// ChatsPending is a message that waits for the model's next request.
type ChatsPending struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Seq  int64  `json:"seq"`
}

// ChatsPendingEdit changes the text of a waiting message.
type ChatsPendingEdit struct {
	Text string `json:"text"`
}

// ChatsMessage is one line of a conversation's transcript.
type ChatsMessage struct {
	Seq  int64  `json:"seq"`
	Turn int    `json:"turn"`
	Role string `json:"role"` // user, assistant, tool, checkpoint or reasoning
	Text string `json:"text"`
	// DurMs is how long the model thought, for reasoning lines.
	DurMs int64     `json:"dur_ms,omitempty"`
	At    time.Time `json:"at"`
}

// ChatsMessageList is the response of the transcript endpoint.
type ChatsMessageList struct {
	Messages []ChatsMessage `json:"messages"`
}

// ChatsEvent is one server-sent event of a conversation. Kind is text,
// reasoning, tool_start, tool_end, checkpoint, turn_start, turn_end, error,
// pending_delivered, sub_agent_done or compacted.
type ChatsEvent struct {
	Kind string `json:"kind"`
	Turn int    `json:"turn,omitempty"`
	Text string `json:"text,omitempty"`
	// Tool is set for tool_start and tool_end.
	Tool *ChatsToolEvent `json:"tool,omitempty"`
	// IDs names the messages of pending_delivered, or the conversation of sub_agent_done.
	IDs []string `json:"ids,omitempty"`
	// Before and After are context sizes in tokens, for compacted.
	Before int `json:"before,omitempty"`
	After  int `json:"after,omitempty"`
}

// ChatsToolEvent describes a tool call.
type ChatsToolEvent struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	OK      bool   `json:"ok,omitempty"`
	Output  string `json:"output,omitempty"`
}
