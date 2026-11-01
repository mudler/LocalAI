package jsonstore

import (
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// access returns the mode the actor has on a node: the highest grant on the
// node or a folder above it, for the principal or one of its groups. For a
// fenced actor it is at most what the fence allows there.
func (s *Store) access(d *data, a workspace.Actor, id int64) workspace.Mode {
	if s.isAdmin(a) {
		return workspace.ModeWrite
	}
	if a.Principal == "" {
		return workspace.ModeNone
	}
	ancs := d.ancestors(id)
	limit := workspace.FenceMode(a, ancs, d.isLive)
	if limit == workspace.ModeNone {
		return workspace.ModeNone
	}
	ps := a.Principals()
	best := workspace.ModeNone
	for _, anc := range ancs {
		for _, p := range ps {
			best = max(best, d.grants[grantKey{p, anc}])
		}
	}
	return min(best, limit)
}

// isLive reports whether the node id exists and is not deleted.
func (d *data) isLive(id int64) bool {
	n := d.nodes[id]
	return n != nil && !n.Deleted
}

// needWrite returns ErrNotFound without read access and ErrReadOnly without
// write access.
func (s *Store) needWrite(d *data, a workspace.Actor, n nodeRow) error {
	if err := workspace.CheckFence(a); err != nil {
		return err
	}
	switch m := s.access(d, a, n.ID); {
	case m < workspace.ModeRead:
		return workspace.ErrNotFound
	case m < workspace.ModeWrite:
		return workspace.ErrReadOnly
	}
	return nil
}

// find resolves p for the actor. need is ModeRead or ModeWrite.
func (s *Store) find(d *data, a workspace.Actor, p string, need workspace.Mode) (nodeRow, error) {
	if err := workspace.CheckFence(a); err != nil {
		return nodeRow{}, err
	}
	n, err := d.lookupPath(p)
	if err != nil {
		return nodeRow{}, err
	}
	m := s.access(d, a, n.ID)
	if m < workspace.ModeRead {
		return nodeRow{}, workspace.ErrNotFound
	}
	if need == workspace.ModeWrite && m < workspace.ModeWrite {
		return nodeRow{}, workspace.ErrReadOnly
	}
	return n, nil
}

func (d *data) toNode(n nodeRow) workspace.Node {
	node := workspace.Node{ID: n.ID, Path: d.pathOf(n.ID), Name: n.Name, Kind: n.Kind, Blob: n.Blob, Size: n.Size, Version: n.Version, Rev: n.Rev}
	if cs := d.sets[n.Rev]; cs != nil {
		node.Modified = msTime(cs.CreatedAt)
	}
	return node
}
