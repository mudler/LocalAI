// SPDX-License-Identifier: MIT
package store

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Finite limits of one CSV export. A store option lowers them; none raises a
// default above what the host accepts as one private file.
const (
	// DefaultExportRows is the most rows one export writes.
	DefaultExportRows = 1000000
	// DefaultExportBytes is the most bytes one export writes.
	DefaultExportBytes = 64 << 20
	// DefaultExportTimeout bounds one export, snapshot to last byte. It is
	// shorter than the host's read deadline, so a slow export fails here with a
	// typed error and not as an unexplained network timeout.
	DefaultExportTimeout = 25 * time.Second

	exportBuffer = 32 << 10
)

// ErrExportTooLarge is returned when the result passes the row or the byte
// bound. Nothing a caller kept from dst is a complete export.
var ErrExportTooLarge = errors.New("the export is larger than the export limit; add a filter or select fewer columns")

// ExportStats describes a finished export.
type ExportStats struct {
	// Rows is the number of data records written, Bytes the size of the
	// whole file including the header and Columns the header labels.
	Rows    int64
	Bytes   int64
	Columns []string
	// Revision is the revision of the snapshot the file holds.
	Revision contract.Revision
}

// openReader opens a database file for one long read. Unlike a write it takes
// no write lock at the start of its transaction, so a snapshot that is being
// streamed never blocks a writer.
func (s *Store) openReader(id string) (*sql.DB, error) {
	fi, err := os.Lstat(s.path(id))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(s.path(id))
	d := fmt.Sprintf("file:%s?mode=rw&_busy_timeout=%d&_foreign_keys=on&_journal_mode=WAL&_query_only=true&_txlock=deferred", esc, s.opts.BusyTimeout.Milliseconds())
	db := sql.OpenDB(capConnector{dsn: d, drv: &sqlite3.SQLiteDriver{}, maxPages: s.opts.MaxPages})
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	return db, nil
}

// ExportCSV writes the rows q selects from one table to dst as CSV, from one
// read snapshot of the database. It is a read: it changes nothing and a
// concurrent write is neither seen nor blocked. q.Cursor must be empty;
// q.Limit, when set, is the number of rows to write.
//
// The file is RFC 4180: a header of column labels, CRLF record ends, fields
// quoted when they hold a comma, quote, CR, LF or a leading or trailing
// space, UTF-8 and no byte order mark. NULL is an empty unquoted field and an
// empty text value is "", so a reader that tells them apart can. Booleans are
// true or false, integers decimal, reals the shortest decimal that parses
// back to the same value and date-times RFC 3339 in UTC. A text cell or a
// label that starts with =, +, -, @, tab or carriage return is prefixed with
// a single quote, so a spreadsheet shows it as text; numeric columns are
// never prefixed. The column "_id" may be selected to export the row ID.
//
// The result is written row by row through a small buffer. When the export
// fails dst holds a partial file and the error says why.
func (s *Store) ExportCSV(ctx context.Context, resourceID string, q Query, dst io.Writer) (ExportStats, error) {
	var stats ExportStats
	if err := validID(resourceID); err != nil {
		return stats, err
	}
	if q.Cursor != "" {
		return stats, badQuery("an export has no cursor")
	}
	if q.Limit < 0 {
		return stats, badQuery("limit cannot be negative")
	}
	maxRows, maxBytes, timeout := int64(s.opts.ExportMaxRows), s.opts.ExportMaxBytes, s.opts.ExportTimeout
	if maxRows <= 0 {
		maxRows = DefaultExportRows
	}
	if maxBytes <= 0 {
		maxBytes = DefaultExportBytes
	}
	if timeout <= 0 {
		timeout = DefaultExportTimeout
	}
	want := int64(q.Limit)
	if want > maxRows {
		return stats, fmt.Errorf("%w: at most %d rows", ErrExportTooLarge, maxRows)
	}
	capped := want == 0
	if capped {
		want = maxRows
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return stats, err
	}

	// Open the snapshot under the store lock, so it cannot start in the
	// middle of a write, then let go of the lock for the stream.
	var (
		db  *sql.DB
		tx  *sql.Tx
		m   meta
		tbl *catalogTable
		pl  selectPlan
	)
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		var err error
		if db, err = s.openReader(resourceID); err != nil {
			return err
		}
		if tx, err = db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true}); err != nil {
			_ = db.Close()
			return err
		}
		fail := func(err error) error {
			_ = tx.Rollback()
			_ = db.Close()
			return err
		}
		if m, err = readMeta(ctx, tx); err != nil {
			return fail(err)
		}
		if m.ResourceID != resourceID || m.Revision.Schema == 0 {
			return fail(ErrUnsafePath)
		}
		cat, err := readCatalog(ctx, tx)
		if err != nil {
			return fail(err)
		}
		if tbl, err = findTable(cat, q.Table); err != nil {
			return fail(err)
		}
		if pl, err = planSelect(tbl, q, true); err != nil {
			return fail(err)
		}
		return nil
	}()
	if err != nil {
		if ce := ctx.Err(); ce != nil {
			return stats, ce
		}
		return stats, err
	}
	defer func() { _ = db.Close() }()
	defer func() { _ = tx.Rollback() }()

	stats.Revision = m.Revision
	out := &csvWriter{w: bufio.NewWriterSize(dst, exportBuffer), max: maxBytes}
	stats.Columns = make([]string, len(pl.sel))
	for i, c := range pl.sel {
		stats.Columns[i] = c.label
	}
	cols := rowIDColumn
	for _, c := range pl.sel {
		cols += "," + c.sql
	}
	fetch := want
	if capped {
		fetch = want + 1
	}
	stmt := "SELECT " + cols + " FROM " + tbl.ID + pl.where + " ORDER BY " + strings.Join(pl.order, ",") + " LIMIT ?" // #nosec G202 -- identifiers are generated by the add-on (t plus hex) or validated; values are bound parameters
	rows, err := tx.QueryContext(ctx, stmt, append(pl.args, fetch)...)
	if err != nil {
		if ce := ctx.Err(); ce != nil {
			return stats, ce
		}
		return stats, err
	}
	defer func() { _ = rows.Close() }()

	finish := func(err error) (ExportStats, error) {
		if ce := ctx.Err(); ce != nil && err != nil {
			err = ce
		}
		stats.Bytes = out.n
		return stats, err
	}
	// The header goes out at once: the destination sees the snapshot is
	// open before the first row is read.
	out.rec = out.rec[:0]
	for i, c := range pl.sel {
		if i > 0 {
			out.rec = append(out.rec, ',')
		}
		out.rec = appendText(out.rec, c.label)
	}
	out.rec = append(out.rec, '\r', '\n')
	if err = out.put(); err != nil {
		return finish(err)
	}
	if err = out.w.Flush(); err != nil {
		return finish(err)
	}
	raw := make([]any, len(pl.sel)+1)
	ptr := make([]any, len(raw))
	for i := range raw {
		ptr[i] = &raw[i]
	}
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return finish(err)
		}
		if stats.Rows >= want {
			// Only a query without a limit reads one row past the bound.
			return finish(fmt.Errorf("%w: at most %d rows", ErrExportTooLarge, maxRows))
		}
		for i := range raw {
			raw[i] = nil
		}
		if err = rows.Scan(ptr...); err != nil {
			return finish(err)
		}
		out.rec = out.rec[:0]
		for i, c := range pl.sel {
			if i > 0 {
				out.rec = append(out.rec, ',')
			}
			out.rec = appendValue(out.rec, c.typ, raw[i+1])
		}
		out.rec = append(out.rec, '\r', '\n')
		if err = out.put(); err != nil {
			return finish(err)
		}
		stats.Rows++
	}
	if err = rows.Err(); err != nil {
		return finish(err)
	}
	if err = ctx.Err(); err != nil {
		return finish(err)
	}
	if err = out.w.Flush(); err != nil {
		return finish(err)
	}
	return finish(nil)
}

// csvWriter counts what it accepts and refuses a record that would take the
// file past its byte bound.
type csvWriter struct {
	w   *bufio.Writer
	n   int64
	max int64
	rec []byte
}

func (c *csvWriter) put() error {
	if c.n+int64(len(c.rec)) > c.max {
		return fmt.Errorf("%w: at most %d bytes", ErrExportTooLarge, c.max)
	}
	n, err := c.w.Write(c.rec)
	c.n += int64(n)
	return err
}

// appendValue encodes one stored value by its column type.
func appendValue(b []byte, typ string, v any) []byte {
	if bs, ok := v.([]byte); ok {
		v = string(bs)
	}
	switch x := v.(type) {
	case nil:
		return b
	case int64:
		switch typ {
		case TypeBoolean:
			return strconv.AppendBool(b, x != 0)
		case TypeReal:
			return appendReal(b, float64(x))
		}
		return strconv.AppendInt(b, x, 10)
	case float64:
		if typ == TypeInteger && x == float64(int64(x)) {
			return strconv.AppendInt(b, int64(x), 10)
		}
		return appendReal(b, x)
	case string:
		if typ == TypeDateTime {
			if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
				// Date-times are safe by construction: digits, dashes and a
				// colon that never open with a formula character.
				return append(b, t.UTC().Format(time.RFC3339Nano)...)
			}
		}
		return appendText(b, x)
	}
	return appendText(b, fmt.Sprint(v))
}

// appendReal is the shortest decimal form that parses back to f. Very large
// and very small magnitudes use an exponent, as a spreadsheet reads them.
func appendReal(b []byte, f float64) []byte {
	a := f
	if a < 0 {
		a = -a
	}
	if a == 0 || (a >= 1e-6 && a < 1e21) {
		return strconv.AppendFloat(b, f, 'f', -1, 64)
	}
	return strconv.AppendFloat(b, f, 'g', -1, 64)
}

// appendText encodes a text cell or a header label: formula guard, then CSV
// quoting. An empty text value is written as "" so that it differs from NULL.
func appendText(b []byte, s string) []byte {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if s != "" {
		switch s[0] {
		case '=', '+', '-', '@', '\t', '\r':
			s = "'" + s
		}
	}
	if s == "" {
		return append(b, '"', '"')
	}
	if !strings.ContainsAny(s, ",\"\r\n") && s[0] != ' ' && s[len(s)-1] != ' ' && s[0] != '\t' && s[len(s)-1] != '\t' {
		return append(b, s...)
	}
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			b = append(b, '"')
		}
		b = append(b, s[i])
	}
	return append(b, '"')
}
