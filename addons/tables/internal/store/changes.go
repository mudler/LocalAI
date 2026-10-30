// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Change sets are the undo of this add-on. Every row write and every schema
// change records, in the same transaction as the change, what it takes to put
// the database back: the ids of inserted rows, the before-image of updated and
// deleted rows, the data of a dropped column or table. The record lives in the
// database file itself, so it is committed or lost with the change, travels
// with every backup and restore of the add-on's data, and goes away when the
// database is deleted.
//
// The stale rule is exact and simple. A change set is bound to the revision
// the change produced. It can be undone only while the database is still at
// that revision, so a rollback never overwrites later work: any later change,
// of any rows, makes it stale. One refinement keeps the rule useful: an undo
// is itself a revision whose state equals the state before the change it
// undid, so undoing the latest change makes the one before it current again,
// and a person can unwind a run of changes from the newest to the oldest.
// There is no redo: an undo is a recorded change of the database, but it has
// no change set of its own.

// Defaults of change sets. They are bounds, never targets.
const (
	// DefaultChangeRetention is how long a change set can be undone.
	DefaultChangeRetention = 7 * 24 * time.Hour
	// DefaultMaxChangeBytes bounds the inverse of one change. A change whose
	// inverse is larger keeps none, and its receipt says so.
	DefaultMaxChangeBytes = 4 << 20
	// DefaultMaxChangesTotal bounds the inverses one database keeps. The
	// oldest are dropped first when a new one would exceed it.
	DefaultMaxChangesTotal = 64 << 20
)

// Errors of the undo calls.
var (
	// ErrChangeNotFound means no change set with this ID: never recorded,
	// too large to keep, or dropped to keep the bound.
	ErrChangeNotFound = errors.New("no change set with this ID")
	// ErrChangeExpired means the change set is older than the retention.
	ErrChangeExpired = errors.New("the change set expired")
	// ErrChangeUndone means the change was undone already.
	ErrChangeUndone = errors.New("the change was undone already")
)

// ChangeStaleError means the database moved on after the change. Nothing was
// applied. Detail says what changed since.
type ChangeStaleError struct {
	Current contract.Revision
	Detail  string
}

func (e *ChangeStaleError) Error() string        { return "the database changed since: " + e.Detail }
func (e *ChangeStaleError) Is(target error) bool { return target == ErrStale }

const changesSchema = `
CREATE TABLE IF NOT EXISTS tables_changes(
 operation_id TEXT PRIMARY KEY,
 class TEXT NOT NULL,
 kind TEXT NOT NULL,
 summary TEXT NOT NULL,
 before_schema INTEGER NOT NULL,
 before_data INTEGER NOT NULL,
 after_schema INTEGER NOT NULL,
 after_data INTEGER NOT NULL,
 inverse TEXT NOT NULL,
 bytes INTEGER NOT NULL,
 created_ns INTEGER NOT NULL,
 undone_by TEXT,
 undone_schema INTEGER,
 undone_data INTEGER
) WITHOUT ROWID;`

// Kinds of a change set that are not a row or schema kind of the store.
const (
	kindDatabaseCreate = "database-create"
	kindDatabaseRename = "database-rename"
)

// Inverse operations.
const (
	invDeleteRows     = "delete-rows"
	invRestoreValues  = "restore-values"
	invReinsertRows   = "reinsert-rows"
	invDropTable      = "drop-table"
	invRenameTable    = "rename-table"
	invRestoreTable   = "restore-table"
	invDropColumn     = "drop-column"
	invRenameColumn   = "rename-column"
	invRestoreColumn  = "restore-column"
	invRenameDatabase = "rename-database"
)

// sqlValue is one stored SQLite value. The zero value is NULL. Integers and
// reals travel as text so no digit is lost to a float.
type sqlValue struct {
	K string `json:"k,omitempty"` // i, r or t
	V string `json:"v,omitempty"`
}

type savedRow struct {
	ID int64      `json:"id"`
	V  []sqlValue `json:"v"`
}

type savedColumn struct {
	ColumnSchema
	Position int `json:"position"`
}

// inverse is the whole recipe of an undo. Table and column fields are internal
// identifiers, which never change on rename.
type inverse struct {
	Op     string        `json:"op"`
	Table  string        `json:"table,omitempty"`
	Column string        `json:"column,omitempty"`
	Name   string        `json:"name,omitempty"`
	To     string        `json:"to,omitempty"`
	IDs    []int64       `json:"ids,omitempty"`
	Cols   []string      `json:"cols,omitempty"`
	Rows   []savedRow    `json:"rows,omitempty"`
	Def    *savedColumn  `json:"def,omitempty"`
	Defs   []savedColumn `json:"defs,omitempty"`
	Pos    int           `json:"pos,omitempty"`
	Seq    *int64        `json:"seq,omitempty"`
}

func (s *Store) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

func (s *Store) retention() time.Duration {
	if s.opts.ChangeRetention > 0 {
		return s.opts.ChangeRetention
	}
	return DefaultChangeRetention
}

func (s *Store) maxChange() int64 {
	if s.opts.MaxChangeBytes > 0 {
		return s.opts.MaxChangeBytes
	}
	return DefaultMaxChangeBytes
}

func (s *Store) maxTotal() int64 {
	if s.opts.MaxChangesTotal > 0 {
		return s.opts.MaxChangesTotal
	}
	return DefaultMaxChangesTotal
}

// recorder collects the inverse of the change that runs in one transaction.
type recorder struct {
	max     int64
	class   string
	kind    string
	inv     inverse
	have    bool
	tooBig  bool
	summary string
	size    int64
}

func (s *Store) newRecorder(class, kind string) *recorder {
	return &recorder{max: s.maxChange(), class: class, kind: kind}
}

// spend counts n bytes of captured data and reports whether it still fits. A
// recorder over its bound keeps nothing and stops capturing.
func (r *recorder) spend(n int) bool {
	if r == nil || r.tooBig {
		return false
	}
	r.size += int64(n)
	if r.size > r.max {
		r.tooBig = true
		r.have = false
		r.inv = inverse{}
		return false
	}
	return true
}

func (r *recorder) set(inv inverse, summary string) {
	if r == nil || r.tooBig {
		return
	}
	r.inv, r.have, r.summary = inv, true, summary
}

func toSQLValue(v any) sqlValue {
	switch x := v.(type) {
	case nil:
		return sqlValue{}
	case int64:
		return sqlValue{K: "i", V: strconv.FormatInt(x, 10)}
	case float64:
		return sqlValue{K: "r", V: strconv.FormatFloat(x, 'g', -1, 64)}
	case string:
		return sqlValue{K: "t", V: x}
	case []byte:
		return sqlValue{K: "t", V: string(x)}
	case time.Time:
		return sqlValue{K: "t", V: x.UTC().Format(time.RFC3339Nano)}
	case bool:
		if x {
			return sqlValue{K: "i", V: "1"}
		}
		return sqlValue{K: "i", V: "0"}
	}
	return sqlValue{K: "t", V: fmt.Sprint(v)}
}

func (v sqlValue) arg() (any, error) {
	switch v.K {
	case "":
		return nil, nil
	case "i":
		return strconv.ParseInt(v.V, 10, 64)
	case "r":
		return strconv.ParseFloat(v.V, 64)
	case "t":
		return v.V, nil
	}
	return nil, errors.New("unknown stored value")
}

// capture reads the rows of ids, or every row when ids is nil, with the given
// columns, until the bound of the recorder. It reports false when the bound
// was reached: the recorder then keeps nothing.
func (r *recorder) capture(ctx context.Context, tx *sql.Tx, table string, cols []string, ids []int64) ([]savedRow, bool, error) {
	if r == nil || r.tooBig {
		return nil, false, nil
	}
	sel := "row_id"
	for _, c := range cols {
		sel += "," + c
	}
	q := "SELECT " + sel + " FROM " + table // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated
	var args []any
	if ids != nil {
		q += " WHERE row_id IN (" + placeholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	q += " ORDER BY row_id"
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var out []savedRow
	for rows.Next() {
		raw := make([]any, len(cols)+1)
		ptr := make([]any, len(raw))
		for i := range raw {
			ptr[i] = &raw[i]
		}
		if err = rows.Scan(ptr...); err != nil {
			return nil, false, err
		}
		id, _ := raw[0].(int64)
		sr := savedRow{ID: id, V: make([]sqlValue, len(cols))}
		n := 24
		for i := range cols {
			sr.V[i] = toSQLValue(raw[i+1])
			n += len(sr.V[i].V) + 12
		}
		if !r.spend(n) {
			return nil, false, nil
		}
		out = append(out, sr)
	}
	return out, true, rows.Err()
}

func colIDs(cols []catalogColumn) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.ID
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// record stores the inverse the recorder collected, in tx, bound to the
// revision the change produced, and returns what the receipt says about it.
// A change with no inverse, or one that was too large to keep, stores nothing.
func (s *Store) record(ctx context.Context, tx *sql.Tx, rec *recorder, opID string, before, after contract.Revision) (contract.UndoNote, error) {
	if rec == nil {
		return contract.UndoNote{Reason: "this change keeps no change set"}, nil
	}
	if rec.tooBig {
		return contract.UndoNote{Reason: "cannot be undone: the data it changed is too large to keep"}, nil
	}
	if !rec.have {
		return contract.UndoNote{Reason: "cannot be undone: this change keeps no change set"}, nil
	}
	b, err := json.Marshal(rec.inv)
	if err != nil {
		return contract.UndoNote{}, err
	}
	if int64(len(b)) > s.maxChange() {
		return contract.UndoNote{Reason: "cannot be undone: the data it changed is too large to keep"}, nil
	}
	summary := contract.CleanUndoText(rec.summary)
	if _, err = tx.ExecContext(ctx, "INSERT INTO tables_changes(operation_id,class,kind,summary,before_schema,before_data,after_schema,after_data,inverse,bytes,created_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		opID, rec.class, rec.kind, summary, before.Schema, before.Data, after.Schema, after.Data, string(b), len(b), s.now().UnixNano()); err != nil {
		return contract.UndoNote{}, capacityError(err)
	}
	if err = s.prune(ctx, tx); err != nil {
		return contract.UndoNote{}, err
	}
	return contract.UndoNote{ID: opID, Available: true, Summary: summary}, nil
}

// prune ends the life of old change sets: past the retention the inverse is
// dropped and the line stays, so the answer is "expired" and not "unknown";
// much later the line goes too. Over the bound of the database the oldest go
// first.
func (s *Store) prune(ctx context.Context, tx *sql.Tx) error {
	ret := s.retention()
	now := s.now()
	if _, err := tx.ExecContext(ctx, "UPDATE tables_changes SET inverse='', bytes=0 WHERE created_ns<? AND bytes>0 AND kind<>?", now.Add(-ret).UnixNano(), kindDatabaseCreate); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM tables_changes WHERE created_ns<? AND kind<>?", now.Add(-4*ret).UnixNano(), kindDatabaseCreate); err != nil {
		return err
	}
	for {
		var total int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(bytes),0) FROM tables_changes").Scan(&total); err != nil {
			return err
		}
		if total <= s.maxTotal() {
			return nil
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM tables_changes WHERE operation_id=(SELECT operation_id FROM tables_changes WHERE bytes>0 ORDER BY created_ns ASC LIMIT 1)")
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
	}
}

// withUndo adds the undo note to a receipt payload.
func withUndo(payload json.RawMessage, n contract.UndoNote) json.RawMessage {
	m := map[string]any{}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &m)
	}
	m["undo"] = n
	b, _ := json.Marshal(m)
	return b
}

type changeRow struct {
	ID, Class, Kind, Summary string
	Before, After            contract.Revision
	Inverse                  string
	CreatedNS                int64
	UndoneBy                 string
	Undone                   contract.Revision
	HasUndone                bool
}

func readChange(ctx context.Context, q rowQuery, id string) (changeRow, bool, error) {
	var c changeRow
	var by sql.NullString
	var us, ud sql.NullInt64
	err := q.QueryRowContext(ctx, "SELECT operation_id,class,kind,summary,before_schema,before_data,after_schema,after_data,inverse,created_ns,undone_by,undone_schema,undone_data FROM tables_changes WHERE operation_id=?", id).
		Scan(&c.ID, &c.Class, &c.Kind, &c.Summary, &c.Before.Schema, &c.Before.Data, &c.After.Schema, &c.After.Data, &c.Inverse, &c.CreatedNS, &by, &us, &ud)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	c.UndoneBy = by.String
	if us.Valid && ud.Valid {
		c.Undone, c.HasUndone = contract.Revision{Schema: uint64(us.Int64), Data: uint64(ud.Int64)}, true // #nosec G115 -- revisions are counters that start at 0 and never go below it
	}
	return c, true, nil
}

// canonical resolves a revision through the undos: the revision an undo
// produced has the state of the revision before the change it undid.
func canonical(ctx context.Context, q rowQuery, cur contract.Revision) (contract.Revision, error) {
	for i := 0; i < 256; i++ {
		var s, d uint64
		err := q.QueryRowContext(ctx, "SELECT before_schema,before_data FROM tables_changes WHERE undone_schema=? AND undone_data=? LIMIT 1", cur.Schema, cur.Data).Scan(&s, &d)
		if errors.Is(err, sql.ErrNoRows) {
			return cur, nil
		}
		if err != nil {
			return cur, err
		}
		cur = contract.Revision{Schema: s, Data: d}
	}
	return cur, nil
}

// describeLater says in words what happened to the database after a change,
// from the receipts of the later revisions.
func describeLater(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, after contract.Revision) string {
	rows, err := q.QueryContext(ctx, "SELECT payload FROM tables_schema_receipts WHERE schema_revision>? OR (schema_revision=? AND data_revision>?) ORDER BY schema_revision,data_revision LIMIT 4", after.Schema, after.Schema, after.Data)
	if err != nil {
		return "the database changed"
	}
	defer func() { _ = rows.Close() }()
	var parts []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) != nil {
			continue
		}
		var v struct {
			Kind    string `json:"kind"`
			Table   string `json:"table"`
			Column  string `json:"column"`
			NewName string `json:"newName"`
			Count   int    `json:"count"`
		}
		_ = json.Unmarshal([]byte(p), &v)
		parts = append(parts, laterWords(v.Kind, v.Table, v.Column, v.NewName, v.Count))
	}
	if len(parts) == 0 {
		return "the database changed"
	}
	return strings.Join(parts, "; ")
}

func laterWords(kind, table, column, newName string, n int) string {
	switch kind {
	case KindRowInsert:
		return fmt.Sprintf("inserted %s into %s", plural(n, "row", "rows"), table)
	case KindRowUpdate:
		return fmt.Sprintf("updated %s of %s", plural(n, "row", "rows"), table)
	case KindRowDelete:
		return fmt.Sprintf("deleted %s of %s", plural(n, "row", "rows"), table)
	case KindTableCreate:
		return fmt.Sprintf("created the table %s", table)
	case KindTableRename:
		return fmt.Sprintf("renamed the table %s to %s", table, newName)
	case KindTableDrop:
		return fmt.Sprintf("dropped the table %s", table)
	case KindColumnAdd:
		return fmt.Sprintf("added the column %s to %s", column, table)
	case KindColumnRename:
		return fmt.Sprintf("renamed the column %s of %s to %s", column, table, newName)
	case KindColumnDrop:
		return fmt.Sprintf("dropped the column %s of %s", column, table)
	case "undo":
		return "undid an earlier change"
	}
	return "changed the database"
}

// ChangeInfo is what the add-on says about one change set.
type ChangeInfo struct {
	contract.ChangeInfo
}

// Change reads the state of one change set. Only a change that is not known is
// an error; every other state is an answer.
func (s *Store) Change(ctx context.Context, resourceID, changeID string) (ChangeInfo, error) {
	if err := validID(resourceID); err != nil {
		return ChangeInfo{}, err
	}
	if err := validOperation(changeID); err != nil {
		return ChangeInfo{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.openDatabase(resourceID)
	if err != nil {
		return ChangeInfo{}, err
	}
	defer func() { _ = db.Close() }()
	m, err := readMeta(ctx, db)
	if err != nil {
		return ChangeInfo{}, err
	}
	if m.ResourceID != resourceID || m.Revision.Schema == 0 {
		return ChangeInfo{}, ErrUnsafePath
	}
	if ok, e := hasChanges(ctx, db); e != nil {
		return ChangeInfo{}, e
	} else if !ok {
		return ChangeInfo{}, ErrChangeNotFound
	}
	c, ok, err := readChange(ctx, db, changeID)
	if err != nil {
		return ChangeInfo{}, err
	}
	if !ok {
		return ChangeInfo{}, ErrChangeNotFound
	}
	info := ChangeInfo{ChangeInfo: contract.ChangeInfo{ID: c.ID, Class: c.Class, Summary: c.Summary, Current: m.Revision, Deletes: c.Kind == kindDatabaseCreate}}
	switch e := s.classify(ctx, db, m, c); {
	case e == nil:
		info.State = contract.ChangeAvailable
	case errors.Is(e, ErrChangeUndone):
		info.State = contract.ChangeUndone
	case errors.Is(e, ErrChangeExpired):
		info.State = contract.ChangeExpired
	default:
		var stale *ChangeStaleError
		if !errors.As(e, &stale) {
			return ChangeInfo{}, e
		}
		info.State, info.Detail = contract.ChangeStale, stale.Detail
	}
	return info, nil
}

func hasChanges(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tables_changes'").Scan(&n)
	return n == 1, err
}

// classify says whether the change set can be undone now: nil, or one of
// ErrChangeUndone, ErrChangeExpired and *ChangeStaleError.
func (s *Store) classify(ctx context.Context, q interface {
	rowQuery
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, m meta, c changeRow) error {
	if c.UndoneBy != "" {
		return ErrChangeUndone
	}
	if c.Kind != kindDatabaseCreate && (c.Inverse == "" || time.Unix(0, c.CreatedNS).Add(s.retention()).Before(s.now())) {
		return ErrChangeExpired
	}
	if c.Kind == kindDatabaseRename {
		var inv inverse
		if json.Unmarshal([]byte(c.Inverse), &inv) != nil {
			return ErrChangeExpired
		}
		if m.Name != inv.To {
			return &ChangeStaleError{Current: m.Revision, Detail: fmt.Sprintf("the database was renamed again, to %q", m.Name)}
		}
		return nil
	}
	canon, err := canonical(ctx, q, m.Revision)
	if err != nil {
		return err
	}
	if canon != c.After {
		return &ChangeStaleError{Current: m.Revision, Detail: "after this change the database " + describeLater(ctx, q, c.After)}
	}
	return nil
}

// UndoChange rolls one change set back as one more recorded change of the
// database, when the database is still at the revision the change produced and
// at the revision expected, the one the caller just read. The operation ID
// makes it idempotent: the same ID with the same change returns the same
// receipt and undoes once.
func (s *Store) UndoChange(ctx context.Context, resourceID, changeID, operationID string, expected contract.Revision) (contract.Receipt, error) {
	if err := validID(resourceID); err != nil {
		return contract.Receipt{}, err
	}
	if err := validOperation(operationID); err != nil {
		return contract.Receipt{}, err
	}
	if err := validOperation(changeID); err != nil {
		return contract.Receipt{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := "undo:" + changeID
	op, found, err := s.lookup(ctx, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	if found && (op.Kind != "undo" || op.Resource != resourceID || op.Name != hash) {
		return contract.Receipt{}, ErrConflict
	}
	db, err := s.openDatabase(resourceID)
	if err != nil {
		return contract.Receipt{}, err
	}
	defer func() { _ = db.Close() }()
	forget := func() {
		if !found {
			_, _ = s.ledger.ExecContext(context.WithoutCancel(ctx), "DELETE FROM operations WHERE operation_id=? AND state='pending'", operationID)
		}
	}
	if !found {
		if err = s.begin(ctx, opRow{ID: operationID, Kind: "undo", Resource: resourceID, Name: hash}); err != nil {
			return contract.Receipt{}, err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		forget()
		return contract.Receipt{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, catalogSchema); err != nil {
		forget()
		return contract.Receipt{}, capacityError(err)
	}
	if r, ok, e := readReceipt(ctx, tx, operationID); e != nil {
		return contract.Receipt{}, e
	} else if ok {
		if r.hash != hash {
			forget()
			return contract.Receipt{}, ErrConflict
		}
		_ = tx.Rollback()
		_ = s.finish(ctx, operationID, r.receipt.Revision)
		return r.receipt, nil
	}
	m, err := readMeta(ctx, tx)
	if err != nil {
		return contract.Receipt{}, err
	}
	if m.ResourceID != resourceID || m.Revision.Schema == 0 {
		forget()
		return contract.Receipt{}, ErrUnsafePath
	}
	c, ok, err := readChange(ctx, tx, changeID)
	if err != nil {
		return contract.Receipt{}, err
	}
	if !ok {
		forget()
		return contract.Receipt{}, ErrChangeNotFound
	}
	if c.Kind == kindDatabaseCreate {
		// The removal of the database is the host's own delete.
		forget()
		return contract.Receipt{}, ErrInvalidOperation
	}
	if e := s.classify(ctx, tx, m, c); e != nil {
		forget()
		return contract.Receipt{}, e
	}
	if expected != (contract.Revision{}) && expected != m.Revision {
		forget()
		return contract.Receipt{}, &ChangeStaleError{Current: m.Revision, Detail: "the database changed while the undo was prepared"}
	}
	var inv inverse
	if err = json.Unmarshal([]byte(c.Inverse), &inv); err != nil {
		forget()
		return contract.Receipt{}, ErrChangeExpired
	}
	if err = s.applyInverse(ctx, tx, inv); err != nil {
		if definiteInverseFailure(err) {
			forget()
		}
		return contract.Receipt{}, err
	}
	next := m.Revision
	if c.Kind != kindDatabaseRename {
		if c.Class == contract.ChangeClassSchema {
			next, err = bumpSchema(ctx, tx, m.Revision)
		} else {
			next, err = bumpData(ctx, tx, m.Revision)
		}
		if err != nil {
			return contract.Receipt{}, capacityError(err)
		}
	}
	us, ud := sql.NullInt64{}, sql.NullInt64{}
	if c.Kind != kindDatabaseRename {
		us, ud = sql.NullInt64{Int64: int64(next.Schema), Valid: true}, sql.NullInt64{Int64: int64(next.Data), Valid: true} // #nosec G115 -- revisions are counters far below the int64 limit
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tables_changes SET undone_by=?, undone_schema=?, undone_data=? WHERE operation_id=?", operationID, us, ud, changeID); err != nil {
		return contract.Receipt{}, capacityError(err)
	}
	payload, _ := json.Marshal(map[string]any{"resourceId": resourceID, "kind": "undo", "change": changeID, "undone": c.Kind, "summary": c.Summary})
	if c.Kind == kindDatabaseRename {
		// A rename never moved the revision, so the receipt of its undo is
		// written at the revision it has.
		next = m.Revision
	}
	if err = writeReceipt(ctx, tx, operationID, hash, next, payload); err != nil {
		return contract.Receipt{}, capacityError(err)
	}
	if err = s.prune(ctx, tx); err != nil {
		return contract.Receipt{}, err
	}
	if err = s.fail(FailBeforeCommit); err != nil {
		return contract.Receipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return contract.Receipt{}, capacityError(err)
	}
	if err = s.fail(FailAfterCommit); err != nil {
		return contract.Receipt{}, err
	}
	if err = s.finish(ctx, operationID, next); err != nil {
		return contract.Receipt{}, err
	}
	return contract.Receipt{OperationID: operationID, State: contract.ReceiptCommitted, Revision: next, Result: contract.Result{Payload: payload}}, nil
}

func definiteInverseFailure(err error) bool {
	for _, t := range []error{ErrRowConstraint, ErrInvalidSchema, ErrCapacity, ErrTableNotFound, ErrColumnNotFound, ErrSchemaExists} {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}

// applyInverse runs an inverse inside tx. The database is at the state the
// inverse was recorded for, so every step is expected to succeed; a failure
// rolls the whole undo back.
func (s *Store) applyInverse(ctx context.Context, tx *sql.Tx, inv inverse) error {
	switch inv.Op {
	case invDeleteRows:
		if len(inv.IDs) == 0 {
			return nil
		}
		args := make([]any, len(inv.IDs))
		for i, id := range inv.IDs {
			args[i] = id
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM "+inv.Table+" WHERE row_id IN ("+placeholders(len(args))+")", args...) // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated
		return err
	case invRestoreValues:
		sets := make([]string, len(inv.Cols))
		for i, c := range inv.Cols {
			sets[i] = c + "=?"
		}
		for _, r := range inv.Rows {
			args, err := valueArgs(r.V)
			if err != nil {
				return err
			}
			args = append(args, r.ID)
			if _, err = tx.ExecContext(ctx, "UPDATE "+inv.Table+" SET "+strings.Join(sets, ",")+" WHERE row_id=?", args...); err != nil { // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated
				return rowError(err, &catalogTable{})
			}
		}
		return nil
	case invReinsertRows:
		return insertSaved(ctx, tx, inv.Table, inv.Cols, inv.Rows)
	case invDropTable:
		if _, err := tx.ExecContext(ctx, "DROP TABLE "+inv.Table); err != nil { // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM tables_catalog_columns WHERE table_id=?", inv.Table); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM tables_catalog_tables WHERE table_id=?", inv.Table)
		return err
	case invRenameTable:
		_, err := tx.ExecContext(ctx, "UPDATE tables_catalog_tables SET name=?, name_key=? WHERE table_id=?", inv.Name, labelKey(inv.Name), inv.Table)
		return err
	case invRenameColumn:
		_, err := tx.ExecContext(ctx, "UPDATE tables_catalog_columns SET name=?, name_key=? WHERE table_id=? AND column_id=?", inv.Name, labelKey(inv.Name), inv.Table, inv.Column)
		return err
	case invRenameDatabase:
		_, err := tx.ExecContext(ctx, "UPDATE tables_meta SET value=? WHERE key='name'", inv.Name)
		return err
	case invRestoreTable:
		return s.restoreTable(ctx, tx, inv)
	case invDropColumn:
		cat, err := readCatalog(ctx, tx)
		if err != nil {
			return err
		}
		for i := range cat {
			if cat[i].ID != inv.Table {
				continue
			}
			var keep []catalogColumn
			for _, k := range cat[i].cols {
				if k.ID != inv.Column {
					keep = append(keep, k)
				}
			}
			if err = s.rebuild(ctx, tx, inv.Table, keep); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "DELETE FROM tables_catalog_columns WHERE table_id=? AND column_id=?", inv.Table, inv.Column)
			return err
		}
		return ErrTableNotFound
	case invRestoreColumn:
		return s.restoreColumn(ctx, tx, inv)
	}
	return ErrInvalidOperation
}

func valueArgs(v []sqlValue) ([]any, error) {
	out := make([]any, 0, len(v)+1)
	for _, x := range v {
		a, err := x.arg()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func insertSaved(ctx context.Context, tx *sql.Tx, table string, cols []string, rows []savedRow) error {
	names := append([]string{"row_id"}, cols...)
	q := "INSERT INTO " + table + "(" + strings.Join(names, ",") + ") VALUES(" + placeholders(len(names)) + ")" // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated
	for _, r := range rows {
		args, err := valueArgs(r.V)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, q, append([]any{r.ID}, args...)...); err != nil {
			return rowError(err, &catalogTable{})
		}
	}
	return nil
}

func (s *Store) restoreTable(ctx context.Context, tx *sql.Tx, inv inverse) error {
	defs := []string{"row_id INTEGER PRIMARY KEY AUTOINCREMENT"}
	cols := make([]string, 0, len(inv.Defs))
	sort.SliceStable(inv.Defs, func(i, j int) bool { return inv.Defs[i].Position < inv.Defs[j].Position })
	for _, d := range inv.Defs {
		ddl, err := columnDDL(catalogColumn{ColumnSchema: d.ColumnSchema})
		if err != nil {
			return err
		}
		defs = append(defs, ddl)
		cols = append(cols, d.ID)
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE "+inv.Table+" ("+strings.Join(defs, ", ")+")"); err != nil { // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated
		return capacityError(err)
	}
	for _, d := range inv.Defs {
		if d.Unique {
			if _, err := tx.ExecContext(ctx, "CREATE UNIQUE INDEX u_"+d.ID+" ON "+inv.Table+"("+d.ID+")"); err != nil { // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated
				return capacityError(err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO tables_catalog_tables(table_id,name,name_key,position) VALUES(?,?,?,?)", inv.Table, inv.Name, labelKey(inv.Name), inv.Pos); err != nil {
		return capacityError(err)
	}
	for _, d := range inv.Defs {
		var def any
		if len(d.Default) > 0 {
			def = string(d.Default)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO tables_catalog_columns(table_id,column_id,name,name_key,type,required,uniq,default_json,position) VALUES(?,?,?,?,?,?,?,?,?)",
			inv.Table, d.ID, d.Name, labelKey(d.Name), d.Type, b2i(d.Required), b2i(d.Unique), def, d.Position); err != nil {
			return capacityError(err)
		}
	}
	if err := insertSaved(ctx, tx, inv.Table, cols, inv.Rows); err != nil {
		return err
	}
	if inv.Seq != nil {
		res, err := tx.ExecContext(ctx, "UPDATE sqlite_sequence SET seq=MAX(seq,?) WHERE name=?", *inv.Seq, inv.Table)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err = tx.ExecContext(ctx, "INSERT INTO sqlite_sequence(name,seq) VALUES(?,?)", inv.Table, *inv.Seq); err != nil {
				return err
			}
		}
	}
	return nil
}

// restoreColumn puts a dropped column back under its own identifier with its
// values. The column first comes back without its rules, so that rows without
// a value yet do not break a required column; the table is then rebuilt with
// the real definition, which also recreates the unique index.
func (s *Store) restoreColumn(ctx context.Context, tx *sql.Tx, inv inverse) error {
	cat, err := readCatalog(ctx, tx)
	if err != nil {
		return err
	}
	var tbl *catalogTable
	for i := range cat {
		if cat[i].ID == inv.Table {
			tbl = &cat[i]
		}
	}
	if tbl == nil || inv.Def == nil {
		return ErrTableNotFound
	}
	d := inv.Def
	loose := catalogColumn{ColumnSchema: ColumnSchema{ID: d.ID, Name: d.Name, Type: d.Type}}
	if err = s.rebuild(ctx, tx, tbl.ID, append(append([]catalogColumn{}, tbl.cols...), loose)); err != nil {
		return err
	}
	for _, r := range inv.Rows {
		args, err := valueArgs(r.V)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE "+tbl.ID+" SET "+d.ID+"=? WHERE row_id=?", append(args, r.ID)...); err != nil { // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated
			return rowError(err, tbl)
		}
	}
	var def any
	if len(d.Default) > 0 {
		def = string(d.Default)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO tables_catalog_columns(table_id,column_id,name,name_key,type,required,uniq,default_json,position) VALUES(?,?,?,?,?,?,?,?,?)",
		tbl.ID, d.ID, d.Name, labelKey(d.Name), d.Type, b2i(d.Required), b2i(d.Unique), def, d.Position); err != nil {
		return capacityError(err)
	}
	full := catalogColumn{ColumnSchema: d.ColumnSchema}
	all := make([]catalogColumn, 0, len(tbl.cols)+1)
	all = append(all, tbl.cols...)
	all = append(all, full)
	return s.rebuild(ctx, tx, tbl.ID, all)
}

// captureTable records what restoring a dropped table takes: its definition,
// its position, its row counter and every row. A table with more data than the
// bound keeps nothing.
func (s *Store) captureTable(ctx context.Context, tx *sql.Tx, tbl *catalogTable, rec *recorder) error {
	if rec == nil || rec.tooBig {
		return nil
	}
	var pos int
	if err := tx.QueryRowContext(ctx, "SELECT position FROM tables_catalog_tables WHERE table_id=?", tbl.ID).Scan(&pos); err != nil {
		return err
	}
	defs := make([]savedColumn, 0, len(tbl.cols))
	for i, c := range tbl.cols {
		var cp int
		if err := tx.QueryRowContext(ctx, "SELECT position FROM tables_catalog_columns WHERE table_id=? AND column_id=?", tbl.ID, c.ID).Scan(&cp); err != nil {
			cp = i + 1
		}
		defs = append(defs, savedColumn{ColumnSchema: c.ColumnSchema, Position: cp})
		rec.spend(len(c.Name) + len(c.Default) + 64)
	}
	rows, ok, err := rec.capture(ctx, tx, tbl.ID, colIDs(tbl.cols), nil)
	if err != nil || !ok {
		return err
	}
	var seq sql.NullInt64
	if err = tx.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name=?", tbl.ID).Scan(&seq); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	inv := inverse{Op: invRestoreTable, Table: tbl.ID, Name: tbl.Name, Pos: pos, Defs: defs, Cols: colIDs(tbl.cols), Rows: rows}
	if seq.Valid {
		inv.Seq = &seq.Int64
	}
	rec.set(inv, fmt.Sprintf("Restore the table %s with %s", tbl.Name, plural(len(rows), "row", "rows")))
	return nil
}

// captureColumn records what restoring a dropped column takes: its definition
// and the values it held. Rows with no value in it are not stored.
func (s *Store) captureColumn(ctx context.Context, tx *sql.Tx, tbl *catalogTable, col *catalogColumn, values int64, rec *recorder) error {
	if rec == nil || rec.tooBig {
		return nil
	}
	var cp int
	if err := tx.QueryRowContext(ctx, "SELECT position FROM tables_catalog_columns WHERE table_id=? AND column_id=?", tbl.ID, col.ID).Scan(&cp); err != nil {
		return err
	}
	rows, ok, err := rec.capture(ctx, tx, tbl.ID, []string{col.ID}, nil)
	if err != nil || !ok {
		return err
	}
	kept := rows[:0]
	for _, r := range rows {
		if r.V[0].K != "" {
			kept = append(kept, r)
		}
	}
	rec.set(inverse{Op: invRestoreColumn, Table: tbl.ID, Def: &savedColumn{ColumnSchema: col.ColumnSchema, Position: cp}, Rows: kept},
		fmt.Sprintf("Restore the column %s of %s with %s", col.Name, tbl.Name, plural(len(kept), "value", "values")))
	return nil
}

// decorate is the receipt of an operation of the ledger, with what it says
// about its undo for the two kinds that have no stored payload: the creation
// and the rename of a database.
func (s *Store) decorate(ctx context.Context, op opRow) contract.Receipt {
	rc := op.receipt()
	if rc.State != contract.ReceiptCommitted || (op.Kind != "create" && op.Kind != "rename") {
		return rc
	}
	note := contract.UndoNote{Reason: "cannot be undone: this change keeps no change set"}
	if db, err := s.openDatabase(op.Resource); err == nil {
		defer func() { _ = db.Close() }()
		if ok, e := hasChanges(ctx, db); e == nil && ok {
			if c, found, e := readChange(ctx, db, op.ID); e == nil && found && (c.Inverse != "" || c.Kind == kindDatabaseCreate) {
				note = contract.UndoNote{ID: op.ID, Available: true, Summary: c.Summary}
			}
		}
	}
	rc.Result.Payload = withUndo(rc.Result.Payload, note)
	return rc
}
