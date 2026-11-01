package store_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/store/storetest"
)

// openSQLite opens a file database the way LocalAI does in production: the
// pure-Go driver, a busy timeout, and the write lock taken at BEGIN.
func openSQLite(path string) *gorm.DB {
	db, err := auth.OpenSQLiteFile(path)
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("the GORM store on SQLite", func() {
	storetest.Run(func() store.Store {
		s, err := store.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db")))
		Expect(err).NotTo(HaveOccurred())
		return s
	})

	// A second handle on the same database sees what the first wrote.
	It("survives a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "studio.db")
		open := func() store.Store {
			s, err := store.NewGORM(openSQLite(path))
			Expect(err).NotTo(HaveOccurred())
			return s
		}
		storetest.Seed(open())
		storetest.CheckSeed(open())
	})

	// ReapTurns frees the chat and restores its messages in one transaction: when
	// the restore fails the chat must stay claimed, or the lost messages could
	// never be given back.
	It("reaps turns atomically", func() {
		ctx := context.Background()
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		s, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		now := time.Now().UTC()
		Expect(s.CreateChat(ctx, session.ChatRecord{ID: "a", Principal: "alice", Created: now, Updated: now})).To(Succeed())
		Expect(s.AddPending(ctx, "alice", "a", "m")).Error().NotTo(HaveOccurred())
		Expect(s.ClaimTurn(ctx, "alice", "a", "r1")).To(Succeed())
		Expect(s.TakePending(ctx, "alice", "a")).Error().NotTo(HaveOccurred())
		Expect(db.Exec(`CREATE TRIGGER no_restore BEFORE UPDATE ON studio_pending
		BEGIN SELECT RAISE(ABORT, 'forced'); END`).Error).NotTo(HaveOccurred())
		Expect(s.ReapTurns(ctx, time.Now().Add(time.Hour))).Error().To(HaveOccurred(), "ReapTurns should fail when the restore fails")
		info, err := s.Turn(ctx, "alice", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Running).To(BeTrue(), "turn after failed reap = %+v; want still running", info)
	})

	// A database made before goals existed: AutoMigrate adds the columns and
	// fills the old rows, so their chats read with no goal and take one.
	It("migrates a table made before goals", func() {
		ctx := context.Background()
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		_, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Exec(`ALTER TABLE studio_chats DROP COLUMN goal`).Error).NotTo(HaveOccurred())
		Expect(db.Exec(`ALTER TABLE studio_chats DROP COLUMN goal_rev`).Error).NotTo(HaveOccurred())
		Expect(db.Exec(`INSERT INTO studio_chats (id, principal, title) VALUES ('old', 'alice', 'Old chat')`).Error).NotTo(HaveOccurred())
		s, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		rec, err := s.GetChat(ctx, "alice", "old")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Goal).To(BeNil())
		Expect(s.UpdateGoal(ctx, "alice", "old", func(*session.Goal) (*session.Goal, error) {
			return &session.Goal{Text: "g", State: session.GoalActive}, nil
		})).To(Succeed())
		rec, err = s.GetChat(ctx, "alice", "old")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Goal.Text).To(Equal("g"))
	})
})
