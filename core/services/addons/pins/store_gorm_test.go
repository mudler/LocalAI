//go:build auth

// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/advisorylock"
)

// The test of two frontends over one database uses Service, which Task 3 adds;
// it lives in Task 3 (service_gorm_test.go).
func TestGormStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		db, err := auth.InitDB(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewGormStore(db)
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// On SQLite the lock is an in-process mutex per key, which a test can hold.
// The migration must wait for the schema-migrate lock like the other shared tables.
func TestGormStoreMigratesUnderTheSchemaMigrateLock(t *testing.T) {
	db, err := auth.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	err = advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		go func() {
			_, e := NewGormStore(db)
			done <- e
		}()
		select {
		case <-done:
			t.Error("NewGormStore migrated while the schema-migrate lock was held")
		case <-time.After(300 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NewGormStore did not finish after the lock was released")
	}
}
