// SPDX-License-Identifier: MIT
package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Finite defaults of the schema model.
const (
	MaxTables       = 50
	MaxColumns      = 64
	MaxLabelRunes   = 64
	MaxDefaultBytes = 1024
	// timeLayout is fixed width so that stored datetimes sort in time order
	// as plain text.
	timeLayout = "2006-01-02T15:04:05.000000000Z"
)

var (
	ErrInvalidSchema  = errors.New("invalid schema change")
	ErrSchemaLimit    = errors.New("schema limit reached")
	ErrSchemaExists   = errors.New("name already used")
	ErrTableNotFound  = errors.New("table not found")
	ErrColumnNotFound = errors.New("column not found")
)

// Change kinds. They are the only schema operations: no type change, no
// trigger, no function, no expression, no view and no raw SQL.
const (
	KindTableCreate  = "table-create"
	KindTableRename  = "table-rename"
	KindTableDrop    = "table-drop"
	KindColumnAdd    = "column-add"
	KindColumnRename = "column-rename"
	KindColumnDrop   = "column-drop"
)

// Logical column types.
const (
	TypeText     = "text"
	TypeInteger  = "integer"
	TypeReal     = "real"
	TypeBoolean  = "boolean"
	TypeDateTime = "datetime"
)

// SchemaChange is one typed schema operation on one database. Table and
// column fields are the people-facing labels; the store maps them to stable
// internal identifiers and never puts a label into SQL.
type SchemaChange struct {
	ResourceID string          `json:"resourceId"`
	Kind       string          `json:"kind"`
	Table      string          `json:"table"`
	Column     string          `json:"column,omitempty"`
	NewName    string          `json:"newName,omitempty"`
	Type       string          `json:"type,omitempty"`
	Required   bool            `json:"required,omitempty"`
	Unique     bool            `json:"unique,omitempty"`
	Default    json.RawMessage `json:"default,omitempty"`
}

// ColumnSchema and TableSchema describe the catalog. ID is the stable internal
// identifier; it never changes on rename.
type ColumnSchema struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Required bool            `json:"required"`
	Unique   bool            `json:"unique"`
	Default  json.RawMessage `json:"default,omitempty"`
}
type TableSchema struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Columns []ColumnSchema `json:"columns"`
}

const catalogSchema = `
CREATE TABLE IF NOT EXISTS tables_catalog_tables(
 table_id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 name_key TEXT NOT NULL UNIQUE,
 position INTEGER NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS tables_catalog_columns(
 table_id TEXT NOT NULL,
 column_id TEXT NOT NULL,
 name TEXT NOT NULL,
 name_key TEXT NOT NULL,
 type TEXT NOT NULL,
 required INTEGER NOT NULL,
 uniq INTEGER NOT NULL,
 default_json TEXT,
 position INTEGER NOT NULL,
 PRIMARY KEY(table_id, column_id),
 UNIQUE(table_id, name_key)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS tables_schema_receipts(
 operation_id TEXT PRIMARY KEY,
 request_hash TEXT NOT NULL,
 schema_revision INTEGER NOT NULL,
 data_revision INTEGER NOT NULL,
 payload TEXT NOT NULL,
 applied_at TEXT NOT NULL
) WITHOUT ROWID;`

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSchema, fmt.Sprintf(format, a...))
}

// validLabel accepts a people-facing table or column name.
func validLabel(n string) error {
	if !utf8.ValidString(n) || n == "" || strings.TrimSpace(n) != n || utf8.RuneCountInString(n) > MaxLabelRunes {
		return invalid("a name is 1 to %d characters without leading or trailing space", MaxLabelRunes)
	}
	for _, r := range n {
		// Control, format (bidi controls, zero-width), line and paragraph
		// separators and the replacement character are refused.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || r == utf8.RuneError {
			return invalid("a name cannot hold control, format or invalid characters")
		}
	}
	return nil
}

// notReserved refuses the label that names the row identifier in queries.
func notReserved(n string) error {
	if labelKey(n) == RowIDLabel {
		return invalid("%q is reserved for the row identifier", RowIDLabel)
	}
	return nil
}

// labelKey is the case-insensitive uniqueness key of a label.
func labelKey(n string) string { return strings.ToLower(strings.ToUpper(n)) }

func validType(t string) bool {
	switch t {
	case TypeText, TypeInteger, TypeReal, TypeBoolean, TypeDateTime:
		return true
	}
	return false
}

func storageType(t string) string {
	switch t {
	case TypeText, TypeDateTime:
		return "TEXT"
	case TypeInteger, TypeBoolean:
		return "INTEGER"
	}
	return "REAL"
}

// literal is a typed default: the SQL literal for DDL and its canonical JSON.
type literal struct {
	sql  string
	json string
}

// encodeDefault validates raw as a value of the logical type and returns the
// only SQL text that may reach a DEFAULT clause. Nothing user supplied is
// copied through except the contents of a quoted text, with quotes doubled.
func encodeDefault(typ string, raw json.RawMessage) (*literal, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if len(raw) > 2*MaxDefaultBytes+64 {
		return nil, invalid("the default is too large")
	}
	switch typ {
	case TypeText:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, invalid("the default of a text column must be a string")
		}
		return textLiteral(s)
	case TypeDateTime:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, invalid("the default of a datetime column must be a string")
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, invalid("the default of a datetime column must be an RFC 3339 time")
		}
		return textLiteral(t.UTC().Format(timeLayout))
	case TypeBoolean:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, invalid("the default of a boolean column must be true or false")
		}
		v := "0"
		if b {
			v = "1"
		}
		return &literal{sql: v, json: strconv.FormatBool(b)}, nil
	case TypeInteger:
		i, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return nil, invalid("the default of an integer column must be a 64-bit integer")
		}
		v := strconv.FormatInt(i, 10)
		return &literal{sql: v, json: v}, nil
	case TypeReal:
		var f float64
		d := json.NewDecoder(bytes.NewReader(raw))
		if err := d.Decode(&f); err != nil || d.More() || raw[0] == '"' {
			return nil, invalid("the default of a real column must be a finite number")
		}
		if f != f || f > 1.7976931348623157e308 || f < -1.7976931348623157e308 {
			return nil, invalid("the default of a real column must be a finite number")
		}
		v := strconv.FormatFloat(f, 'g', -1, 64)
		return &literal{sql: v, json: v}, nil
	}
	return nil, invalid("unknown column type")
}

func textLiteral(s string) (*literal, error) {
	if !utf8.ValidString(s) || len(s) > MaxDefaultBytes {
		return nil, invalid("a text default is valid UTF-8 of at most %d bytes", MaxDefaultBytes)
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return nil, invalid("a text default cannot hold control characters")
		}
	}
	j, _ := json.Marshal(s)
	return &literal{sql: "'" + strings.ReplaceAll(s, "'", "''") + "'", json: string(j)}, nil
}

// validate checks the shape of a request without a database.
func (c SchemaChange) validate() error {
	if err := validID(c.ResourceID); err != nil {
		return err
	}
	if err := validLabel(c.Table); err != nil {
		return err
	}
	needColumn := c.Kind == KindColumnAdd || c.Kind == KindColumnRename || c.Kind == KindColumnDrop
	if needColumn {
		if err := validLabel(c.Column); err != nil {
			return err
		}
		if err := notReserved(c.Column); err != nil {
			return err
		}
	} else if c.Column != "" {
		return invalid("column is not used by %s", c.Kind)
	}
	switch c.Kind {
	case KindTableCreate, KindTableDrop:
		if c.NewName != "" {
			return invalid("newName is not used by %s", c.Kind)
		}
	case KindTableRename, KindColumnRename:
		if err := validLabel(c.NewName); err != nil {
			return err
		}
		if c.Kind == KindColumnRename {
			if err := notReserved(c.NewName); err != nil {
				return err
			}
		}
	case KindColumnDrop:
		if c.NewName != "" {
			return invalid("newName is not used by %s", c.Kind)
		}
	case KindColumnAdd:
		if !validType(c.Type) {
			return invalid("column type must be text, integer, real, boolean or datetime")
		}
		if _, err := encodeDefault(c.Type, c.Default); err != nil {
			return err
		}
		if c.NewName != "" {
			return invalid("newName is not used by %s", c.Kind)
		}
	default:
		return invalid("unknown schema operation")
	}
	if c.Kind != KindColumnAdd && (c.Type != "" || c.Required || c.Unique || len(bytes.TrimSpace(c.Default)) > 0) {
		return invalid("column definition fields are only used by column-add")
	}
	return nil
}

// fingerprint is the canonical identity of a request, used to tell a replay
// from a different request that reuses an operation ID.
func (c SchemaChange) fingerprint() string {
	d := bytes.TrimSpace(c.Default)
	if string(d) == "null" {
		d = nil
	}
	if len(d) > 0 {
		var b bytes.Buffer
		if json.Compact(&b, d) == nil {
			d = b.Bytes()
		}
	}
	c.Default = d
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func newIdentifier(prefix string) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

// ---- catalog ----

type catalogTable struct {
	TableSchema
	key  string
	cols []catalogColumn
}
type catalogColumn struct {
	ColumnSchema
	key string
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func hasCatalog(ctx context.Context, q queryer) (bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT 1 FROM sqlite_master WHERE type='table' AND name='tables_catalog_tables'")
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	ok := rows.Next()
	return ok, rows.Err()
}

// readCatalog lists tables and columns in creation order. A database made
// before the catalog existed has no tables.
func readCatalog(ctx context.Context, q queryer) ([]catalogTable, error) {
	if ok, err := hasCatalog(ctx, q); err != nil || !ok {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, "SELECT table_id,name,name_key FROM tables_catalog_tables ORDER BY position")
	if err != nil {
		return nil, err
	}
	var out []catalogTable
	for rows.Next() {
		var t catalogTable
		if err = rows.Scan(&t.ID, &t.Name, &t.key); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t.Columns = []ColumnSchema{}
		out = append(out, t)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	for i := range out {
		cr, err := q.QueryContext(ctx, "SELECT column_id,name,name_key,type,required,uniq,default_json FROM tables_catalog_columns WHERE table_id=? ORDER BY position", out[i].ID)
		if err != nil {
			return nil, err
		}
		for cr.Next() {
			var c catalogColumn
			var def sql.NullString
			var req, uniq int
			if err = cr.Scan(&c.ID, &c.Name, &c.key, &c.Type, &req, &uniq, &def); err != nil {
				_ = cr.Close()
				return nil, err
			}
			c.Required, c.Unique = req == 1, uniq == 1
			if def.Valid {
				c.Default = json.RawMessage(def.String)
			}
			out[i].cols = append(out[i].cols, c)
			out[i].Columns = append(out[i].Columns, c.ColumnSchema)
		}
		err = cr.Err()
		_ = cr.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (t *catalogTable) column(name string) *catalogColumn {
	k := labelKey(name)
	for i := range t.cols {
		if t.cols[i].key == k {
			return &t.cols[i]
		}
	}
	return nil
}

// columnDDL is the definition of one column, built from internal identifiers
// and a typed default only.
func columnDDL(c catalogColumn) (string, error) {
	var b strings.Builder
	b.WriteString(c.ID + " " + storageType(c.Type))
	if c.Type == TypeBoolean {
		fmt.Fprintf(&b, " CHECK (%s IN (0,1))", c.ID)
	}
	if c.Required {
		b.WriteString(" NOT NULL")
	}
	if len(c.Default) > 0 {
		lit, err := encodeDefault(c.Type, c.Default)
		if err != nil || lit == nil {
			return "", invalid("a stored default is not valid")
		}
		b.WriteString(" DEFAULT " + lit.sql)
	}
	return b.String(), nil
}

// ---- operations ----

// effect is one line of the plan of a change.
type effect struct {
	Action  string `json:"action"`
	Table   string `json:"table,omitempty"`
	Column  string `json:"column,omitempty"`
	To      string `json:"to,omitempty"`
	Type    string `json:"type,omitempty"`
	Rows    *int64 `json:"rowsLost,omitempty"`
	Columns *int   `json:"columnsLost,omitempty"`
}

type outcome struct {
	effects []effect
	tableID string
	column  string
}

func countRows(ctx context.Context, tx *sql.Tx, table string) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n)
	return n, err
}

func countValues(ctx context.Context, tx *sql.Tx, table, column string) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, "SELECT COUNT("+column+") FROM "+table).Scan(&n)
	return n, err
}

// apply runs one change inside tx. Identifiers in SQL are always internal.
func (s *Store) apply(ctx context.Context, tx *sql.Tx, c SchemaChange) (outcome, error) {
	if _, err := tx.ExecContext(ctx, catalogSchema); err != nil {
		return outcome{}, err
	}
	cat, err := readCatalog(ctx, tx)
	if err != nil {
		return outcome{}, err
	}
	var tbl *catalogTable
	key := labelKey(c.Table)
	for i := range cat {
		if cat[i].key == key {
			tbl = &cat[i]
		}
	}
	taken := func(name string) bool {
		k := labelKey(name)
		for i := range cat {
			if cat[i].key == k && &cat[i] != tbl {
				return true
			}
		}
		return false
	}
	if c.Kind == KindTableCreate {
		if tbl != nil {
			return outcome{}, fmt.Errorf("%w: table %q", ErrSchemaExists, c.Table)
		}
		if len(cat) >= MaxTables {
			return outcome{}, fmt.Errorf("%w: at most %d tables", ErrSchemaLimit, MaxTables)
		}
		id, err := newIdentifier("t_")
		if err != nil {
			return outcome{}, err
		}
		if _, err = tx.ExecContext(ctx, "CREATE TABLE "+id+" (row_id INTEGER PRIMARY KEY AUTOINCREMENT)"); err != nil {
			return outcome{}, capacityError(err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO tables_catalog_tables(table_id,name,name_key,position) VALUES(?,?,?,(SELECT COALESCE(MAX(position),0)+1 FROM tables_catalog_tables))", id, c.Table, key); err != nil {
			return outcome{}, capacityError(err)
		}
		return outcome{effects: []effect{{Action: "create table", Table: c.Table}}, tableID: id}, nil
	}
	if tbl == nil {
		return outcome{}, fmt.Errorf("%w: %q", ErrTableNotFound, c.Table)
	}
	out := outcome{tableID: tbl.ID}
	switch c.Kind {
	case KindTableRename:
		if taken(c.NewName) {
			return out, fmt.Errorf("%w: table %q", ErrSchemaExists, c.NewName)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE tables_catalog_tables SET name=?, name_key=? WHERE table_id=?", c.NewName, labelKey(c.NewName), tbl.ID); err != nil {
			return out, capacityError(err)
		}
		out.effects = []effect{{Action: "rename table", Table: tbl.Name, To: c.NewName}}
	case KindTableDrop:
		rows, err := countRows(ctx, tx, tbl.ID)
		if err != nil {
			return out, err
		}
		cols := len(tbl.cols)
		for _, q := range []string{"DROP TABLE " + tbl.ID} {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return out, err
			}
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM tables_catalog_columns WHERE table_id=?", tbl.ID); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM tables_catalog_tables WHERE table_id=?", tbl.ID); err != nil {
			return out, err
		}
		out.effects = []effect{{Action: "drop table", Table: tbl.Name, Rows: &rows, Columns: &cols}}
	case KindColumnRename:
		col := tbl.column(c.Column)
		if col == nil {
			return out, fmt.Errorf("%w: %q", ErrColumnNotFound, c.Column)
		}
		if o := tbl.column(c.NewName); o != nil && o.ID != col.ID {
			return out, fmt.Errorf("%w: column %q", ErrSchemaExists, c.NewName)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE tables_catalog_columns SET name=?, name_key=? WHERE table_id=? AND column_id=?", c.NewName, labelKey(c.NewName), tbl.ID, col.ID); err != nil {
			return out, capacityError(err)
		}
		out.column = col.ID
		out.effects = []effect{{Action: "rename column", Table: tbl.Name, Column: col.Name, To: c.NewName}}
	case KindColumnDrop:
		col := tbl.column(c.Column)
		if col == nil {
			return out, fmt.Errorf("%w: %q", ErrColumnNotFound, c.Column)
		}
		lost, err := countValues(ctx, tx, tbl.ID, col.ID)
		if err != nil {
			return out, err
		}
		var keep []catalogColumn
		for _, k := range tbl.cols {
			if k.ID != col.ID {
				keep = append(keep, k)
			}
		}
		if err = s.rebuild(ctx, tx, tbl.ID, keep); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM tables_catalog_columns WHERE table_id=? AND column_id=?", tbl.ID, col.ID); err != nil {
			return out, err
		}
		out.column = col.ID
		out.effects = []effect{{Action: "drop column", Table: tbl.Name, Column: col.Name, Rows: &lost}}
	case KindColumnAdd:
		return s.addColumn(ctx, tx, tbl, c)
	default:
		return out, invalid("unknown schema operation")
	}
	return out, nil
}

func (s *Store) addColumn(ctx context.Context, tx *sql.Tx, tbl *catalogTable, c SchemaChange) (outcome, error) {
	out := outcome{tableID: tbl.ID}
	if tbl.column(c.Column) != nil {
		return out, fmt.Errorf("%w: column %q", ErrSchemaExists, c.Column)
	}
	if len(tbl.cols) >= MaxColumns {
		return out, fmt.Errorf("%w: at most %d columns per table", ErrSchemaLimit, MaxColumns)
	}
	lit, err := encodeDefault(c.Type, c.Default)
	if err != nil {
		return out, err
	}
	rows, err := countRows(ctx, tx, tbl.ID)
	if err != nil {
		return out, err
	}
	if c.Required && lit == nil && rows > 0 {
		return out, invalid("a required column needs a default when the table has rows")
	}
	if c.Unique && lit != nil && rows > 1 {
		return out, invalid("a default value would be duplicated in %d rows of a unique column", rows)
	}
	id, err := newIdentifier("c_")
	if err != nil {
		return out, err
	}
	col := catalogColumn{ColumnSchema: ColumnSchema{ID: id, Name: c.Column, Type: c.Type, Required: c.Required, Unique: c.Unique}, key: labelKey(c.Column)}
	var def any
	if lit != nil {
		col.Default = json.RawMessage(lit.json)
		def = lit.json
	}
	ddl, err := columnDDL(col)
	if err != nil {
		return out, err
	}
	if lit != nil && c.Type == TypeReal {
		// SQLite's decimal parser is not exact for every double. A default
		// that would not read back as the number asked for is refused.
		var want, got float64
		_ = json.Unmarshal([]byte(lit.json), &want)
		if err = tx.QueryRowContext(ctx, "SELECT CAST("+lit.sql+" AS REAL)").Scan(&got); err != nil {
			return out, err
		}
		if got != want {
			return out, invalid("the real default cannot be stored exactly")
		}
	}
	if c.Required && lit == nil {
		// SQLite cannot add a NOT NULL column without a default, even to an
		// empty table. The table is empty here: rebuild it with the column.
		if err = s.rebuild(ctx, tx, tbl.ID, append(append([]catalogColumn{}, tbl.cols...), col)); err != nil {
			return out, err
		}
	} else {
		if _, err = tx.ExecContext(ctx, "ALTER TABLE "+tbl.ID+" ADD COLUMN "+ddl); err != nil {
			return out, capacityError(err)
		}
		if c.Unique {
			if _, err = tx.ExecContext(ctx, "CREATE UNIQUE INDEX u_"+id+" ON "+tbl.ID+"("+id+")"); err != nil {
				return out, invalid("the column cannot be unique: %v", err)
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO tables_catalog_columns(table_id,column_id,name,name_key,type,required,uniq,default_json,position) VALUES(?,?,?,?,?,?,?,?,?)",
		tbl.ID, id, c.Column, col.key, c.Type, b2i(c.Required), b2i(c.Unique), def, len(tbl.cols)+1); err != nil {
		return out, capacityError(err)
	}
	out.column = id
	out.effects = []effect{{Action: "add column", Table: tbl.Name, Column: c.Column, Type: c.Type}}
	return out, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// rebuild replaces a table by a copy that has exactly the columns in keep:
// create the new table, copy the rows, drop the old one and give the new one
// the old internal name. It runs inside the caller's transaction, so a failure
// at any step leaves the original untouched.
func (s *Store) rebuild(ctx context.Context, tx *sql.Tx, id string, keep []catalogColumn) error {
	tmp, err := newIdentifier("t_")
	if err != nil {
		return err
	}
	defs := []string{"row_id INTEGER PRIMARY KEY AUTOINCREMENT"}
	names := []string{"row_id"}
	for _, c := range keep {
		d, err := columnDDL(c)
		if err != nil {
			return err
		}
		defs = append(defs, d)
		names = append(names, c.ID)
	}
	if _, err = tx.ExecContext(ctx, "CREATE TABLE "+tmp+" ("+strings.Join(defs, ", ")+")"); err != nil {
		return capacityError(err)
	}
	// Columns that exist in the old table are copied; a column that is new
	// has no source and the table is empty.
	var old = map[string]bool{}
	rows, err := tx.QueryContext(ctx, "SELECT name FROM pragma_table_info('"+id+"')") // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated; values are bound parameters
	if err != nil {
		return err
	}
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			_ = rows.Close()
			return err
		}
		old[n] = true
	}
	_ = rows.Close()
	var copyCols []string
	for _, n := range names {
		if old[n] {
			copyCols = append(copyCols, n)
		}
	}
	list := strings.Join(copyCols, ",")
	if _, err = tx.ExecContext(ctx, "INSERT INTO "+tmp+"("+list+") SELECT "+list+" FROM "+id); err != nil { // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated; values are bound parameters
		return capacityError(err)
	}
	if err = s.fail(FailRebuildCopy); err != nil {
		return err
	}
	// The AUTOINCREMENT counter lives in sqlite_sequence, keyed by table
	// name, and dies with the old table. Read it now so a row ID that was
	// handed out before is never handed out again.
	var seq sql.NullInt64
	if err = tx.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name=?", id).Scan(&seq); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DROP TABLE "+id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "ALTER TABLE "+tmp+" RENAME TO "+id); err != nil {
		return err
	}
	if seq.Valid {
		// The rename carried the copy's own counter (the highest copied
		// row ID); keep whichever of the two is larger.
		res, err := tx.ExecContext(ctx, "UPDATE sqlite_sequence SET seq=MAX(seq,?) WHERE name=?", seq.Int64, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err = tx.ExecContext(ctx, "INSERT INTO sqlite_sequence(name,seq) VALUES(?,?)", id, seq.Int64); err != nil {
				return err
			}
		}
	}
	for _, c := range keep {
		if c.Unique {
			if _, err = tx.ExecContext(ctx, "CREATE UNIQUE INDEX u_"+c.ID+" ON "+id+"("+c.ID+")"); err != nil {
				return capacityError(err)
			}
		}
	}
	return nil
}

// ---- public operations ----

func summarize(o outcome) json.RawMessage {
	var rows int64
	cols, tables := 0, 0
	for _, e := range o.effects {
		if e.Rows != nil {
			rows += *e.Rows
		}
		if e.Columns != nil {
			cols += *e.Columns
		}
		if e.Action == "drop table" {
			tables++
		}
		if e.Action == "drop column" {
			cols++
		}
	}
	b, _ := json.Marshal(map[string]any{
		"count":    len(o.effects),
		"sample":   o.effects,
		"dataLoss": map[string]any{"rows": rows, "columns": cols, "tables": tables},
	})
	return b
}

// PreviewSchema reports the exact effects of a change against the current
// schema, including the data a drop would destroy, and changes nothing. The
// plan is computed by running the change in a transaction that is rolled back,
// so a preview accepts a change only when its apply would.
func (s *Store) PreviewSchema(ctx context.Context, request SchemaChange) (contract.Preview, error) {
	if err := request.validate(); err != nil {
		return contract.Preview{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.openDatabase(request.ResourceID)
	if err != nil {
		return contract.Preview{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Preview{}, err
	}
	defer func() { _ = tx.Rollback() }()
	m, err := readMeta(ctx, tx)
	if err != nil {
		return contract.Preview{}, err
	}
	if m.ResourceID != request.ResourceID || m.Revision.Schema == 0 {
		return contract.Preview{}, ErrUnsafePath
	}
	// The plan is made with a failpoint-free copy of the store state.
	o, err := s.applyNoFail(ctx, tx, request)
	if err != nil {
		return contract.Preview{}, err
	}
	return contract.Preview{Revision: m.Revision, Summary: summarize(o)}, nil
}

func (s *Store) applyNoFail(ctx context.Context, tx *sql.Tx, c SchemaChange) (outcome, error) {
	keep := s.failpoint
	s.failpoint = nil
	defer func() { s.failpoint = keep }()
	return s.apply(ctx, tx, c)
}

// ApplySchema applies one change when the database is still at the expected
// revision. The change, the new revision and the operation receipt commit in
// one SQLite transaction. The same operation ID with the same request returns
// the recorded receipt; the same ID with a different request is a conflict.
func (s *Store) ApplySchema(ctx context.Context, request SchemaChange, expected contract.Revision, operationID string) (contract.Receipt, error) {
	if err := validOperation(operationID); err != nil {
		return contract.Receipt{}, err
	}
	if err := request.validate(); err != nil {
		return contract.Receipt{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := request.fingerprint()
	op, found, err := s.lookup(ctx, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	if found {
		if op.Kind != "schema" || op.Resource != request.ResourceID || op.Name != hash {
			return contract.Receipt{}, ErrConflict
		}
	}
	db, err := s.openDatabase(request.ResourceID)
	if err != nil {
		return contract.Receipt{}, err
	}
	defer func() { _ = db.Close() }()
	if !found {
		if err = s.begin(ctx, opRow{ID: operationID, Kind: "schema", Resource: request.ResourceID, Name: hash}); err != nil {
			return contract.Receipt{}, err
		}
	}
	forget := func() {
		// The change did not commit and cannot have: the operation never
		// happened, so its ledger line must not block the ID.
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
	// A receipt already in the database is the authority for a replay.
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
	if m.ResourceID != request.ResourceID || m.Revision.Schema == 0 {
		forget()
		return contract.Receipt{}, ErrUnsafePath
	}
	if m.Revision != expected {
		forget()
		return contract.Receipt{}, &StaleError{Current: m.Revision}
	}
	o, err := s.apply(ctx, tx, request)
	if err != nil {
		if errors.Is(err, ErrInvalidSchema) || errors.Is(err, ErrSchemaLimit) || errors.Is(err, ErrSchemaExists) || errors.Is(err, ErrTableNotFound) || errors.Is(err, ErrColumnNotFound) || errors.Is(err, ErrCapacity) {
			forget()
		}
		return contract.Receipt{}, err
	}
	next, err := bumpSchema(ctx, tx, m.Revision)
	if err != nil {
		return contract.Receipt{}, capacityError(err)
	}
	payload := schemaPayload(request, o)
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

func schemaPayload(c SchemaChange, o outcome) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"resourceId": c.ResourceID, "kind": c.Kind, "table": c.Table, "column": c.Column, "newName": c.NewName, "tableId": o.tableID, "columnId": o.column, "effects": o.effects})
	return b
}

// TableNames lists table labels in creation order; used by Inspect.
func tableNames(cat []catalogTable) []string {
	out := make([]string, 0, len(cat))
	for _, t := range cat {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}
