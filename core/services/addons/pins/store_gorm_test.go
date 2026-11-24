//go:build auth

// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Gorm store", func() {
	Context("contract", func() {
		registerStoreContract(func() Store {
			db, err := auth.InitDB(":memory:")
			Expect(err).ToNot(HaveOccurred())
			s, err := NewGormStore(db)
			Expect(err).ToNot(HaveOccurred())
			return s
		})
	})

	// On SQLite the lock is an in-process mutex per key, which a test can hold.
	// The migration must wait for the schema-migrate lock like the other shared tables.
	It("migrates under the schema-migrate lock", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		done := make(chan error, 1)
		err = advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
			go func() {
				defer GinkgoRecover()
				_, e := NewGormStore(db)
				done <- e
			}()
			select {
			case <-done:
				Fail("NewGormStore migrated while the schema-migrate lock was held")
			case <-time.After(300 * time.Millisecond):
			}
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		select {
		case e := <-done:
			Expect(e).ToNot(HaveOccurred())
		case <-time.After(5 * time.Second):
			Fail("NewGormStore did not finish after the lock was released")
		}
	})
})
