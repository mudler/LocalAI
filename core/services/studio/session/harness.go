package session

import (
	"context"
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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
	// Actor is whose chat this is, so the harness can pick the model client and
	// the user for it.
	Actor workspace.Actor
	// Inputs, when set, is asked before every model request for texts the host
	// wants the model to see (messages the user sent while the turn ran). The
	// texts reach the model as one user message, in order. settle is called
	// with nil once the request succeeded or with its error, so the host can
	// put the texts back. settle may be nil. Nil Inputs: nothing to add.
	Inputs func() (texts []string, settle func(error))
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

// Media is an image or a video that goes to the model as part of the user's
// message, next to the text. DataURI is a base64 data: URI.
type Media struct {
	Kind    MediaKind
	DataURI string
}

// MediaKind tells what a Media holds.
type MediaKind string

// Media kinds.
const (
	MediaImage MediaKind = "image"
	MediaVideo MediaKind = "video"
)

// MediaSession is implemented by a HarnessSession that can send media parts
// with a message. A harness without it gets the text only.
type MediaSession interface {
	SendMedia(ctx context.Context, text string, parts []Media) error
}

// Tool is one tool that the session runs in the chat's sandbox.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage // a JSON Schema of type object
	Run         func(ctx context.Context, args json.RawMessage) (string, error)
	// Provider says who serves a tool whose name an add-on may shadow (bash,
	// say): "builtin" or the add-on's ID. It is empty for every other tool.
	// The harness does not read it.
	Provider string
	// StandInFor is set on the stand-in tool of an add-on that cannot work
	// now: the built-in tools that add-on replaces (bash, say), so the chat
	// can tell that the built-in serves in its place. The harness does not
	// read it.
	StandInFor []string
}
