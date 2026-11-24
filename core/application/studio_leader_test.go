package application

import (
	"context"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var _ = Describe("studio leader locks", func() {
	It("frees the lock of a chat host at shutdown so another frontend can lead", func() {
		// Not PostgreSQL, so advisorylock uses its in-process lock, which has
		// the same try-lock semantics as pg_try_advisory_lock.
		db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		a := &Application{authDB: db}
		ctx := context.Background()

		lead := a.studioLeader(advisorylock.KeyChatsTriggers, "Triggers")
		Expect(lead(ctx)).To(BeTrue(), "the first frontend leads")

		other := advisorylock.NewHeldLock(db, advisorylock.KeyChatsTriggers)
		DeferCleanup(other.Close)
		ok, err := other.TryAcquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse(), "a second frontend follows while the first leads")

		a.closeStudioLocks()
		ok, err = other.TryAcquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue(), "the lock is free after shutdown")
		Expect(lead(ctx)).To(BeFalse(), "a closed lock is never taken again")
	})
})
