package workerctl

// ChatStartRequest is the payload of a chat start: a frontend asks a chat
// worker to run the next turn of an unattended chat (a trigger run, a job or a
// connector message). The worker reads the chat, its owner and its model from
// the shared database, so the request names only the chat and the message.
type ChatStartRequest struct {
	// Principal is the owner of the chat as the chats service names it.
	Principal string `json:"principal"`
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
}

// ChatStartReply is the worker's answer to a ChatStartRequest. With Error
// empty, Started says whether the message began a turn on the worker;
// otherwise a turn was running, and the message waits under PendingID. A
// non-empty Error is the worker's refusal: the worker is present, and nothing
// started.
type ChatStartReply struct {
	Started   bool   `json:"started,omitempty"`
	PendingID string `json:"pending_id,omitempty"`
	Error     string `json:"error,omitempty"`
}
