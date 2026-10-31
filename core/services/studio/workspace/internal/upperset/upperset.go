// Package upperset reads an overlay upper layer and maps its entries to
// workspace paths. It is the part of CommitUpper that does not depend on how
// a store keeps its nodes, so the JSON and GORM stores share it.
package upperset

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

// Depth is the number of slashes in p, so a parent sorts before its children.
func Depth(p string) int { return strings.Count(p, "/") }

// SortedKeys returns the keys of m in order.
func SortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

// File is a file of the layer, stored as a blob. Sum is empty for a file of a
// mount whose folder is gone.
type File struct {
	Sum  string
	Size int64
}

// entry is a usable overlay entry, mapped to a mount and a path below it.
type entry struct {
	mount  int
	rel    []string
	kind   workspace.EntryKind
	opaque bool
	file   File
}

// rawConflict is a conflict whose path depends on where the mount stands now.
// mount is -1 when path is already final.
type rawConflict struct {
	path   string
	mount  int
	rel    []string
	reason string
}

// Set is an upper layer mapped to workspace paths. Read fills it; Place
// fills the exported fields.
type Set struct {
	entries []entry
	raw     []rawConflict

	Files     map[string]File
	Dirs      map[string]bool // path to opaque
	Whiteouts []string
	Conflicts []workspace.Conflict

	Where map[string]Loc  // workspace path to mount and path below it
	Shown map[string]bool // paths of entries that became conflicts
}

// Loc says where a workspace path stands in its mount.
type Loc struct {
	Mount int
	Rel   []string
}

// JoinRel joins the segments rel below the folder base.
func JoinRel(base string, rel []string) string {
	for _, sg := range rel {
		base = workspace.JoinChild(base, sg)
	}
	return base
}

// Read walks the layer, stores the files of write mounts as blobs (at most
// max bytes each) and maps every entry to a mount. Entries it cannot use
// become conflicts. pts holds the mount points by mount index; gone marks
// the write mounts whose folder is deleted.
func Read(ctx context.Context, v workspace.View, pts map[int]string, gone map[int]bool, src workspace.UpperSource, blobs blob.Store, max int64) (*Set, error) {
	u := &Set{}
	lit := func(up, reason string) {
		u.raw = append(u.raw, rawConflict{path: "/" + up, mount: -1, reason: reason})
	}
	err := src.Walk(ctx, func(e workspace.UpperEntry) error {
		up := strings.Trim(e.Path, "/")
		if up == "" {
			return nil
		}
		mi := -1
		for i, pt := range pts {
			if up == pt || strings.HasPrefix(up, pt+"/") {
				mi = i
				break
			}
		}
		if mi < 0 {
			if e.Kind == workspace.EntryDir {
				for _, pt := range pts {
					if strings.HasPrefix(pt, up+"/") {
						return nil // overlayfs makes the parents of a mount point
					}
				}
			}
			lit(up, workspace.ReasonOutsideView)
			return nil
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(up, pts[mi]), "/")
		if v.Mounts[mi].Mode < workspace.ModeWrite {
			// A folder alone changes nothing: the host backend lists every
			// folder of its work tree, and overlayfs makes the parents of a
			// changed file. A file, a whiteout or an opaque folder is a
			// change that a read-only mount refuses.
			if e.Kind != workspace.EntryDir || (rest != "" && workspace.IsOpaque(e.Xattrs)) {
				lit(up, workspace.ReasonReadOnly)
			}
			return nil
		}
		var rel []string
		if rest != "" {
			var err error
			if rel, err = workspace.SplitPath(rest); err != nil {
				lit(up, workspace.ReasonBadName)
				return nil
			}
		}
		add := func(reason string) {
			u.raw = append(u.raw, rawConflict{mount: mi, rel: rel, reason: reason})
		}
		switch {
		case e.Kind == workspace.EntryDir:
			u.entries = append(u.entries, entry{mount: mi, rel: rel, kind: workspace.EntryDir, opaque: workspace.IsOpaque(e.Xattrs)})
		case rest == "":
			add(workspace.ReasonUnsupported) // only a folder can stand at a mount root
		case e.Kind == workspace.EntryFile && gone[mi]:
			u.entries = append(u.entries, entry{mount: mi, rel: rel, kind: workspace.EntryFile})
		case e.Kind == workspace.EntryFile:
			rc, err := e.Open()
			if err != nil {
				return err
			}
			sum, size, err := blobs.Put(ctx, rc, max)
			_ = rc.Close()
			if errors.Is(err, workspace.ErrTooLarge) {
				add(workspace.ReasonTooLarge)
				return nil
			}
			if err != nil {
				return err
			}
			u.entries = append(u.entries, entry{mount: mi, rel: rel, kind: workspace.EntryFile, file: File{Sum: sum, Size: size}})
		case e.Kind == workspace.EntryWhiteout:
			u.entries = append(u.entries, entry{mount: mi, rel: rel, kind: workspace.EntryWhiteout})
		default:
			add(workspace.ReasonUnsupported)
		}
		return nil
	})
	return u, err
}

// Place maps the entries to the current paths of their mounts.
func (u *Set) Place(v workspace.View, paths map[int]string, gone map[int]bool) {
	u.Files, u.Dirs = map[string]File{}, map[string]bool{}
	u.Where, u.Shown = map[string]Loc{}, map[string]bool{}
	for _, r := range u.raw {
		p := r.path
		if r.mount >= 0 {
			base, ok := paths[r.mount]
			if !ok {
				base = v.Mounts[r.mount].Path
			} else {
				u.Shown[JoinRel(base, r.rel)] = true
			}
			p = JoinRel(base, r.rel)
		}
		u.Conflicts = append(u.Conflicts, workspace.Conflict{Path: p, Reason: r.reason})
	}
	for _, e := range u.entries {
		if gone[e.mount] {
			if len(e.rel) > 0 {
				u.Conflicts = append(u.Conflicts, workspace.Conflict{Path: JoinRel(v.Mounts[e.mount].Path, e.rel), Reason: workspace.ReasonParentDeleted})
			}
			continue
		}
		wp := JoinRel(paths[e.mount], e.rel)
		u.Where[wp] = Loc{Mount: e.mount, Rel: e.rel}
		switch e.kind {
		case workspace.EntryDir:
			u.Dirs[wp] = e.opaque
		case workspace.EntryFile:
			u.Files[wp] = e.file
		case workspace.EntryWhiteout:
			u.Whiteouts = append(u.Whiteouts, wp)
		}
	}
}
