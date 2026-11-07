package workspace

import "errors"

// Errors returned by the store. Test them with errors.Is.
var (
	// ErrNotFound means the path does not exist or the actor has no grant for it.
	ErrNotFound = errors.New("workspace: not found")
	// ErrExists means the target name is taken.
	ErrExists = errors.New("workspace: already exists")
	// ErrBadName means a path segment breaks the name rules.
	ErrBadName = errors.New("workspace: invalid name")
	// ErrReadOnly means the actor can read the node but not write it.
	ErrReadOnly = errors.New("workspace: read-only")
	// ErrConflict means an undo found conflicts and changed nothing.
	ErrConflict = errors.New("workspace: conflict")
	// ErrTooLarge means a file is larger than the size limit.
	ErrTooLarge = errors.New("workspace: file too large")
	// ErrInvalid means the operation is not possible, for example moving a
	// folder into itself.
	ErrInvalid = errors.New("workspace: invalid operation")
	// ErrSharedMove means the folder, or a folder in it, is shared, and the
	// move would take the share into another home or into a folder that
	// LocalAI manages (see workfiles.GrantsMayMove). Nothing was moved.
	ErrSharedMove = errors.New("workspace: this folder or a folder in it is shared; stop sharing it before you move it there")
	// ErrNotAdmin means the call needs the admin principal.
	ErrNotAdmin = errors.New("workspace: admin principal required")
	// ErrCommitted means the write is durable but the call could not read the
	// change set back, for example because its context ended. The returned
	// change set has at least the revision. Do not retry the write.
	ErrCommitted = errors.New("workspace: committed")
)
