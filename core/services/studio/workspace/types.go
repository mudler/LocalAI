package workspace

import (
	"context"
	"io"
	"io/fs"
	"time"
)

// Mode is an access level. The values are ordered: a higher mode includes the lower ones.
type Mode int

// Access levels.
const (
	ModeNone Mode = iota
	ModeRead
	ModeWrite
)

// Actor is who makes a call: a principal, the groups it belongs to (both
// opaque IDs from the host application), and an optional chat ID.
type Actor struct {
	Principal string
	Groups    []string
	ChatID    string
}

func (a Actor) principals() []string {
	return append([]string{a.Principal}, a.Groups...)
}

func (a Actor) label() string {
	if a.ChatID != "" {
		return "chat " + a.ChatID
	}
	return a.Principal
}

// Kind is the kind of a node.
type Kind string

// Node kinds.
const (
	KindFile Kind = "file"
	KindDir  Kind = "dir"
)

// Node is a file or a folder.
type Node struct {
	ID      int64
	Path    string // starts with "/"; the root is "/"
	Name    string
	Kind    Kind
	Blob    string // SHA-256 in hex, empty for folders
	Size    int64
	Version int64 // increments on each change to this node
	Rev     int64 // revision of the last change
	// ConflictOf is set only in the result of Put, when the store kept the
	// content as a conflict copy. It holds the path the caller asked for.
	ConflictOf string
}

// OpKind is the kind of one journal operation.
type OpKind string

// Operation kinds.
const (
	OpCreate OpKind = "create"
	OpWrite  OpKind = "write"
	OpMove   OpKind = "move"
	OpDelete OpKind = "delete"
)

// Op is one change to one node.
type Op struct {
	NodeID  int64
	Kind    OpKind
	OldPath string // empty for a new node
	NewPath string
	OldBlob string
	NewBlob string
}

// ChangeSetKind tells what made a change set.
type ChangeSetKind string

// Change set kinds.
const (
	ChangeUser   ChangeSetKind = "user"
	ChangeAgent  ChangeSetKind = "agent"
	ChangeUpload ChangeSetKind = "upload"
	ChangeUndo   ChangeSetKind = "undo"
)

// ChangeSet is the operations committed together at one revision.
type ChangeSet struct {
	Rev       int64
	Actor     string
	ChatID    string
	Title     string
	Kind      ChangeSetKind
	Undoes    int64 // revision this change set undoes, or 0
	CreatedAt time.Time
	Ops       []Op
}

// Conflict reasons.
const (
	ReasonChanged       = "changed"        // someone changed the node after the base
	ReasonNameTaken     = "name-taken"     // another live node has the name
	ReasonParentDeleted = "parent-deleted" // the folder to restore into is deleted
	ReasonOutsideView   = "outside-view"   // an overlay entry outside every mount
	ReasonReadOnly      = "read-only"      // an overlay entry in a read-only mount
	ReasonUnsupported   = "unsupported"    // a symbolic link, device or similar entry
	ReasonBadName       = "bad-name"       // a name the store refuses
	ReasonTooLarge      = "too-large"      // a file over the size limit
)

// Conflict reports one path that a call did not change as asked.
type Conflict struct {
	Path   string
	Reason string
	Kept   string // path of the conflict copy, when the store kept one
}

// MountRequest asks for one folder in a view.
type MountRequest struct {
	Path string // workspace path of the folder
	At   string // one name: the folder's name inside the view
	Mode Mode   // ModeRead or ModeWrite
}

// Mount is one folder of a view.
type Mount struct {
	Path   string
	At     string
	Mode   Mode // at most the mode the actor has
	NodeID int64
}

// View is the set of folders that one session sees.
type View struct {
	Actor  Actor
	Mounts []Mount
}

// HistoryQuery selects change sets for History.
type HistoryQuery struct {
	Path      string // "/" or empty for everything the actor can read
	Limit     int    // 0 means 50; at most 1000
	BeforeRev int64  // only change sets with a lower revision; 0 means no bound
}

// PutOptions controls Put.
type PutOptions struct {
	// BaseVersion is the version of the existing file that the writer
	// started from. When it does not match, the store keeps both versions.
	// Zero means the writer expects no file at the path.
	BaseVersion int64
	// Overwrite replaces an existing file without the version check.
	Overwrite bool
	// MaxSize limits this file in bytes, below the store limit. Zero means
	// the store limit only.
	MaxSize int64
	// Title names the change set. Empty means "Upload <name>", which fits a
	// person's upload; a writer that is not an upload names its own change.
	Title string
}

// UndoOptions controls Undo.
type UndoOptions struct {
	// SkipConflicts reverts the nodes without a conflict and reports the rest.
	SkipConflicts bool
}

// EntryKind is the kind of an overlay upper entry.
type EntryKind int

// Entry kinds.
const (
	EntryFile EntryKind = iota + 1
	EntryDir
	EntryWhiteout // character device 0/0
	EntrySymlink
	EntryOther
)

// UpperEntry is one entry of an overlay upper layer.
type UpperEntry struct {
	Path   string // relative to the layer root, forward slashes
	Kind   EntryKind
	Mode   fs.FileMode
	Xattrs map[string][]byte             // only the overlay.* attributes
	Open   func() (io.ReadCloser, error) // EntryFile only
}

// UpperOptions controls CommitUpper.
type UpperOptions struct {
	// Title is the title of the change set.
	Title string
	// Own holds the revisions of the change sets that this caller committed
	// earlier from the same upper layer. Journal rows at these revisions do
	// not count as changes after the base. The store ignores a revision whose
	// change set has another actor principal or chat ID than the caller.
	Own []int64
}

// UpperSource lists the entries of an overlay upper layer, in overlayfs format.
type UpperSource interface {
	Walk(ctx context.Context, fn func(e UpperEntry) error) error
}

// Store is the workspace contract. LocalStore implements it; wstest.RunStoreSuite checks it.
type Store interface {
	// Put stores r at path p and creates missing parent folders.
	Put(ctx context.Context, a Actor, p string, r io.Reader, o PutOptions) (Node, error)
	// Mkdir creates the folder p and missing parent folders.
	Mkdir(ctx context.Context, a Actor, p string) (Node, error)
	// Move moves or renames a node and creates missing parent folders of to.
	Move(ctx context.Context, a Actor, from, to string) (Node, error)
	// Delete sets the tombstone on p and on everything below it.
	Delete(ctx context.Context, a Actor, p string) error
	Open(ctx context.Context, a Actor, p string) (io.ReadSeekCloser, Node, error)
	Stat(ctx context.Context, a Actor, p string) (Node, error)
	// List returns the live children of a folder, sorted by name.
	List(ctx context.Context, a Actor, p string) ([]Node, error)

	// Begin groups several calls into one change set with one title.
	Begin(ctx context.Context, a Actor, title string) (Tx, error)

	// History returns change sets that touched p or a path below it, newest first.
	History(ctx context.Context, a Actor, q HistoryQuery) ([]ChangeSet, error)
	// Undo reverts change set rev as a new change set.
	Undo(ctx context.Context, a Actor, rev int64, o UndoOptions) (ChangeSet, []Conflict, error)

	Grant(ctx context.Context, admin Actor, principal, p string, m Mode) error
	Revoke(ctx context.Context, admin Actor, principal, p string) error
	View(ctx context.Context, a Actor, mounts []MountRequest) (View, error)

	// Materialize builds a read-only tree of the view in dir and returns the revision it used.
	Materialize(ctx context.Context, v View, dir string) (int64, error)
	// CommitUpper commits an overlay upper layer as one change set.
	CommitUpper(ctx context.Context, v View, a Actor, base int64, upper UpperSource, o UpperOptions) (ChangeSet, []Conflict, error)

	// ChangesSince returns change sets after rev that touch a node the actor can read, oldest first.
	ChangesSince(ctx context.Context, a Actor, rev int64, limit int) ([]ChangeSet, error)

	// ReadablePrefixes returns the folder paths where the actor has read access
	// or more, without a path that is under another one in the list. The admin
	// gets ["/"]. An actor with no grant gets an empty list.
	ReadablePrefixes(ctx context.Context, a Actor) ([]string, error)
}

// Tx is an open change set. Use only its methods until Commit or Rollback.
type Tx interface {
	Put(ctx context.Context, p string, r io.Reader, o PutOptions) (Node, error)
	Mkdir(ctx context.Context, p string) (Node, error)
	Move(ctx context.Context, from, to string) (Node, error)
	Delete(ctx context.Context, p string) error
	// Commit writes the change set. A Tx without changes returns a zero ChangeSet.
	Commit() (ChangeSet, error)
	Rollback() error
}
