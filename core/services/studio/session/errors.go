package session

import "errors"

// Errors returned by the package. Test them with errors.Is.
var (
	// ErrNotFound means the chat does not exist or belongs to another principal.
	ErrNotFound = errors.New("session: not found")
	// ErrExists means a chat with this ID exists already.
	ErrExists = errors.New("session: already exists")
	// ErrInvalid means a call argument is empty or malformed.
	ErrInvalid = errors.New("session: invalid argument")
	// ErrClosed means the chat or the manager is closed.
	ErrClosed = errors.New("session: closed")
	// ErrNoWorkspace means the manager has no workspace, so the call cannot
	// store or find files. Chats without a workspace still talk and use the
	// host tools.
	ErrNoWorkspace = errors.New("session: there is no workspace")
	// ErrNoHome means the user has no home folder in the workspace.
	ErrNoHome = errors.New("session: the user has no home folder")
	// ErrTurnRunning means the call needs an idle chat and a turn is running.
	ErrTurnRunning = errors.New("session: a turn is running")
	// ErrNoTurn means Inject found no running turn.
	ErrNoTurn = errors.New("session: no turn is running")
	// ErrTooManyVMs means the cap on live sandboxes is reached and none is idle.
	ErrTooManyVMs = errors.New("session: too many live sandboxes")
	// ErrNoSandbox means this host cannot run sandboxes.
	ErrNoSandbox = errors.New("session: no sandbox on this host")
)
