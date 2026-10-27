package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"errors"
	"io"
)

// precheckPut refuses a Put before any bytes are stored, when the actor has
// no write access where the file would go. q is the database, or the open
// transaction when the caller holds one.
func (s *Workspace) precheckPut(ctx context.Context, q querier, a Actor, segs []string) error {
	dir, name := segs[:len(segs)-1], segs[len(segs)-1]
	anc, covered, blocked, err := deepestDir(ctx, q, dir)
	if err != nil {
		return err
	}
	target := anc
	if !blocked && covered == len(dir) {
		if ex, ok, err := childByName(ctx, q, anc.id, name); err != nil {
			return err
		} else if ok {
			target = ex
		}
	}
	return s.needWrite(ctx, q, a, target)
}

func (s *Workspace) Put(ctx context.Context, a Actor, p string, r io.Reader, o PutOptions) (Node, error) {
	segs, err := splitPath(p)
	if err != nil {
		return Node{}, err
	}
	if len(segs) == 0 {
		return Node{}, ErrInvalid
	}
	if err := s.precheckPut(ctx, s.db, a, segs); err != nil {
		return Node{}, err
	}
	sum, size, err := s.blobs.Put(ctx, r, s.limit(o.MaxSize))
	if err != nil {
		return Node{}, err
	}
	title := "Upload " + segs[len(segs)-1]
	if o.Title != "" {
		title = o.Title
	}
	w, err := s.beginWrite(ctx, a, title, ChangeUpload)
	if err != nil {
		return Node{}, err
	}
	defer w.rollback()
	n, asked, err := w.put(segs, sum, size, o)
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
	node.ConflictOf = asked
	return node, nil
}

func (s *Workspace) Mkdir(ctx context.Context, a Actor, p string) (Node, error) {
	segs, err := splitPath(p)
	if err != nil {
		return Node{}, err
	}
	title := "Create folder /"
	if len(segs) > 0 {
		title = "Create folder " + segs[len(segs)-1]
	}
	w, err := s.beginWrite(ctx, a, title, s.kindFor(a))
	if err != nil {
		return Node{}, err
	}
	defer w.rollback()
	n, err := w.mkdir(segs)
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

func (s *Workspace) Open(ctx context.Context, a Actor, p string) (io.ReadSeekCloser, Node, error) {
	n, err := s.find(ctx, s.db, a, p, ModeRead)
	if err != nil {
		return nil, Node{}, err
	}
	if n.kind != KindFile {
		return nil, Node{}, ErrInvalid
	}
	node, err := s.toNode(ctx, s.db, n)
	if err != nil {
		return nil, Node{}, err
	}
	f, err := s.blobs.Open(ctx, n.blob)
	if err != nil {
		return nil, Node{}, err
	}
	return f, node, nil
}

func (s *Workspace) Stat(ctx context.Context, a Actor, p string) (Node, error) {
	n, err := s.find(ctx, s.db, a, p, ModeRead)
	if err != nil {
		return Node{}, err
	}
	return s.toNode(ctx, s.db, n)
}

func (s *Workspace) List(ctx context.Context, a Actor, p string) ([]Node, error) {
	n, err := s.find(ctx, s.db, a, p, ModeRead)
	if err != nil {
		return nil, err
	}
	if n.kind != KindDir {
		return nil, ErrInvalid
	}
	kids, err := children(ctx, s.db, n.id)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(kids))
	for _, k := range kids {
		node, err := s.toNode(ctx, s.db, k)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, nil
}
