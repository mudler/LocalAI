// Package dbdialect tells which database a gorm handle talks to. It imports
// only gorm, so that a leaf package can use it.
package dbdialect

import (
	"strings"

	"gorm.io/gorm"
)

// IsPostgres reports whether the gorm dialect is PostgreSQL. The connection
// fence, the advisory locks and the LISTEN carrier are built out of
// PostgreSQL-only pieces, and the single-binary path runs on SQLite.
func IsPostgres(db *gorm.DB) bool {
	return strings.Contains(db.Dialector.Name(), "postgres")
}
