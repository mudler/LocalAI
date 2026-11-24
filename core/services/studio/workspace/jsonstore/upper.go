package jsonstore

import (
	"context"
	"errors"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/internal/upperset"
)

// resolveMounts finds, by node ID, where each write mount stands now and
// requires write access on it. A mount whose folder is deleted is in gone.
func (s *Store) resolveMounts(d *data, a workspace.Actor, v workspace.View) (map[int]string, map[int]bool, error) {
	paths, gone := map[int]string{}, map[int]bool{}
	for i, m := range v.Mounts {
		if m.Mode < workspace.ModeWrite {
			continue
		}
		n, err := d.get(m.NodeID)
		if errors.Is(err, workspace.ErrNotFound) {
			gone[i] = true
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		// A tombstone keeps its place in the tree, so access still applies.
		if err := s.needWrite(d, a, n); err != nil {
			return nil, nil, err
		}
		if n.Deleted || n.Kind != workspace.KindDir {
			gone[i] = true
			continue
		}
		paths[i] = d.pathOf(n.ID)
	}
	return paths, gone, nil
}

// CommitUpper commits an overlay upper layer as one change set of kind
// agent. base is the revision the layer started from (the result of
// Materialize). A node changed after base is a conflict: the store keeps both
// versions or leaves the node as it is, and reports the conflict. Changes
// made by the change sets in o.Own do not count, when they have the same
// actor principal and chat ID as a.
//
// Each mount is found again by its node ID, so a folder that was renamed or
// moved after the view was taken is still the one that gets the changes.
func (s *Store) CommitUpper(ctx context.Context, v workspace.View, a workspace.Actor, base int64, src workspace.UpperSource, o workspace.UpperOptions) (workspace.ChangeSet, []workspace.Conflict, error) {
	pts, err := workspace.CheckMountPoints(v)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	// Check access before the layer is read: a guest without write access
	// must not make the store keep its files.
	var gone0 map[int]bool
	err = s.readLocked(ctx, func(d *data) error {
		var err error
		_, gone0, err = s.resolveMounts(d, a, v)
		return err
	})
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	u, err := upperset.Read(ctx, v, pts, gone0, src, s.blobs, s.maxSize)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	w, err := s.beginWrite(ctx, a, o.Title, workspace.ChangeAgent)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	defer w.rollback()
	own := w.d.ownRevisions(a, o.Own)
	paths, gone, err := s.resolveMounts(w.d, a, v)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	for i := range gone0 {
		gone[i] = true // its files have no blobs, even if the folder is back
	}
	u.Place(v, paths, gone)
	open := map[int]string{}
	for i, p := range paths {
		if !gone[i] {
			open[i] = p
		}
	}
	c := &upperCommit{w: w, u: u, v: v, base: base, own: own, open: open}
	if err := c.run(); err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	cs, err := w.commit()
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	return cs, u.Conflicts, nil
}

type upperCommit struct {
	w    *writer
	u    *upperset.Set
	v    workspace.View
	base int64
	own  map[int64]bool // revisions of the caller's own change sets
	open map[int]string // current path of each write mount that is not gone
}

func (c *upperCommit) conflict(p, reason, kept string) {
	c.u.Conflicts = append(c.u.Conflicts, workspace.Conflict{Path: p, Reason: reason, Kept: kept})
}

// denied reports whether err means the actor may not change a node.
func denied(err error) bool {
	return errors.Is(err, workspace.ErrReadOnly) || errors.Is(err, workspace.ErrNotFound)
}

// canDelete requires write access on n and on the folder that holds it.
func (c *upperCommit) canDelete(n nodeRow) error {
	if err := c.w.checkWrite(n); err != nil {
		return err
	}
	return c.w.checkParentWrite(n)
}

// nodeAtBase finds the node that stood at the end of rel, below the mount's
// folder, at the base revision. It follows the journal: for each name it takes
// the node whose latest row at or before the base put it under the parent
// found so far with that name and not deleted.
func (c *upperCommit) nodeAtBase(mount int, rel []string, depth int) (int64, bool, error) {
	parent := c.v.Mounts[mount].NodeID
	for _, name := range rel[:depth] {
		id, ok := c.w.d.childAtRev(parent, name, c.base)
		if !ok {
			return 0, false, nil
		}
		parent = id
	}
	return parent, true, nil
}

// goneAt finds, for the node at workspace path p, the first node on its path
// at the base (a folder above it, or the node itself) that another writer
// deleted, moved or changed after it. depth counts the path segments below
// the mount that lead to that node.
func (c *upperCommit) goneAt(p string) (depth int, id int64, ok bool, err error) {
	loc, found := c.u.Where[p]
	if !found {
		return 0, 0, false, nil
	}
	for d := 1; d <= len(loc.Rel); d++ {
		id, found, err := c.nodeAtBase(loc.Mount, loc.Rel, d)
		if err != nil {
			return 0, 0, false, err
		}
		if !found {
			return 0, 0, false, nil
		}
		changed, err := c.changedAfter(id, false)
		if err != nil {
			return 0, 0, false, err
		}
		if changed {
			return d, id, true, nil
		}
	}
	return 0, 0, false, nil
}

// deletedAfterBase reports whether the node at workspace path p, or a folder
// above it, existed at the base and was deleted, moved or changed after it by
// another writer.
func (c *upperCommit) deletedAfterBase(p string) (bool, error) {
	_, _, ok, err := c.goneAt(p)
	return ok, err
}

// live reports whether the node and every folder above it are not deleted.
func (c *upperCommit) live(id int64) (bool, error) {
	for _, a := range c.w.d.ancestors(id) {
		n, err := c.w.d.get(a)
		if err != nil {
			return false, err
		}
		if n.Deleted {
			return false, nil
		}
	}
	return true, nil
}

// copyTarget finds where the layer's version of f goes when the node it
// edited was deleted or moved after the base. A node that moved keeps its
// copy beside it, or at the same relative path inside the moved folder. A
// node that was deleted keeps its copy in the nearest live folder above it.
func (c *upperCommit) copyTarget(f string, depth int, id int64) (dir []string, name string, err error) {
	loc := c.u.Where[f]
	name = loc.Rel[len(loc.Rel)-1]
	x, err := c.w.d.get(id)
	if err != nil {
		return nil, "", err
	}
	alive, err := c.live(id)
	if err != nil {
		return nil, "", err
	}
	switch {
	case alive && depth == len(loc.Rel):
		name = x.Name
		dir, err = workspace.SplitPath(c.w.d.pathOf(x.Parent))
		return dir, name, err
	case alive:
		segs, err := workspace.SplitPath(c.w.d.pathOf(id))
		if err != nil {
			return nil, "", err
		}
		return append(segs, loc.Rel[depth:len(loc.Rel)-1]...), name, nil
	default:
		dir, _, err = c.fallbackDir(loc, depth)
		return dir, name, err
	}
}

// fallbackDir finds the folder for a copy that has no valid place of its
// own: the nearest folder above the node, as it stood at the base, that is
// still live and still inside the original write mount, or the mount folder
// itself. ok is false when the mount is gone.
func (c *upperCommit) fallbackDir(loc upperset.Loc, depth int) (dir []string, ok bool, err error) {
	if _, open := c.open[loc.Mount]; !open {
		return nil, false, nil
	}
	cands := []int64{}
	for k := depth - 1; k >= 1; k-- {
		aid, found, err := c.nodeAtBase(loc.Mount, loc.Rel, k)
		if err != nil {
			return nil, false, err
		}
		if found {
			cands = append(cands, aid)
		}
	}
	cands = append(cands, c.v.Mounts[loc.Mount].NodeID)
	for _, aid := range cands {
		if live, err := c.live(aid); err != nil {
			return nil, false, err
		} else if !live {
			continue
		}
		segs, err := workspace.SplitPath(c.w.d.pathOf(aid))
		if err != nil {
			return nil, false, err
		}
		if c.inMount(segs, loc.Mount) {
			return segs, true, nil
		}
	}
	return nil, false, nil
}

// inMount reports whether dir is the folder of write mount m or below it.
func (c *upperCommit) inMount(dir []string, m int) bool {
	mp, ok := c.open[m]
	if !ok {
		return false
	}
	segs, err := workspace.SplitPath(mp)
	return err == nil && len(dir) >= len(segs) && slices.Equal(dir[:len(segs)], segs)
}

// inWriteMount reports whether dir lies inside a write mount of the view.
func (c *upperCommit) inWriteMount(dir []string) bool {
	for m := range c.open {
		if c.inMount(dir, m) {
			return true
		}
	}
	return false
}

// keepGone stores the layer's version of f as a conflict copy when the node
// it edited is gone from its base location, and reports the conflict. When
// the actor may not write where the copy goes, the conflict has no Kept path.
// A copy lands only inside a write mount of the view: when the node now
// stands elsewhere, the copy goes to the nearest live folder above it in its
// original mount, and when there is none, no copy is kept.
func (c *upperCommit) keepGone(f string, uf upperset.File, depth int, id int64) error {
	dir, name, err := c.copyTarget(f, depth, id)
	if err != nil {
		return err
	}
	if !c.inWriteMount(dir) {
		loc := c.u.Where[f]
		var ok bool
		if dir, ok, err = c.fallbackDir(loc, depth); err != nil {
			return err
		}
		if !ok {
			c.conflict(f, workspace.ReasonChanged, "")
			return nil
		}
		name = loc.Rel[len(loc.Rel)-1]
	}
	kept := ""
	err = c.w.step(func() error {
		parent, err := c.w.parentForWrite(dir)
		if err != nil {
			return err
		}
		cp, err := c.w.conflictCopy(parent.ID, name, uf.Sum, uf.Size)
		if err != nil {
			return err
		}
		kept = c.w.d.pathOf(cp.ID)
		return nil
	})
	if err != nil {
		if denied(err) || errors.Is(err, workspace.ErrExists) {
			c.conflict(f, workspace.ReasonChanged, "")
			return nil
		}
		return err
	}
	c.conflict(f, workspace.ReasonChanged, kept)
	return nil
}

func (c *upperCommit) run() error {
	deletes := map[string]bool{}
	for _, p := range c.u.Whiteouts {
		deletes[p] = true
	}
	for _, d := range upperset.SortedKeys(c.u.Dirs) {
		if c.u.Dirs[d] {
			if err := c.hiddenUnder(d, deletes); err != nil {
				return err
			}
		}
	}
	if err := c.moves(deletes); err != nil {
		return err
	}
	if err := c.deletes(deletes); err != nil {
		return err
	}
	if err := c.folders(); err != nil {
		return err
	}
	return c.writeFiles()
}

// hiddenUnder adds to out every node below the opaque folder d that the
// layer does not show. Inside an opaque folder the lower layer is hidden, so
// a folder that the layer shows hides its own lower children too.
func (c *upperCommit) hiddenUnder(d string, out map[string]bool) error {
	n, err := c.w.d.lookupPath(d)
	if errors.Is(err, workspace.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if n.Kind != workspace.KindDir {
		return nil
	}
	for _, k := range c.w.d.children(n.ID) {
		p := workspace.JoinChild(d, k.Name)
		_, inFiles := c.u.Files[p]
		_, inDirs := c.u.Dirs[p]
		inFiles = inFiles || c.u.Shown[p]
		switch {
		case !inFiles && !inDirs:
			out[p] = true
		case inDirs && k.Kind == workspace.KindDir:
			if err := c.hiddenUnder(p, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// moves turns a delete plus a new file with the same blob into one move.
func (c *upperCommit) moves(deletes map[string]bool) error {
	d := c.w.d
	used := map[string]bool{}
	for _, p := range upperset.SortedKeys(deletes) {
		n, err := d.lookupPath(p)
		if errors.Is(err, workspace.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if n.Kind != workspace.KindFile || c.canDelete(n) != nil {
			continue
		}
		if changed, err := c.changedAfter(n.ID, true); err != nil {
			return err
		} else if changed {
			continue
		}
		for _, f := range upperset.SortedKeys(c.u.Files) {
			if used[f] || c.u.Files[f].Sum != n.Blob {
				continue
			}
			if _, err := d.lookupPath(f); err == nil {
				continue // the target exists: not a new file
			} else if !errors.Is(err, workspace.ErrNotFound) {
				return err
			}
			if gone, err := c.deletedAfterBase(f); err != nil {
				return err
			} else if gone {
				continue // another writer removed the target: report it below
			}
			to, err := workspace.SplitPath(f)
			if err != nil {
				return err
			}
			err = c.w.step(func() error {
				_, err := c.w.moveNode(n, to)
				return err
			})
			if errors.Is(err, workspace.ErrExists) || errors.Is(err, workspace.ErrInvalid) || denied(err) {
				break // keep it as a delete plus a create
			}
			if err != nil {
				return err
			}
			used[f] = true
			delete(deletes, p)
			delete(c.u.Files, f)
			break
		}
	}
	return nil
}

func (c *upperCommit) deletes(deletes map[string]bool) error {
	d := c.w.d
	paths := upperset.SortedKeys(deletes)
	slices.SortStableFunc(paths, func(a, b string) int { return upperset.Depth(b) - upperset.Depth(a) })
	for _, p := range paths {
		n, err := d.lookupPath(p)
		if errors.Is(err, workspace.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		changed, err := c.changedInSubtree(n.ID)
		if err != nil {
			return err
		}
		if changed {
			c.conflict(p, workspace.ReasonChanged, "")
			continue
		}
		if err := c.canDelete(n); denied(err) {
			c.conflict(p, workspace.ReasonReadOnly, "")
			continue
		} else if err != nil {
			return err
		}
		if err := c.w.deleteNode(n); err != nil {
			return err
		}
	}
	return nil
}

func (c *upperCommit) folders() error {
	dirs := upperset.SortedKeys(c.u.Dirs)
	slices.SortStableFunc(dirs, func(a, b string) int { return upperset.Depth(a) - upperset.Depth(b) })
	for _, d := range dirs {
		n, err := c.w.d.lookupPath(d)
		switch {
		case err == nil && n.Kind == workspace.KindDir:
			continue
		case err == nil: // a file stands where the folder goes
			if changed, err := c.changedAfter(n.ID, true); err != nil {
				return err
			} else if changed {
				c.conflict(d, workspace.ReasonChanged, "")
				continue
			}
			if err := c.canDelete(n); denied(err) {
				c.conflict(d, workspace.ReasonReadOnly, "")
				continue
			} else if err != nil {
				return err
			}
			if err := c.w.deleteNode(n); err != nil {
				return err
			}
		case !errors.Is(err, workspace.ErrNotFound):
			return err
		}
		segs, err := workspace.SplitPath(d)
		if err != nil {
			return err
		}
		if gone, err := c.deletedAfterBase(d); err != nil {
			return err
		} else if gone {
			continue // the files below it report the conflict
		}
		if err := c.w.step(func() error {
			_, err := c.w.parentForWrite(segs)
			return err
		}); err != nil {
			if errors.Is(err, workspace.ErrExists) {
				c.conflict(d, workspace.ReasonNameTaken, "")
				continue
			}
			if denied(err) {
				c.conflict(d, workspace.ReasonReadOnly, "")
				continue
			}
			return err
		}
	}
	return nil
}

func (c *upperCommit) writeFiles() error {
	d := c.w.d
	for _, f := range upperset.SortedKeys(c.u.Files) {
		uf := c.u.Files[f]
		if uf.Sum == "" {
			return workspace.ErrInvalid // never write a file node without a blob
		}
		n, err := d.lookupPath(f)
		switch {
		case errors.Is(err, workspace.ErrNotFound):
			if depth, id, gone, err := c.goneAt(f); err != nil {
				return err
			} else if gone {
				if err := c.keepGone(f, uf, depth, id); err != nil {
					return err
				}
				continue
			}
			if err := c.create(f, uf); err != nil {
				return err
			}
		case err != nil:
			return err
		case n.Kind == workspace.KindDir:
			changed, err := c.changedInSubtree(n.ID)
			if err != nil {
				return err
			}
			if changed {
				if err := c.keepCopy(f, n, uf); err != nil {
					return err
				}
				continue
			}
			if err := c.canDelete(n); denied(err) {
				c.conflict(f, workspace.ReasonReadOnly, "")
				continue
			} else if err != nil {
				return err
			}
			if err := c.w.deleteNode(n); err != nil {
				return err
			}
			if err := c.create(f, uf); err != nil {
				return err
			}
		case n.Blob == uf.Sum:
			// not a change
		default:
			if changed, err := c.changedAfter(n.ID, true); err != nil {
				return err
			} else if changed {
				if err := c.keepCopy(f, n, uf); err != nil {
					return err
				}
				continue
			}
			if err := c.w.checkWrite(n); denied(err) {
				c.conflict(f, workspace.ReasonReadOnly, "")
				continue
			} else if err != nil {
				return err
			}
			ns := n.state
			ns.Blob, ns.Size = uf.Sum, uf.Size
			if _, err := c.w.apply(n.ID, workspace.KindFile, workspace.OpWrite, ns); err != nil {
				return err
			}
		}
	}
	return nil
}

// keepCopy stores the layer's version of f beside n, which another writer
// changed after the base, and reports the conflict.
func (c *upperCommit) keepCopy(f string, n nodeRow, uf upperset.File) error {
	if err := c.w.checkParentWrite(n); denied(err) {
		c.conflict(f, workspace.ReasonReadOnly, "")
		return nil
	} else if err != nil {
		return err
	}
	cp, err := c.w.conflictCopy(n.Parent, n.Name, uf.Sum, uf.Size)
	if err != nil {
		return err
	}
	c.conflict(f, workspace.ReasonChanged, c.w.d.pathOf(cp.ID))
	return nil
}

// create adds a new file at f. A sibling with the same folded name makes it a
// conflict copy.
func (c *upperCommit) create(f string, uf upperset.File) error {
	if uf.Sum == "" {
		return workspace.ErrInvalid
	}
	segs, err := workspace.SplitPath(f)
	if err != nil {
		return err
	}
	dir, name := segs[:len(segs)-1], segs[len(segs)-1]
	err = c.w.step(func() error {
		parent, err := c.w.parentForWrite(dir)
		if err != nil {
			return err
		}
		if _, taken := c.w.d.childByFold(parent.ID, name); taken {
			cp, err := c.w.conflictCopy(parent.ID, name, uf.Sum, uf.Size)
			if err != nil {
				return err
			}
			c.conflict(f, workspace.ReasonNameTaken, c.w.d.pathOf(cp.ID))
			return nil
		}
		_, err = c.w.apply(0, workspace.KindFile, workspace.OpCreate, state{Parent: parent.ID, Name: name, Blob: uf.Sum, Size: uf.Size})
		return err
	})
	if errors.Is(err, workspace.ErrExists) {
		c.conflict(f, workspace.ReasonNameTaken, "")
		return nil
	}
	if denied(err) {
		c.conflict(f, workspace.ReasonReadOnly, "")
		return nil
	}
	return err
}
