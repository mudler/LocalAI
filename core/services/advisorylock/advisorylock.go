package advisorylock

import (
	"context"
	"fmt"
	"hash/fnv"

	"gorm.io/gorm"
)

// TryWithLockCtx attempts to acquire a PostgreSQL advisory lock using the provided context.
// Returns (true, nil) if the lock was acquired and fn executed, (false, nil) if the lock
// was already held, or (false, error) on failure.
func TryWithLockCtx(ctx context.Context, db *gorm.DB, key int64, fn func() error) (bool, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return false, fmt.Errorf("get sql.DB: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("advisory lock conn: %w", err)
	}
	defer conn.Close()

	var acquired bool
	if err := conn.QueryRowContext(ctx,
		"SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		return false, fmt.Errorf("pg_try_advisory_lock: %w", err)
	}
	if !acquired {
		return false, nil
	}
	// Always unlock, even if context is cancelled (use Background for cleanup)
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)

	return true, fn()
}

// Deprecated: Use TryWithLockCtx instead.
//
// TryWithLock attempts a non-blocking advisory lock on a dedicated connection.
// If acquired, fn runs and the lock is released on the same connection.
// Returns (true, fn-error) if acquired, (false, nil) if not.
func TryWithLock(db *gorm.DB, key int64, fn func() error) (bool, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return false, fmt.Errorf("advisorylock: getting sql.DB: %w", err)
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return false, fmt.Errorf("advisorylock: getting connection: %w", err)
	}
	defer conn.Close()

	var acquired bool
	if err := conn.QueryRowContext(context.Background(),
		"SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		return false, fmt.Errorf("advisorylock: try lock %d: %w", key, err)
	}
	if !acquired {
		return false, nil
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
	return true, fn()
}

// Deprecated: TryLock may use different pool connections for lock/unlock.
// Use TryWithLock instead, which pins a single connection.
//
// TryLock attempts to acquire a PostgreSQL advisory lock (non-blocking).
// Returns true if the lock was acquired.
func TryLock(db *gorm.DB, key int64) bool {
	var acquired bool
	db.Raw("SELECT pg_try_advisory_lock(?)", key).Scan(&acquired)
	return acquired
}

// Deprecated: Unlock may use a different pool connection than TryLock.
// Use TryWithLock instead, which pins a single connection.
//
// Unlock releases a PostgreSQL advisory lock.
func Unlock(db *gorm.DB, key int64) {
	db.Exec("SELECT pg_advisory_unlock(?)", key)
}

// Deprecated: Use WithLockCtx instead.
//
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

// KeyFromString converts an arbitrary string to an advisory lock key via FNV-1a hashing.
func KeyFromString(s string) int64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return int64(h.Sum64()>>1) | 0x100000000
}

// WithLockCtx is like WithLock but respects context cancellation.
// If ctx is cancelled while waiting for the lock, the function returns ctx.Err().
func WithLockCtx(ctx context.Context, db *gorm.DB, key int64, fn func() error) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("advisorylock: getting sql.DB: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("advisorylock: getting connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return fmt.Errorf("advisorylock: acquiring lock %d: %w", key, err)
	}
	// Always release the lock, even if ctx is cancelled
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)

	return fn()
}
