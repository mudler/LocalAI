//go:build !auth

package auth

import (
	"fmt"

	"gorm.io/gorm"
)

func openSQLiteDialector(path string) (gorm.Dialector, error) {
	return nil, fmt.Errorf("SQLite auth database requires building with -tags auth (CGO); use DATABASE_URL with PostgreSQL instead")
}
