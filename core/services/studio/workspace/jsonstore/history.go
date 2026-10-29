package jsonstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// readableParent tells whether the actor can read the folder with id.
func (s *Store) readableParent(d *data, a workspace.Actor, id int64) bool {
	return id != 0 && s.access(d, a, id) >= workspace.ModeRead
}

// visibleOps keeps what the actor can see of the operations of rev. A path
// is visible when the actor can read its parent folder now. A move out of a
// hidden folder shows as a create, and a move into a hidden folder shows as
// a delete at the old path.
// When it drops or rewrites any operation, the title of the change set may
// name hidden paths, so the result also tells the caller to use a generic one.
func (s *Store) visibleOps(d *data, a workspace.Actor, rev int64, ops []workspace.Op) ([]workspace.Op, bool, error) {
	rows := d.byRev[rev]
	if len(rows) != len(ops) {
		return nil, false, workspace.ErrInvalid
	}
	var out []workspace.Op
	changed := false
	for i, op := range ops {
		row := d.journal[rows[i]]
		oldOK := s.readableParent(d, a, row.Old.Parent)
		newOK := s.readableParent(d, a, row.New.Parent)
		if (row.Old.Parent != 0 && !oldOK) || (row.New.Parent != 0 && !newOK) {
			changed = true
		}
		switch {
		case !oldOK && !newOK:
			continue
		case !oldOK:
			op.OldPath, op.OldBlob = "", ""
			if op.Kind == workspace.OpMove {
				op.Kind = workspace.OpCreate
			}
		case !newOK:
			op.NewPath, op.NewBlob = op.OldPath, ""
			if op.Kind == workspace.OpMove {
				op.Kind = workspace.OpDelete
			}
		}
		out = append(out, op)
	}
	return out, changed, nil
}

// visible limits cs to what the actor can see.
func (s *Store) visible(d *data, a workspace.Actor, cs workspace.ChangeSet) (workspace.ChangeSet, error) {
	ops, changed, err := s.visibleOps(d, a, cs.Rev, cs.Ops)
	if err != nil {
		return workspace.ChangeSet{}, err
	}
	cs.Ops = ops
	if changed {
		cs.Title = workspace.GenericTitle(cs)
	}
	return cs, nil
}

// touches reports whether a journal row has np or a path below it on either
// side.
func touches(r journalRow, np string) bool {
	under := func(p string) bool {
		return p == np || (np == "/" && strings.HasPrefix(p, "/")) || strings.HasPrefix(p, np+"/")
	}
	return under(r.Old.Path) || under(r.New.Path)
}

func (s *Store) History(ctx context.Context, a workspace.Actor, hq workspace.HistoryQuery) ([]workspace.ChangeSet, error) {
	p := hq.Path
	if p == "" {
		p = "/"
	}
	limit := hq.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 1000)
	before := hq.BeforeRev
	if before <= 0 {
		before = math.MaxInt64
	}
	var out []workspace.ChangeSet
	err := s.readLocked(ctx, func(d *data) error {
		n, err := s.find(d, a, p, workspace.ModeRead)
		if err != nil {
			return err
		}
		np := d.pathOf(n.ID)
		var revs []int64
		for i := len(d.revs) - 1; i >= 0 && len(revs) < limit; i-- {
			rev := d.revs[i]
			if rev >= before {
				continue
			}
			if hq.ChatID != "" {
				if c, ok := d.sets[rev]; !ok || c.ChatID != hq.ChatID {
					continue
				}
			}
			for _, ji := range d.byRev[rev] {
				if touches(d.journal[ji], np) {
					revs = append(revs, rev)
					break
				}
			}
		}
		for _, rev := range revs {
			cs, err := s.visible(d, a, d.changeSet(rev))
			if err != nil {
				return err
			}
			if len(cs.Ops) > 0 {
				out = append(out, cs)
			}
		}
		return nil
	})
	return out, err
}

func (s *Store) ChangesSince(ctx context.Context, a workspace.Actor, rev int64, limit int) ([]workspace.ChangeSet, error) {
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 1000)
	var out []workspace.ChangeSet
	err := s.readLocked(ctx, func(d *data) error {
		start, _ := slices.BinarySearch(d.revs, rev+1)
		for _, r := range d.revs[start:] {
			cs, err := s.visible(d, a, d.changeSet(r))
			if err != nil {
				return err
			}
			if len(cs.Ops) == 0 {
				continue
			}
			out = append(out, cs)
			if len(out) == limit {
				break
			}
		}
		return nil
	})
	return out, err
}

// undoItem is one node of a change set: the state to restore (target) and
// the state the change set left (expected).
type undoItem struct {
	id               int64
	target, expected state
}

// undoPlan reads the journal of rev. Nodes come in the reverse order of their
// first operation, so a folder is restored before its children and a child is
// deleted before its folder.
func (d *data) undoPlan(rev int64) []undoItem {
	items := map[int64]*undoItem{}
	var order []int64
	for _, ji := range d.byRev[rev] {
		r := d.journal[ji]
		it, ok := items[r.Node]
		if !ok {
			it = &undoItem{id: r.Node, target: r.Old.state()}
			items[r.Node] = it
			order = append(order, r.Node)
		}
		it.expected = r.New.state()
	}
	out := make([]undoItem, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		it := *items[order[i]]
		if it.target.Deleted {
			// A node that did not exist: keep its location and blob, set the tombstone.
			t := it.expected
			t.Deleted = true
			it.target = t
		}
		out = append(out, it)
	}
	return out
}

func sameState(a, b state) bool {
	return a.Parent == b.Parent && a.Name == b.Name && a.Blob == b.Blob && a.Deleted == b.Deleted
}

func undoOp(cur, target state) workspace.OpKind {
	switch {
	case target.Deleted && !cur.Deleted:
		return workspace.OpDelete
	case !target.Deleted && cur.Deleted:
		return workspace.OpCreate
	case cur.Parent != target.Parent || cur.Name != target.Name:
		return workspace.OpMove
	default:
		return workspace.OpWrite
	}
}

func (s *Store) Undo(ctx context.Context, a workspace.Actor, rev int64, o workspace.UndoOptions) (workspace.ChangeSet, []workspace.Conflict, error) {
	w, err := s.beginWrite(ctx, a, fmt.Sprintf("Undo of revision %d", rev), workspace.ChangeUndo)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	defer w.rollback()
	d := w.d
	if _, ok := d.sets[rev]; !ok {
		return workspace.ChangeSet{}, nil, workspace.ErrNotFound
	}
	w.undoes = rev
	plan := d.undoPlan(rev)
	for _, it := range plan {
		n, err := d.get(it.id)
		if err != nil {
			return workspace.ChangeSet{}, nil, err
		}
		if err := w.checkWrite(n); err != nil {
			return workspace.ChangeSet{}, nil, err
		}
		// A node that changes parent, name or tombstone edits its parents.
		if n.Parent == it.target.Parent && n.Name == it.target.Name && n.Deleted == it.target.Deleted {
			continue
		}
		for _, pid := range []int64{n.Parent, it.target.Parent} {
			if pid == 0 {
				continue
			}
			par, err := d.get(pid)
			if errors.Is(err, workspace.ErrNotFound) {
				continue
			}
			if err != nil {
				return workspace.ChangeSet{}, nil, err
			}
			if err := w.checkWrite(par); err != nil {
				return workspace.ChangeSet{}, nil, err
			}
		}
	}
	var conflicts []workspace.Conflict
	for _, it := range plan {
		cur, err := d.get(it.id)
		if err != nil {
			return workspace.ChangeSet{}, nil, err
		}
		p := d.pathOf(it.id)
		changed := !sameState(cur.state, it.expected)
		if !changed && it.target.Deleted && !cur.Deleted && cur.Kind == workspace.KindDir {
			// A folder this change set created is not the folder it left
			// once it has live children.
			changed = len(d.children(it.id)) > 0
		}
		if changed {
			conflicts = append(conflicts, workspace.Conflict{Path: p, Reason: workspace.ReasonChanged})
			continue
		}
		if sameState(cur.state, it.target) {
			continue
		}
		if !it.target.Deleted {
			par, err := d.get(it.target.Parent)
			if err != nil && !errors.Is(err, workspace.ErrNotFound) {
				return workspace.ChangeSet{}, nil, err
			}
			if err != nil || par.Deleted {
				conflicts = append(conflicts, workspace.Conflict{Path: p, Reason: workspace.ReasonParentDeleted})
				continue
			}
			if cur.Parent != it.target.Parent && slices.Contains(d.ancestors(it.target.Parent), it.id) {
				conflicts = append(conflicts, workspace.Conflict{Path: p, Reason: workspace.ReasonChanged})
				continue
			}
			if other, taken := d.childByFold(it.target.Parent, it.target.Name); taken && other.ID != it.id {
				conflicts = append(conflicts, workspace.Conflict{Path: p, Reason: workspace.ReasonNameTaken})
				continue
			}
		}
		if _, err := w.apply(it.id, cur.Kind, undoOp(cur.state, it.target), it.target); err != nil {
			return workspace.ChangeSet{}, nil, err
		}
	}
	if len(conflicts) > 0 && !o.SkipConflicts {
		return workspace.ChangeSet{}, conflicts, workspace.ErrConflict
	}
	cs, err := w.commit()
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	return cs, conflicts, nil
}
