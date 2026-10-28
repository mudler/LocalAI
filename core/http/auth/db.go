package auth

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/pkg/sqlitedb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenSQLiteFile opens a SQLite file with the project settings of
// pkg/sqlitedb (WAL, busy timeout, immediate write transactions) and creates no tables. Features
// with their own tables use it when there is no shared database. It needs the
// "auth" build tag, like InitDB.
func OpenSQLiteFile(path string) (*gorm.DB, error) {
	return sqlitedb.Open(path, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
}

// InitDB initializes the auth database. If databaseURL starts with "postgres://"
// or "postgresql://", it connects to PostgreSQL; otherwise it treats the value
// as a SQLite file path (use ":memory:" for in-memory).
// SQLite support requires building with the "auth" build tag (CGO).
func InitDB(databaseURL string) (*gorm.DB, error) {
	var db *gorm.DB
	var err error
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		db, err = gorm.Open(postgres.Open(databaseURL), cfg)
	} else {
		db, err = sqlitedb.Open(databaseURL, cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open auth database: %w", err)
	}

	if err := db.AutoMigrate(&User{}, &Session{}, &UserAPIKey{}, &UsageRecord{}, &UserPermission{}, &InviteCode{}, &QuotaRule{}); err != nil {
		return nil, fmt.Errorf("failed to migrate auth tables: %w", err)
	}

	// Backfill: users created before the provider column existed have an empty
	// provider - treat them as local accounts so the UI can identify them.
	db.Exec("UPDATE users SET provider = ? WHERE provider = '' OR provider IS NULL", ProviderLocal)

	// Backfill: pre-feature usage_records have no source column. Classify them so the
	// new per-source aggregators include them.
	if err := BackfillUsageSource(db); err != nil {
		return nil, fmt.Errorf("failed to backfill usage source: %w", err)
	}

	// Create composite index on users(provider, subject) for fast OAuth lookups
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_users_provider_subject ON users(provider, subject)").Error; err != nil {
		// Ignore error on postgres if index already exists
		if !strings.Contains(err.Error(), "already exists") {
			return nil, fmt.Errorf("failed to create composite index: %w", err)
		}
	}

	return db, nil
}
