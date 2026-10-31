package jsonstore

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

// defaultBlobGrace is how long a purge waits before it removes a blob that no
// row refers to.
const defaultBlobGrace = 2 * time.Minute

// Purge erases the nodes that the change sets revs deleted. See workspace.Purger.
func (s *Store) Purge(ctx context.Context, a workspace.Actor, p string, revs []int64, dryRun bool) (workspace.PurgeResult, error) {
	return s.purge(ctx, a, dryRun, func(d *data) []int64 {
		scope, err := d.lookupPath(p)
		if err != nil {
			return nil
		}
		var roots []int64
		for _, rev := range revs {
			for _, ji := range d.byRev[rev] {
				r := d.journal[ji]
				if r.Op != workspace.OpDelete {
					continue
				}
				// A later change set that touched the node makes this
				// deletion no longer the last word on it.
				if last := d.byNode[r.Node]; d.journal[last[len(last)-1]].Rev != rev {
					continue
				}
				if !slices.Contains(d.ancestors(r.Node), scope.ID) {
					continue
				}
				roots = append(roots, r.Node)
			}
		}
		return roots
	})
}

// PurgeAll erases every deleted node at or under p. See workspace.Purger.
func (s *Store) PurgeAll(ctx context.Context, a workspace.Actor, p string, dryRun bool) (workspace.PurgeResult, error) {
	return s.purge(ctx, a, dryRun, func(d *data) []int64 {
		n, err := d.lookupPath(p)
		if err != nil {
			return nil
		}
		var roots []int64
		var walk func(id int64)
		walk = func(id int64) {
			for kid := range d.kids[id] {
				if d.nodes[kid].Deleted {
					roots = append(roots, kid)
					continue
				}
				walk(kid)
			}
		}
		walk(n.ID)
		return roots
	})
}

func (s *Store) purge(ctx context.Context, a workspace.Actor, dryRun bool, pick func(d *data) []int64) (workspace.PurgeResult, error) {
	res, freed, err := s.purgeLocked(ctx, a, dryRun, pick)
	if err != nil || len(freed) == 0 {
		return res, err
	}
	grace := s.blobGrace
	switch {
	case grace == 0:
		grace = defaultBlobGrace
	case grace < 0:
		grace = 0
	}
	// Outside the lock: the check of the blobs takes the read lock.
	blob.RemoveLater(s.blobs, freed, grace, s.blobsInUse)
	return res, nil
}

// purgeLocked removes the nodes under the write lock and returns the blobs
// that nothing refers to any more.
func (s *Store) purgeLocked(ctx context.Context, a workspace.Actor, dryRun bool, pick func(d *data) []int64) (workspace.PurgeResult, []string, error) {
	res := workspace.PurgeResult{ByRev: map[int64]workspace.PurgeCount{}}
	if err := ctx.Err(); err != nil {
		return res, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.d

	gone := map[int64]bool{}
	for _, root := range pick(d) {
		n, ok := d.nodes[root]
		if !ok || gone[root] || !n.Deleted || s.needWrite(d, a, *n) != nil {
			continue
		}
		ids, whole := deletedSubtree(d, root)
		if !whole {
			continue
		}
		for _, id := range ids {
			gone[id] = true
		}
	}
	for id := range gone {
		n := d.nodes[id]
		c := res.ByRev[n.Rev]
		if n.Kind == workspace.KindDir {
			c.Dirs++
			res.Dirs++
		} else {
			c.Files++
			c.Bytes += n.Size
			res.Files++
			res.Bytes += n.Size
		}
		res.ByRev[n.Rev] = c
	}
	if dryRun || len(gone) == 0 {
		return res, nil, nil
	}

	snap := d.snapshot()
	freed := map[string]struct{}{}
	touched := map[int64]bool{}
	kept := snap.Nodes[:0:0]
	for _, n := range snap.Nodes {
		if gone[n.ID] {
			freed[n.Blob] = struct{}{}
			continue
		}
		kept = append(kept, n)
	}
	snap.Nodes = kept
	var rows []journalRow
	for _, r := range snap.Journal {
		if gone[r.Node] {
			touched[r.Rev] = true
			freed[r.Old.Blob] = struct{}{}
			freed[r.New.Blob] = struct{}{}
			continue
		}
		r.ID = int64(len(rows)) + 1
		rows = append(rows, r)
	}
	snap.Journal = rows
	live := map[int64]bool{}
	for _, r := range rows {
		live[r.Rev] = true
	}
	var sets []changeRec
	for _, c := range snap.Sets {
		if touched[c.Rev] && !live[c.Rev] {
			continue
		}
		sets = append(sets, c)
	}
	snap.Sets = sets
	var grants []grantRec
	for _, g := range snap.Grants {
		if !gone[g.Node] {
			grants = append(grants, g)
		}
	}
	snap.Grants = grants

	if s.dir != "" {
		if err := s.writeSnapshot(snap); err != nil {
			return workspace.PurgeResult{}, nil, err
		}
	}
	nd := newData()
	nd.restore(snap)
	s.d = nd

	delete(freed, "")
	return res, slices.Collect(maps.Keys(freed)), nil
}

// deletedSubtree returns the node and everything below it in any state. whole
// is false when a node below is live, which a purge keeps.
func deletedSubtree(d *data, root int64) (ids []int64, whole bool) {
	stack := []int64{root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !d.nodes[id].Deleted {
			return nil, false
		}
		ids = append(ids, id)
		for kid := range d.kids[id] {
			stack = append(stack, kid)
		}
	}
	return ids, true
}

// blobsInUse tells which of shas a node or a journal row refers to.
func (s *Store) blobsInUse(ctx context.Context, shas []string) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(shas))
	for _, sha := range shas {
		want[sha] = false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	mark := func(sha string) {
		if _, ok := want[sha]; ok {
			want[sha] = true
		}
	}
	for _, n := range s.d.nodes {
		mark(n.Blob)
	}
	for _, r := range s.d.journal {
		mark(r.Old.Blob)
		mark(r.New.Blob)
	}
	return want, nil
}
