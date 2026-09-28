package advisorylock

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// expectCloseIsFinal checks that Close frees the lock for a rival and that the
// closed HeldLock never takes it back, as a still-running loop would try to.
func expectCloseIsFinal(db *gorm.DB, key int64) {
	ctx := context.Background()
	l, rival := NewHeldLock(db, key), NewHeldLock(db, key)
	DeferCleanup(rival.Release)

	ok, err := l.TryAcquire(ctx)
	Expect(err).ToNot(HaveOccurred())
	Expect(ok).To(BeTrue())
	l.Close()
	Expect(l.Held()).To(BeFalse())

	ok, err = l.TryAcquire(ctx)
	Expect(err).ToNot(HaveOccurred())
	Expect(ok).To(BeFalse(), "a closed lock is never taken again")

	ok, err = rival.TryAcquire(ctx)
	Expect(err).ToNot(HaveOccurred())
	Expect(ok).To(BeTrue(), "Close frees the lock for others")
	l.Close() // idempotent
}

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

	It("never takes the lock again after Close", func() {
		db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		expectCloseIsFinal(db, 12103)
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

	It("never takes the lock again after Close", func() {
		expectCloseIsFinal(db, 12203)
	})

	It("sets short TCP keepalives on its session so a dead host's lock expires", func() {
		// Killing a host without closing its socket is not practical in a
		// test; check the settings that make the server notice one.
		ctx := context.Background()
		l := NewHeldLock(db, 12204)
		DeferCleanup(l.Release)
		ok, err := l.TryAcquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())

		show := func(name string) string {
			var v string
			Expect(l.conn.QueryRowContext(ctx, "SHOW "+name).Scan(&v)).To(Succeed())
			return v
		}
		Expect(show("tcp_keepalives_idle")).To(Equal("10"))
		Expect(show("tcp_keepalives_interval")).To(Equal("5"))
		Expect(show("tcp_keepalives_count")).To(Equal("3"))
		Expect(show("tcp_user_timeout")).To(Equal("30000")) // milliseconds
	})

	It("reuses one session while the lock is taken elsewhere", func() {
		ctx := context.Background()
		holder, follower := NewHeldLock(db, 12205), NewHeldLock(db, 12205)
		DeferCleanup(holder.Release)
		DeferCleanup(follower.Release)
		ok, err := holder.TryAcquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())

		pid := func() int {
			var p int
			Expect(follower.conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&p)).To(Succeed())
			return p
		}
		ok, err = follower.TryAcquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse())
		before := pid()
		ok, err = follower.TryAcquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(pid()).To(Equal(before), "a follower must not open a connection per attempt")
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
