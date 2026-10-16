// SPDX-License-Identifier: MIT
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Finite limits of row queries and mutations.
const (
	// RowIDLabel names the stable row identifier in filters and sorts. No
	// column may carry this label.
	RowIDLabel      = "_id"
	MaxFilterDepth  = 8
	MaxFilterNodes  = 64
	MaxInValues     = 100
	MaxSortKeys     = 4
	DefaultPage     = 50
	MaxPage         = 500
	MaxOffset       = 100000
	MaxAggregates   = 8
	MaxGroups       = 500
	MaxResultBytes  = 512 << 10
	MaxTextBytes    = 64 << 10
	MaxFilterText   = 1024
	maxSafeInteger  = 1 << 53
	rowIDColumn     = "row_id"
	cursorVersion   = 1
	likeEscapeChar  = `\`
	sampleRowsLimit = 10
)

var (
	ErrInvalidQuery  = errors.New("invalid query")
	ErrInvalidRows   = errors.New("invalid row change")
	ErrRowConstraint = errors.New("row violates a column rule")
	ErrTooManyRows   = errors.New("too many rows; narrow the change")
	ErrNoRows        = errors.New("no row matches")
	ErrRowNotFound   = errors.New("row not found")
	ErrStaleCursor   = errors.New("cursor is no longer valid")
	ErrArithmetic    = errors.New("aggregate overflowed")
)

func badQuery(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidQuery, fmt.Sprintf(format, a...))
}

// Filter is a bounded tree. A node is exactly one of: And (all children),
// Or (any child) or a leaf (Column, Op and its operand). Comparisons never
// match NULL: use is-null / is-not-null for that. Text operators are ASCII
// case-insensitive; eq/ne/lt/... on text are case-sensitive and binary.
type Filter struct {
	And    []Filter          `json:"and,omitempty"`
	Or     []Filter          `json:"or,omitempty"`
	Column string            `json:"column,omitempty"`
	Op     string            `json:"op,omitempty"`
	Value  json.RawMessage   `json:"value,omitempty"`
	Values []json.RawMessage `json:"values,omitempty"`
}

// Filter operators.
const (
	OpEq         = "eq"
	OpNe         = "ne"
	OpLt         = "lt"
	OpLte        = "lte"
	OpGt         = "gt"
	OpGte        = "gte"
	OpIn         = "in"
	OpIsNull     = "is-null"
	OpIsNotNull  = "is-not-null"
	OpContains   = "contains"
	OpStartsWith = "starts-with"
	OpEndsWith   = "ends-with"
)

type SortKey struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
}

// Query selects rows of one table. Columns are labels; empty selects all.
// Rows are ordered by Sort and then by row ID, so a page boundary is stable.
type Query struct {
	Table   string    `json:"table"`
	Columns []string  `json:"columns,omitempty"`
	Filter  *Filter   `json:"filter,omitempty"`
	Sort    []SortKey `json:"sort,omitempty"`
	Limit   int       `json:"limit,omitempty"`
	Cursor  string    `json:"cursor,omitempty"`
}

type Row struct {
	ID     int64 `json:"id"`
	Values []any `json:"values"`
}

// RowPage is one page. Cursor is set exactly when HasMore is true; it is only
// valid while the database revision is the one of this page.
type RowPage struct {
	Table    string            `json:"table"`
	Columns  []string          `json:"columns"`
	Rows     []Row             `json:"rows"`
	HasMore  bool              `json:"hasMore"`
	Cursor   string            `json:"cursor,omitempty"`
	Revision contract.Revision `json:"revision"`
}

type Aggregation struct {
	Fn     string `json:"fn"`
	Column string `json:"column,omitempty"`
}

// AggregateQuery computes count, sum, avg, min and max over the filtered rows,
// optionally grouped by one column.
type AggregateQuery struct {
	Table      string        `json:"table"`
	Filter     *Filter       `json:"filter,omitempty"`
	Aggregates []Aggregation `json:"aggregates"`
	GroupBy    string        `json:"groupBy,omitempty"`
	Limit      int           `json:"limit,omitempty"`
}

type AggregateGroup struct {
	// Group holds the single group-by value (null is a group of its own) and
	// is absent for an ungrouped query.
	Group  []any `json:"group,omitempty"`
	Values []any `json:"values"`
}

type AggregateResult struct {
	Table      string            `json:"table"`
	Aggregates []string          `json:"aggregates"`
	Groups     []AggregateGroup  `json:"groups"`
	Truncated  bool              `json:"truncated"`
	Revision   contract.Revision `json:"revision"`
}

// ---- typed values ----

// isNull reports a JSON null or an absent value.
func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

func plainInteger(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	i := 0
	if s[0] == '-' {
		i = 1
	}
	if i == len(s) {
		return false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// encodeValue turns a JSON value into the Go value that is bound to a
// statement. NULL is returned as nil without error; callers decide whether
// NULL is allowed.
func encodeValue(typ string, raw json.RawMessage) (any, error) {
	raw = bytes.TrimSpace(raw)
	if isNull(raw) {
		return nil, nil
	}
	switch typ {
	case TypeText:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, errors.New("must be a string")
		}
		if len(s) > MaxTextBytes {
			return nil, fmt.Errorf("text is longer than %d bytes", MaxTextBytes)
		}
		if !utf8.ValidString(s) {
			return nil, errors.New("text must be valid UTF-8")
		}
		for _, r := range s {
			if r == utf8.RuneError || (unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r') {
				return nil, errors.New("text cannot hold control or invalid characters")
			}
		}
		return s, nil
	case TypeInteger:
		str := string(raw)
		if raw[0] == '"' {
			if err := json.Unmarshal(raw, &str); err != nil {
				return nil, errors.New("must be an integer")
			}
		}
		if !plainInteger(str) {
			return nil, errors.New("must be a 64-bit integer, as a JSON integer or a decimal string")
		}
		i, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			return nil, errors.New("must be a 64-bit integer")
		}
		return i, nil
	case TypeReal:
		if raw[0] == '"' {
			return nil, errors.New("must be a number")
		}
		f, err := strconv.ParseFloat(string(raw), 64)
		if err != nil || f != f || f > 1.7976931348623157e308 || f < -1.7976931348623157e308 {
			return nil, errors.New("must be a finite number")
		}
		return f, nil
	case TypeBoolean:
		switch string(raw) {
		case "true":
			return int64(1), nil
		case "false":
			return int64(0), nil
		}
		return nil, errors.New("must be true or false")
	case TypeDateTime:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, errors.New("must be an RFC 3339 time string")
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, errors.New("must be an RFC 3339 time")
		}
		return t.UTC().Format(timeLayout), nil
	}
	return nil, errors.New("unknown column type")
}

// decodeValue is the JSON form of a stored value. Integers outside the range
// a JSON number holds exactly (2^53) are decimal strings.
func decodeValue(typ string, v any) any {
	if v == nil {
		return nil
	}
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	switch typ {
	case TypeText:
		if s, ok := v.(string); ok {
			return s
		}
	case TypeInteger:
		if i, ok := v.(int64); ok {
			if i > maxSafeInteger || i < -maxSafeInteger {
				return strconv.FormatInt(i, 10)
			}
			return i
		}
		if f, ok := v.(float64); ok {
			return decodeValue(typ, int64(f))
		}
	case TypeReal:
		switch x := v.(type) {
		case float64:
			return x
		case int64:
			return float64(x)
		}
	case TypeBoolean:
		if i, ok := v.(int64); ok {
			return i != 0
		}
	case TypeDateTime:
		if s, ok := v.(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t.UTC().Format(time.RFC3339Nano)
			}
			return s
		}
	}
	return v
}

// ---- references and the filter compiler ----

// colRef is a column the SQL may name: only ever an internal identifier.
type colRef struct {
	sql   string
	typ   string
	label string
}

func (t *catalogTable) ref(label string, allowID bool) (colRef, error) {
	if labelKey(label) == RowIDLabel {
		if !allowID {
			return colRef{}, badQuery("%q is always returned as the row id", RowIDLabel)
		}
		return colRef{sql: rowIDColumn, typ: TypeInteger, label: RowIDLabel}, nil
	}
	if err := validLabel(label); err != nil {
		return colRef{}, badQuery("unknown column")
	}
	c := t.column(label)
	if c == nil {
		return colRef{}, fmt.Errorf("%w: %q", ErrColumnNotFound, label)
	}
	return colRef{sql: c.ID, typ: c.Type, label: c.Name}, nil
}

func findTable(cat []catalogTable, label string) (*catalogTable, error) {
	if err := validLabel(label); err != nil {
		return nil, fmt.Errorf("%w: %q", ErrTableNotFound, label)
	}
	k := labelKey(label)
	for i := range cat {
		if cat[i].key == k {
			return &cat[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrTableNotFound, label)
}

type compiler struct {
	tbl   *catalogTable
	args  []any
	nodes int
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (c *compiler) node(f *Filter, depth int) (string, error) {
	c.nodes++
	if depth > MaxFilterDepth {
		return "", badQuery("a filter is at most %d levels deep", MaxFilterDepth)
	}
	if c.nodes > MaxFilterNodes {
		return "", badQuery("a filter has at most %d conditions", MaxFilterNodes)
	}
	kinds := 0
	if f.And != nil {
		kinds++
	}
	if f.Or != nil {
		kinds++
	}
	if f.Op != "" || f.Column != "" {
		kinds++
	}
	if kinds != 1 {
		return "", badQuery("a filter node is exactly one of and, or, or a column condition")
	}
	if f.And != nil || f.Or != nil {
		list, joiner := f.And, " AND "
		if f.Or != nil {
			list, joiner = f.Or, " OR "
		}
		if len(f.Value) > 0 || len(f.Values) > 0 {
			return "", badQuery("and/or nodes take no value")
		}
		if len(list) == 0 {
			return "", badQuery("and/or needs at least one condition")
		}
		parts := make([]string, 0, len(list))
		for i := range list {
			p, err := c.node(&list[i], depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, p)
		}
		return "(" + strings.Join(parts, joiner) + ")", nil
	}
	ref, err := c.tbl.ref(f.Column, true)
	if err != nil {
		return "", err
	}
	switch f.Op {
	case OpIsNull, OpIsNotNull:
		if len(f.Value) > 0 || len(f.Values) > 0 {
			return "", badQuery("%s takes no value", f.Op)
		}
		if f.Op == OpIsNull {
			return ref.sql + " IS NULL", nil
		}
		return ref.sql + " IS NOT NULL", nil
	case OpEq, OpNe, OpLt, OpLte, OpGt, OpGte:
		if len(f.Values) > 0 {
			return "", badQuery("%s takes value, not values", f.Op)
		}
		if ref.typ == TypeBoolean && f.Op != OpEq && f.Op != OpNe {
			return "", badQuery("a boolean column supports eq, ne, is-null and is-not-null")
		}
		if isNull(f.Value) {
			return "", badQuery("%s needs a value; use is-null to test for NULL", f.Op)
		}
		v, err := encodeValue(ref.typ, f.Value)
		if err != nil {
			return "", badQuery("column %q: %v", ref.label, err)
		}
		c.args = append(c.args, v)
		return ref.sql + " " + map[string]string{OpEq: "=", OpNe: "<>", OpLt: "<", OpLte: "<=", OpGt: ">", OpGte: ">="}[f.Op] + " ?", nil
	case OpIn:
		if len(f.Value) > 0 {
			return "", badQuery("in takes values, not value")
		}
		if len(f.Values) == 0 || len(f.Values) > MaxInValues {
			return "", badQuery("in takes 1 to %d values", MaxInValues)
		}
		marks := make([]string, len(f.Values))
		for i, raw := range f.Values {
			if isNull(raw) {
				return "", badQuery("in cannot hold null; use is-null")
			}
			v, err := encodeValue(ref.typ, raw)
			if err != nil {
				return "", badQuery("column %q: %v", ref.label, err)
			}
			c.args = append(c.args, v)
			marks[i] = "?"
		}
		return ref.sql + " IN (" + strings.Join(marks, ",") + ")", nil
	case OpContains, OpStartsWith, OpEndsWith:
		if ref.typ != TypeText {
			return "", badQuery("%s applies to text columns", f.Op)
		}
		if len(f.Values) > 0 || isNull(f.Value) {
			return "", badQuery("%s takes one text value", f.Op)
		}
		v, err := encodeValue(TypeText, f.Value)
		s, _ := v.(string)
		if err != nil || len(s) > MaxFilterText {
			return "", badQuery("%s takes a text value of at most %d bytes", f.Op, MaxFilterText)
		}
		esc := likeEscape(s)
		switch f.Op {
		case OpContains:
			esc = "%" + esc + "%"
		case OpStartsWith:
			esc += "%"
		default:
			esc = "%" + esc
		}
		c.args = append(c.args, esc)
		return ref.sql + " LIKE ? ESCAPE '" + likeEscapeChar + "'", nil
	}
	return "", badQuery("unknown operator")
}

// where compiles an optional filter to a WHERE clause and its arguments.
func (c *compiler) where(f *Filter) (string, error) {
	if f == nil {
		return "", nil
	}
	s, err := c.node(f, 1)
	if err != nil {
		return "", err
	}
	return " WHERE " + s, nil
}

// ---- reading ----

// readTx runs fn in one snapshot of one database: metadata, catalog and rows
// are read in the same transaction, so a page never mixes two revisions.
func (s *Store) readTx(ctx context.Context, resourceID string, fn func(ctx context.Context, tx *sql.Tx, m meta, cat []catalogTable) error) error {
	if err := validID(resourceID); err != nil {
		return err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.openDatabase(resourceID)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	m, err := readMeta(ctx, tx)
	if err != nil {
		return err
	}
	if m.ResourceID != resourceID || m.Revision.Schema == 0 {
		return ErrUnsafePath
	}
	cat, err := readCatalog(ctx, tx)
	if err != nil {
		return err
	}
	return fn(ctx, tx, m, cat)
}

type cursorToken struct {
	V int               `json:"v"`
	O int               `json:"o"`
	R contract.Revision `json:"r"`
	H string            `json:"h"`
}

func encodeCursor(c cursorToken) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursorToken, error) {
	var c cursorToken
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) > 512 || json.Unmarshal(b, &c) != nil || c.V != cursorVersion || c.O < 0 || c.O > MaxOffset {
		return c, badQuery("the cursor is not valid")
	}
	return c, nil
}

func queryHash(parts ...any) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// selectPlan is the compiled shape of a Query: the selected columns, the
// WHERE clause with its bound values and the ORDER BY terms. Every identifier
// in it comes from the catalog.
type selectPlan struct {
	sel   []colRef
	where string
	args  []any
	order []string
}

// planSelect resolves the labels of q against tbl. With allowID the row
// identifier label may be selected as a column (the export does; a page of
// rows always returns the identifier on its own).
func planSelect(tbl *catalogTable, q Query, allowID bool) (selectPlan, error) {
	var sel []colRef
	if len(q.Columns) == 0 {
		for _, c := range tbl.cols {
			sel = append(sel, colRef{sql: c.ID, typ: c.Type, label: c.Name})
		}
	} else {
		if len(q.Columns) > MaxColumns {
			return selectPlan{}, badQuery("too many columns")
		}
		seen := map[string]bool{}
		for _, l := range q.Columns {
			r, err := tbl.ref(l, allowID)
			if err != nil {
				return selectPlan{}, err
			}
			if seen[r.sql] {
				return selectPlan{}, badQuery("column %q is selected twice", l)
			}
			seen[r.sql] = true
			sel = append(sel, r)
		}
	}
	comp := &compiler{tbl: tbl}
	where, err := comp.where(q.Filter)
	if err != nil {
		return selectPlan{}, err
	}
	if len(q.Sort) > MaxSortKeys {
		return selectPlan{}, badQuery("at most %d sort keys", MaxSortKeys)
	}
	var order []string
	seen := map[string]bool{}
	for _, k := range q.Sort {
		r, err := tbl.ref(k.Column, true)
		if err != nil {
			return selectPlan{}, err
		}
		if seen[r.sql] {
			return selectPlan{}, badQuery("column %q is sorted twice", k.Column)
		}
		seen[r.sql] = true
		dir := " ASC"
		if k.Desc {
			dir = " DESC"
		}
		order = append(order, r.sql+dir)
	}
	if !seen[rowIDColumn] {
		order = append(order, rowIDColumn+" ASC")
	}

	return selectPlan{sel: sel, where: where, args: comp.args, order: order}, nil
}

// Query returns one page of rows. Every identifier in the SQL comes from the
// catalog and every value is bound; labels are only looked up.
func (s *Store) Query(ctx context.Context, resourceID string, q Query) (RowPage, error) {
	var out RowPage
	err := s.readTx(ctx, resourceID, func(ctx context.Context, tx *sql.Tx, m meta, cat []catalogTable) error {
		tbl, err := findTable(cat, q.Table)
		if err != nil {
			return err
		}
		limit := q.Limit
		switch {
		case limit == 0:
			limit = DefaultPage
		case limit < 0 || limit > MaxPage:
			return badQuery("limit is 1 to %d", MaxPage)
		}
		pl, err := planSelect(tbl, q, false)
		if err != nil {
			return err
		}
		sel, where, order, args0 := pl.sel, pl.where, pl.order, pl.args
		ids := make([]string, len(sel))
		for i, c := range sel {
			ids[i] = c.sql
		}
		hash := queryHash(tbl.ID, ids, q.Filter, q.Sort)
		offset := 0
		if q.Cursor != "" {
			c, err := decodeCursor(q.Cursor)
			if err != nil {
				return err
			}
			if c.H != hash || c.R != m.Revision {
				return ErrStaleCursor
			}
			offset = c.O
		}
		cols := "row_id"
		for _, c := range sel {
			cols += "," + c.sql
		}
		stmt := "SELECT " + cols + " FROM " + tbl.ID + where + " ORDER BY " + strings.Join(order, ",") + " LIMIT ? OFFSET ?" // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated; values are bound parameters
		args := append(args0, limit+1, offset)
		rows, err := tx.QueryContext(ctx, stmt, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		out = RowPage{Table: tbl.Name, Columns: make([]string, len(sel)), Rows: []Row{}, Revision: m.Revision}
		for i, c := range sel {
			out.Columns[i] = c.label
		}
		size := 0
		for rows.Next() {
			if err = ctx.Err(); err != nil {
				return err
			}
			if len(out.Rows) == limit {
				out.HasMore = true
				break
			}
			raw := make([]any, len(sel)+1)
			ptr := make([]any, len(raw))
			for i := range raw {
				ptr[i] = &raw[i]
			}
			if err = rows.Scan(ptr...); err != nil {
				return err
			}
			id, _ := raw[0].(int64)
			r := Row{ID: id, Values: make([]any, len(sel))}
			for i, c := range sel {
				r.Values[i] = decodeValue(c.typ, raw[i+1])
			}
			b, _ := json.Marshal(r)
			if size+len(b) > MaxResultBytes {
				if len(out.Rows) == 0 {
					return badQuery("one row is larger than %d bytes; select fewer columns", MaxResultBytes)
				}
				out.HasMore = true
				break
			}
			size += len(b)
			out.Rows = append(out.Rows, r)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if out.HasMore {
			next := offset + len(out.Rows)
			if next > MaxOffset {
				return badQuery("results beyond %d rows cannot be paged; add a filter", MaxOffset)
			}
			out.Cursor = encodeCursor(cursorToken{V: cursorVersion, O: next, R: m.Revision, H: hash})
		}
		return nil
	})
	return out, mapQueryError(err)
}

func mapQueryError(err error) error {
	if err != nil && strings.Contains(err.Error(), "integer overflow") {
		return fmt.Errorf("%w: %v", ErrArithmetic, err)
	}
	return err
}

// Aggregate computes aggregates over the rows a filter selects. NULL values
// are ignored by sum, avg, min, max and count(column), as in SQL; sum, avg,
// min and max over no values are null.
func (s *Store) Aggregate(ctx context.Context, resourceID string, q AggregateQuery) (AggregateResult, error) {
	var out AggregateResult
	err := s.readTx(ctx, resourceID, func(ctx context.Context, tx *sql.Tx, m meta, cat []catalogTable) error {
		tbl, err := findTable(cat, q.Table)
		if err != nil {
			return err
		}
		if len(q.Aggregates) == 0 || len(q.Aggregates) > MaxAggregates {
			return badQuery("1 to %d aggregates", MaxAggregates)
		}
		limit := q.Limit
		switch {
		case limit == 0:
			limit = MaxGroups
		case limit < 0 || limit > MaxGroups:
			return badQuery("limit is 1 to %d groups", MaxGroups)
		}
		var exprs, names []string
		var types []string
		for _, a := range q.Aggregates {
			switch a.Fn {
			case "count":
				if a.Column == "" {
					exprs = append(exprs, "COUNT(*)")
					names = append(names, "count")
				} else {
					r, err := tbl.ref(a.Column, true)
					if err != nil {
						return err
					}
					exprs = append(exprs, "COUNT("+r.sql+")")
					names = append(names, "count("+r.label+")")
				}
				types = append(types, TypeInteger)
			case "sum", "avg", "min", "max":
				if a.Column == "" {
					return badQuery("%s needs a column", a.Fn)
				}
				r, err := tbl.ref(a.Column, true)
				if err != nil {
					return err
				}
				numeric := r.typ == TypeInteger || r.typ == TypeReal
				if (a.Fn == "sum" || a.Fn == "avg") && !numeric {
					return badQuery("%s applies to integer and real columns", a.Fn)
				}
				if (a.Fn == "min" || a.Fn == "max") && r.typ == TypeBoolean {
					return badQuery("%s does not apply to boolean columns", a.Fn)
				}
				exprs = append(exprs, strings.ToUpper(a.Fn)+"("+r.sql+")")
				names = append(names, a.Fn+"("+r.label+")")
				t := r.typ
				if a.Fn == "avg" {
					t = TypeReal
				}
				types = append(types, t)
			default:
				return badQuery("unknown aggregate %q", a.Fn)
			}
		}
		comp := &compiler{tbl: tbl}
		where, err := comp.where(q.Filter)
		if err != nil {
			return err
		}
		sel := strings.Join(exprs, ",")
		var group *colRef
		stmt := ""
		if q.GroupBy != "" {
			g, err := tbl.ref(q.GroupBy, true)
			if err != nil {
				return err
			}
			group = &g
			stmt = "SELECT " + g.sql + "," + sel + " FROM " + tbl.ID + where + " GROUP BY " + g.sql + " ORDER BY " + g.sql + " ASC LIMIT ?"
			comp.args = append(comp.args, limit+1)
		} else {
			stmt = "SELECT " + sel + " FROM " + tbl.ID + where
		}
		rows, err := tx.QueryContext(ctx, stmt, comp.args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		out = AggregateResult{Table: tbl.Name, Aggregates: names, Groups: []AggregateGroup{}, Revision: m.Revision}
		width := len(exprs)
		if group != nil {
			width++
		}
		for rows.Next() {
			if err = ctx.Err(); err != nil {
				return err
			}
			if len(out.Groups) == limit && group != nil {
				out.Truncated = true
				break
			}
			raw := make([]any, width)
			ptr := make([]any, width)
			for i := range raw {
				ptr[i] = &raw[i]
			}
			if err = rows.Scan(ptr...); err != nil {
				return err
			}
			g := AggregateGroup{Values: make([]any, len(exprs))}
			off := 0
			if group != nil {
				g.Group = []any{decodeValue(group.typ, raw[0])}
				off = 1
			}
			for i := range exprs {
				g.Values[i] = decodeValue(types[i], raw[off+i])
				// A real sum or average can overflow to infinity, which JSON
				// cannot carry: report it as the typed arithmetic error.
				if f, ok := g.Values[i].(float64); ok && (math.IsInf(f, 0) || math.IsNaN(f)) {
					return fmt.Errorf("%w: %s is not finite", ErrArithmetic, names[i])
				}
			}
			out.Groups = append(out.Groups, g)
		}
		return rows.Err()
	})
	return out, mapQueryError(err)
}
