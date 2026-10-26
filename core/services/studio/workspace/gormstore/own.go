package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"encoding/json"
)

// ownRevisions keeps the revisions in own whose change sets have the
// principal and chat ID of a, and returns them as a JSON array for
// json_each. A revision of another actor or chat is dropped, so a caller
// cannot hide another writer's change by naming its revision.
func ownRevisions(ctx context.Context, q querier, a Actor, own []int64) (string, error) {
	if len(own) == 0 {
		return "[]", nil
	}
	in, err := json.Marshal(own)
	if err != nil {
		return "", err
	}
	rows, err := q.QueryContext(ctx, `SELECT rev FROM studio_ws_changesets
		WHERE rev IN (SELECT value FROM json_each(?)) AND actor = ? AND chat_id = ?
		ORDER BY rev`, string(in), a.Principal, a.ChatID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	keep := []int64{}
	for rows.Next() {
		var r int64
		if err := rows.Scan(&r); err != nil {
			return "", err
		}
		keep = append(keep, r)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	out, err := json.Marshal(keep)
	return string(out), err
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
	err := c.w.tx.QueryRowContext(c.w.ctx, `SELECT EXISTS (SELECT 1 FROM studio_ws_journal
		WHERE node_id = ? AND rev > ? AND rev != ? AND rev NOT IN (SELECT value FROM json_each(?)))`,
		id, c.base, skip, c.own).Scan(&hit)
	return hit, err
}

// changedInSubtree reports whether node id or a live node below it has a
// journal row after the base from a change set that is not one of the
// caller's own. Rows of the change set in progress count.
func (c *upperCommit) changedInSubtree(id int64) (bool, error) {
	var hit bool
	err := c.w.tx.QueryRowContext(c.w.ctx, `WITH RECURSIVE sub(id) AS (
			SELECT id FROM studio_ws_nodes WHERE id = ?
			UNION ALL
			SELECT n.id FROM studio_ws_nodes n JOIN sub ON n.parent_id = sub.id WHERE n.deleted = 0)
		SELECT EXISTS (SELECT 1 FROM studio_ws_journal j JOIN sub ON j.node_id = sub.id
			WHERE j.rev > ? AND j.rev NOT IN (SELECT value FROM json_each(?)))`,
		id, c.base, c.own).Scan(&hit)
	return hit, err
}
