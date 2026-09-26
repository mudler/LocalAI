package advisorylock

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// heldLockCheckTimeout bounds the liveness check and the unlock, so a hung
// database cannot stall the caller's loop.
const heldLockCheckTimeout = 5 * time.Second

// HeldLock is an advisory lock that stays taken across calls, for leader
// election where leadership must be sticky. TryWithLockCtx releases the lock
// when fn returns; with a short fn, every contender wins it in turn and
// leadership flips on each tick.
//
// On PostgreSQL the lock belongs to one session, so HeldLock keeps a
// dedicated connection out of the pool while it holds the lock. If that
// session dies, the server drops the lock and another instance can take it.
// On other dialects it holds the package's in-process lock for the key.
type HeldLock struct {
	db  *gorm.DB
	key int64

	mu    sync.Mutex
	conn  *sql.Conn // PostgreSQL: the session that holds the lock
	local bool      // other dialects: this lock holds the in-process slot
}

// NewHeldLock returns a lock for key on db. It takes nothing until TryAcquire.
func NewHeldLock(db *gorm.DB, key int64) *HeldLock {
	return &HeldLock{db: db, key: key}
}

// TryAcquire takes the lock without blocking. It returns true if this
// HeldLock holds the lock afterwards, including when it already held it.
func (l *HeldLock) TryAcquire(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn != nil || l.local {
		return true, nil
	}

	if !isPostgres(l.db) {
		select {
		case localLockChan(l.key) <- struct{}{}:
			l.local = true
			return true, nil
		default:
			return false, nil
		}
	}

	sqlDB, err := l.db.DB()
	if err != nil {
		return false, fmt.Errorf("get sql.DB: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("advisory lock conn: %w", err)
	}
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", l.key).Scan(&acquired); err != nil {
		// The lock may have been granted before the error (a cancelled
		// context, say); discarding the session is the only way to be sure
		// it is not left held.
		discardConn(conn)
		return false, fmt.Errorf("pg_try_advisory_lock: %w", err)
	}
	if !acquired {
		_ = conn.Close() // holds nothing, so it may go back to the pool
		return false, nil
	}
	l.conn = conn
	return true, nil
}

// Held reports whether this HeldLock believes it holds the lock. It does not
// touch the database; use Verify for that.
func (l *HeldLock) Held() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.conn != nil || l.local
}

// Verify reports whether the lock is still held. On PostgreSQL it checks
// that the holding session is alive; if it is not, the lock is gone
// server-side, so Verify drops it here too and returns false.
func (l *HeldLock) Verify(ctx context.Context) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.local {
		return true
	}
	if l.conn == nil {
		return false
	}
	pctx, cancel := context.WithTimeout(ctx, heldLockCheckTimeout)
	defer cancel()
	if err := l.conn.PingContext(pctx); err != nil {
		xlog.Warn("advisory lock session lost, giving up the lock", "key", l.key, "error", err)
		// A ping that only timed out may leave the session (and the lock)
		// alive; discarding closes it, so the server releases the lock.
		discardConn(l.conn)
		l.conn = nil
		return false
	}
	return true
}

// Release gives up the lock. It is safe to call when the lock is not held.
func (l *HeldLock) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.local {
		<-localLockChan(l.key)
		l.local = false
		return
	}
	if l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), heldLockCheckTimeout)
	defer cancel()
	if _, err := l.conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", l.key); err != nil {
		xlog.Warn("advisory lock unlock failed, closing its session instead", "key", l.key, "error", err)
	}
	// Never hand the session back to the pool: if the unlock failed it still
	// holds the lock, and a later pool user would silently inherit it.
	discardConn(l.conn)
	l.conn = nil
}

// discardConn closes conn's underlying session instead of returning it to
// the pool. database/sql drops a connection when Raw's callback returns
// driver.ErrBadConn.
func discardConn(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}
