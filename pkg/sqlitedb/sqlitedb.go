// Package sqlitedb opens SQLite for the whole project. Every SQLite file that
// LocalAI owns (the account database, which also holds the chats, the
// workspace and the triggers) goes through Open, so the connection settings
// live in one place.
package sqlitedb

import (
	"net/url"
	"sort"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// maxOpenConns bounds the pool of a file database.
const maxOpenConns = 16

// Dialector returns the SQLite dialector of LocalAI. It is the pure-Go driver
// (modernc SQLite, through the glebarez GORM dialector), so a binary built with
// CGO_ENABLED=0 and no build tag opens SQLite files too.
func Dialector(path string) gorm.Dialector {
	return &sqlite.Dialector{DriverName: driverName, DSN: buildSQLiteDSN(path)}
}

// Open opens the SQLite file at path (or ":memory:") with the project settings.
func Open(path string, cfg *gorm.Config) (*gorm.DB, error) {
	db, err := gorm.Open(Dialector(path), cfg)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	switch {
	case IsMemory(path):
		// Each connection of a pool would see its own empty in-memory database.
		sqlDB.SetMaxOpenConns(1)
	default:
		// SQLite has one writer. A bounded pool keeps the writers waiting
		// on busy_timeout from piling up file handles; readers still run in
		// parallel under WAL.
		sqlDB.SetMaxOpenConns(maxOpenConns)
		sqlDB.SetMaxIdleConns(maxOpenConns)
	}
	return db, nil
}

// IsMemory tells whether path is an in-memory SQLite database.
func IsMemory(path string) bool {
	return strings.HasPrefix(strings.TrimPrefix(path, "file:"), ":memory:")
}

// buildSQLiteDSN augments a SQLite file path with the project defaults:
//
//   - journal_mode(WAL): readers no longer block the writer and the writer no
//     longer blocks readers. In the default rollback journal a read cursor held
//     open by one connection makes the commit of a writer on another connection
//     fail with SQLITE_BUSY, and the busy handler cannot help (the writer waits
//     for a reader that may wait for the writer). This was the cause of the
//     intermittent "database is locked" errors of the chats stores.
//   - synchronous(NORMAL): the usual pairing with WAL; a power loss can drop
//     the last commits but never corrupts the file.
//   - busy_timeout(10000): a writer waits up to 10s for the one write lock
//     instead of failing at once. Network-backed storage is prone to transient
//     lock contention during migration (see #10506).
//   - foreign_keys(1): enforce the foreign keys the schema declares.
//   - _txlock=immediate takes the write lock at BEGIN, avoiding the failure of
//     a read transaction that later upgrades to a write. The driver ignores this
//     option next to _time_format, so the driver of Dialector is wrapped (see
//     immediate.go) and enforces it.
//   - _time_format=sqlite writes times in the layout the previous (cgo) driver
//     used, so a database file stays readable by both.
//
// WAL needs a shared-memory file and does not work over SMB/NFS. On such
// storage, pass _pragma=journal_mode(DELETE) in the database URL: a
// caller-supplied value always wins.
//
// The pragma options of the previous driver (_busy_timeout, _foreign_keys,
// _journal_mode, _synchronous) are still accepted and become _pragma values,
// so an existing database URL keeps working.
func buildSQLiteDSN(path string) string {
	base := path
	rawQuery := ""
	if i := strings.IndexByte(path, '?'); i >= 0 {
		base = path[:i]
		rawQuery = path[i+1:]
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		// An unparseable query string means a hand-crafted DSN we should not
		// risk corrupting; leave it untouched.
		return path
	}

	hasPragma := func(name string) bool {
		for _, p := range values["_pragma"] {
			if strings.HasPrefix(strings.ToLower(p), name+"(") {
				return true
			}
		}
		return false
	}
	for _, legacy := range []string{"busy_timeout", "foreign_keys", "journal_mode", "synchronous"} {
		if v := values.Get("_" + legacy); v != "" {
			values.Del("_" + legacy)
			if !hasPragma(legacy) {
				values.Add("_pragma", legacy+"("+v+")")
			}
		}
	}
	// busy_timeout comes first: the pragmas that follow may already need a lock.
	// A caller-supplied journal_mode is kept; WAL does not apply to :memory:.
	defaults := []string{"busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)"}
	if !IsMemory(base) {
		defaults = append(defaults, "journal_mode(WAL)")
	}
	var pragmas []string
	for _, d := range defaults {
		if !hasPragma(d[:strings.IndexByte(d, '(')]) {
			pragmas = append(pragmas, d)
		}
	}
	pragmas = append(pragmas, values["_pragma"]...)
	// busy_timeout goes first.
	sort.SliceStable(pragmas, func(i, j int) bool {
		return strings.HasPrefix(pragmas[i], "busy_timeout(") && !strings.HasPrefix(pragmas[j], "busy_timeout(")
	})
	values["_pragma"] = pragmas
	if values.Get("_txlock") == "" {
		values.Set("_txlock", "immediate")
	}
	if values.Get("_time_format") == "" {
		values.Set("_time_format", "sqlite")
	}

	return base + "?" + values.Encode()
}
