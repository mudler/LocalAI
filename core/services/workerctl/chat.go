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
	// Resume asks the worker to go on with the interrupted turn of the chat
	// whose resume is due, instead of starting a turn from Text. The worker
	// takes the claim in the store, so a repeated request starts nothing.
	Resume bool `json:"resume,omitempty"`
	// Grant is set for a connector message: what the sender of the turn may
	// do. The worker keeps it for the turn, so a tool it builds is offered
	// only for the capabilities in it. A start without a grant for a connector
	// chat gets none of the gated tools.
	Grant *ChatGrant `json:"grant,omitempty"`
	// Sender and the Thread fields are display data of a connector message:
	// the transcript names the sender, and the first turn of a thread chat
	// keeps the block of earlier messages the model read. They never change
	// what the model gets.
	Sender      *ChatSender `json:"sender,omitempty"`
	Thread      string      `json:"thread,omitempty"`
	ThreadLines int         `json:"thread_lines,omitempty"`
	ThreadCut   bool        `json:"thread_cut,omitempty"`
}

// ChatSender is who wrote a connector message, as the transcript shows it.
type ChatSender struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
	Standing string `json:"standing,omitempty"`
}

// ChatGrant is the outcome of the permission check for the senders of one
// connector turn (see studio.Authorize). It holds IDs, never message text.
type ChatGrant struct {
	Binding string   `json:"binding"`
	Kind    string   `json:"kind,omitempty"`
	Senders []string `json:"senders,omitempty"`
	Roles   []string `json:"roles,omitempty"`
	// Caps are the capabilities every sender of the turn holds.
	Caps []string `json:"caps,omitempty"`
	// Denied says why a capability in the ceiling is not held, by name.
	Denied map[string]string `json:"denied,omitempty"`
	// Direct says the chat is one-to-one and Paired that the sender is paired
	// with the bot. A grant from an older frontend has neither, which reads as
	// a group chat and an unpaired sender.
	Direct bool `json:"direct,omitempty"`
	Paired bool `json:"paired,omitempty"`
	// ImpliedWrite says the sender holds tools.write for the write tools of
	// an add-on the agent is connected to without the ceiling listing it.
	ImpliedWrite bool `json:"implied_write,omitempty"`
	// EveryoneWrite says the ceiling holds tools.write and the sender was let
	// in, so the write tools of an add-on that keeps the owner's own data are
	// open to this sender, however they were let in.
	EveryoneWrite bool `json:"everyone_write,omitempty"`
	// Sender says how the sender was let in (paired, listed, role, channel,
	// anyone, or mixed for several senders) and DeniedCodes the refusal code
	// of each capability in Denied. Both are only for the host log and the
	// latest decisions; an older frontend sends neither.
	Sender      string            `json:"sender,omitempty"`
	DeniedCodes map[string]string `json:"denied_codes,omitempty"`
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
