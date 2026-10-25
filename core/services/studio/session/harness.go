package session

import (
	"context"
	"encoding/json"
)

// Harness starts agent sessions.
type Harness interface {
	Start(ctx context.Context, env HarnessEnv) (HarnessSession, error)
}

// HarnessEnv is what a harness gets when a chat opens.
type HarnessEnv struct {
	SystemPrompt string
	// TurnPrompt is a block that the product adds to the system prompt for
	// the turns that follow (see TurnTools.Prompt). The harness puts it after
	// SystemPrompt. It is empty when the product adds none. A change restarts
	// the harness, so it is fixed for the life of one HarnessSession.
	TurnPrompt string
	Tools      []Tool // the harness must not add its own shell or file tools
	Dir        string // a private state folder for this chat
	Resume     []byte // the harness's own state from the last turn; nil for a new chat
	Emit       func(Event)
}

// HarnessSession is one running agent conversation.
type HarnessSession interface {
	// Send runs one turn and returns at the end of the turn.
	Send(ctx context.Context, text string) error
	// Inject adds a message to the running turn.
	Inject(text string) error
	Interrupt() error
	// Export returns the resume state, saved after each turn.
	Export() ([]byte, error)
	Close() error
}

// Tool is one tool that the session runs in the chat's sandbox.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage // a JSON Schema of type object
	Run         func(ctx context.Context, args json.RawMessage) (string, error)
}
