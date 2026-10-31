package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"sort"
	"strings"
)

func (s *Workspace) isAdmin(a Actor) bool {
	return s.admin != "" && a.Principal == s.admin
}

// Grant gives principal mode m on the folder p and everything below it.
func (s *Workspace) Grant(ctx context.Context, admin Actor, principal, p string, m Mode) error {
	if !s.isAdmin(admin) {
		return ErrNotAdmin
	}
	if principal == "" || (m != ModeRead && m != ModeWrite) {
		return ErrInvalid
	}
	n, err := lookupPath(ctx, s.db, p)
	if err != nil {
		return err
	}
	if n.kind != KindDir {
		return ErrInvalid
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO studio_ws_grants (principal, node_id, mode) VALUES (?, ?, ?)
		ON CONFLICT (principal, node_id) DO UPDATE SET mode = excluded.mode`, principal, n.id, int(m))
	return err
}

// Revoke removes the grant of principal on p. A missing grant is not an error.
func (s *Workspace) Revoke(ctx context.Context, admin Actor, principal, p string) error {
	if !s.isAdmin(admin) {
		return ErrNotAdmin
	}
	n, err := lookupPath(ctx, s.db, p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM studio_ws_grants WHERE principal = ? AND node_id = ?`, principal, n.id)
	return err
}

// View checks each request against the actor's grants. A mount gets at most
// the mode the actor has.
func (s *Workspace) View(ctx context.Context, a Actor, reqs []MountRequest) (View, error) {
	v := View{Actor: a}
	seen := map[string]bool{}
	for _, r := range reqs {
		at, err := cleanName(r.At)
		if err != nil {
			return View{}, err
		}
		if f := foldName(at); seen[f] {
			return View{}, ErrInvalid
		} else {
			seen[f] = true
		}
		if r.Mode != ModeRead && r.Mode != ModeWrite {
			return View{}, ErrInvalid
		}
		n, err := lookupPath(ctx, s.db, r.Path)
		if err != nil {
			return View{}, err
		}
		m, err := s.access(ctx, s.db, a, n.id)
		if err != nil {
			return View{}, err
		}
		if m < ModeRead {
			return View{}, ErrNotFound
		}
		if n.kind != KindDir {
			return View{}, ErrInvalid
		}
		mode := min(m, r.Mode)
		p, err := pathOf(ctx, s.db, n.id)
		if err != nil {
			return View{}, err
		}
		v.Mounts = append(v.Mounts, Mount{Path: p, At: at, Mode: mode, NodeID: n.id})
	}
	return v, nil
}

// ReadablePrefixes returns the folder paths where the actor has read access or
// more, without a path that is under another one in the list, sorted. The
// admin gets ["/"]. An actor with no grant gets an empty list.
func (s *Workspace) ReadablePrefixes(ctx context.Context, a Actor) ([]string, error) {
	if s.isAdmin(a) {
		return []string{"/"}, nil
	}
	out := []string{}
	if a.Principal == "" {
		return out, nil
	}
	ps := principals(a)
	args := []any{int(ModeRead)}
	for _, p := range ps {
		args = append(args, p)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT g.node_id FROM studio_ws_grants g JOIN studio_ws_nodes n ON n.id = g.node_id
		WHERE g.mode >= ? AND n.deleted = 0 AND n.kind = 'dir' AND g.principal IN (`+strings.Repeat(",?", len(ps))[1:]+`)`, args...)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, id := range ids {
		p, err := pathOf(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	// An ancestor is a string prefix of its descendants, so it sorts first and
	// is kept before them. Other names may sort between a folder and its
	// children ("/a-b" sorts between "/a" and "/a/b"), so each path is checked
	// against every kept prefix, not only the last one.
	for _, p := range paths {
		covered := false
		for _, k := range out {
			if Within(p, k) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out, nil
}

// Grants returns the grants on the live folder p itself, sorted by principal.
func (s *Workspace) Grants(ctx context.Context, admin Actor, p string) ([]Grant, error) {
	if !s.isAdmin(admin) {
		return nil, ErrNotAdmin
	}
	n, err := lookupPath(ctx, s.db, p)
	if err != nil {
		return nil, err
	}
	if n.kind != KindDir {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT principal, mode FROM studio_ws_grants WHERE node_id = ? ORDER BY principal`, n.id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		var m int
		if err := rows.Scan(&g.Principal, &m); err != nil {
			return nil, err
		}
		g.Mode = Mode(m)
		out = append(out, g)
	}
	return out, rows.Err()
}
