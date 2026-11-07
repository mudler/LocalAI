package workspace

import (
	"context"
	"io"
	"io/fs"
	"os"
	"slices"
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
//
// An actor can also carry a fence (see Fenced). The fence is unexported so
// that only Fenced sets it: a copy of the actor keeps it, and an Actor built
// as a new literal has none.
type Actor struct {
	Principal string
	Groups    []string
	ChatID    string

	// fence holds one layer per Fenced call, nil for an actor with no fence.
	// The actor reaches a node only when every layer allows it. The layers
	// are never changed in place, so copies of the actor can share them.
	fence [][]FenceRoot
}

// MaxFenceRoots is the most roots that one Fenced call may give. A store
// refuses an actor with more (ErrInvalid).
const MaxFenceRoots = 32

// maxFenceLayers bounds how many times an actor may be fenced. Each layer
// costs one more check for every node, and a deeper chain than this is a bug
// in the caller. A store refuses an actor with more (ErrInvalid).
const maxFenceLayers = 8

// FenceRoot is a folder a fenced actor may reach, by node ID, and the most
// it may do there.
type FenceRoot struct {
	Node int64
	Mode Mode // ModeRead or ModeWrite
}

// Fenced returns a copy of a that reaches only the given folders and what is
// below them, at most at each root's mode, and never more than its grants
// allow. Fencing a fenced actor keeps the intersection: a fence never grows.
// Fenced with no roots gives an actor that reaches nothing.
//
// A fenced actor never takes the administrator shortcut, cannot grant or
// revoke, and cannot delete, move or rename one of its roots.
func (a Actor) Fenced(roots ...FenceRoot) Actor {
	layer := make([]FenceRoot, len(roots))
	copy(layer, roots)
	layers := make([][]FenceRoot, 0, len(a.fence)+1)
	layers = append(layers, a.fence...)
	a.fence = append(layers, layer)
	return a
}

// Fence returns a copy of the roots of the actor's fence, and false for an
// actor with no fence. For an actor fenced more than once the list holds the
// roots of every Fenced call, in order; the actor reaches only what all of
// them allow, so use SameFence to compare two fences.
func (a Actor) Fence() ([]FenceRoot, bool) {
	if a.fence == nil {
		return nil, false
	}
	out := []FenceRoot{}
	for _, l := range a.fence {
		out = append(out, l...)
	}
	return out, true
}

// SameFence reports whether a and b have the same fence: both none, or the
// same roots from the same Fenced calls in the same order.
func (a Actor) SameFence(b Actor) bool {
	if (a.fence == nil) != (b.fence == nil) || len(a.fence) != len(b.fence) {
		return false
	}
	for i := range a.fence {
		if !slices.Equal(a.fence[i], b.fence[i]) {
			return false
		}
	}
	return true
}

func (a Actor) hasFence() bool { return a.fence != nil }

// checkFence returns ErrInvalid for a fence with too many layers or roots,
// or with a root that names no node or no valid mode.
func (a Actor) checkFence() error {
	if len(a.fence) > maxFenceLayers {
		return ErrInvalid
	}
	for _, l := range a.fence {
		if len(l) > MaxFenceRoots {
			return ErrInvalid
		}
		for _, r := range l {
			if r.Node <= 0 || (r.Mode != ModeRead && r.Mode != ModeWrite) {
				return ErrInvalid
			}
		}
	}
	return nil
}

// fenceMode returns the most the fence allows on a node, given the node and
// every folder above it (ancestors) and which of them are live. In each
// layer it is the highest mode of a live root among the ancestors; over the
// layers it is the lowest. An actor with no fence gets ModeWrite, and an
// invalid fence ModeNone. A deleted root counts for nothing, so a folder in
// the Trash keeps no part of the fence alive.
func (a Actor) fenceMode(ancestors []int64, live func(id int64) bool) Mode {
	if a.fence == nil {
		return ModeWrite
	}
	if a.checkFence() != nil {
		return ModeNone
	}
	out := ModeWrite
	for _, l := range a.fence {
		best := ModeNone
		for _, r := range l {
			if r.Mode > best && slices.Contains(ancestors, r.Node) && live(r.Node) {
				best = r.Mode
			}
		}
		out = min(out, best)
		if out == ModeNone {
			break
		}
	}
	return out
}

// isFenceRoot reports whether id is a root of any layer of the fence.
func (a Actor) isFenceRoot(id int64) bool {
	for _, l := range a.fence {
		for _, r := range l {
			if r.Node == id {
				return true
			}
		}
	}
	return false
}

// fenceNodes returns the node of every root of every layer, without repeats.
func (a Actor) fenceNodes() []int64 {
	var out []int64
	for _, l := range a.fence {
		for _, r := range l {
			if !slices.Contains(out, r.Node) {
				out = append(out, r.Node)
			}
		}
	}
	return out
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
	// Modified is when the change of Rev was made. List fills it; other calls
	// may leave it zero.
	Modified time.Time
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
	// NotMounted counts the shared folders a fenced view leaves out because
	// the fence does not reach them (the budget of a fence layer is limited).
	// It is for the prompt and the editor; it grants nothing.
	NotMounted int
}

// HistoryQuery selects change sets for History.
type HistoryQuery struct {
	Path      string // "/" or empty for everything the actor can read
	Limit     int    // 0 means 50; at most 1000
	BeforeRev int64  // only change sets with a lower revision; 0 means no bound
	// ChatID keeps only the change sets that one chat made (ChangeSet.ChatID).
	// Empty means change sets of every chat and of no chat.
	ChatID string
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
	// NodeByID returns a live node by its ID, at its current path, so a
	// caller that keeps a node ID follows a rename or a move. It returns
	// ErrNotFound when the node, or a folder above it, is deleted, or when the
	// actor cannot read it.
	NodeByID(ctx context.Context, a Actor, id int64) (Node, error)

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

// RootStore is a Store that also builds a view through an open folder. The host
// uses it for a folder that an add-on owns: every step goes through the root, so
// a link that the add-on plants inside the folder cannot send a file outside
// it, which a path would allow. The stores of this package implement it, and
// their Materialize opens the folder and calls it.
type RootStore interface {
	Store
	// MaterializeRoot builds the view in dir, which must be empty (ErrExists
	// otherwise), and returns the revision it used.
	MaterializeRoot(ctx context.Context, v View, dir *os.Root) (int64, error)
}

// Grant is one grant on a folder: a principal or group and its mode.
type Grant struct {
	Principal string
	Mode      Mode
}

// GrantLister is a Store that lists the grants on one folder, so an
// administrator can see who may use it. The stores of this package implement it.
type GrantLister interface {
	// Grants returns the grants on the live folder p itself, not the ones on
	// the folders above or below it, sorted by principal. Only the admin may
	// call it (ErrNotAdmin). A missing folder is ErrNotFound and a file is
	// ErrInvalid.
	Grants(ctx context.Context, admin Actor, p string) ([]Grant, error)
}

// NodeGrant is a grant on one folder, with the folder's node and path.
type NodeGrant struct {
	NodeID int64
	Path   string
	Grant
}

// GrantTree is a Store that lists the grants inside a folder, so the owner of
// a home can see which of its folders are shared. The stores of this package
// implement it.
type GrantTree interface {
	// GrantsBelow returns the grants on the live folders strictly below the
	// live folder p, sorted by path and principal. Only the admin may call it
	// (ErrNotAdmin). A missing folder is ErrNotFound.
	GrantsBelow(ctx context.Context, admin Actor, p string) ([]NodeGrant, error)
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

// PurgeCount counts what a purge removes, or would remove.
type PurgeCount struct {
	Files int   // files
	Dirs  int   // folders
	Bytes int64 // size of the files
}

// PurgeResult is the outcome of a purge.
type PurgeResult struct {
	PurgeCount
	// ByRev counts the removed nodes by the revision that deleted them. A
	// folder that goes with its parent is counted under the revision that
	// deleted it, which can be one the call did not name.
	ByRev map[int64]PurgeCount
}

// Add counts one removed node: a folder, or a file of size bytes, deleted by
// the change set rev. ByRev must not be nil.
func (r *PurgeResult) Add(rev int64, dir bool, size int64) {
	c := r.ByRev[rev]
	if dir {
		c.Dirs++
		r.Dirs++
	} else {
		c.Files++
		c.Bytes += size
		r.Files++
		r.Bytes += size
	}
	r.ByRev[rev] = c
}

// Purger is a Store that can erase deleted nodes for good. A delete only sets
// a tombstone, so the Trash can restore it; a purge removes the tombstoned
// nodes, their journal rows and the change sets that no operation is left in,
// and then the blobs that nothing refers to any more. A purged deletion
// cannot be undone. Both stores of this package implement it, and the
// conformance suite checks it.
type Purger interface {
	// Purge erases the nodes at or under the folder p that the change sets
	// revs deleted, with everything below them. A node that is live again, or that a later
	// change set changed, is kept. The actor needs write access to a node, or
	// the node is kept. With dryRun the result says what would go and nothing
	// changes.
	Purge(ctx context.Context, a Actor, p string, revs []int64, dryRun bool) (PurgeResult, error)
	// PurgeAll erases every deleted node at or under the folder p, the same
	// way. A deleted node that sits above p is not touched.
	PurgeAll(ctx context.Context, a Actor, p string, dryRun bool) (PurgeResult, error)
}
