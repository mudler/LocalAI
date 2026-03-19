//go:build auth

package auth

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openSQLiteDialector(path string) (gorm.Dialector, error) {
	return sqlite.Open(path), nil
}
