package jsonstore

import (
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ownRevisions keeps the revisions in own whose change sets have the
// principal and chat ID of a. A revision of another actor or chat is dropped,
// so a caller cannot hide another writer's change by naming its revision.
func (d *data) ownRevisions(a workspace.Actor, own []int64) map[int64]bool {
	keep := map[int64]bool{}
	for _, rev := range own {
		if c, ok := d.sets[rev]; ok && c.Actor == a.Principal && c.ChatID == a.ChatID {
			keep[rev] = true
		}
	}
	return keep
}

// childAtRev finds the node that stood under parent with name at revision
// base: the node whose latest journal row at or before base put it there and
// did not delete it.
func (d *data) childAtRev(parent int64, name string, base int64) (int64, bool) {
	for _, ji := range d.byNew[parent] {
		r := d.journal[ji]
		if r.New.Name != name || r.New.Deleted || r.Rev > base {
			continue
		}
		latest := int64(0)
		for _, ki := range d.byNode[r.Node] {
			if k := d.journal[ki]; k.Rev <= base && k.ID > latest {
				latest = k.ID
			}
		}
		if r.ID == latest {
			return r.Node, true
		}
	}
	return 0, false
}

// changedAfter reports whether a journal row of node id after the base comes
// from a change set that is not one of the caller's own. Rows of the change
// set in progress count only when self is true.
func (c *upperCommit) changedAfter(id int64, self bool) (bool, error) {
	skip := int64(-1)
	if !self {
		skip = c.w.rev
	}
	for _, ji := range c.w.d.byNode[id] {
		if r := c.w.d.journal[ji]; r.Rev > c.base && r.Rev != skip && !c.own[r.Rev] {
			return true, nil
		}
	}
	return false, nil
}

// changedInSubtree reports whether node id or a live node below it has a
// journal row after the base from a change set that is not one of the
// caller's own. Rows of the change set in progress count.
func (c *upperCommit) changedInSubtree(id int64) (bool, error) {
	for _, n := range c.w.d.subtree(id, false) {
		for _, ji := range c.w.d.byNode[n.ID] {
			if r := c.w.d.journal[ji]; r.Rev > c.base && !c.own[r.Rev] {
				return true, nil
			}
		}
	}
	return false, nil
}
