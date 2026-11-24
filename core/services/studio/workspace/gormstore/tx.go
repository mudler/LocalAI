package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"io"
)

type localTx struct {
	w *writer
}

func (s *Workspace) Begin(ctx context.Context, a Actor, title string) (Tx, error) {
	w, err := s.beginWrite(ctx, a, title, s.kindFor(a))
	if err != nil {
		return nil, err
	}
	return &localTx{w: w}, nil
}

func (t *localTx) node(n nodeRow) (Node, error) {
	return t.w.s.toNode(t.w.ctx, t.w.tx, n)
}

func (t *localTx) Put(ctx context.Context, p string, r io.Reader, o PutOptions) (Node, error) {
	if t.w.done {
		return Node{}, ErrInvalid
	}
	segs, err := splitPath(p)
	if err != nil {
		return Node{}, err
	}
	if len(segs) == 0 {
		return Node{}, ErrInvalid
	}
	if err := t.w.s.precheckPut(t.w.ctx, t.w.tx, t.w.actor, segs); err != nil {
		return Node{}, err
	}
	sum, size, err := t.w.s.blobs.Put(ctx, r, t.w.s.limit(o.MaxSize))
	if err != nil {
		return Node{}, err
	}
	var n nodeRow
	var asked string
	if err := t.w.step(func() error {
		var err error
		n, asked, err = t.w.put(segs, sum, size, o)
		return err
	}); err != nil {
		return Node{}, err
	}
	node, err := t.node(n)
	node.ConflictOf = asked
	return node, err
}

func (t *localTx) Mkdir(ctx context.Context, p string) (Node, error) {
	segs, err := splitPath(p)
	if err != nil {
		return Node{}, err
	}
	var n nodeRow
	if err := t.w.step(func() error {
		var err error
		n, err = t.w.mkdir(segs)
		return err
	}); err != nil {
		return Node{}, err
	}
	return t.node(n)
}

func (t *localTx) Commit() (ChangeSet, error) { return t.w.commit() }

func (t *localTx) Rollback() error {
	t.w.rollback()
	return nil
}

func (t *localTx) Move(ctx context.Context, from, to string) (Node, error) {
	var n nodeRow
	if err := t.w.step(func() error {
		var err error
		n, err = t.w.move(from, to)
		return err
	}); err != nil {
		return Node{}, err
	}
	return t.node(n)
}

func (t *localTx) Delete(ctx context.Context, p string) error {
	return t.w.step(func() error { return t.w.delete(p) })
}
