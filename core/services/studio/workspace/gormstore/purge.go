package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

// purgeChunk is how many ids go into one IN list.
const purgeChunk = 200

func inList(n int) string { return strings.Repeat(",?", n)[1:] }

// chunks calls fn with slices of at most purgeChunk ids.
func chunks[T any](ids []T, fn func([]T) error) error {
	for len(ids) > 0 {
		n := min(len(ids), purgeChunk)
		if err := fn(ids[:n]); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

func idArgs[T any](ids []T) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// Purge erases the nodes that the change sets revs deleted. See Purger.
func (s *Workspace) Purge(ctx context.Context, a Actor, p string, revs []int64, dryRun bool) (PurgeResult, error) {
	return s.purge(ctx, a, dryRun, func(q querier) ([]int64, error) {
		scope, err := lookupPath(ctx, q, p)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var roots []int64
		err = chunks(revs, func(part []int64) error {
			// Only a deletion that is the last word on its node counts.
			rows, err := q.QueryContext(ctx, `SELECT j.node_id FROM studio_ws_journal j
				WHERE j.rev IN (`+inList(len(part))+`) AND j.op = 'delete'
				AND j.id = (SELECT MAX(l.id) FROM studio_ws_journal l WHERE l.node_id = j.node_id)`, idArgs(part)...)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					return err
				}
				roots = append(roots, id)
			}
			return rows.Err()
		})
		if err != nil {
			return nil, err
		}
		// Keep the nodes inside the scope.
		in := roots[:0]
		for _, id := range roots {
			up, err := ancestors(ctx, q, id)
			if err != nil {
				return nil, err
			}
			if slices.Contains(up, scope.id) {
				in = append(in, id)
			}
		}
		return in, nil
	})
}

// PurgeAll erases every deleted node at or under p. See Purger.
func (s *Workspace) PurgeAll(ctx context.Context, a Actor, p string, dryRun bool) (PurgeResult, error) {
	return s.purge(ctx, a, dryRun, func(q querier) ([]int64, error) {
		n, err := lookupPath(ctx, q, p)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, nil
			}
			return nil, err
		}
		rows, err := q.QueryContext(ctx, `WITH RECURSIVE sub(id) AS (
				SELECT id FROM studio_ws_nodes WHERE id = ?
				UNION ALL
				SELECT n.id FROM studio_ws_nodes n JOIN sub ON n.parent_id = sub.id)
			SELECT n.id FROM studio_ws_nodes n JOIN sub ON n.id = sub.id WHERE n.deleted = 1`, n.id)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var roots []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			roots = append(roots, id)
		}
		return roots, rows.Err()
	})
}

func (s *Workspace) purge(ctx context.Context, a Actor, dryRun bool, pick func(q querier) ([]int64, error)) (PurgeResult, error) {
	res := PurgeResult{ByRev: map[int64]PurgeCount{}}
	t, err := s.db.BeginTx(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = t.Rollback() }()
	// The revision row lock makes writers and purges wait for each other.
	if _, err := t.ExecContext(ctx, `UPDATE studio_ws_meta SET value = value WHERE key = 'rev'`); err != nil {
		return res, err
	}
	roots, err := pick(t)
	if err != nil {
		return res, err
	}

	var order []nodeRow // parents before children
	gone := map[int64]bool{}
	for _, root := range roots {
		if gone[root] {
			continue
		}
		n, err := getNode(ctx, t, root)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return res, err
		}
		if !n.deleted || s.needWrite(ctx, t, a, n) != nil {
			continue
		}
		nodes, err := scanNodes(t.QueryContext(ctx, `WITH RECURSIVE sub(id, depth) AS (
				SELECT id, 0 FROM studio_ws_nodes WHERE id = ?
				UNION ALL
				SELECT n.id, sub.depth + 1 FROM studio_ws_nodes n JOIN sub ON n.parent_id = sub.id)
			SELECT `+nodeCols+` FROM studio_ws_nodes JOIN sub ON studio_ws_nodes.id = sub.id ORDER BY sub.depth, studio_ws_nodes.id`, root))
		if err != nil {
			return res, err
		}
		whole := true
		for _, c := range nodes {
			if !c.deleted {
				whole = false
				break
			}
		}
		if !whole {
			continue
		}
		for _, c := range nodes {
			if !gone[c.id] {
				gone[c.id] = true
				order = append(order, c)
			}
		}
	}
	for _, n := range order {
		res.Add(n.rev, n.kind == KindDir, n.size)
	}
	if dryRun || len(order) == 0 {
		return res, nil
	}

	ids := make([]int64, len(order))
	freed := map[string]struct{}{}
	for i, n := range order {
		ids[i] = n.id
		freed[n.blob] = struct{}{}
	}
	touched := map[int64]bool{}
	err = chunks(ids, func(part []int64) error {
		rows, err := t.QueryContext(ctx, `SELECT rev, old_blob, new_blob FROM studio_ws_journal WHERE node_id IN (`+inList(len(part))+`)`, idArgs(part)...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var rev int64
			var ob, nb string
			if err := rows.Scan(&rev, &ob, &nb); err != nil {
				return err
			}
			touched[rev] = true
			freed[ob], freed[nb] = struct{}{}, struct{}{}
		}
		return rows.Err()
	})
	if err != nil {
		return res, err
	}
	delete(freed, "")

	for _, table := range []string{"studio_ws_grants", "studio_ws_journal"} {
		err := chunks(ids, func(part []int64) error {
			_, err := t.ExecContext(ctx, `DELETE FROM `+table+` WHERE node_id IN (`+inList(len(part))+`)`, idArgs(part)...)
			return err
		})
		if err != nil {
			return res, err
		}
	}
	// Children go before their parents: the ids are in the order of depth.
	rev := make([]int64, len(ids))
	for i, id := range ids {
		rev[len(ids)-1-i] = id
	}
	err = chunks(rev, func(part []int64) error {
		_, err := t.ExecContext(ctx, `DELETE FROM studio_ws_nodes WHERE id IN (`+inList(len(part))+`)`, idArgs(part)...)
		return err
	})
	if err != nil {
		return res, err
	}
	revs := make([]int64, 0, len(touched))
	for r := range touched {
		revs = append(revs, r)
	}
	err = chunks(revs, func(part []int64) error {
		_, err := t.ExecContext(ctx, `DELETE FROM studio_ws_changesets WHERE rev IN (`+inList(len(part))+`)
			AND NOT EXISTS (SELECT 1 FROM studio_ws_journal j WHERE j.rev = studio_ws_changesets.rev)`, idArgs(part)...)
		return err
	})
	if err != nil {
		return res, err
	}
	if err := t.Commit(); err != nil {
		return res, err
	}

	shas := make([]string, 0, len(freed))
	for sha := range freed {
		shas = append(shas, sha)
	}
	blob.RemoveLater(s.blobs, shas, blob.Grace(s.grace), s.blobsInUse)
	return res, nil
}

// blobsInUse tells which of shas a node or a journal row refers to.
func (s *Workspace) blobsInUse(ctx context.Context, shas []string) (map[string]bool, error) {
	used := make(map[string]bool, len(shas))
	err := chunks(shas, func(part []string) error {
		list := inList(len(part))
		args := idArgs(part)
		all := append(append(append([]any{}, args...), args...), args...)
		rows, err := s.db.QueryContext(ctx, `SELECT blob FROM studio_ws_nodes WHERE blob IN (`+list+`)
			UNION SELECT old_blob FROM studio_ws_journal WHERE old_blob IN (`+list+`)
			UNION SELECT new_blob FROM studio_ws_journal WHERE new_blob IN (`+list+`)`, all...)
		if err != nil {
			return err
		}
		return scanStrings(rows, used)
	})
	return used, err
}

func scanStrings(rows *sql.Rows, into map[string]bool) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return err
		}
		into[sha] = true
	}
	return rows.Err()
}
