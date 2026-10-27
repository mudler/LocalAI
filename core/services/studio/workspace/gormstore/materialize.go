package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"os"
	"path/filepath"
	"strings"
)

// mountPoint validates a mount's At: a relative path of clean names with no
// empty, dot or dot-dot segment. It returns the cleaned slash path.
func mountPoint(at string) (string, error) {
	if strings.HasPrefix(at, "/") || strings.HasSuffix(at, "/") {
		return "", ErrInvalid
	}
	parts := strings.Split(at, "/")
	for i, p := range parts {
		n, err := cleanName(p)
		if err != nil || n != p {
			return "", ErrInvalid
		}
		parts[i] = n
	}
	return strings.Join(parts, "/"), nil
}

// checkMountPoints validates every readable mount and rejects duplicate and
// nested points. It returns the cleaned points by mount index.
func checkMountPoints(v View) (map[int]string, error) {
	pts := map[int]string{}
	for i, m := range v.Mounts {
		if m.Mode < ModeRead {
			continue
		}
		p, err := mountPoint(m.At)
		if err != nil {
			return nil, err
		}
		for _, q := range pts {
			if p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
				return nil, ErrInvalid
			}
		}
		pts[i] = p
	}
	return pts, nil
}

func (s *Workspace) currentRev(ctx context.Context, q querier) (int64, error) {
	var rev int64
	err := q.QueryRowContext(ctx, `SELECT CAST(value AS BIGINT) FROM studio_ws_meta WHERE key = 'rev'`).Scan(&rev)
	return rev, err
}

// Materialize builds the view in dir: one folder per mount, named by its At,
// with folders as real folders and files as hard links to their blobs. dir
// must be missing or empty. It returns the revision it used.
func (s *Workspace) Materialize(ctx context.Context, v View, dir string) (int64, error) {
	pts, err := checkMountPoints(v)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- the materialized tree is read by the sandbox process
		return 0, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	if len(ents) > 0 {
		return 0, ErrExists
	}
	// One transaction reads the whole tree, so the revision is consistent. The
	// files are linked after it ends: linking can copy from an object store, and
	// a transaction held for that long would make writers wait.
	type item struct {
		path, blob string
		dir        bool
	}
	var items []item
	base, err := func() (int64, error) {
		tx, err := s.db.BeginTx(ctx)
		if err != nil {
			return 0, err
		}
		defer tx.Rollback()
		base, err := s.currentRev(ctx, tx)
		if err != nil {
			return 0, err
		}
		for i, m := range v.Mounts {
			if m.Mode < ModeRead {
				continue
			}
			top, err := getNode(ctx, tx, m.NodeID)
			if err != nil {
				return 0, err
			}
			if top.deleted || top.kind != KindDir {
				return 0, ErrNotFound
			}
			if mode, err := s.access(ctx, tx, v.Actor, top.id); err != nil {
				return 0, err
			} else if mode < ModeRead {
				return 0, ErrNotFound
			}
			rows, err := subtree(ctx, tx, top.id, false)
			if err != nil {
				return 0, err
			}
			mp := filepath.Join(dir, filepath.FromSlash(pts[i]))
			if rel, err := filepath.Rel(dir, mp); err != nil || !filepath.IsLocal(rel) {
				return 0, ErrInvalid
			}
			hostPath := map[int64]string{top.id: mp}
			items = append(items, item{path: mp, dir: true})
			for _, n := range rows {
				if n.id == top.id {
					continue
				}
				hp := filepath.Join(hostPath[n.parent], n.name)
				hostPath[n.id] = hp
				items = append(items, item{path: hp, blob: n.blob, dir: n.kind == KindDir})
			}
		}
		return base, nil
	}()
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		if it.dir {
			if err := os.MkdirAll(it.path, 0o755); err != nil { // #nosec G301 -- the materialized tree is read by the sandbox process
				return 0, err
			}
			continue
		}
		if err := s.blobs.LinkTo(ctx, it.blob, it.path); err != nil {
			return 0, err
		}
	}
	return base, nil
}
