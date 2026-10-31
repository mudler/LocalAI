package jsonstore

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// MaterializeRoot builds the view in dir: one folder per mount, named by its At,
// with folders as real folders and files as hard links to their blobs. dir
// must be empty. It returns the revision it used.
func (s *Store) MaterializeRoot(ctx context.Context, v workspace.View, dir *os.Root) (int64, error) {
	pts, err := workspace.CheckMountPoints(v)
	if err != nil {
		return 0, err
	}
	if err := workspace.CheckEmptyRoot(dir); err != nil {
		return 0, err
	}
	// One read lock covers the whole tree, so the revision is consistent. The
	// files are linked after it ends: linking can copy from an object store,
	// and a lock held for that long would make writers wait.
	type item struct {
		path, blob string
		dir        bool
	}
	var items []item
	var base int64
	err = s.readLocked(ctx, func(d *data) error {
		base = d.rev
		for i, m := range v.Mounts {
			if m.Mode < workspace.ModeRead {
				continue
			}
			top, err := d.get(m.NodeID)
			if err != nil {
				return err
			}
			if top.Deleted || top.Kind != workspace.KindDir || s.access(d, v.Actor, top.ID) < workspace.ModeRead {
				return workspace.ErrNotFound
			}
			mp := filepath.FromSlash(pts[i])
			if !filepath.IsLocal(mp) {
				return workspace.ErrInvalid
			}
			hostPath := map[int64]string{top.ID: mp}
			items = append(items, item{path: mp, dir: true})
			for _, n := range d.subtree(top.ID, false) {
				if n.ID == top.ID {
					continue
				}
				hp := filepath.Join(hostPath[n.Parent], n.Name)
				hostPath[n.ID] = hp
				items = append(items, item{path: hp, blob: n.Blob, dir: n.Kind == workspace.KindDir})
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		if it.dir {
			if err := dir.MkdirAll(it.path, 0o755); err != nil { // #nosec G301 -- the materialized tree is read by the sandbox process
				return 0, err
			}
			continue
		}
		if err := s.blobs.LinkTo(ctx, it.blob, dir, it.path); err != nil {
			return 0, err
		}
	}
	return base, nil
}

// Materialize builds the view in the folder dir, which is made when missing.
func (s *Store) Materialize(ctx context.Context, v workspace.View, dir string) (int64, error) {
	return workspace.MaterializeAt(ctx, s, v, dir)
}
