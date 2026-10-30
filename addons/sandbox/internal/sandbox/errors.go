package sandbox

import coresandbox "github.com/mudler/LocalAI/core/services/studio/sandbox"

// Errors returned by backends and sandboxes. Test them with errors.Is.
var (
	ErrUnsupported = coresandbox.ErrUnsupported
	ErrGone        = coresandbox.ErrGone
	ErrBusy        = coresandbox.ErrBusy
)
