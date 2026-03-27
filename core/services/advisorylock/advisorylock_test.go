package advisorylock

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	t.Skip("requires docker")

	ctx := t.Context()

	pgC, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategyAndDeadline(60*time.Second,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { pgC.Terminate(context.Background()) })

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open gorm: %v", err)
	}
	return db
}

func TestWithLock_PreventsConcurrentExecution(t *testing.T) {
	db := setupTestDB(t)

	const lockKey int64 = 999

	var (
		mu          sync.Mutex
		maxRunning  int32
		running     int32
		concurrency int32
	)

	var wg sync.WaitGroup
	wg.Add(2)

	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			err := WithLock(db, lockKey, func() error {
				cur := atomic.AddInt32(&running, 1)
				mu.Lock()
				if cur > maxRunning {
					maxRunning = cur
				}
				if cur > 1 {
					atomic.AddInt32(&concurrency, 1)
				}
				mu.Unlock()

				// Hold the lock briefly so the other goroutine has a chance to contend.
				time.Sleep(50 * time.Millisecond)

				atomic.AddInt32(&running, -1)
				return nil
			})
			if err != nil {
				t.Errorf("WithLock returned error: %v", err)
			}
		}()
	}

	wg.Wait()

	if maxRunning > 1 {
		t.Errorf("expected max 1 goroutine inside lock at a time, observed %d", maxRunning)
	}
	if concurrency > 0 {
		t.Errorf("detected concurrent execution inside advisory lock")
	}
}

func TestTryLock_AcquiresAndUnlocks(t *testing.T) {
	db := setupTestDB(t)

	const lockKey int64 = 888

	// NOTE: TryLock and Unlock may use different pool connections,
	// so this test documents the API contract but may sometimes pass
	// even when the implementation has a connection-affinity bug.
	acquired := TryLock(db, lockKey)
	if !acquired {
		t.Fatal("expected TryLock to acquire the lock")
	}

	Unlock(db, lockKey)

	reacquired := TryLock(db, lockKey)
	if !reacquired {
		t.Error("expected TryLock to re-acquire the lock after Unlock")
	}

	// Clean up
	Unlock(db, lockKey)
}

func TestKeyFromUUID_Deterministic(t *testing.T) {
	a := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	b := [16]byte{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

	keyA1 := KeyFromUUID(a)
	keyA2 := KeyFromUUID(a)
	keyB := KeyFromUUID(b)

	if keyA1 != keyA2 {
		t.Errorf("KeyFromUUID not deterministic: got %d and %d for same input", keyA1, keyA2)
	}
	if keyA1 == keyB {
		t.Errorf("KeyFromUUID returned same key %d for different inputs", keyA1)
	}
}

func TestAllLockKeysUnique(t *testing.T) {
	// Collect all advisory lock keys from both the advisorylock package
	// and the messaging package. They share the same PostgreSQL lock
	// namespace so every key must be unique.
	keys := map[int64]string{
		messaging.AdvisoryLockCronScheduler:    "messaging.AdvisoryLockCronScheduler",
		messaging.AdvisoryLockStaleNodeCleanup: "messaging.AdvisoryLockStaleNodeCleanup",
		messaging.AdvisoryLockGalleryDedup:     "messaging.AdvisoryLockGalleryDedup",
		messaging.AdvisoryLockAgentScheduler:   "messaging.AdvisoryLockAgentScheduler",
		messaging.AdvisoryLockHealthCheck:      "messaging.AdvisoryLockHealthCheck",
		messaging.AdvisoryLockSchemaMigrate:    "messaging.AdvisoryLockSchemaMigrate",
	}

	// If any keys collide, the map will have fewer entries than expected.
	expectedCount := 6
	if len(keys) != expectedCount {
		t.Errorf("expected %d unique advisory lock keys, got %d — some keys have the same value", expectedCount, len(keys))
	}
}
