package sandbox

import "errors"

// Errors returned by backends and sandboxes. Test them with errors.Is.
var (
	// ErrUnsupported means the host cannot run this backend: no /dev/kvm, a
	// missing binary, or a version below the minimum. The message names the
	// missing piece.
	ErrUnsupported = errors.New("sandbox: unsupported on this host")
	// ErrGone means the guest stopped: a crash, an exit, a suspend or
	// Destroy. Checkpoint still works after ErrGone while the upper layer
	// exists.
	ErrGone = errors.New("sandbox: guest is gone")
	// ErrBusy means a checkpoint, refresh or suspend is already running on
	// this sandbox.
	ErrBusy = errors.New("sandbox: busy")
)
