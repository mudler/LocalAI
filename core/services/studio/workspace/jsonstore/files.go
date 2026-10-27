package jsonstore

import (
	"context"
	"io"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// precheckPut refuses a Put before any bytes are stored, when the actor has
// no write access where the file would go.
func (s *Store) precheckPut(d *data, a workspace.Actor, segs []string) error {
	dir, name := segs[:len(segs)-1], segs[len(segs)-1]
	anc, covered, blocked := d.deepestDir(dir)
	target := anc
	if !blocked && covered == len(dir) {
		if ex, ok := d.childByName(anc.ID, name); ok {
			target = ex
		}
	}
	return s.needWrite(d, a, target)
}

func (s *Store) Put(ctx context.Context, a workspace.Actor, p string, r io.Reader, o workspace.PutOptions) (workspace.Node, error) {
	segs, err := workspace.SplitPath(p)
	if err != nil {
		return workspace.Node{}, err
	}
	if len(segs) == 0 {
		return workspace.Node{}, workspace.ErrInvalid
	}
	if err := s.readLocked(ctx, func(d *data) error { return s.precheckPut(d, a, segs) }); err != nil {
		return workspace.Node{}, err
	}
	sum, size, err := s.blobs.Put(ctx, r, s.limit(o.MaxSize))
	if err != nil {
		return workspace.Node{}, err
	}
	w, err := s.beginWrite(ctx, a, "Upload "+segs[len(segs)-1], workspace.ChangeUpload)
	if err != nil {
		return workspace.Node{}, err
	}
	defer w.rollback()
	n, asked, err := w.put(segs, sum, size, o)
	if err != nil {
		return workspace.Node{}, err
	}
	node := w.d.toNode(n)
	if _, err := w.commit(); err != nil {
		return workspace.Node{}, err
	}
	node.ConflictOf = asked
	return node, nil
}

func (s *Store) Mkdir(ctx context.Context, a workspace.Actor, p string) (workspace.Node, error) {
	segs, err := workspace.SplitPath(p)
	if err != nil {
		return workspace.Node{}, err
	}
	title := "Create folder /"
	if len(segs) > 0 {
		title = "Create folder " + segs[len(segs)-1]
	}
	w, err := s.beginWrite(ctx, a, title, s.kindFor(a))
	if err != nil {
		return workspace.Node{}, err
	}
	defer w.rollback()
	n, err := w.mkdir(segs)
	if err != nil {
		return workspace.Node{}, err
	}
	node := w.d.toNode(n)
	if _, err := w.commit(); err != nil {
		return workspace.Node{}, err
	}
	return node, nil
}

func (s *Store) Open(ctx context.Context, a workspace.Actor, p string) (io.ReadSeekCloser, workspace.Node, error) {
	var n nodeRow
	var node workspace.Node
	err := s.readLocked(ctx, func(d *data) error {
		var err error
		if n, err = s.find(d, a, p, workspace.ModeRead); err != nil {
			return err
		}
		if n.Kind != workspace.KindFile {
			return workspace.ErrInvalid
		}
		node = d.toNode(n)
		return nil
	})
	if err != nil {
		return nil, workspace.Node{}, err
	}
	f, err := s.blobs.Open(ctx, n.Blob)
	if err != nil {
		return nil, workspace.Node{}, err
	}
	return f, node, nil
}

func (s *Store) Stat(ctx context.Context, a workspace.Actor, p string) (workspace.Node, error) {
	var node workspace.Node
	err := s.readLocked(ctx, func(d *data) error {
		n, err := s.find(d, a, p, workspace.ModeRead)
		if err != nil {
			return err
		}
		node = d.toNode(n)
		return nil
	})
	return node, err
}

func (s *Store) List(ctx context.Context, a workspace.Actor, p string) ([]workspace.Node, error) {
	var out []workspace.Node
	err := s.readLocked(ctx, func(d *data) error {
		n, err := s.find(d, a, p, workspace.ModeRead)
		if err != nil {
			return err
		}
		if n.Kind != workspace.KindDir {
			return workspace.ErrInvalid
		}
		kids := d.children(n.ID)
		out = make([]workspace.Node, 0, len(kids))
		for _, k := range kids {
			out = append(out, d.toNode(k))
		}
		return nil
	})
	return out, err
}
