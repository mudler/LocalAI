package gormstore

import (
	. "github.com/mudler/LocalAI/core/services/studio/workspace"

	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

// querier is what *sql.DB and *sql.Tx have in common.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// state is the part of a node that operations change and undo restores.
type state struct {
	parent  int64 // 0 for the root and for a node that does not exist yet
	name    string
	blob    string
	size    int64
	deleted bool
}

type nodeRow struct {
	id      int64
	kind    Kind
	version int64
	rev     int64
	state
}

const nodeCols = `studio_ws_nodes.id, COALESCE(studio_ws_nodes.parent_id, 0), studio_ws_nodes.name, studio_ws_nodes.kind, studio_ws_nodes.blob, studio_ws_nodes.size, studio_ws_nodes.version, studio_ws_nodes.rev, studio_ws_nodes.deleted`

func scanNode(sc interface{ Scan(...any) error }) (nodeRow, error) {
	var n nodeRow
	var kind string
	var del int
	err := sc.Scan(&n.id, &n.parent, &n.name, &kind, &n.blob, &n.size, &n.version, &n.rev, &del)
	n.kind = Kind(kind)
	n.deleted = del != 0
	return n, err
}

// scanNodes reads all rows and closes them.
func scanNodes(rows *sql.Rows, err error) ([]nodeRow, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []nodeRow
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func getNode(ctx context.Context, q querier, id int64) (nodeRow, error) {
	n, err := scanNode(q.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM studio_ws_nodes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nodeRow{}, ErrNotFound
	}
	return n, err
}

func childWhere(ctx context.Context, q querier, where string, args ...any) (nodeRow, bool, error) {
	n, err := scanNode(q.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM studio_ws_nodes WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nodeRow{}, false, nil
	}
	if err != nil {
		return nodeRow{}, false, err
	}
	return n, true, nil
}

// childByName finds a live child by its exact name.
func childByName(ctx context.Context, q querier, parent int64, name string) (nodeRow, bool, error) {
	return childWhere(ctx, q, `parent_id = ? AND name = ? AND deleted = 0`, parent, name)
}

// childByFold finds a live child whose name folds to the same value as name.
func childByFold(ctx context.Context, q querier, parent int64, name string) (nodeRow, bool, error) {
	return childWhere(ctx, q, `parent_id = ? AND name_fold = ? AND deleted = 0`, parent, foldName(name))
}

// children returns the live children of a folder, sorted by name.
func children(ctx context.Context, q querier, id int64) ([]nodeRow, error) {
	rows, err := scanNodes(q.QueryContext(ctx, `SELECT `+nodeCols+` FROM studio_ws_nodes WHERE parent_id = ? AND deleted = 0`, id))
	if err != nil {
		return nil, err
	}
	// Sorted here, byte by byte, because the databases order text by their own
	// collations and a listing must look the same on both.
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows, nil
}

// lookup follows exact names from the root.
func lookup(ctx context.Context, q querier, segs []string) (nodeRow, error) {
	n, err := getNode(ctx, q, rootID)
	if err != nil {
		return nodeRow{}, err
	}
	for _, s := range segs {
		if n.kind != KindDir {
			return nodeRow{}, ErrNotFound
		}
		c, ok, err := childByName(ctx, q, n.id, s)
		if err != nil {
			return nodeRow{}, err
		}
		if !ok {
			return nodeRow{}, ErrNotFound
		}
		n = c
	}
	return n, nil
}

func lookupPath(ctx context.Context, q querier, p string) (nodeRow, error) {
	segs, err := splitPath(p)
	if err != nil {
		return nodeRow{}, err
	}
	return lookup(ctx, q, segs)
}

// deepestDir walks segs from the root. It returns the deepest live node on
// the path and how many segments lead to it. blocked is true when that node
// is a file that stands where a folder must be.
func deepestDir(ctx context.Context, q querier, segs []string) (nodeRow, int, bool, error) {
	n, err := getNode(ctx, q, rootID)
	if err != nil {
		return nodeRow{}, 0, false, err
	}
	for i, s := range segs {
		c, ok, err := childByName(ctx, q, n.id, s)
		if err != nil {
			return nodeRow{}, 0, false, err
		}
		if !ok {
			return n, i, false, nil
		}
		if c.kind != KindDir {
			return c, i + 1, true, nil
		}
		n = c
	}
	return n, len(segs), false, nil
}

// pathOf returns the path of a node, live or not.
func pathOf(ctx context.Context, q querier, id int64) (string, error) {
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE up(id, parent_id, name, depth) AS (
			SELECT id, parent_id, name, 0 FROM studio_ws_nodes WHERE id = ?
			UNION ALL
			SELECT n.id, n.parent_id, n.name, up.depth + 1 FROM studio_ws_nodes n JOIN up ON n.id = up.parent_id)
		SELECT name FROM up WHERE parent_id IS NOT NULL ORDER BY depth DESC`, id)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var segs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		segs = append(segs, s)
	}
	return joinPath(segs), rows.Err()
}

// ancestors returns the node and every folder above it, nearest first.
func ancestors(ctx context.Context, q querier, id int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE up(id, parent_id, depth) AS (
			SELECT id, parent_id, 0 FROM studio_ws_nodes WHERE id = ?
			UNION ALL
			SELECT n.id, n.parent_id, up.depth + 1 FROM studio_ws_nodes n JOIN up ON n.id = up.parent_id)
		SELECT id FROM up ORDER BY depth`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// subtree returns the node and its live descendants: parents first, or
// deepest first when deepestFirst is true.
func subtree(ctx context.Context, q querier, id int64, deepestFirst bool) ([]nodeRow, error) {
	order := `sub.depth, studio_ws_nodes.id`
	if deepestFirst {
		order = `sub.depth DESC, studio_ws_nodes.id`
	}
	return scanNodes(q.QueryContext(ctx, `WITH RECURSIVE sub(id, depth) AS (
			SELECT id, 0 FROM studio_ws_nodes WHERE id = ?
			UNION ALL
			SELECT n.id, sub.depth + 1 FROM studio_ws_nodes n JOIN sub ON n.parent_id = sub.id WHERE n.deleted = 0)
		SELECT `+nodeCols+` FROM studio_ws_nodes JOIN sub ON studio_ws_nodes.id = sub.id ORDER BY `+order, id))
}

// access returns the mode the actor has on a node: the highest grant on the
// node or a folder above it, for the principal or one of its groups.
func (s *Workspace) access(ctx context.Context, q querier, a Actor, id int64) (Mode, error) {
	if s.admin != "" && a.Principal == s.admin {
		return ModeWrite, nil
	}
	if a.Principal == "" {
		return ModeNone, nil
	}
	ps := principals(a)
	args := []any{id}
	for _, p := range ps {
		args = append(args, p)
	}
	var m sql.NullInt64
	err := q.QueryRowContext(ctx, `WITH RECURSIVE up(id, parent_id) AS (
			SELECT id, parent_id FROM studio_ws_nodes WHERE id = ?
			UNION ALL
			SELECT n.id, n.parent_id FROM studio_ws_nodes n JOIN up ON n.id = up.parent_id)
		SELECT MAX(g.mode) FROM studio_ws_grants g JOIN up ON g.node_id = up.id
		WHERE g.principal IN (`+strings.Repeat(",?", len(ps))[1:]+`)`, args...).Scan(&m)
	return Mode(m.Int64), err
}

// needWrite returns ErrNotFound without read access and ErrReadOnly without write access.
func (s *Workspace) needWrite(ctx context.Context, q querier, a Actor, n nodeRow) error {
	m, err := s.access(ctx, q, a, n.id)
	if err != nil {
		return err
	}
	if m < ModeRead {
		return ErrNotFound
	}
	if m < ModeWrite {
		return ErrReadOnly
	}
	return nil
}

// find resolves p for the actor. need is ModeRead or ModeWrite.
func (s *Workspace) find(ctx context.Context, q querier, a Actor, p string, need Mode) (nodeRow, error) {
	n, err := lookupPath(ctx, q, p)
	if err != nil {
		return nodeRow{}, err
	}
	m, err := s.access(ctx, q, a, n.id)
	if err != nil {
		return nodeRow{}, err
	}
	if m < ModeRead {
		return nodeRow{}, ErrNotFound
	}
	if need == ModeWrite && m < ModeWrite {
		return nodeRow{}, ErrReadOnly
	}
	return n, nil
}

func (s *Workspace) toNode(ctx context.Context, q querier, n nodeRow) (Node, error) {
	p, err := pathOf(ctx, q, n.id)
	if err != nil {
		return Node{}, err
	}
	return Node{ID: n.id, Path: p, Name: n.name, Kind: n.kind, Blob: n.blob, Size: n.size, Version: n.version, Rev: n.rev}, nil
}
