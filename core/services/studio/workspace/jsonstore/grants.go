package jsonstore

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// setGrant stores or removes one grant and writes it to the log.
func (s *Store) setGrant(rec grantRec) error {
	if err := s.persist(record{Grants: []grantRec{rec}}); err != nil {
		return err
	}
	s.d.putGrant(rec)
	s.maybeCompact()
	return nil
}

// Grant gives principal mode m (read, data or write) on the folder p and
// everything below it. A grant that carried the page mark loses it.
func (s *Store) Grant(ctx context.Context, admin workspace.Actor, principal, p string, m workspace.Mode) error {
	return s.grant(ctx, admin, principal, p, m, false)
}

// GrantPage is Grant with the page mark: the grant was made through the page
// routes by the person who owns the page.
func (s *Store) GrantPage(ctx context.Context, admin workspace.Actor, principal, p string, m workspace.Mode) error {
	return s.grant(ctx, admin, principal, p, m, true)
}

// PageShared reports whether the actor, or one of its groups, holds a grant
// with the page mark on the live folder p itself.
func (s *Store) PageShared(ctx context.Context, a workspace.Actor, p string) (bool, error) {
	found := false
	err := s.readLocked(ctx, func(d *data) error {
		n, err := d.lookupPath(p)
		if err != nil {
			return err
		}
		for _, who := range a.Principals() {
			if d.pages[grantKey{who, n.ID}] {
				found = true
			}
		}
		return nil
	})
	if errors.Is(err, workspace.ErrNotFound) {
		return false, nil
	}
	return found, err
}

func (s *Store) grant(ctx context.Context, admin workspace.Actor, principal, p string, m workspace.Mode, page bool) error {
	if !s.isAdmin(admin) {
		return workspace.ErrNotAdmin
	}
	if principal == "" || workspace.StoredMode(m) == 0 {
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
	return s.setGrant(grantRec{Principal: principal, Node: n.ID, Mode: workspace.StoredMode(m), Page: page})
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
	return s.setGrant(grantRec{Principal: principal, Node: n.ID})
}

// View checks each request against the actor's grants. A mount gets at most
// the mode the actor has.
func (s *Store) View(ctx context.Context, a workspace.Actor, reqs []workspace.MountRequest) (workspace.View, error) {
	v := workspace.View{Actor: a}
	if err := workspace.CheckFence(a); err != nil {
		return workspace.View{}, err
	}
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
// admin gets ["/"]. An actor with no grant gets an empty list. A fenced actor
// gets only folders that are roots of its fence or inside them.
func (s *Store) ReadablePrefixes(ctx context.Context, a workspace.Actor) ([]string, error) {
	if s.isAdmin(a) {
		return []string{"/"}, nil
	}
	out := []string{}
	if a.Principal == "" {
		return out, nil
	}
	if err := workspace.CheckFence(a); err != nil {
		return nil, err
	}
	fenced := workspace.HasFence(a)
	err := s.readLocked(ctx, func(d *data) error {
		ps := a.Principals()
		var paths []string
		seen := map[int64]bool{}
		add := func(id int64) {
			n := d.nodes[id]
			if seen[id] || n == nil || n.Deleted || n.Kind != workspace.KindDir {
				return
			}
			seen[id] = true
			// A grant may cover more than the fence and a root may lie
			// outside every grant: keep a folder only when both allow it.
			if fenced && s.access(d, a, id) < workspace.ModeRead {
				return
			}
			paths = append(paths, d.pathOf(id))
		}
		for k, m := range d.grants {
			if m >= workspace.ModeRead && slices.Contains(ps, k.Principal) {
				add(k.Node)
			}
		}
		for _, id := range workspace.FenceNodes(a) {
			add(id)
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

// Grants returns the grants on the live folder p itself, sorted by principal.
func (s *Store) Grants(ctx context.Context, admin workspace.Actor, p string) ([]workspace.Grant, error) {
	if !s.isAdmin(admin) {
		return nil, workspace.ErrNotAdmin
	}
	out := []workspace.Grant{}
	err := s.readLocked(ctx, func(d *data) error {
		n, err := d.lookupPath(p)
		if err != nil {
			return err
		}
		if n.Kind != workspace.KindDir {
			return workspace.ErrInvalid
		}
		for k, m := range d.grants {
			if k.Node == n.ID && m >= workspace.ModeRead {
				out = append(out, workspace.Grant{Principal: k.Principal, Mode: m, Page: d.pages[k]})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b workspace.Grant) int { return strings.Compare(a.Principal, b.Principal) })
	return out, nil
}

// GrantsBelow returns the grants on the live folders strictly below the live
// folder p, sorted by path and principal.
func (s *Store) GrantsBelow(ctx context.Context, admin workspace.Actor, p string) ([]workspace.NodeGrant, error) {
	if !s.isAdmin(admin) {
		return nil, workspace.ErrNotAdmin
	}
	var out []workspace.NodeGrant
	err := s.readLocked(ctx, func(d *data) error {
		top, err := d.lookupPath(p)
		if err != nil {
			return err
		}
		for k, m := range d.grants {
			if k.Node == top.ID || m < workspace.ModeRead || !d.isLive(k.Node) {
				continue
			}
			n, err := d.get(k.Node)
			if err != nil || n.Kind != workspace.KindDir {
				continue
			}
			np := d.pathOf(k.Node)
			if !workspace.Within(np, d.pathOf(top.ID)) {
				continue
			}
			out = append(out, workspace.NodeGrant{NodeID: k.Node, Path: np, Grant: workspace.Grant{Principal: k.Principal, Mode: m, Page: d.pages[k]}})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b workspace.NodeGrant) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return strings.Compare(a.Principal, b.Principal)
	})
	return out, nil
}
