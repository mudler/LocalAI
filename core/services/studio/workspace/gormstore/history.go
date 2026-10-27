package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// revs runs a query that returns one revision per row and reads all rows.
func (s *Workspace) revs(ctx context.Context, query string, args ...any) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var r int64
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// opParents holds the parent ids of one journal row.
type opParents struct{ old, new int64 }

// readableParent tells whether the actor can read the folder with id.
func (s *Workspace) readableParent(ctx context.Context, a Actor, id int64) (bool, error) {
	if id == 0 {
		return false, nil
	}
	m, err := s.access(ctx, s.db, a, id)
	if err != nil {
		return false, err
	}
	return m >= ModeRead, nil
}

// visibleOps keeps what the actor can see of the operations of rev. A path
// is visible when the actor can read its parent folder now. A move out of a
// hidden folder shows as a create, and a move into a hidden folder shows as
// a delete at the old path.
// When it drops or rewrites any operation, the title of the change set may
// name hidden paths, so the result also tells the caller to use a generic one.
func (s *Workspace) visibleOps(ctx context.Context, a Actor, rev int64, ops []Op) ([]Op, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(old_parent, 0), COALESCE(new_parent, 0) FROM studio_ws_journal WHERE rev = ? ORDER BY id`, rev)
	if err != nil {
		return nil, false, err
	}
	var ps []opParents
	for rows.Next() {
		var p opParents
		if err := rows.Scan(&p.old, &p.new); err != nil {
			rows.Close()
			return nil, false, err
		}
		ps = append(ps, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if len(ps) != len(ops) {
		return nil, false, ErrInvalid
	}
	var out []Op
	changed := false
	for i, op := range ops {
		oldOK, err := s.readableParent(ctx, a, ps[i].old)
		if err != nil {
			return nil, false, err
		}
		newOK, err := s.readableParent(ctx, a, ps[i].new)
		if err != nil {
			return nil, false, err
		}
		if (ps[i].old != 0 && !oldOK) || (ps[i].new != 0 && !newOK) {
			changed = true
		}
		switch {
		case !oldOK && !newOK:
			continue
		case !oldOK:
			op.OldPath, op.OldBlob = "", ""
			if op.Kind == OpMove {
				op.Kind = OpCreate
			}
		case !newOK:
			op.NewPath, op.NewBlob = op.OldPath, ""
			if op.Kind == OpMove {
				op.Kind = OpDelete
			}
		}
		out = append(out, op)
	}
	return out, changed, nil
}

// visible limits cs to what the actor can see.
func (s *Workspace) visible(ctx context.Context, a Actor, cs ChangeSet) (ChangeSet, error) {
	ops, changed, err := s.visibleOps(ctx, a, cs.Rev, cs.Ops)
	if err != nil {
		return ChangeSet{}, err
	}
	cs.Ops = ops
	if changed {
		cs.Title = GenericTitle(cs)
	}
	return cs, nil
}

func (s *Workspace) History(ctx context.Context, a Actor, hq HistoryQuery) ([]ChangeSet, error) {
	p := hq.Path
	if p == "" {
		p = "/"
	}
	n, err := s.find(ctx, s.db, a, p, ModeRead)
	if err != nil {
		return nil, err
	}
	np, err := pathOf(ctx, s.db, n.id)
	if err != nil {
		return nil, err
	}
	limit := hq.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	before := hq.BeforeRev
	if before <= 0 {
		before = math.MaxInt64
	}
	like := "/%"
	if np != "/" {
		like = escapeLike(np) + "/%"
	}
	revs, err := s.revs(ctx, `SELECT DISTINCT rev FROM studio_ws_journal
		WHERE rev < ? AND (old_path = ? OR new_path = ? OR old_path LIKE ? ESCAPE '\' OR new_path LIKE ? ESCAPE '\')
		ORDER BY rev DESC LIMIT ?`, before, np, np, like, like, limit)
	if err != nil {
		return nil, err
	}
	var out []ChangeSet
	for _, r := range revs {
		cs, err := s.loadChangeSet(ctx, s.db, r)
		if err != nil {
			return nil, err
		}
		if cs, err = s.visible(ctx, a, cs); err != nil {
			return nil, err
		}
		if len(cs.Ops) > 0 {
			out = append(out, cs)
		}
	}
	return out, nil
}

func (s *Workspace) ChangesSince(ctx context.Context, a Actor, rev int64, limit int) ([]ChangeSet, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var out []ChangeSet
	cursor := rev
	for len(out) < limit {
		batch, err := s.revs(ctx, `SELECT rev FROM studio_ws_changesets WHERE rev > ? ORDER BY rev LIMIT 200`, cursor)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			cursor = r
			cs, err := s.loadChangeSet(ctx, s.db, r)
			if err != nil {
				return nil, err
			}
			if cs, err = s.visible(ctx, a, cs); err != nil {
				return nil, err
			}
			if len(cs.Ops) == 0 {
				continue
			}
			out = append(out, cs)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
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
func undoPlan(ctx context.Context, q querier, rev int64) ([]undoItem, error) {
	rows, err := q.QueryContext(ctx, `SELECT node_id,
			COALESCE(old_parent, 0), old_name, old_blob, old_size, old_deleted,
			COALESCE(new_parent, 0), new_name, new_blob, new_size, new_deleted
		FROM studio_ws_journal WHERE rev = ? ORDER BY id`, rev)
	if err != nil {
		return nil, err
	}
	items := map[int64]*undoItem{}
	var order []int64
	for rows.Next() {
		var id int64
		var o, n state
		var od, nd int
		if err := rows.Scan(&id, &o.parent, &o.name, &o.blob, &o.size, &od, &n.parent, &n.name, &n.blob, &n.size, &nd); err != nil {
			rows.Close()
			return nil, err
		}
		o.deleted, n.deleted = od != 0, nd != 0
		it, ok := items[id]
		if !ok {
			it = &undoItem{id: id, target: o}
			items[id] = it
			order = append(order, id)
		}
		it.expected = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]undoItem, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		it := *items[order[i]]
		if it.target.deleted {
			// A node that did not exist: keep its location and blob, set the tombstone.
			t := it.expected
			t.deleted = true
			it.target = t
		}
		out = append(out, it)
	}
	return out, nil
}

func sameState(a, b state) bool {
	return a.parent == b.parent && a.name == b.name && a.blob == b.blob && a.deleted == b.deleted
}

func undoOp(cur, target state) OpKind {
	switch {
	case target.deleted && !cur.deleted:
		return OpDelete
	case !target.deleted && cur.deleted:
		return OpCreate
	case cur.parent != target.parent || cur.name != target.name:
		return OpMove
	default:
		return OpWrite
	}
}

func (s *Workspace) Undo(ctx context.Context, a Actor, rev int64, o UndoOptions) (ChangeSet, []Conflict, error) {
	if _, err := s.loadChangeSet(ctx, s.db, rev); err != nil {
		return ChangeSet{}, nil, err
	}
	w, err := s.beginWrite(ctx, a, fmt.Sprintf("Undo of revision %d", rev), ChangeUndo)
	if err != nil {
		return ChangeSet{}, nil, err
	}
	defer w.rollback()
	w.undoes = rev
	plan, err := undoPlan(ctx, w.tx, rev)
	if err != nil {
		return ChangeSet{}, nil, err
	}
	for _, it := range plan {
		n, err := getNode(ctx, w.tx, it.id)
		if err != nil {
			return ChangeSet{}, nil, err
		}
		if err := w.checkWrite(n); err != nil {
			return ChangeSet{}, nil, err
		}
		// A node that changes parent, name or tombstone edits its parents.
		if n.parent == it.target.parent && n.name == it.target.name && n.deleted == it.target.deleted {
			continue
		}
		for _, pid := range []int64{n.parent, it.target.parent} {
			if pid == 0 {
				continue
			}
			par, err := getNode(ctx, w.tx, pid)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return ChangeSet{}, nil, err
			}
			if err := w.checkWrite(par); err != nil {
				return ChangeSet{}, nil, err
			}
		}
	}
	var conflicts []Conflict
	for _, it := range plan {
		cur, err := getNode(ctx, w.tx, it.id)
		if err != nil {
			return ChangeSet{}, nil, err
		}
		p, err := pathOf(ctx, w.tx, it.id)
		if err != nil {
			return ChangeSet{}, nil, err
		}
		changed := !sameState(cur.state, it.expected)
		if !changed && it.target.deleted && !cur.deleted && cur.kind == KindDir {
			// A folder this change set created is not the folder it left
			// once it has live children.
			kids, err := children(ctx, w.tx, it.id)
			if err != nil {
				return ChangeSet{}, nil, err
			}
			changed = len(kids) > 0
		}
		if changed {
			conflicts = append(conflicts, Conflict{Path: p, Reason: ReasonChanged})
			continue
		}
		if sameState(cur.state, it.target) {
			continue
		}
		if !it.target.deleted {
			par, err := getNode(ctx, w.tx, it.target.parent)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return ChangeSet{}, nil, err
			}
			if err != nil || par.deleted {
				conflicts = append(conflicts, Conflict{Path: p, Reason: ReasonParentDeleted})
				continue
			}
			if cur.parent != it.target.parent {
				up, err := ancestors(ctx, w.tx, it.target.parent)
				if err != nil {
					return ChangeSet{}, nil, err
				}
				if slices.Contains(up, it.id) {
					conflicts = append(conflicts, Conflict{Path: p, Reason: ReasonChanged})
					continue
				}
			}
			if other, taken, err := childByFold(ctx, w.tx, it.target.parent, it.target.name); err != nil {
				return ChangeSet{}, nil, err
			} else if taken && other.id != it.id {
				conflicts = append(conflicts, Conflict{Path: p, Reason: ReasonNameTaken})
				continue
			}
		}
		if _, err := w.apply(it.id, cur.kind, undoOp(cur.state, it.target), it.target); err != nil {
			return ChangeSet{}, nil, err
		}
	}
	if len(conflicts) > 0 && !o.SkipConflicts {
		return ChangeSet{}, conflicts, ErrConflict
	}
	cs, err := w.commit()
	if err != nil {
		if errors.Is(err, ErrCommitted) {
			return cs, conflicts, err // the undo is durable
		}
		return ChangeSet{}, nil, err
	}
	return cs, conflicts, nil
}
