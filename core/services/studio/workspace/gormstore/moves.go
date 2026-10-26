package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"errors"
)

// moveNode moves src to the path to. It checks write access on the target
// folder; the caller checks it on src.
func (w *writer) moveNode(src nodeRow, to []string) (nodeRow, error) {
	if src.id == rootID || len(to) == 0 {
		return nodeRow{}, ErrInvalid
	}
	dir, name := to[:len(to)-1], to[len(to)-1]
	parent, err := w.parentForWrite(dir)
	if err != nil {
		return nodeRow{}, err
	}
	ancs, err := ancestors(w.ctx, w.tx, parent.id)
	if err != nil {
		return nodeRow{}, err
	}
	for _, id := range ancs {
		if id == src.id {
			return nodeRow{}, ErrInvalid
		}
	}
	if other, taken, err := childByFold(w.ctx, w.tx, parent.id, name); err != nil {
		return nodeRow{}, err
	} else if taken && other.id != src.id {
		return nodeRow{}, ErrExists
	}
	if parent.id == src.parent && name == src.name {
		return src, nil
	}
	ns := src.state
	ns.parent, ns.name = parent.id, name
	return w.apply(src.id, src.kind, OpMove, ns)
}

// checkParentWrite requires write access on the parent of n: changing a
// directory entry needs write on the folder that holds it.
func (w *writer) checkParentWrite(n nodeRow) error {
	if n.parent == 0 {
		return nil
	}
	parent, err := getNode(w.ctx, w.tx, n.parent)
	if err != nil {
		return err
	}
	return w.checkWrite(parent)
}

func (w *writer) move(from, to string) (nodeRow, error) {
	src, err := w.s.find(w.ctx, w.tx, w.actor, from, ModeWrite)
	if err != nil {
		return nodeRow{}, err
	}
	if err := w.checkParentWrite(src); err != nil {
		return nodeRow{}, err
	}
	segs, err := splitPath(to)
	if err != nil {
		return nodeRow{}, err
	}
	return w.moveNode(src, segs)
}

// deleteNode sets the tombstone on n and its live descendants, deepest first.
func (w *writer) deleteNode(n nodeRow) error {
	if n.id == rootID {
		return ErrInvalid
	}
	rows, err := subtree(w.ctx, w.tx, n.id, true)
	if err != nil {
		return err
	}
	for _, r := range rows {
		ns := r.state
		ns.deleted = true
		if _, err := w.apply(r.id, r.kind, OpDelete, ns); err != nil {
			return err
		}
	}
	return nil
}

func (w *writer) delete(p string) error {
	n, err := w.s.find(w.ctx, w.tx, w.actor, p, ModeWrite)
	if err != nil {
		return err
	}
	if err := w.checkParentWrite(n); err != nil {
		return err
	}
	return w.deleteNode(n)
}

func (s *Workspace) Move(ctx context.Context, a Actor, from, to string) (Node, error) {
	w, err := s.beginWrite(ctx, a, "Move "+from+" to "+to, s.kindFor(a))
	if err != nil {
		return Node{}, err
	}
	defer w.rollback()
	n, err := w.move(from, to)
	if err != nil {
		return Node{}, err
	}
	node, err := s.toNode(ctx, w.tx, n)
	if err != nil {
		return Node{}, err
	}
	if _, err := w.commit(); err != nil {
		if errors.Is(err, ErrCommitted) {
			return node, err // the write is durable
		}
		return Node{}, err
	}
	return node, nil
}

func (s *Workspace) Delete(ctx context.Context, a Actor, p string) error {
	w, err := s.beginWrite(ctx, a, "Delete "+p, s.kindFor(a))
	if err != nil {
		return err
	}
	defer w.rollback()
	if err := w.delete(p); err != nil {
		return err
	}
	_, err = w.commit()
	return err
}
