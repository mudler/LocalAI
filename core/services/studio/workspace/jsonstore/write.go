package jsonstore

import (
	"context"
	"slices"
	"sort"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// writer is one change set in progress. It holds the write lock of the store
// from beginWrite until commit or rollback, so change sets never overlap and
// the revision it takes is its own.
type writer struct {
	s      *Store
	d      *data
	actor  workspace.Actor
	title  string
	kind   workspace.ChangeSetKind
	rev    int64
	undoes int64
	ops    int
	done   bool
	start  int // journal length when the change set began
}

func (s *Store) beginWrite(ctx context.Context, a workspace.Actor, title string, kind workspace.ChangeSetKind) (*writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.d.beginTx()
	return &writer{s: s, d: s.d, actor: a, title: title, kind: kind, rev: s.d.rev + 1, start: len(s.d.journal)}, nil
}

// rollback drops the change set and releases the lock. It is safe to call
// after commit.
func (w *writer) rollback() {
	if w.done {
		return
	}
	w.done = true
	w.d.rollbackTo(mark{journal: w.start})
	w.d.endTx()
	w.s.mu.Unlock()
}

// commit writes the change set. A writer without operations commits nothing
// and returns a zero ChangeSet.
func (w *writer) commit() (workspace.ChangeSet, error) {
	if w.done {
		return workspace.ChangeSet{}, workspace.ErrInvalid
	}
	if w.ops == 0 {
		w.rollback()
		return workspace.ChangeSet{}, nil
	}
	d := w.d
	set := changeRec{
		Rev: w.rev, Actor: w.actor.Principal, ChatID: w.actor.ChatID, Title: w.title,
		Kind: w.kind, Undoes: w.undoes, CreatedAt: w.s.now().UnixMilli(),
	}
	rec := record{Set: &set, Journal: slices.Clone(d.journal[w.start:])}
	for id := range d.dirty {
		rec.Nodes = append(rec.Nodes, *d.nodes[id])
	}
	sort.Slice(rec.Nodes, func(i, j int) bool { return rec.Nodes[i].ID < rec.Nodes[j].ID })
	if err := w.s.persist(rec); err != nil {
		w.rollback()
		return workspace.ChangeSet{}, err
	}
	d.addSet(set)
	w.s.maybeCompact()
	cs := d.changeSet(w.rev)
	w.done = true
	d.endTx()
	w.s.mu.Unlock()
	return cs, nil
}

// step runs fn as a unit. When fn fails, its changes are undone and the
// writer stays usable.
func (w *writer) step(fn func() error) error {
	if w.done {
		return workspace.ErrInvalid
	}
	m, ops := w.d.mark(), w.ops
	if err := fn(); err != nil {
		w.ops = ops
		w.d.rollbackTo(m)
		return err
	}
	return nil
}

// apply moves node id (0 for a new node) to the state ns and journals the
// operation with the full state before and after.
func (w *writer) apply(id int64, kind workspace.Kind, op workspace.OpKind, ns state) (nodeRow, error) {
	d := w.d
	before := state{Deleted: true}
	beforePath := ""
	var n nodeRow
	if id != 0 {
		cur, err := d.get(id)
		if err != nil {
			return nodeRow{}, err
		}
		before, beforePath = cur.state, d.pathOf(id)
		n = cur
		n.Version++
	} else {
		id = d.nextID
		n = nodeRow{ID: id, Kind: kind, Version: 1}
	}
	n.state, n.Rev = ns, w.rev
	if err := d.setNode(n); err != nil {
		return nodeRow{}, err
	}
	d.appendJournal(journalRow{
		Rev: w.rev, Node: id, Op: op,
		Old: side{Parent: before.Parent, Name: before.Name, Path: beforePath, Blob: before.Blob, Size: before.Size, Deleted: before.Deleted},
		New: side{Parent: ns.Parent, Name: ns.Name, Path: d.pathOf(id), Blob: ns.Blob, Size: ns.Size, Deleted: ns.Deleted},
	})
	w.ops++
	return d.get(id)
}

func (w *writer) checkWrite(n nodeRow) error {
	return w.s.needWrite(w.d, w.actor, n)
}

// ensureDirs creates the missing folders of segs and returns the last one.
// The caller checks access first.
func (w *writer) ensureDirs(segs []string) (nodeRow, error) {
	n, covered, blocked := w.d.deepestDir(segs)
	if blocked {
		return nodeRow{}, workspace.ErrExists
	}
	for _, s := range segs[covered:] {
		if _, taken := w.d.childByFold(n.ID, s); taken {
			return nodeRow{}, workspace.ErrExists
		}
		var err error
		if n, err = w.apply(0, workspace.KindDir, workspace.OpCreate, state{Parent: n.ID, Name: s}); err != nil {
			return nodeRow{}, err
		}
	}
	return n, nil
}

// parentForWrite checks write access on the deepest existing folder of dir
// and creates the missing folders.
func (w *writer) parentForWrite(dir []string) (nodeRow, error) {
	anc, _, blocked := w.d.deepestDir(dir)
	if err := w.checkWrite(anc); err != nil {
		return nodeRow{}, err
	}
	if blocked {
		return nodeRow{}, workspace.ErrExists
	}
	return w.ensureDirs(dir)
}

// put writes a blob at segs. It returns the node and, for a conflict copy,
// the path the caller asked for.
func (w *writer) put(segs []string, sum string, size int64, o workspace.PutOptions) (nodeRow, string, error) {
	if len(segs) == 0 {
		return nodeRow{}, "", workspace.ErrInvalid
	}
	dir, name := segs[:len(segs)-1], segs[len(segs)-1]
	anc, covered, blocked := w.d.deepestDir(dir)
	if !blocked && covered == len(dir) {
		if ex, ok := w.d.childByName(anc.ID, name); ok {
			return w.overwrite(ex, sum, size, o)
		}
	}
	parent, err := w.parentForWrite(dir)
	if err != nil {
		return nodeRow{}, "", err
	}
	if _, taken := w.d.childByFold(parent.ID, name); taken {
		return nodeRow{}, "", workspace.ErrExists
	}
	n, err := w.apply(0, workspace.KindFile, workspace.OpCreate, state{Parent: parent.ID, Name: name, Blob: sum, Size: size})
	return n, "", err
}

func (w *writer) overwrite(ex nodeRow, sum string, size int64, o workspace.PutOptions) (nodeRow, string, error) {
	if err := w.checkWrite(ex); err != nil {
		return nodeRow{}, "", err
	}
	if ex.Kind != workspace.KindFile {
		return nodeRow{}, "", workspace.ErrExists
	}
	if ex.Blob == sum {
		return ex, "", nil
	}
	if o.Overwrite || o.BaseVersion == ex.Version {
		ns := ex.state
		ns.Blob, ns.Size = sum, size
		n, err := w.apply(ex.ID, workspace.KindFile, workspace.OpWrite, ns)
		return n, "", err
	}
	n, err := w.conflictCopy(ex.Parent, ex.Name, sum, size)
	if err != nil {
		return nodeRow{}, "", err
	}
	return n, w.d.pathOf(ex.ID), nil
}

// conflictCopy stores a blob next to name under a free conflict name.
func (w *writer) conflictCopy(parent int64, name, sum string, size int64) (nodeRow, error) {
	for i := 1; i < 1000; i++ {
		cand, err := workspace.CleanName(workspace.ConflictName(name, w.actor.Label(), i))
		if err != nil {
			return nodeRow{}, err
		}
		if _, taken := w.d.childByFold(parent, cand); !taken {
			return w.apply(0, workspace.KindFile, workspace.OpCreate, state{Parent: parent, Name: cand, Blob: sum, Size: size})
		}
	}
	return nodeRow{}, workspace.ErrExists
}

func (w *writer) mkdir(segs []string) (nodeRow, error) {
	if len(segs) == 0 {
		root, err := w.d.get(rootID)
		if err != nil {
			return nodeRow{}, err
		}
		if err := w.checkWrite(root); err != nil {
			return nodeRow{}, err
		}
		return nodeRow{}, workspace.ErrExists
	}
	anc, covered, blocked := w.d.deepestDir(segs)
	if blocked || covered == len(segs) {
		if err := w.checkWrite(anc); err != nil {
			return nodeRow{}, err
		}
		return nodeRow{}, workspace.ErrExists
	}
	return w.parentForWrite(segs)
}

// changeSet builds the change set of one revision, with its operations.
func (d *data) changeSet(rev int64) workspace.ChangeSet {
	rec := d.sets[rev]
	cs := workspace.ChangeSet{
		Rev: rec.Rev, Actor: rec.Actor, ChatID: rec.ChatID, Title: rec.Title,
		Kind: rec.Kind, Undoes: rec.Undoes, CreatedAt: msTime(rec.CreatedAt),
	}
	for _, i := range d.byRev[rev] {
		r := d.journal[i]
		cs.Ops = append(cs.Ops, workspace.Op{NodeID: r.Node, Kind: r.Op, OldPath: r.Old.Path, NewPath: r.New.Path, OldBlob: r.Old.Blob, NewBlob: r.New.Blob})
	}
	return cs
}
