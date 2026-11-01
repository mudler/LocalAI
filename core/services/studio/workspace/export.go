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
