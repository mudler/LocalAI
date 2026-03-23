package advisorylock

import (
	"context"
	"fmt"
	"hash/fnv"

	"gorm.io/gorm"
)

// Well-known advisory lock keys for distributed coordination.
const (
	KeyHealthCheck    int64 = 101 // Only one frontend runs node health checks
	KeySchemaMigrate  int64 = 103 // Serialize AutoMigrate across frontend + workers
)

// TryLock attempts to acquire a PostgreSQL advisory lock (non-blocking).
// Returns true if the lock was acquired.
func TryLock(db *gorm.DB, key int64) bool {
	var acquired bool
	db.Raw("SELECT pg_try_advisory_lock(?)", key).Scan(&acquired)
	return acquired
}

// Unlock releases a PostgreSQL advisory lock.
func Unlock(db *gorm.DB, key int64) {
	db.Exec("SELECT pg_advisory_unlock(?)", key)
}

// WithLock acquires a PostgreSQL advisory lock for the duration of fn.
// Uses a dedicated database connection to ensure the lock is held correctly.
// The lock is automatically released when fn returns (or panics).
func WithLock(db *gorm.DB, key int64, fn func() error) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("advisorylock: getting sql.DB: %w", err)
	}

	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("advisorylock: getting connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(context.Background(),
		"SELECT pg_advisory_lock($1)", key); err != nil {
		return fmt.Errorf("advisorylock: acquiring lock %d: %w", key, err)
	}
	defer conn.ExecContext(context.Background(),
		"SELECT pg_advisory_unlock($1)", key)

	return fn()
}

// KeyFromUUID converts a UUID (as [16]byte) to a lock key via FNV-1a hashing.
func KeyFromUUID(b [16]byte) int64 {
	h := fnv.New64a()
	h.Write(b[:])
	return int64(h.Sum64()>>1) | 0x100000000
}
