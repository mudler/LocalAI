//go:build auth

package auth

import (
	"net/url"
	"strings"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openSQLiteDialector(path string) (gorm.Dialector, error) {
	return sqlite.Open(buildSQLiteDSN(path)), nil
}

// buildSQLiteDSN augments a SQLite file path with connection pragmas that make
// the auth DB resilient on slow or contended storage.
//
//   - _busy_timeout=5000 makes SQLite retry for up to 5s on SQLITE_BUSY instead
//     of failing immediately. Network-backed storage (SMB/CIFS/NFS, e.g. Azure
//     Files) is prone to transient lock contention during migration (see #10506).
//   - _txlock=immediate takes the write lock at BEGIN, avoiding deadlocks when a
//     read transaction later upgrades to a write during AutoMigrate.
//
// We deliberately do NOT set WAL journal mode: WAL relies on a shared-memory
// mmap that does not work over SMB/NFS, which is exactly the failing case here.
//
// Caller-supplied values for either pragma are preserved.
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

	if values.Get("_busy_timeout") == "" {
		values.Set("_busy_timeout", "5000")
	}
	if values.Get("_txlock") == "" {
		values.Set("_txlock", "immediate")
	}

	return base + "?" + values.Encode()
}
