// SPDX-License-Identifier: MIT

// Package store keeps the Tables databases: one private SQLite file per
// database, named by the opaque resource ID the host assigned.
//
// The host owns identity and access. This package never decides who may see a
// database; it only guarantees that an ID reaches exactly one file inside its
// own directory, that an operation ID has one outcome, and that a database is
// never visible half built.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var (
	ErrInvalidID        = errors.New("invalid resource id")
	ErrInvalidName      = errors.New("invalid database name")
	ErrInvalidOperation = errors.New("invalid operation id")
	ErrExists           = errors.New("database already exists")
	ErrNotFound         = errors.New("database not found")
	ErrConflict         = errors.New("operation id already used for a different request")
	ErrUnsafePath       = errors.New("unsafe storage path")
	// ErrCapacity means a write would grow a database file past its cap.
	ErrCapacity = errors.New("database is at its size limit")
)

const (
	// MaxNameRunes bounds a database name.
	MaxNameRunes = 80
	// DefaultBusyTimeout is how long SQLite waits for a lock held by another
	// connection before it reports busy.
	DefaultBusyTimeout = 3 * time.Second
	// DefaultOpTimeout bounds one whole store operation.
	DefaultOpTimeout = 10 * time.Second
	// MaxPages caps one database file (page size 4096: 1 GiB).
	MaxPages = 262144
	// FormatVersion is the on-disk format of the metadata tables.
	FormatVersion = 1
	tmpPrefix     = ".creating-"
)

// idPattern is the same shape the runtime protocol enforces for resource and
// operation IDs. Anything else never touches the file system.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type Options struct {
	// BusyTimeout and OpTimeout default when zero.
	BusyTimeout time.Duration
	OpTimeout   time.Duration
	// MaxPages caps one database file in 4096-byte pages. It defaults to
	// MaxPages (1 GiB) and is lowered only by tests.
	MaxPages int
	// ExportMaxRows, ExportMaxBytes and ExportTimeout bound one CSV export.
	// They default when zero.
	ExportMaxRows  int
	ExportMaxBytes int64
	ExportTimeout  time.Duration
}
type DatabaseInfo struct {
	ResourceID string            `json:"resourceId"`
	Name       string            `json:"name"`
	Revision   contract.Revision `json:"revision"`
}
type Schema struct {
	DatabaseInfo
	// Tables lists table names; Detail has their columns.
	Tables []string      `json:"tables"`
	Detail []TableSchema `json:"detail"`
}

// Store holds the databases under one private directory.
type Store struct {
	dir    string
	dbDir  string
	opts   Options
	mu     sync.Mutex
	ledger *sql.DB
	// failpoint is a test hook; see SetFailpoint.
	failpoint func(string) error
}

const ledgerSchema = `CREATE TABLE IF NOT EXISTS operations(
 operation_id TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 resource_id TEXT NOT NULL,
 name TEXT NOT NULL,
 state TEXT NOT NULL,
 schema_revision INTEGER NOT NULL DEFAULT 0,
 data_revision INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID`

const databaseSchema = `
CREATE TABLE tables_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE tables_operations(
 operation_id TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 schema_revision INTEGER NOT NULL,
 data_revision INTEGER NOT NULL,
 applied_at TEXT NOT NULL
) WITHOUT ROWID;` + catalogSchema

// Open prepares dir (mode 0700) and its ledger. The directory must not be a
// symbolic link. Half-built databases from an earlier crash are removed.
func Open(dir string, o Options) (*Store, error) {
	if o.BusyTimeout <= 0 {
		o.BusyTimeout = DefaultBusyTimeout
	}
	if o.OpTimeout <= 0 {
		o.OpTimeout = DefaultOpTimeout
	}
	if o.MaxPages <= 0 {
		o.MaxPages = MaxPages
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, err
	}
	if err = privateDir(abs); err != nil {
		return nil, err
	}
	s := &Store{dir: abs, dbDir: filepath.Join(abs, "databases"), opts: o}
	if err = privateDir(s.dbDir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dbDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			_ = os.Remove(filepath.Join(s.dbDir, e.Name()))
		}
	}
	ledger := filepath.Join(abs, "ledger.sqlite")
	if err = privateFile(ledger); err != nil {
		return nil, err
	}
	s.ledger, err = openSQLite(ledger, o.BusyTimeout, "FULL", "WAL", o.MaxPages)
	if err != nil {
		return nil, err
	}
	if _, err = s.ledger.Exec(ledgerSchema); err != nil {
		_ = s.ledger.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.ledger.Close() }

// privateDir makes sure path is a real directory (never a link) of mode 0700.
func privateDir(path string) error {
	fi, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		if err = os.Mkdir(path, 0o700); err != nil {
			return err
		}
	case err != nil:
		return err
	case !fi.IsDir():
		return ErrUnsafePath
	}
	if fi, err = os.Lstat(path); err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafePath
	}
	return os.Chmod(path, 0o700) // #nosec G302 -- directory mode: the owner needs the search bit
}

// privateFile creates path when absent (mode 0600) and refuses links.
func privateFile(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path is under the add-on data directory and is built from a validated identifier
		if e != nil {
			return e
		}
		return f.Close()
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return ErrUnsafePath
	}
	return nil
}

func dsn(path string, busy time.Duration, sync, journal string) string {
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	return fmt.Sprintf("file:%s?mode=rw&_busy_timeout=%d&_foreign_keys=on&_journal_mode=%s&_synchronous=%s&_txlock=immediate", esc, busy.Milliseconds(), journal, sync)
}

// capConnector opens every connection with PRAGMA max_page_count applied.
// The pragma is per connection, so setting it once on one connection would
// leave every later connection unbounded.
type capConnector struct {
	dsn      string
	drv      *sqlite3.SQLiteDriver
	maxPages int
}

func (c capConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.drv.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	ex, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("sqlite driver cannot set the size cap")
	}
	// PRAGMA max_page_count returns a row; Exec on this driver discards it.
	if _, err = ex.ExecContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", c.maxPages), nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (c capConnector) Driver() driver.Driver { return c.drv }

// capacityError types the SQLite "database is full" failure.
func capacityError(err error) error {
	var se sqlite3.Error
	if errors.As(err, &se) && se.Code == sqlite3.ErrFull {
		return fmt.Errorf("%w: %v", ErrCapacity, err)
	}
	return err
}

func openSQLite(path string, busy time.Duration, sync, journal string, maxPages int) (*sql.DB, error) {
	if maxPages <= 0 {
		maxPages = MaxPages
	}
	db := sql.OpenDB(capConnector{dsn: dsn(path, busy, sync, journal), drv: &sqlite3.SQLiteDriver{}, maxPages: maxPages})
	// One connection: statements of one operation share a transaction scope
	// and the pool can never exceed the per-file lock budget.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	return db, nil
}

func validID(id string) error {
	if !idPattern.MatchString(id) {
		return ErrInvalidID
	}
	return nil
}
func validOperation(id string) error {
	if !idPattern.MatchString(id) {
		return ErrInvalidOperation
	}
	return nil
}

// CheckName reports whether n is an acceptable database name.
func CheckName(n string) (string, error) { return n, validName(n) }

func validName(n string) error {
	if !utf8.ValidString(n) || strings.TrimSpace(n) != n || n == "" || utf8.RuneCountInString(n) > MaxNameRunes {
		return ErrInvalidName
	}
	for _, r := range n {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ErrInvalidName
		}
	}
	return nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dbDir, id+".db") }

// present reports whether anything, a link included, occupies the file path.
func (s *Store) present(id string) bool {
	_, err := os.Lstat(s.path(id))
	return err == nil
}

// openDatabase opens an existing database file that is a regular file and not
// a link, so an attacker-placed link is never followed or written through.
func (s *Store) openDatabase(id string) (*sql.DB, error) {
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
	return openSQLite(s.path(id), s.opts.BusyTimeout, "FULL", "WAL", s.opts.MaxPages)
}

type opRow struct {
	ID, Kind, Resource, Name, State string
	Revision                        contract.Revision
}

func (s *Store) lookup(ctx context.Context, id string) (opRow, bool, error) {
	var r opRow
	err := s.ledger.QueryRowContext(ctx, "SELECT operation_id,kind,resource_id,name,state,schema_revision,data_revision FROM operations WHERE operation_id=?", id).
		Scan(&r.ID, &r.Kind, &r.Resource, &r.Name, &r.State, &r.Revision.Schema, &r.Revision.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

func (s *Store) begin(ctx context.Context, op opRow) error {
	_, err := s.ledger.ExecContext(ctx, "INSERT INTO operations(operation_id,kind,resource_id,name,state) VALUES(?,?,?,?,'pending')", op.ID, op.Kind, op.Resource, op.Name)
	return err
}

func (s *Store) finish(ctx context.Context, id string, rev contract.Revision) error {
	_, err := s.ledger.ExecContext(ctx, "UPDATE operations SET state='committed', schema_revision=?, data_revision=? WHERE operation_id=?", rev.Schema, rev.Data, id)
	return err
}

func (r opRow) receipt() contract.Receipt {
	switch r.State {
	case "committed":
		payload, _ := json.Marshal(map[string]string{"resourceId": r.Resource, "name": r.Name})
		return contract.Receipt{OperationID: r.ID, State: contract.ReceiptCommitted, Revision: r.Revision, Result: contract.Result{Payload: payload}}
	case "pending":
		return contract.Receipt{OperationID: r.ID, State: contract.ReceiptPending}
	}
	return contract.Receipt{OperationID: r.ID, State: contract.ReceiptUnknown}
}

func (s *Store) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.opts.OpTimeout)
}

type meta struct {
	Name       string
	ResourceID string
	CreatedBy  string
	Revision   contract.Revision
}

func readMeta(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (meta, error) {
	rows, err := q.QueryContext(ctx, "SELECT key,value FROM tables_meta")
	if err != nil {
		return meta{}, err
	}
	defer func() { _ = rows.Close() }()
	var m meta
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			return m, err
		}
		switch k {
		case "name":
			m.Name = v
		case "resource_id":
			m.ResourceID = v
		case "created_by":
			m.CreatedBy = v
		case "schema_revision":
			if _, err = fmt.Sscan(v, &m.Revision.Schema); err != nil {
				return m, fmt.Errorf("read the schema revision: %w", err)
			}
		case "data_revision":
			if _, err = fmt.Sscan(v, &m.Revision.Data); err != nil {
				return m, fmt.Errorf("read the data revision: %w", err)
			}
		}
	}
	return m, rows.Err()
}

// inspect reads and verifies a database's metadata. A file whose recorded ID is
// not its name was copied or swapped in and is refused.
func (s *Store) inspect(ctx context.Context, id string) (meta, []catalogTable, error) {
	db, err := s.openDatabase(id)
	if err != nil {
		return meta{}, nil, err
	}
	defer func() { _ = db.Close() }()
	m, err := readMeta(ctx, db)
	if err != nil {
		return m, nil, err
	}
	if m.ResourceID != id || m.Revision.Schema == 0 {
		return m, nil, ErrUnsafePath
	}
	cat, err := readCatalog(ctx, db)
	return m, cat, err
}

// build creates the database in a private temporary file and links it into
// place only when it is complete, so no reader or crash sees a partial file.
func (s *Store) build(ctx context.Context, id, name, opID string) error {
	var salt [8]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return err
	}
	tmp := filepath.Join(s.dbDir, tmpPrefix+id+"-"+hex.EncodeToString(salt[:]))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path is under the add-on data directory and is built from a validated identifier
	if err != nil {
		return err
	}
	_ = f.Close()
	defer func() { _ = os.Remove(tmp) }()
	defer func() { _ = os.Remove(tmp + "-wal") }()
	defer func() { _ = os.Remove(tmp + "-shm") }()
	defer func() { _ = os.Remove(tmp + "-journal") }()
	db, err := openSQLite(tmp, s.opts.BusyTimeout, "FULL", "DELETE", s.opts.MaxPages)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err == nil {
		_, err = tx.ExecContext(ctx, databaseSchema)
		for k, v := range map[string]string{"name": name, "resource_id": id, "created_by": opID, "format": fmt.Sprint(formatVersion), "schema_revision": "1", "data_revision": "0"} {
			if err != nil {
				break
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO tables_meta(key,value) VALUES(?,?)", k, v)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO tables_operations(operation_id,kind,schema_revision,data_revision,applied_at) VALUES(?,?,?,?,?)", opID, "create", 1, 0, time.Now().UTC().Format(time.RFC3339Nano))
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return capacityError(err)
	}
	if f, e := os.Open(tmp); e == nil { // #nosec G304 -- path is under the add-on data directory and is built from a validated identifier
		_ = f.Sync()
		_ = f.Close()
	}
	// Link never replaces: an existing file or link makes this fail.
	if err = os.Link(tmp, s.path(id)); err != nil {
		if os.IsExist(err) {
			return ErrExists
		}
		return err
	}
	if d, e := os.Open(s.dbDir); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Create makes the database resourceID with the given name. The same
// operation ID with the same arguments returns the same result, also after a
// crash or a lost reply, and never produces a second file.
func (s *Store) Create(ctx context.Context, resourceID, name, operationID string) (DatabaseInfo, error) {
	if err := validID(resourceID); err != nil {
		return DatabaseInfo{}, err
	}
	if err := validOperation(operationID); err != nil {
		return DatabaseInfo{}, err
	}
	if err := validName(name); err != nil {
		return DatabaseInfo{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	op, found, err := s.lookup(ctx, operationID)
	if err != nil {
		return DatabaseInfo{}, err
	}
	want := opRow{ID: operationID, Kind: "create", Resource: resourceID, Name: name}
	if found {
		if op.Kind != want.Kind || op.Resource != resourceID || op.Name != name {
			return DatabaseInfo{}, ErrConflict
		}
		if op.State == "committed" {
			return DatabaseInfo{ResourceID: resourceID, Name: name, Revision: op.Revision}, nil
		}
	} else {
		if s.present(resourceID) {
			return DatabaseInfo{}, ErrExists
		}
		if err = s.begin(ctx, want); err != nil {
			return DatabaseInfo{}, err
		}
	}
	if !s.present(resourceID) {
		if err = s.build(ctx, resourceID, name, operationID); err != nil {
			return DatabaseInfo{}, err
		}
	}
	// The file exists: it must be the one this operation made.
	m, _, err := s.inspect(ctx, resourceID)
	if err != nil {
		return DatabaseInfo{}, err
	}
	if m.CreatedBy != operationID {
		return DatabaseInfo{}, ErrExists
	}
	if err = s.finish(ctx, operationID, m.Revision); err != nil {
		return DatabaseInfo{}, err
	}
	return DatabaseInfo{ResourceID: resourceID, Name: name, Revision: m.Revision}, nil
}

// Rename changes the name stored in the database resourceID. It never
// consults an owner: the host has already admitted the caller for exactly this
// resource.
func (s *Store) Rename(ctx context.Context, resourceID, name, operationID string) (contract.Receipt, error) {
	if err := validID(resourceID); err != nil {
		return contract.Receipt{}, err
	}
	if err := validOperation(operationID); err != nil {
		return contract.Receipt{}, err
	}
	if err := validName(name); err != nil {
		return contract.Receipt{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	op, found, err := s.lookup(ctx, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	want := opRow{ID: operationID, Kind: "rename", Resource: resourceID, Name: name}
	if found {
		if op.Kind != want.Kind || op.Resource != resourceID || op.Name != name {
			return contract.Receipt{}, ErrConflict
		}
		if op.State == "committed" {
			return op.receipt(), nil
		}
	}
	db, err := s.openDatabase(resourceID)
	if err != nil {
		return contract.Receipt{}, err
	}
	defer func() { _ = db.Close() }()
	if !found {
		if err = s.begin(ctx, want); err != nil {
			return contract.Receipt{}, err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Receipt{}, err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	m, err := readMeta(ctx, tx)
	if err != nil {
		return contract.Receipt{}, err
	}
	if m.ResourceID != resourceID {
		return contract.Receipt{}, ErrUnsafePath
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO tables_operations(operation_id,kind,schema_revision,data_revision,applied_at) VALUES(?,?,?,?,?)",
		operationID, "rename", m.Revision.Schema, m.Revision.Data, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return contract.Receipt{}, capacityError(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tables_meta SET value=? WHERE key='name'", name); err != nil {
		return contract.Receipt{}, capacityError(err)
	}
	if err = tx.Commit(); err != nil {
		return contract.Receipt{}, capacityError(err)
	}
	rollback = false
	if err = s.finish(ctx, operationID, m.Revision); err != nil {
		return contract.Receipt{}, err
	}
	row, _, err := s.lookup(ctx, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	return row.receipt(), nil
}

// Delete removes the database resourceID: its file and the journal files
// beside it. It is the host's UI-only deletion, reached through the add-on's
// admin tool and never through a model tool. The same operation ID with the
// same arguments returns the same receipt, also after a crash between the
// removal and the receipt, and a database that is already gone is the same
// outcome. The other operations of the database stay in the ledger.
func (s *Store) Delete(ctx context.Context, resourceID, operationID string) (contract.Receipt, error) {
	if err := validID(resourceID); err != nil {
		return contract.Receipt{}, err
	}
	if err := validOperation(operationID); err != nil {
		return contract.Receipt{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	op, found, err := s.lookup(ctx, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	want := opRow{ID: operationID, Kind: "delete", Resource: resourceID}
	if found {
		if op.Kind != want.Kind || op.Resource != resourceID {
			return contract.Receipt{}, ErrConflict
		}
		if op.State == "committed" {
			return op.receipt(), nil
		}
	} else if err = s.begin(ctx, want); err != nil {
		return contract.Receipt{}, err
	}
	// The revision the deletion removed, when the file still tells it. A
	// retry after a crash finds no file and records the first revision.
	rev := contract.Revision{Schema: 1}
	if fi, e := os.Lstat(s.path(resourceID)); e == nil {
		if !fi.Mode().IsRegular() {
			return contract.Receipt{}, ErrUnsafePath
		}
		if m, _, e := s.inspect(ctx, resourceID); e == nil && m.Revision.Schema != 0 {
			rev = m.Revision
		}
	} else if !os.IsNotExist(e) {
		return contract.Receipt{}, e
	}
	if err = s.fail("delete-before-remove"); err != nil {
		return contract.Receipt{}, err
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if e := os.Remove(s.path(resourceID) + suffix); e != nil && !os.IsNotExist(e) {
			return contract.Receipt{}, e
		}
	}
	if d, e := os.Open(s.dbDir); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	if err = s.fail("delete-after-remove"); err != nil {
		return contract.Receipt{}, err
	}
	if err = s.finish(ctx, operationID, rev); err != nil {
		return contract.Receipt{}, err
	}
	row, _, err := s.lookup(ctx, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	return row.receipt(), nil
}

// Inspect describes one database.
func (s *Store) Inspect(ctx context.Context, resourceID string) (Schema, error) {
	if err := validID(resourceID); err != nil {
		return Schema{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	m, cat, err := s.inspect(ctx, resourceID)
	if err != nil {
		return Schema{}, err
	}
	detail := make([]TableSchema, 0, len(cat))
	for _, t := range cat {
		detail = append(detail, t.TableSchema)
	}
	return Schema{DatabaseInfo: DatabaseInfo{ResourceID: resourceID, Name: m.Name, Revision: m.Revision}, Tables: tableNames(cat), Detail: detail}, nil
}

// Exists reports whether a complete database with this ID is present. A
// database is only ever linked into place whole, so presence means complete.
func (s *Store) Exists(ctx context.Context, resourceID string) (bool, error) {
	if err := validID(resourceID); err != nil {
		return false, err
	}
	_, err := s.Inspect(ctx, resourceID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Receipt is the recorded outcome of an operation. An operation never seen is
// unknown, which is not a failure to apply.
func (s *Store) Receipt(ctx context.Context, operationID string) (contract.Receipt, error) {
	if err := validOperation(operationID); err != nil {
		return contract.Receipt{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	op, found, err := s.lookup(ctx, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	if !found {
		return contract.Receipt{OperationID: operationID, State: contract.ReceiptUnknown}, nil
	}
	return op.receipt(), nil
}
