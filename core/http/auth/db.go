package auth

import (
	"fmt"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// InitDB initializes the auth database. If databaseURL starts with "postgres://"
// or "postgresql://", it connects to PostgreSQL; otherwise it treats the value
// as a SQLite file path (use ":memory:" for in-memory).
// SQLite support requires building with the "auth" build tag (CGO).
func InitDB(databaseURL string) (*gorm.DB, error) {
	var dialector gorm.Dialector

	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		dialector = postgres.Open(databaseURL)
	} else {
		d, err := openSQLiteDialector(databaseURL)
		if err != nil {
			return nil, err
		}
		dialector = d
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open auth database: %w", err)
	}

	if err := db.AutoMigrate(&User{}, &Session{}, &UserAPIKey{}, &UsageRecord{}, &UserPermission{}, &InviteCode{}, &QuotaRule{}); err != nil {
		return nil, fmt.Errorf("failed to migrate auth tables: %w", err)
	}

	// Backfill: users created before the provider column existed have an empty
	// provider — treat them as local accounts so the UI can identify them.
	db.Exec("UPDATE users SET provider = ? WHERE provider = '' OR provider IS NULL", ProviderLocal)

	// Create composite index on users(provider, subject) for fast OAuth lookups
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_users_provider_subject ON users(provider, subject)").Error; err != nil {
		// Ignore error on postgres if index already exists
		if !strings.Contains(err.Error(), "already exists") {
			return nil, fmt.Errorf("failed to create composite index: %w", err)
		}
	}

	return db, nil
}
