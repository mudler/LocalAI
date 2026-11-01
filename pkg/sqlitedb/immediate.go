package sqlitedb

import (
	"context"
	"database/sql"
	"database/sql/driver"
)

// driverName is the database/sql name of the pure-Go driver wrapped so that
// every write transaction starts with BEGIN IMMEDIATE.
const driverName = "sqlite_localai"

// The driver has a _txlock=immediate option, but it never reads it when
// _time_format is also set (applyQueryParams returns after _time_format), and
// the project needs both: _time_format keeps the time layout of the previous
// driver. Without the lock, a transaction that reads and then writes starts
// deferred and fails at once with SQLITE_BUSY_SNAPSHOT when another connection
// committed in between; the busy timeout does not apply to it. The wrapper
// makes the lock mode independent of the other options.
func init() {
	db, err := sql.Open("sqlite", "")
	if err != nil {
		panic("sqlitedb: pure-Go SQLite driver is missing: " + err.Error())
	}
	sql.Register(driverName, immediateDriver{inner: db.Driver()})
	_ = db.Close()
}

type immediateDriver struct{ inner driver.Driver }

func (d immediateDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &immediateConn{Conn: c}, nil
}

// immediateConn forwards to the pure-Go driver connection and replaces the
// start of a transaction.
type immediateConn struct{ driver.Conn }

//nolint:staticcheck // the driver implements the non-context Execer and Queryer only.
func (c *immediateConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	return c.Conn.(driver.Execer).Exec(query, args) //nolint:staticcheck
}

//nolint:staticcheck // see Exec.
func (c *immediateConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	return c.Conn.(driver.Queryer).Query(query, args) //nolint:staticcheck
}

func (c *immediateConn) Begin() (driver.Tx, error) { //nolint:staticcheck // required by driver.Conn
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx takes the write lock at once for a read-write transaction. A
// read-only one stays deferred and is a plain snapshot read.
func (c *immediateConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if opts.ReadOnly {
		return c.Conn.Begin() //nolint:staticcheck
	}
	if _, err := c.Exec("BEGIN IMMEDIATE", nil); err != nil {
		return nil, err
	}
	return immediateTx{c: c}, nil
}

type immediateTx struct{ c *immediateConn }

func (t immediateTx) Commit() error {
	_, err := t.c.Exec("COMMIT", nil)
	return err
}

func (t immediateTx) Rollback() error {
	_, err := t.c.Exec("ROLLBACK", nil)
	return err
}
