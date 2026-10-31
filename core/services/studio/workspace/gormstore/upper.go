package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/workspace/internal/upperset"
)

// resolveMounts finds, by node ID, where each write mount stands now and
// requires write access on it. A mount whose folder is deleted is in gone.
func (s *Workspace) resolveMounts(ctx context.Context, q querier, a Actor, v View) (map[int]string, map[int]bool, error) {
	paths, gone := map[int]string{}, map[int]bool{}
	for i, m := range v.Mounts {
		if m.Mode < ModeWrite {
			continue
		}
		n, err := getNode(ctx, q, m.NodeID)
		if errors.Is(err, ErrNotFound) {
			gone[i] = true
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		// A tombstone keeps its place in the tree, so access still applies.
		if err := s.needWrite(ctx, q, a, n); err != nil {
			return nil, nil, err
		}
		if n.deleted || n.kind != KindDir {
			gone[i] = true
			continue
		}
		if paths[i], err = pathOf(ctx, q, n.id); err != nil {
			return nil, nil, err
		}
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
func (s *Workspace) CommitUpper(ctx context.Context, v View, a Actor, base int64, src UpperSource, o UpperOptions) (ChangeSet, []Conflict, error) {
	pts, err := CheckMountPoints(v)
	if err != nil {
		return ChangeSet{}, nil, err
	}
	// Check access before the layer is read: a guest without write access
	// must not make the store keep its files.
	chk, err := s.db.BeginTx(ctx)
	if err != nil {
		return ChangeSet{}, nil, err
	}
	_, gone0, err := s.resolveMounts(ctx, chk, a, v)
	_ = chk.Rollback()
	if err != nil {
		return ChangeSet{}, nil, err
	}
	u, err := upperset.Read(ctx, v, pts, gone0, src, s.blobs, s.maxSize)
	if err != nil {
		return ChangeSet{}, nil, err
	}
	w, err := s.beginWrite(ctx, a, o.Title, ChangeAgent)
	if err != nil {
		return ChangeSet{}, nil, err
	}
	defer w.rollback()
	own, err := ownRevisions(ctx, w.tx, a, o.Own)
	if err != nil {
		return ChangeSet{}, nil, err
	}
	paths, gone, err := s.resolveMounts(ctx, w.tx, a, v)
	if err != nil {
		return ChangeSet{}, nil, err
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
		return ChangeSet{}, nil, err
	}
	cs, err := w.commit()
	if err != nil {
		if errors.Is(err, ErrCommitted) {
			return cs, u.Conflicts, err // the change set is durable
		}
		return ChangeSet{}, nil, err
	}
	return cs, u.Conflicts, nil
}

type upperCommit struct {
	w    *writer
	u    *upperset.Set
	v    View
	base int64
	own  []int64        // the caller's own revisions, as ownRevisions kept them
	open map[int]string // current path of each write mount that is not gone
}

func (c *upperCommit) conflict(p, reason, kept string) {
	c.u.Conflicts = append(c.u.Conflicts, Conflict{Path: p, Reason: reason, Kept: kept})
}

// denied reports whether err means the actor may not change a node.
func denied(err error) bool { return errors.Is(err, ErrReadOnly) || errors.Is(err, ErrNotFound) }

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
	ctx, q := c.w.ctx, c.w.tx
	parent := c.v.Mounts[mount].NodeID
	for _, name := range rel[:depth] {
		var id int64
		err := q.QueryRowContext(ctx, `SELECT j.node_id FROM studio_ws_journal j
			WHERE j.new_parent = ? AND j.new_name = ? AND j.new_deleted = 0 AND j.rev <= ?
			AND j.id = (SELECT MAX(k.id) FROM studio_ws_journal k WHERE k.node_id = j.node_id AND k.rev <= ?)
			LIMIT 1`, parent, name, c.base, c.base).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
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
	ids, err := ancestors(c.w.ctx, c.w.tx, id)
	if err != nil {
		return false, err
	}
	for _, a := range ids {
		n, err := getNode(c.w.ctx, c.w.tx, a)
		if err != nil {
			return false, err
		}
		if n.deleted {
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
	x, err := getNode(c.w.ctx, c.w.tx, id)
	if err != nil {
		return nil, "", err
	}
	alive, err := c.live(id)
	if err != nil {
		return nil, "", err
	}
	var p string
	switch {
	case alive && depth == len(loc.Rel):
		name = x.name
		p, err = pathOf(c.w.ctx, c.w.tx, x.parent)
	case alive:
		p, err = pathOf(c.w.ctx, c.w.tx, id)
		if err == nil {
			var segs []string
			if segs, err = splitPath(p); err == nil {
				return append(segs, loc.Rel[depth:len(loc.Rel)-1]...), name, nil
			}
		}
	default:
		dir, _, err = c.fallbackDir(loc, depth)
		return dir, name, err
	}
	if err != nil {
		return nil, "", err
	}
	dir, err = splitPath(p)
	return dir, name, err
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
		p, err := pathOf(c.w.ctx, c.w.tx, aid)
		if err != nil {
			return nil, false, err
		}
		segs, err := splitPath(p)
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
	segs, err := splitPath(mp)
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
			c.conflict(f, ReasonChanged, "")
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
		cp, err := c.w.conflictCopy(parent.id, name, uf.Sum, uf.Size)
		if err != nil {
			return err
		}
		kept, err = pathOf(c.w.ctx, c.w.tx, cp.id)
		return err
	})
	if err != nil {
		if denied(err) || errors.Is(err, ErrExists) {
			c.conflict(f, ReasonChanged, "")
			return nil
		}
		return err
	}
	c.conflict(f, ReasonChanged, kept)
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
	n, err := lookupPath(c.w.ctx, c.w.tx, d)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if n.kind != KindDir {
		return nil
	}
	kids, err := children(c.w.ctx, c.w.tx, n.id)
	if err != nil {
		return err
	}
	for _, k := range kids {
		p := joinChild(d, k.name)
		_, inFiles := c.u.Files[p]
		_, inDirs := c.u.Dirs[p]
		inFiles = inFiles || c.u.Shown[p]
		switch {
		case !inFiles && !inDirs:
			out[p] = true
		case inDirs && k.kind == KindDir:
			if err := c.hiddenUnder(p, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// moves turns a delete plus a new file with the same blob into one move.
func (c *upperCommit) moves(deletes map[string]bool) error {
	ctx, q := c.w.ctx, c.w.tx
	used := map[string]bool{}
	for _, p := range upperset.SortedKeys(deletes) {
		n, err := lookupPath(ctx, q, p)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if n.kind != KindFile || c.canDelete(n) != nil {
			continue
		}
		if changed, err := c.changedAfter(n.id, true); err != nil {
			return err
		} else if changed {
			continue
		}
		for _, f := range upperset.SortedKeys(c.u.Files) {
			if used[f] || c.u.Files[f].Sum != n.blob {
				continue
			}
			if _, err := lookupPath(ctx, q, f); err == nil {
				continue // the target exists: not a new file
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
			if gone, err := c.deletedAfterBase(f); err != nil {
				return err
			} else if gone {
				continue // another writer removed the target: report it below
			}
			to, err := splitPath(f)
			if err != nil {
				return err
			}
			err = c.w.step(func() error {
				_, err := c.w.moveNode(n, to)
				return err
			})
			if errors.Is(err, ErrExists) || errors.Is(err, ErrInvalid) || denied(err) {
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
	ctx, q := c.w.ctx, c.w.tx
	paths := upperset.SortedKeys(deletes)
	slices.SortStableFunc(paths, func(a, b string) int { return upperset.Depth(b) - upperset.Depth(a) })
	for _, p := range paths {
		n, err := lookupPath(ctx, q, p)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		changed, err := c.changedInSubtree(n.id)
		if err != nil {
			return err
		}
		if changed {
			c.conflict(p, ReasonChanged, "")
			continue
		}
		if err := c.canDelete(n); denied(err) {
			c.conflict(p, ReasonReadOnly, "")
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
	ctx, q := c.w.ctx, c.w.tx
	dirs := upperset.SortedKeys(c.u.Dirs)
	slices.SortStableFunc(dirs, func(a, b string) int { return upperset.Depth(a) - upperset.Depth(b) })
	for _, d := range dirs {
		n, err := lookupPath(ctx, q, d)
		switch {
		case err == nil && n.kind == KindDir:
			continue
		case err == nil: // a file stands where the folder goes
			if changed, err := c.changedAfter(n.id, true); err != nil {
				return err
			} else if changed {
				c.conflict(d, ReasonChanged, "")
				continue
			}
			if err := c.canDelete(n); denied(err) {
				c.conflict(d, ReasonReadOnly, "")
				continue
			} else if err != nil {
				return err
			}
			if err := c.w.deleteNode(n); err != nil {
				return err
			}
		case !errors.Is(err, ErrNotFound):
			return err
		}
		segs, err := splitPath(d)
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
			if errors.Is(err, ErrExists) {
				c.conflict(d, ReasonNameTaken, "")
				continue
			}
			if denied(err) {
				c.conflict(d, ReasonReadOnly, "")
				continue
			}
			return err
		}
	}
	return nil
}

func (c *upperCommit) writeFiles() error {
	ctx, q := c.w.ctx, c.w.tx
	for _, f := range upperset.SortedKeys(c.u.Files) {
		uf := c.u.Files[f]
		if uf.Sum == "" {
			return ErrInvalid // never write a file node without a blob
		}
		n, err := lookupPath(ctx, q, f)
		switch {
		case errors.Is(err, ErrNotFound):
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
		case n.kind == KindDir:
			changed, err := c.changedInSubtree(n.id)
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
				c.conflict(f, ReasonReadOnly, "")
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
		case n.blob == uf.Sum:
			// not a change
		default:
			if changed, err := c.changedAfter(n.id, true); err != nil {
				return err
			} else if changed {
				if err := c.keepCopy(f, n, uf); err != nil {
					return err
				}
				continue
			}
			if err := c.w.checkWrite(n); denied(err) {
				c.conflict(f, ReasonReadOnly, "")
				continue
			} else if err != nil {
				return err
			}
			ns := n.state
			ns.blob, ns.size = uf.Sum, uf.Size
			if _, err := c.w.apply(n.id, KindFile, OpWrite, ns); err != nil {
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
		c.conflict(f, ReasonReadOnly, "")
		return nil
	} else if err != nil {
		return err
	}
	cp, err := c.w.conflictCopy(n.parent, n.name, uf.Sum, uf.Size)
	if err != nil {
		return err
	}
	kept, err := pathOf(c.w.ctx, c.w.tx, cp.id)
	if err != nil {
		return err
	}
	c.conflict(f, ReasonChanged, kept)
	return nil
}

// create adds a new file at f. A sibling with the same folded name makes it a
// conflict copy.
func (c *upperCommit) create(f string, uf upperset.File) error {
	if uf.Sum == "" {
		return ErrInvalid
	}
	segs, err := splitPath(f)
	if err != nil {
		return err
	}
	dir, name := segs[:len(segs)-1], segs[len(segs)-1]
	err = c.w.step(func() error {
		parent, err := c.w.parentForWrite(dir)
		if err != nil {
			return err
		}
		if _, taken, err := childByFold(c.w.ctx, c.w.tx, parent.id, name); err != nil {
			return err
		} else if taken {
			cp, err := c.w.conflictCopy(parent.id, name, uf.Sum, uf.Size)
			if err != nil {
				return err
			}
			kept, err := pathOf(c.w.ctx, c.w.tx, cp.id)
			if err != nil {
				return err
			}
			c.conflict(f, ReasonNameTaken, kept)
			return nil
		}
		_, err = c.w.apply(0, KindFile, OpCreate, state{parent: parent.id, name: name, blob: uf.Sum, size: uf.Size})
		return err
	})
	if errors.Is(err, ErrExists) {
		c.conflict(f, ReasonNameTaken, "")
		return nil
	}
	if denied(err) {
		c.conflict(f, ReasonReadOnly, "")
		return nil
	}
	return err
}
