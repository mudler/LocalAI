package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"strings"
)

// revList writes a list of revisions as "?, ?, ?" with its arguments. It is
// plain SQL with placeholders, so it runs the same on SQLite and PostgreSQL;
// json_each takes an array only on SQLite. The list must not be empty.
func revList(revs []int64) (string, []any) {
	args := make([]any, len(revs))
	for i, r := range revs {
		args[i] = r
	}
	return strings.TrimSuffix(strings.Repeat("?, ", len(revs)), ", "), args
}

// notInRevs returns the SQL condition "col NOT IN (revs)" and its arguments.
// An empty list excludes nothing, so it gives a condition that always holds.
func notInRevs(col string, revs []int64) (string, []any) {
	if len(revs) == 0 {
		return "1 = 1", nil
	}
	list, args := revList(revs)
	return col + " NOT IN (" + list + ")", args
}

// ownRevisions keeps the revisions in own whose change sets have the
// principal and chat ID of a. A revision of another actor or chat is
// dropped, so a caller cannot hide another writer's change by naming its
// revision.
func ownRevisions(ctx context.Context, q querier, a Actor, own []int64) ([]int64, error) {
	if len(own) == 0 {
		return nil, nil
	}
	list, args := revList(own)
	rows, err := q.QueryContext(ctx, `SELECT rev FROM studio_ws_changesets
		WHERE rev IN (`+list+`) AND actor = ? AND chat_id = ?
		ORDER BY rev`, append(args, a.Principal, a.ChatID)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keep []int64
	for rows.Next() {
		var r int64
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		keep = append(keep, r)
	}
	return keep, rows.Err()
}

// changedAfter reports whether a journal row of node id after the base comes
// from a change set that is not one of the caller's own. Rows of the change
// set in progress count only when self is true.
func (c *upperCommit) changedAfter(id int64, self bool) (bool, error) {
	skip := int64(-1)
	if !self {
		skip = c.w.rev
	}
	var hit bool
	notOwn, ownArgs := notInRevs("rev", c.own)
	err := c.w.tx.QueryRowContext(c.w.ctx, `SELECT EXISTS (SELECT 1 FROM studio_ws_journal
		WHERE node_id = ? AND rev > ? AND rev != ? AND `+notOwn+`)`,
		append([]any{id, c.base, skip}, ownArgs...)...).Scan(&hit)
	return hit, err
}

// changedInSubtree reports whether node id or a live node below it has a
// journal row after the base from a change set that is not one of the
// caller's own. Rows of the change set in progress count.
func (c *upperCommit) changedInSubtree(id int64) (bool, error) {
	var hit bool
	notOwn, ownArgs := notInRevs("j.rev", c.own)
	err := c.w.tx.QueryRowContext(c.w.ctx, `WITH RECURSIVE sub(id) AS (
			SELECT id FROM studio_ws_nodes WHERE id = ?
			UNION ALL
			SELECT n.id FROM studio_ws_nodes n JOIN sub ON n.parent_id = sub.id WHERE n.deleted = 0)
		SELECT EXISTS (SELECT 1 FROM studio_ws_journal j JOIN sub ON j.node_id = sub.id
			WHERE j.rev > ? AND `+notOwn+`)`,
		append([]any{id, c.base}, ownArgs...)...).Scan(&hit)
	return hit, err
}
