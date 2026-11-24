package sandbox

import (
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// IndexedEntry is an upper layer entry with a key. The key must change
// whenever the entry may have changed. A backend builds it from six parts:
// the inode number, the inode generation, the size, the change time (ctime),
// the modification time (mtime) in nanoseconds, and the i_version counter.
// Times with only second resolution are not enough, because two writes in
// one second give the same key. An empty key means "always send".
type IndexedEntry struct {
	workspace.UpperEntry
	Key string
}

// IndexRecord is what UpperIndex keeps for one path.
type IndexRecord struct {
	Kind workspace.EntryKind `json:"kind"`
	Key  string              `json:"key,omitempty"`
}

// UpperIndex records the entries of an upper layer at the last successful
// commit, by path. It makes repeated commits of one upper layer correct:
//
//   - A file whose key is unchanged is not sent again, so a file that made a
//     conflict copy does not make another one at each checkpoint. A file
//     below an opaque folder is always sent, because the commit deletes what
//     an opaque folder does not show.
//   - A path that was in the layer and is gone now is sent as a whiteout.
//     Overlayfs leaves no whiteout when the guest deletes or renames a file
//     that the lower layer does not have, for example a file that an earlier
//     checkpoint committed.
//
// Save the index that Plan returns only after the commit succeeded.
type UpperIndex map[string]IndexRecord

// Plan returns the entries to commit and the index to save after the commit.
// entries must list the whole upper layer, parents before children.
func (ix UpperIndex) Plan(entries []IndexedEntry) ([]workspace.UpperEntry, UpperIndex) {
	next := UpperIndex{}
	opaque := map[string]bool{}
	var send []workspace.UpperEntry
	for _, e := range entries {
		p := strings.Trim(e.Path, "/")
		if p == "" {
			continue
		}
		e.Path = p
		next[p] = IndexRecord{Kind: e.Kind, Key: e.Key}
		if e.Kind == workspace.EntryDir && workspace.IsOpaque(e.Xattrs) {
			opaque[p] = true
		}
		if e.Kind == workspace.EntryFile && e.Key != "" && !belowOpaque(p, opaque) {
			if old, ok := ix[p]; ok && old.Kind == workspace.EntryFile && old.Key == e.Key {
				continue
			}
		}
		send = append(send, e.UpperEntry)
	}
	var gone []string
	for p := range ix {
		if _, ok := next[p]; !ok {
			gone = append(gone, p)
		}
	}
	slices.Sort(gone)
	for _, p := range gone {
		send = append(send, workspace.FixtureWhiteout(p))
	}
	return send, next
}

// belowOpaque reports whether a folder above p is opaque.
func belowOpaque(p string, opaque map[string]bool) bool {
	for {
		i := strings.LastIndexByte(p, '/')
		if i < 0 {
			return false
		}
		p = p[:i]
		if opaque[p] {
			return true
		}
	}
}
