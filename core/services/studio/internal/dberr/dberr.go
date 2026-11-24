// Package dberr reads the errors of the SQL databases the Studio stores run
// on (SQLite and PostgreSQL) through GORM.
package dberr

import "strings"

// IsUniqueViolation reports whether err is a unique constraint violation.
// SQLite says "UNIQUE constraint failed" and PostgreSQL "duplicate key
// value violates unique constraint"; GORM passes both on as text.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate key")
}
