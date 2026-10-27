package jsonstore

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// setGrant stores or removes one grant and writes it to the log.
func (s *Store) setGrant(rec grantRec) error {
	if err := s.persist(record{Grants: []grantRec{rec}}); err != nil {
		return err
	}
	k := grantKey{rec.Principal, rec.Node}
	if rec.Mode == workspace.ModeNone {
		delete(s.d.grants, k)
	} else {
		s.d.grants[k] = rec.Mode
	}
	s.maybeCompact()
	return nil
}

// Grant gives principal mode m on the folder p and everything below it.
func (s *Store) Grant(ctx context.Context, admin workspace.Actor, principal, p string, m workspace.Mode) error {
	if !s.isAdmin(admin) {
		return workspace.ErrNotAdmin
	}
	if principal == "" || (m != workspace.ModeRead && m != workspace.ModeWrite) {
		return workspace.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.d.lookupPath(p)
	if err != nil {
		return err
	}
	if n.Kind != workspace.KindDir {
		return workspace.ErrInvalid
	}
	return s.setGrant(grantRec{Principal: principal, Node: n.ID, Mode: m})
}

// Revoke removes the grant of principal on p. A missing grant is not an error.
func (s *Store) Revoke(ctx context.Context, admin workspace.Actor, principal, p string) error {
	if !s.isAdmin(admin) {
		return workspace.ErrNotAdmin
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.d.lookupPath(p)
	if err != nil {
		return err
	}
	if _, ok := s.d.grants[grantKey{principal, n.ID}]; !ok {
		return nil
	}
	return s.setGrant(grantRec{Principal: principal, Node: n.ID, Mode: workspace.ModeNone})
}

// View checks each request against the actor's grants. A mount gets at most
// the mode the actor has.
func (s *Store) View(ctx context.Context, a workspace.Actor, reqs []workspace.MountRequest) (workspace.View, error) {
	v := workspace.View{Actor: a}
	err := s.readLocked(ctx, func(d *data) error {
		seen := map[string]bool{}
		for _, r := range reqs {
			at, err := workspace.CleanName(r.At)
			if err != nil {
				return err
			}
			if f := workspace.FoldName(at); seen[f] {
				return workspace.ErrInvalid
			} else {
				seen[f] = true
			}
			if r.Mode != workspace.ModeRead && r.Mode != workspace.ModeWrite {
				return workspace.ErrInvalid
			}
			n, err := d.lookupPath(r.Path)
			if err != nil {
				return err
			}
			m := s.access(d, a, n.ID)
			if m < workspace.ModeRead {
				return workspace.ErrNotFound
			}
			if n.Kind != workspace.KindDir {
				return workspace.ErrInvalid
			}
			v.Mounts = append(v.Mounts, workspace.Mount{Path: d.pathOf(n.ID), At: at, Mode: min(r.Mode, m), NodeID: n.ID})
		}
		return nil
	})
	if err != nil {
		return workspace.View{}, err
	}
	return v, nil
}

// ReadablePrefixes returns the folder paths where the actor has read access or
// more, without a path that is under another one in the list, sorted. The
// admin gets ["/"]. An actor with no grant gets an empty list.
func (s *Store) ReadablePrefixes(ctx context.Context, a workspace.Actor) ([]string, error) {
	if s.isAdmin(a) {
		return []string{"/"}, nil
	}
	out := []string{}
	if a.Principal == "" {
		return out, nil
	}
	err := s.readLocked(ctx, func(d *data) error {
		ps := a.Principals()
		var paths []string
		seen := map[int64]bool{}
		for k, m := range d.grants {
			n := d.nodes[k.Node]
			if m < workspace.ModeRead || seen[k.Node] || n == nil || n.Deleted || n.Kind != workspace.KindDir || !slices.Contains(ps, k.Principal) {
				continue
			}
			seen[k.Node] = true
			paths = append(paths, d.pathOf(k.Node))
		}
		slices.Sort(paths)
		// An ancestor is a string prefix of its descendants, so it sorts first
		// and is kept before them. Other names may sort between a folder and
		// its children ("/a-b" sorts between "/a" and "/a/b"), so each path is
		// checked against every kept prefix, not only the last one.
		for _, p := range paths {
			if !slices.ContainsFunc(out, func(k string) bool { return workspace.Within(p, k) }) {
				out = append(out, p)
			}
		}
		return nil
	})
	return out, err
}
