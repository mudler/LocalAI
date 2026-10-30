// Package gormstore is a workspace.Store on a GORM database (SQLite or
// PostgreSQL) and a blob.Store.
//
// The database keeps the tree of nodes, the journal of operations, the change
// sets and the grants. The bytes of files live in the blob store, named by
// their SHA-256, so they can sit on a local disk or in an object store that
// every frontend of a cluster shares. Nothing is hard-deleted: a delete sets a
// tombstone and undo writes a new change set.
//
// The SQL is the same for both databases except for the few places that the
// dialect type below hides: placeholders, ids of inserted rows and the table
// definitions.
package gormstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	. "github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

// rootID is the node ID of the root folder.
const rootID = 1

// Options configures a Store.
type Options struct {
	// Admin is the principal that manages grants. It has write access to
	// every node. Empty means no admin: Grant and Revoke always fail.
	Admin string
	// MaxFileSize limits each file in bytes. Zero means no limit.
	MaxFileSize int64
	// Now returns the time recorded in change sets. Nil means time.Now.
	Now func() time.Time
}

// Workspace is a workspace.Store.
type Workspace struct {
	db      *conn
	blobs   blob.Store
	admin   string
	maxSize int64
	now     func() time.Time
}

var (
	_ Store       = (*Workspace)(nil)
	_ GrantLister = (*Workspace)(nil)
)

// New returns a Store on db and creates its tables. db must be SQLite or
// PostgreSQL.
func New(db *gorm.DB, blobs blob.Store, o Options) (*Workspace, error) {
	if db == nil || blobs == nil {
		return nil, errors.New("workspace: a database and a blob store are required")
	}
	raw, err := db.DB()
	if err != nil {
		return nil, err
	}
	var d dialect
	switch db.Dialector.Name() {
	case "sqlite":
		d = sqliteDialect
	case "postgres":
		d = postgresDialect
	default:
		return nil, fmt.Errorf("workspace: database %q is not supported", db.Dialector.Name())
	}
	c := &conn{DB: raw, d: d}
	if err := migrate(context.Background(), c); err != nil {
		return nil, err
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Workspace{db: c, blobs: blobs, admin: o.Admin, maxSize: o.MaxFileSize, now: now}, nil
}

// limit returns the smaller non-zero of the store limit and a per-call limit.
func (s *Workspace) limit(perCall int64) int64 {
	switch {
	case s.maxSize == 0:
		return perCall
	case perCall == 0 || perCall > s.maxSize:
		return s.maxSize
	default:
		return perCall
	}
}

// kindFor returns the change set kind for a call by a.
func (s *Workspace) kindFor(a Actor) ChangeSetKind {
	if a.ChatID != "" {
		return ChangeAgent
	}
	return ChangeUser
}

func principals(a Actor) []string { return a.Principals() }
func label(a Actor) string        { return a.Label() }

var (
	cleanName    = CleanName
	splitPath    = SplitPath
	joinPath     = JoinPath
	conflictName = ConflictName
	foldName     = FoldName
	joinChild    = JoinChild
)

// dialect holds what differs between the databases.
type dialect struct {
	name string
	// idColumn is the definition of an integer primary key that the database fills in.
	idColumn string
}

var (
	sqliteDialect   = dialect{name: "sqlite", idColumn: "INTEGER PRIMARY KEY"}
	postgresDialect = dialect{name: "postgres", idColumn: "BIGSERIAL PRIMARY KEY"}
)

// conn is a database handle that writes placeholders the way its database
// wants them. It satisfies querier.
type conn struct {
	*sql.DB
	d dialect
}

// rebind turns the "?" placeholders of a query into "$1", "$2" for PostgreSQL.
func (d dialect) rebind(q string) string {
	if d.name != "postgres" {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (c *conn) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return c.DB.ExecContext(ctx, c.d.rebind(q), args...)
}

func (c *conn) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return c.DB.QueryContext(ctx, c.d.rebind(q), args...)
}

func (c *conn) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return c.DB.QueryRowContext(ctx, c.d.rebind(q), args...)
}

// tx is an open database transaction with the same rebinding.
type tx struct {
	*sql.Tx
	d dialect
}

func (c *conn) BeginTx(ctx context.Context) (*tx, error) {
	t, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &tx{Tx: t, d: c.d}, nil
}

func (t *tx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.Tx.ExecContext(ctx, t.d.rebind(q), args...)
}

func (t *tx) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(ctx, t.d.rebind(q), args...)
}

func (t *tx) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(ctx, t.d.rebind(q), args...)
}

// mapUnique turns a unique-index violation of either database into ErrExists.
func mapUnique(err error) error {
	if err == nil {
		return nil
	}
	m := err.Error()
	if strings.Contains(m, "UNIQUE constraint failed") || strings.Contains(m, "duplicate key value") || strings.Contains(m, "SQLSTATE 23505") {
		return ErrExists
	}
	return err
}
