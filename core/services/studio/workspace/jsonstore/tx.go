package jsonstore

import (
	"context"
	"io"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// localTx is an open change set. It holds the write lock of the store until
// Commit or Rollback, so a caller must not call the store itself in between.
type localTx struct {
	w *writer
}

func (s *Store) Begin(ctx context.Context, a workspace.Actor, title string) (workspace.Tx, error) {
	w, err := s.beginWrite(ctx, a, title, s.kindFor(a))
	if err != nil {
		return nil, err
	}
	return &localTx{w: w}, nil
}

func (t *localTx) node(n nodeRow) workspace.Node { return t.w.d.toNode(n) }

func (t *localTx) Put(ctx context.Context, p string, r io.Reader, o workspace.PutOptions) (workspace.Node, error) {
	if t.w.done {
		return workspace.Node{}, workspace.ErrInvalid
	}
	segs, err := workspace.SplitPath(p)
	if err != nil {
		return workspace.Node{}, err
	}
	if len(segs) == 0 {
		return workspace.Node{}, workspace.ErrInvalid
	}
	if err := t.w.s.precheckPut(t.w.d, t.w.actor, segs); err != nil {
		return workspace.Node{}, err
	}
	sum, size, err := t.w.s.blobs.Put(ctx, r, t.w.s.limit(o.MaxSize))
	if err != nil {
		return workspace.Node{}, err
	}
	var n nodeRow
	var asked string
	if err := t.w.step(func() error {
		var err error
		n, asked, err = t.w.put(segs, sum, size, o)
		return err
	}); err != nil {
		return workspace.Node{}, err
	}
	node := t.node(n)
	node.ConflictOf = asked
	return node, nil
}

func (t *localTx) Mkdir(_ context.Context, p string) (workspace.Node, error) {
	segs, err := workspace.SplitPath(p)
	if err != nil {
		return workspace.Node{}, err
	}
	var n nodeRow
	if err := t.w.step(func() error {
		var err error
		n, err = t.w.mkdir(segs)
		return err
	}); err != nil {
		return workspace.Node{}, err
	}
	return t.node(n), nil
}

func (t *localTx) Commit() (workspace.ChangeSet, error) { return t.w.commit() }

func (t *localTx) Rollback() error {
	t.w.rollback()
	return nil
}

func (t *localTx) Move(_ context.Context, from, to string) (workspace.Node, error) {
	var n nodeRow
	if err := t.w.step(func() error {
		var err error
		n, err = t.w.move(from, to)
		return err
	}); err != nil {
		return workspace.Node{}, err
	}
	return t.node(n), nil
}

func (t *localTx) Delete(_ context.Context, p string) error {
	return t.w.step(func() error { return t.w.delete(p) })
}
