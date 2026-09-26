package advisorylock

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// expectStickyHandover checks the contract both backends share: the first
// holder keeps the lock across repeated checks while a rival is denied, and
// the rival gets it once the holder releases.
func expectStickyHandover(db *gorm.DB, key int64) {
	ctx := context.Background()
	first, second := NewHeldLock(db, key), NewHeldLock(db, key)
	DeferCleanup(first.Release)
	DeferCleanup(second.Release)

	ok, err := first.TryAcquire(ctx)
	Expect(err).ToNot(HaveOccurred())
	Expect(ok).To(BeTrue())

	for range 5 {
		Expect(first.Held()).To(BeTrue())
		Expect(first.Verify(ctx)).To(BeTrue())
		ok, err = second.TryAcquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse(), "a rival must not take a held lock")
		Expect(second.Held()).To(BeFalse())
	}

	first.Release()
	Expect(first.Held()).To(BeFalse())
	Expect(first.Verify(ctx)).To(BeFalse())

	ok, err = second.TryAcquire(ctx)
	Expect(err).ToNot(HaveOccurred())
	Expect(ok).To(BeTrue(), "the lock is free once the holder releases it")
	Expect(second.Verify(ctx)).To(BeTrue())
}

var _ = Describe("HeldLock (SQLite fallback)", Label("sqlite"), func() {
	It("stays with its holder until Release", func() {
		db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		expectStickyHandover(db, 12101)
	})

	It("is idempotent: acquiring twice and releasing twice is safe", func() {
		db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		l := NewHeldLock(db, 12102)
		ok, err := l.TryAcquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, err = l.TryAcquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue(), "a holder asking again keeps the lock")
		l.Release()
		l.Release()
		ok, err = l.TryAcquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		l.Release()
	})
})

var _ = Describe("HeldLock (PostgreSQL)", func() {
	var db *gorm.DB

	BeforeEach(func() {
		db = testutil.SetupTestDB()
	})

	It("stays with its holder until Release, then hands over", func() {
		expectStickyHandover(db, 12201)
	})

	It("drops leadership when the holding session dies, freeing the lock", func() {
		ctx := context.Background()
		first, second := NewHeldLock(db, 12202), NewHeldLock(db, 12202)
		DeferCleanup(first.Release)
		DeferCleanup(second.Release)

		ok, err := first.TryAcquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())

		// Kill the holder's backend, as a network cut or DB restart would.
		var pid int
		Expect(first.conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid)).To(Succeed())
		Expect(db.Exec("SELECT pg_terminate_backend(?)", pid).Error).ToNot(HaveOccurred())

		Expect(first.Verify(ctx)).To(BeFalse(), "a dead session no longer holds the lock")
		Expect(first.Held()).To(BeFalse())

		ok, err = second.TryAcquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue(), "the server released the dead session's lock")
	})
})
