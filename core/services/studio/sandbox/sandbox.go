package sandbox

import (
	"context"
	"fmt"
	"io"
	"net"
	"syscall"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Defaults for the zero fields of a Spec and a Cmd.
const (
	DefaultCPUs         = 2
	DefaultMemory int64 = 2 << 30
	DefaultDisk   int64 = 10 << 30
	DefaultDir          = "/workspace"
)

// AgentPort is the vsock port of the guest agent. A Spec must not use it in
// Env.Ports.
const AgentPort = 1024

// Backend creates sandboxes of one kind.
type Backend interface {
	// Create materializes the view, starts a guest and waits until the guest
	// agent is ready.
	Create(ctx context.Context, spec Spec) (Sandbox, error)
	// Resume restores a suspended sandbox. Processes continue where they
	// stopped. env must have the same Store, the same Actor and handlers for
	// the same ports as when the sandbox was created.
	Resume(ctx context.Context, snap SnapshotRef, env Env) (Sandbox, error)
}

// Discarder is implemented by a backend that must be told when a suspended
// sandbox is not wanted any more, because the snapshot is more than a folder
// the caller can remove (the host backend keeps the processes of a suspended
// sandbox running). A snapshot of another backend is ignored.
type Discarder interface {
	Discard(ctx context.Context, snap SnapshotRef) error
}

// Env carries what a sandbox needs from the host at run time. Resume needs it
// again.
type Env struct {
	Store workspace.Store
	Actor workspace.Actor
	Ports map[int]PortHandler // guest port -> host handler; empty means no network
}

// Spec describes a new sandbox.
type Spec struct {
	Env
	Image  string         // OCI reference with digest; empty means the backend's default image
	View   workspace.View // must have at least one mount
	CPUs   int            // default 2
	Memory int64          // bytes, default 2 GiB
	Disk   int64          // upper image size in bytes, default 10 GiB, sparse
}

// PortHandler serves one connection that the guest opened to its port. The
// handler owns conn and must close it.
type PortHandler func(ctx context.Context, conn net.Conn)

// Sandbox is one running guest bound to one workspace view, one actor and one
// base revision.
type Sandbox interface {
	ID() string
	Exec(ctx context.Context, cmd Cmd) (Process, error)
	// Checkpoint commits the agent's changes as one change set.
	Checkpoint(ctx context.Context, title string) (workspace.ChangeSet, []workspace.Conflict, error)
	// Refresh checkpoints, rebuilds the lower from the current revision,
	// clears the upper and restarts the guest. Processes do not survive it.
	// When the checkpoint fails, Refresh changes nothing and returns the
	// error.
	Refresh(ctx context.Context) error
	// Suspend checkpoints, then saves the guest's memory and devices. The
	// sandbox stops running; Exec and Process calls return ErrGone.
	Suspend(ctx context.Context) (SnapshotRef, error)
	// Destroy stops the guest and deletes its files. Committed changes stay
	// in the workspace.
	Destroy(ctx context.Context) error
}

// Cmd is one process to run in the guest.
type Cmd struct {
	Args           []string
	Env            []string  // added to the image environment; a later entry wins
	Dir            string    // default /workspace
	TTY            *TTY      // nil means pipes
	Stdin          io.Reader // nil means an empty stdin
	Stdout, Stderr io.Writer // nil discards; Stderr is unused with a TTY
}

// TTY is the size of a terminal.
type TTY struct{ Rows, Cols uint16 }

// Process is a started process. When the ctx given to Exec ends before the
// process exits, the process group is killed and Wait returns ctx.Err().
type Process interface {
	// Wait waits for the exit and for its output. A process killed by a
	// signal exits with 128 plus the signal number.
	Wait() (exitCode int, err error)
	// Signal sends sig to the process group.
	Signal(sig syscall.Signal) error
	// Resize changes the terminal size. It fails for a process without a TTY.
	Resize(rows, cols uint16) error
}

// SnapshotRef names a suspended sandbox.
type SnapshotRef struct {
	Backend string // for example "chv"
	Path    string // folder that holds the snapshot
}

// WithDefaults returns s with its zero fields set to the defaults.
func (s Spec) WithDefaults() Spec {
	if s.CPUs == 0 {
		s.CPUs = DefaultCPUs
	}
	if s.Memory == 0 {
		s.Memory = DefaultMemory
	}
	if s.Disk == 0 {
		s.Disk = DefaultDisk
	}
	return s
}

// Check reports what is wrong with s. Call it after WithDefaults.
func (s Spec) Check() error {
	if s.Store == nil {
		return fmt.Errorf("sandbox: %w: no store", workspace.ErrInvalid)
	}
	if len(s.View.Mounts) == 0 {
		return fmt.Errorf("sandbox: %w: the view has no mount", workspace.ErrInvalid)
	}
	if s.CPUs < 1 || s.Memory < 256<<20 || s.Disk < 64<<20 {
		return fmt.Errorf("sandbox: %w: need at least 1 CPU, 256 MiB of memory and a 64 MiB disk", workspace.ErrInvalid)
	}
	return CheckPorts(s.Ports)
}

// CheckPorts reports a port outside 1 to 65535, the reserved AgentPort or a
// nil handler.
func CheckPorts(ports map[int]PortHandler) error {
	for p, h := range ports {
		switch {
		case p < 1 || p > 65535:
			return fmt.Errorf("sandbox: %w: port %d is out of range", workspace.ErrInvalid, p)
		case p == AgentPort:
			return fmt.Errorf("sandbox: %w: port %d is reserved for the guest agent", workspace.ErrInvalid, p)
		case h == nil:
			return fmt.Errorf("sandbox: %w: port %d has no handler", workspace.ErrInvalid, p)
		}
	}
	return nil
}
