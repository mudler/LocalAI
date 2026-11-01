package jsonstore

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// moveNode moves src to the path to. It checks write access on the target
// folder; the caller checks it on src.
func (w *writer) moveNode(src nodeRow, to []string) (nodeRow, error) {
	if src.ID == rootID || len(to) == 0 {
		return nodeRow{}, workspace.ErrInvalid
	}
	dir, name := to[:len(to)-1], to[len(to)-1]
	parent, err := w.parentForWrite(dir)
	if err != nil {
		return nodeRow{}, err
	}
	if slices.Contains(w.d.ancestors(parent.ID), src.ID) {
		return nodeRow{}, workspace.ErrInvalid
	}
	if other, taken := w.d.childByFold(parent.ID, name); taken && other.ID != src.ID {
		return nodeRow{}, workspace.ErrExists
	}
	if parent.ID == src.Parent && name == src.Name {
		return src, nil
	}
	ns := src.state
	ns.Parent, ns.Name = parent.ID, name
	return w.apply(src.ID, src.Kind, workspace.OpMove, ns)
}

// checkParentWrite requires write access on the parent of n: changing a
// directory entry needs write on the folder that holds it.
func (w *writer) checkParentWrite(n nodeRow) error {
	if n.Parent == 0 {
		return nil
	}
	parent, err := w.d.get(n.Parent)
	if err != nil {
		return err
	}
	return w.checkWrite(parent)
}

func (w *writer) move(from, to string) (nodeRow, error) {
	src, err := w.s.find(w.d, w.actor, from, workspace.ModeWrite)
	if err != nil {
		return nodeRow{}, err
	}
	if err := w.checkParentWrite(src); err != nil {
		return nodeRow{}, err
	}
	segs, err := workspace.SplitPath(to)
	if err != nil {
		return nodeRow{}, err
	}
	return w.moveNode(src, segs)
}

// deleteNode sets the tombstone on n and its live descendants, deepest first.
func (w *writer) deleteNode(n nodeRow) error {
	if n.ID == rootID {
		return workspace.ErrInvalid
	}
	for _, r := range w.d.subtree(n.ID, true) {
		ns := r.state
		ns.Deleted = true
		if _, err := w.apply(r.ID, r.Kind, workspace.OpDelete, ns); err != nil {
			return err
		}
	}
	return nil
}

func (w *writer) delete(p string) error {
	n, err := w.s.find(w.d, w.actor, p, workspace.ModeWrite)
	if err != nil {
		return err
	}
	if err := w.checkParentWrite(n); err != nil {
		return err
	}
	return w.deleteNode(n)
}

func (s *Store) Move(ctx context.Context, a workspace.Actor, from, to string) (workspace.Node, error) {
	w, err := s.beginWrite(ctx, a, "Move "+from+" to "+to, s.kindFor(a))
	if err != nil {
		return workspace.Node{}, err
	}
	defer w.rollback()
	n, err := w.move(from, to)
	if err != nil {
		return workspace.Node{}, err
	}
	node := w.d.toNode(n)
	if _, err := w.commit(); err != nil {
		return workspace.Node{}, err
	}
	return node, nil
}

func (s *Store) Delete(ctx context.Context, a workspace.Actor, p string) error {
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
