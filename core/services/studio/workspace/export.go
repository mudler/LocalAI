package workspace

// The helpers below are the name and path rules, exported for the store
// implementations in sub-packages, so every store applies the same rules.

// CleanName returns the NFC form of a name, or ErrBadName.
func CleanName(s string) (string, error) { return cleanName(s) }

// SplitPath splits a path into clean segments. "/" gives none.
func SplitPath(p string) ([]string, error) { return splitPath(p) }

// JoinPath joins segments into a path that starts with "/".
func JoinPath(segs []string) string { return joinPath(segs) }

// ConflictName returns the name of the i-th conflict copy of name.
func ConflictName(name, label string, i int) string { return conflictName(name, label, i) }

// Principals returns the actor's principal followed by its groups.
func (a Actor) Principals() []string { return a.principals() }

// Label names the actor in conflict copies: the principal, or "chat <id>".
func (a Actor) Label() string { return a.label() }

// JoinChild joins a folder path and a child name.
func JoinChild(dir, name string) string { return joinChild(dir, name) }

// The helpers below apply an actor's fence (see Actor.Fenced). They are
// exported for the store implementations, so every store applies the same
// rules.

// HasFence reports whether the actor has a fence.
func HasFence(a Actor) bool { return a.hasFence() }

// CheckFence returns ErrInvalid when the actor's fence has more than
// MaxFenceRoots roots in one Fenced call, too many Fenced calls, or a root
// with no node or no valid mode. An actor with no fence passes.
func CheckFence(a Actor) error { return a.checkFence() }

// FenceMode returns the most the actor's fence allows on a node. ancestors
// holds the node and every folder above it; live tells whether a node is
// live (not deleted). The store takes the lower of this and the actor's
// grants. An actor with no fence gets ModeWrite, an invalid fence ModeNone.
func FenceMode(a Actor, ancestors []int64, live func(id int64) bool) Mode {
	return a.fenceMode(ancestors, live)
}

// IsFenceRoot reports whether the node id is a root of the actor's fence. A
// store refuses to delete, move or rename it for that actor (ErrReadOnly).
func IsFenceRoot(a Actor, id int64) bool { return a.isFenceRoot(id) }

// FenceNodes returns the nodes of the roots of the actor's fence, without
// repeats, for ReadablePrefixes.
func FenceNodes(a Actor) []int64 { return a.fenceNodes() }
