package auth

import (
	"net/url"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// openSQLiteDialector returns the SQLite dialector of LocalAI. It is the
// pure-Go driver (modernc SQLite, through the glebarez GORM dialector), so a
// binary built with CGO_ENABLED=0 and no build tag opens SQLite files too.
func openSQLiteDialector(path string) (gorm.Dialector, error) {
	return sqlite.Open(buildSQLiteDSN(path)), nil
}

// buildSQLiteDSN augments a SQLite file path with connection pragmas that make
// the auth DB resilient on slow or contended storage.
//
//   - busy_timeout(5000) makes SQLite retry for up to 5s on SQLITE_BUSY instead
//     of failing immediately. Network-backed storage (SMB/CIFS/NFS, e.g. Azure
//     Files) is prone to transient lock contention during migration (see #10506).
//   - _txlock=immediate takes the write lock at BEGIN, avoiding deadlocks when a
//     read transaction later upgrades to a write during AutoMigrate.
//   - _time_format=sqlite writes times in the layout the previous (cgo) driver
//     used, so a database file stays readable by both.
//
// We deliberately do NOT set WAL journal mode: WAL relies on a shared-memory
// mmap that does not work over SMB/NFS, which is exactly the failing case here.
//
// Caller-supplied values are preserved. The pragma options of the previous
// driver (_busy_timeout, _foreign_keys, _journal_mode, _synchronous) are
// still accepted and become _pragma values, so an existing database URL keeps
// working.
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
	if !hasPragma("busy_timeout") {
		values.Add("_pragma", "busy_timeout(5000)")
	}
	if values.Get("_txlock") == "" {
		values.Set("_txlock", "immediate")
	}
	if values.Get("_time_format") == "" {
		values.Set("_time_format", "sqlite")
	}

	return base + "?" + values.Encode()
}
