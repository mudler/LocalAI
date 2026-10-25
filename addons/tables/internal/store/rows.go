// SPDX-License-Identifier: MIT
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Row mutation kinds.
const (
	KindRowInsert = "row-insert"
	KindRowUpdate = "row-update"
	KindRowDelete = "row-delete"
)

// Finite limits of one row mutation.
const (
	MaxInsertRows   = 100
	MaxAffectedRows = 1000
)

// RowChange is one typed row operation on one table. Rows and Set map column
// labels to JSON values. Update and delete name their rows either by explicit
// IDs or by a filter, never both and never neither: there is no way to touch
// every row without saying so with a filter that matches them, and a change
// that would touch more than MaxAffectedRows is refused.
type RowChange struct {
	ResourceID string                       `json:"resourceId"`
	Kind       string                       `json:"kind"`
	Table      string                       `json:"table"`
	Rows       []map[string]json.RawMessage `json:"rows,omitempty"`
	Set        map[string]json.RawMessage   `json:"set,omitempty"`
	IDs        []int64                      `json:"ids,omitempty"`
	Filter     *Filter                      `json:"filter,omitempty"`
}

func badRows(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRows, fmt.Sprintf(format, a...))
}

func (c RowChange) validate() error {
	if err := validID(c.ResourceID); err != nil {
		return err
	}
	if err := validLabel(c.Table); err != nil {
		return badRows("table name is not valid")
	}
	switch c.Kind {
	case KindRowInsert:
		if len(c.Rows) == 0 || len(c.Rows) > MaxInsertRows {
			return badRows("insert takes 1 to %d rows", MaxInsertRows)
		}
		if c.Set != nil || c.IDs != nil || c.Filter != nil {
			return badRows("insert takes only rows")
		}
	case KindRowUpdate, KindRowDelete:
		if c.Rows != nil {
			return badRows("%s does not take rows", c.Kind)
		}
		if c.Kind == KindRowUpdate && len(c.Set) == 0 {
			return badRows("update needs at least one column in set")
		}
		if c.Kind == KindRowDelete && c.Set != nil {
			return badRows("delete does not take set")
		}
		if (len(c.IDs) > 0) == (c.Filter != nil) {
			return badRows("name the rows by exactly one of ids or filter")
		}
		if len(c.IDs) > MaxAffectedRows {
			return fmt.Errorf("%w: at most %d ids", ErrTooManyRows, MaxAffectedRows)
		}
		seen := map[int64]bool{}
		for _, id := range c.IDs {
			if id <= 0 || seen[id] {
				return badRows("ids are distinct positive integers")
			}
			seen[id] = true
		}
	default:
		return badRows("unknown row operation")
	}
	return nil
}

// fingerprint is the canonical identity of a request: map keys are sorted by
// the encoder and ids are sorted, so the same request in another spelling
// is the same request.
func (c RowChange) fingerprint() string {
	n := c
	trim := func(m map[string]json.RawMessage) map[string]json.RawMessage {
		if m == nil {
			return nil
		}
		o := make(map[string]json.RawMessage, len(m))
		for k, v := range m {
			var b bytes.Buffer
			t := bytes.TrimSpace(v)
			if json.Compact(&b, t) == nil {
				t = b.Bytes()
			}
			o[labelKey(k)] = append(json.RawMessage(nil), t...)
		}
		return o
	}
	n.Set = trim(c.Set)
	n.Rows = nil
	for _, r := range c.Rows {
		n.Rows = append(n.Rows, trim(r))
	}
	n.IDs = append([]int64(nil), c.IDs...)
	sort.Slice(n.IDs, func(i, j int) bool { return n.IDs[i] < n.IDs[j] })
	n.Table = labelKey(c.Table)
	b, _ := json.Marshal(n)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type rowOutcome struct {
	action   string
	table    string
	count    int
	ids      []int64
	hash     string
	sample   []map[string]any
	inserted []int64
}

func idsHash(ids []int64) string {
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(strconv.FormatInt(id, 10) + ","))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// assign resolves a label to a column and encodes its value.
func assign(tbl *catalogTable, label string, raw json.RawMessage) (catalogColumn, any, error) {
	if labelKey(label) == RowIDLabel {
		return catalogColumn{}, nil, badRows("the row id cannot be written")
	}
	if validLabel(label) != nil {
		return catalogColumn{}, nil, fmt.Errorf("%w: %q", ErrColumnNotFound, label)
	}
	col := tbl.column(label)
	if col == nil {
		return catalogColumn{}, nil, fmt.Errorf("%w: %q", ErrColumnNotFound, label)
	}
	v, err := encodeValue(col.Type, raw)
	if err != nil {
		return *col, nil, badRows("column %q: %v", col.Name, err)
	}
	if v == nil && col.Required {
		return *col, nil, fmt.Errorf("%w: column %q is required", ErrRowConstraint, col.Name)
	}
	return *col, v, nil
}

var constraintColumn = regexp.MustCompile(`\.(c_[0-9a-f]+)`)

// rowError types a constraint failure with the label of the column, never the
// internal identifier.
func rowError(err error, tbl *catalogTable) error {
	if err = capacityError(err); errors.Is(err, ErrCapacity) {
		return err
	}
	var se sqlite3.Error
	if errors.As(err, &se) && se.Code == sqlite3.ErrConstraint {
		label := ""
		if m := constraintColumn.FindStringSubmatch(err.Error()); m != nil {
			for _, c := range tbl.cols {
				if c.ID == m[1] {
					label = c.Name
				}
			}
		}
		switch se.ExtendedCode {
		case sqlite3.ErrConstraintUnique:
			return fmt.Errorf("%w: the value of column %q is already used by another row", ErrRowConstraint, label)
		case sqlite3.ErrConstraintNotNull:
			return fmt.Errorf("%w: column %q is required", ErrRowConstraint, label)
		}
		return fmt.Errorf("%w: a column rule failed", ErrRowConstraint)
	}
	return mapQueryError(err)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// affected resolves the rows of an update or delete against the transaction's
// view: explicit ids must all exist; a filter is bounded. The result is sorted.
func affected(ctx context.Context, tx *sql.Tx, tbl *catalogTable, c RowChange) ([]int64, error) {
	var q string
	var args []any
	if len(c.IDs) > 0 {
		q = "SELECT row_id FROM " + tbl.ID + " WHERE row_id IN (" + placeholders(len(c.IDs)) + ") ORDER BY row_id"
		for _, id := range c.IDs {
			args = append(args, id)
		}
	} else {
		comp := &compiler{tbl: tbl}
		where, err := comp.where(c.Filter)
		if err != nil {
			return nil, badRows("%v", err)
		}
		q = "SELECT row_id FROM " + tbl.ID + where + " ORDER BY row_id LIMIT ?"
		args = append(comp.args, MaxAffectedRows+1)
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > MaxAffectedRows {
		return nil, fmt.Errorf("%w: the filter matches more than %d rows", ErrTooManyRows, MaxAffectedRows)
	}
	if len(c.IDs) > 0 && len(ids) != len(c.IDs) {
		return nil, fmt.Errorf("%w: %d of %d ids do not exist", ErrRowNotFound, len(c.IDs)-len(ids), len(c.IDs))
	}
	if len(ids) == 0 {
		return nil, ErrNoRows
	}
	return ids, nil
}

// snapshot reads the given columns of the first rows of ids for a preview.
func snapshot(ctx context.Context, tx *sql.Tx, tbl *catalogTable, ids []int64, cols []catalogColumn) ([]map[string]any, error) {
	if len(ids) > sampleRowsLimit {
		ids = ids[:sampleRowsLimit]
	}
	sel := "row_id"
	for _, c := range cols {
		sel += "," + c.ID
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+sel+" FROM "+tbl.ID+" WHERE row_id IN ("+placeholders(len(ids))+") ORDER BY row_id", args...) // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated; values are bound parameters
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []map[string]any{}
	for rows.Next() {
		raw := make([]any, len(cols)+1)
		ptr := make([]any, len(raw))
		for i := range raw {
			ptr[i] = &raw[i]
		}
		if err = rows.Scan(ptr...); err != nil {
			return nil, err
		}
		v := map[string]any{}
		for i, c := range cols {
			v[c.Name] = decodeValue(c.Type, raw[i+1])
		}
		id, _ := raw[0].(int64)
		out = append(out, map[string]any{"id": id, "values": v})
	}
	return out, rows.Err()
}

func jsonValue(typ string, v any) any { return decodeValue(typ, v) }

// runRows executes a change inside tx and returns what it did. The caller
// commits or rolls back.
func (s *Store) runRows(ctx context.Context, tx *sql.Tx, cat []catalogTable, c RowChange) (rowOutcome, error) {
	tbl, err := findTable(cat, c.Table)
	if err != nil {
		return rowOutcome{}, err
	}
	out := rowOutcome{table: tbl.Name}
	switch c.Kind {
	case KindRowInsert:
		out.action = "insert rows"
		for _, in := range c.Rows {
			var names []string
			var args []any
			used := map[string]bool{}
			labels := make([]string, 0, len(in))
			for l := range in {
				labels = append(labels, l)
			}
			sort.Strings(labels)
			for _, l := range labels {
				col, v, err := assign(tbl, l, in[l])
				if err != nil {
					return out, err
				}
				if used[col.ID] {
					return out, badRows("column %q is given twice", col.Name)
				}
				used[col.ID] = true
				names = append(names, col.ID)
				args = append(args, v)
			}
			for _, col := range tbl.cols {
				if col.Required && !used[col.ID] && len(col.Default) == 0 {
					return out, fmt.Errorf("%w: column %q is required", ErrRowConstraint, col.Name)
				}
			}
			var res sql.Result
			if len(names) == 0 {
				res, err = tx.ExecContext(ctx, "INSERT INTO "+tbl.ID+" DEFAULT VALUES") // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated; values are bound parameters
			} else {
				res, err = tx.ExecContext(ctx, "INSERT INTO "+tbl.ID+"("+strings.Join(names, ",")+") VALUES("+placeholders(len(names))+")", args...) // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated; values are bound parameters
			}
			if err != nil {
				return out, rowError(err, tbl)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return out, err
			}
			out.inserted = append(out.inserted, id)
			out.count++
		}
		sort.Slice(out.inserted, func(i, j int) bool { return out.inserted[i] < out.inserted[j] })
		out.ids = out.inserted
		out.hash = idsHash(out.ids)
		for i, in := range c.Rows {
			if i == sampleRowsLimit {
				break
			}
			v := map[string]any{}
			for l, raw := range in {
				col := tbl.column(l)
				x, _ := encodeValue(col.Type, raw)
				v[col.Name] = jsonValue(col.Type, x)
			}
			out.sample = append(out.sample, map[string]any{"values": v})
		}
		return out, nil
	case KindRowUpdate:
		out.action = "update rows"
		ids, err := affected(ctx, tx, tbl, c)
		if err != nil {
			return out, err
		}
		var sets []string
		var args []any
		var cols []catalogColumn
		labels := make([]string, 0, len(c.Set))
		for l := range c.Set {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		used := map[string]bool{}
		for _, l := range labels {
			col, v, err := assign(tbl, l, c.Set[l])
			if err != nil {
				return out, err
			}
			if used[col.ID] {
				return out, badRows("column %q is given twice", col.Name)
			}
			used[col.ID] = true
			cols = append(cols, col)
			sets = append(sets, col.ID+"=?")
			args = append(args, v)
		}
		before, err := snapshot(ctx, tx, tbl, ids, cols)
		if err != nil {
			return out, err
		}
		for _, id := range ids {
			args = append(args, id)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE "+tbl.ID+" SET "+strings.Join(sets, ",")+" WHERE row_id IN ("+placeholders(len(ids))+")", args...); err != nil { // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated; values are bound parameters
			return out, rowError(err, tbl)
		}
		after := map[string]any{}
		for _, col := range cols {
			v, _ := encodeValue(col.Type, c.Set[findLabel(c.Set, col.Name)])
			after[col.Name] = jsonValue(col.Type, v)
		}
		for _, b := range before {
			out.sample = append(out.sample, map[string]any{"id": b["id"], "before": b["values"], "after": after})
		}
		out.ids, out.count, out.hash = ids, len(ids), idsHash(ids)
		return out, nil
	case KindRowDelete:
		out.action = "delete rows"
		ids, err := affected(ctx, tx, tbl, c)
		if err != nil {
			return out, err
		}
		before, err := snapshot(ctx, tx, tbl, ids, allColumns(tbl))
		if err != nil {
			return out, err
		}
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+tbl.ID+" WHERE row_id IN ("+placeholders(len(ids))+")", args...); err != nil { // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated; values are bound parameters
			return out, rowError(err, tbl)
		}
		out.sample = before
		out.ids, out.count, out.hash = ids, len(ids), idsHash(ids)
		return out, nil
	}
	return out, badRows("unknown row operation")
}

func allColumns(t *catalogTable) []catalogColumn {
	out := make([]catalogColumn, len(t.cols))
	for i, c := range t.cols {
		out[i] = c
	}
	return out
}

func findLabel(m map[string]json.RawMessage, name string) string {
	k := labelKey(name)
	for l := range m {
		if labelKey(l) == k {
			return l
		}
	}
	return name
}

func rowSummary(o rowOutcome) json.RawMessage {
	loss := 0
	if strings.HasPrefix(o.action, "delete") {
		loss = o.count
	}
	if o.sample == nil {
		o.sample = []map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{
		"count":        o.count,
		"action":       o.action,
		"table":        o.table,
		"sample":       o.sample,
		"affectedHash": o.hash,
		"dataLoss":     map[string]int{"rows": loss, "columns": 0, "tables": 0},
	})
	return b
}

func rowPayload(c RowChange, o rowOutcome) json.RawMessage {
	ids := o.ids
	if ids == nil {
		ids = []int64{}
	}
	b, _ := json.Marshal(map[string]any{"resourceId": c.ResourceID, "kind": c.Kind, "table": o.table, "count": o.count, "ids": ids, "affectedHash": o.hash})
	return b
}

// PreviewRows reports the exact effects of a change: the count, a bounded
// sample and the revision it is valid for. The change runs in a transaction
// that is rolled back, so a preview accepts a change only when apply would at
// the same revision, constraint conflicts included, and changes nothing.
func (s *Store) PreviewRows(ctx context.Context, change RowChange) (contract.Preview, error) {
	if err := change.validate(); err != nil {
		return contract.Preview{}, err
	}
	var out contract.Preview
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.openDatabase(change.ResourceID)
	if err != nil {
		return out, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	m, err := readMeta(ctx, tx)
	if err != nil {
		return out, err
	}
	if m.ResourceID != change.ResourceID || m.Revision.Schema == 0 {
		return out, ErrUnsafePath
	}
	cat, err := readCatalog(ctx, tx)
	if err != nil {
		return out, err
	}
	o, err := s.runRows(ctx, tx, cat, change)
	if err != nil {
		return out, err
	}
	return contract.Preview{Revision: m.Revision, Summary: rowSummary(o)}, nil
}

// ApplyRows applies one change when the database is still at the expected
// revision, both schema and data. The revision is per database: any committed
// row or schema change moves it, so a proposal whose affected rows could have
// changed is stale and must be previewed again. A filter is therefore never
// evaluated against rows that appeared after the preview. The change, the
// data revision step and the operation receipt commit in one transaction.
func (s *Store) ApplyRows(ctx context.Context, change RowChange, expected contract.Revision, operationID string) (contract.Receipt, error) {
	if err := validOperation(operationID); err != nil {
		return contract.Receipt{}, err
	}
	if err := change.validate(); err != nil {
		return contract.Receipt{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := change.fingerprint()
	op, found, err := s.lookup(ctx, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	if found && (op.Kind != "rows" || op.Resource != change.ResourceID || op.Name != hash) {
		return contract.Receipt{}, ErrConflict
	}
	db, err := s.openDatabase(change.ResourceID)
	if err != nil {
		return contract.Receipt{}, err
	}
	defer func() { _ = db.Close() }()
	if !found {
		if err = s.begin(ctx, opRow{ID: operationID, Kind: "rows", Resource: change.ResourceID, Name: hash}); err != nil {
			return contract.Receipt{}, err
		}
	}
	forget := func() {
		if !found {
			_, _ = s.ledger.ExecContext(context.WithoutCancel(ctx), "DELETE FROM operations WHERE operation_id=? AND state='pending'", operationID)
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
	if m.ResourceID != change.ResourceID || m.Revision.Schema == 0 {
		forget()
		return contract.Receipt{}, ErrUnsafePath
	}
	if m.Revision != expected {
		forget()
		return contract.Receipt{}, &StaleError{Current: m.Revision}
	}
	cat, err := readCatalog(ctx, tx)
	if err != nil {
		return contract.Receipt{}, err
	}
	o, err := s.runRows(ctx, tx, cat, change)
	if err != nil {
		if definiteRowFailure(err) {
			forget()
		}
		return contract.Receipt{}, err
	}
	next, err := bumpData(ctx, tx, m.Revision)
	if err != nil {
		return contract.Receipt{}, capacityError(err)
	}
	payload := rowPayload(change, o)
	if err = writeReceipt(ctx, tx, operationID, hash, next, payload); err != nil {
		return contract.Receipt{}, capacityError(err)
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

// definiteRowFailure reports an error that proves the change did not and
// cannot commit, so its operation ID is free for a corrected request.
func definiteRowFailure(err error) bool {
	for _, t := range []error{ErrInvalidRows, ErrInvalidQuery, ErrRowConstraint, ErrTooManyRows, ErrNoRows, ErrRowNotFound,
		ErrTableNotFound, ErrColumnNotFound, ErrCapacity, ErrArithmetic} {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}
