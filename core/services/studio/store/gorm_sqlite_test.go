package store_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
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

	It("reads a chat stored before the ScopedAgent column as not scoped", func() {
		path := filepath.Join(GinkgoT().TempDir(), "studio.db")
		db := openSQLite(path)
		first, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.CreateChat(context.Background(), session.ChatRecord{ID: "old", Principal: "alice", Title: "Old", Created: time.Now(), Updated: time.Now()})).To(Succeed())
		// The table as it was before the column.
		Expect(db.Exec("ALTER TABLE studio_chats DROP COLUMN scoped_agent").Error).To(Succeed())
		second, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		got, err := second.GetChat(context.Background(), "alice", "old")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ScopedAgent).To(BeFalse())
	})

	It("reads the single connection stored before sets as a set of one, and writes the list", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		s, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.CreateChat(context.Background(), session.ChatRecord{ID: "old", Principal: "alice", Title: "Old", Created: time.Now(), Updated: time.Now()})).To(Succeed())
		// The column as the earlier version wrote it: one binding ID per add-on.
		Expect(db.Exec(`UPDATE studio_chats SET connections = ? WHERE id = ?`, `{"tracker":"b1"}`, "old").Error).To(Succeed())
		got, err := s.GetChat(context.Background(), "alice", "old")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Connections).To(Equal(connsets.Sets{"tracker": {"b1"}}))
		Expect(s.SetConnections(context.Background(), "alice", "old", got.Connections)).To(Succeed())
		var raw string
		Expect(db.Raw(`SELECT connections FROM studio_chats WHERE id = ?`, "old").Scan(&raw).Error).To(Succeed())
		Expect(raw).To(Equal(`{"tracker":["b1"]}`))
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

	// The beat of a turn and the cutoff of the reaper come from the database
	// clock, so the reap does not depend on the clock of the calling machine.
	It("measures how long a turn is idle on the database clock", func() {
		ctx := context.Background()
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		s, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		now := time.Now().UTC()
		Expect(s.CreateChat(ctx, session.ChatRecord{ID: "a", Principal: "alice", Created: now, Updated: now})).To(Succeed())
		Expect(s.ClaimTurn(ctx, "alice", "a", "r1")).To(Succeed())
		Expect(db.Exec("UPDATE studio_chats SET run_beat = ? WHERE id = ?", now.Add(-10*time.Minute), "a").Error).To(Succeed())
		Expect(s.ReapTurns(ctx, 15*time.Minute)).To(BeEmpty(), "a turn idle for 10 minutes was reaped at 15")
		Expect(s.TouchTurn(ctx, "alice", "a", "r1")).To(Succeed())
		Expect(db.Exec("UPDATE studio_chats SET run_beat = ? WHERE id = ?", now.Add(-10*time.Minute), "a").Error).To(Succeed())
		Expect(s.ReapTurns(ctx, 5*time.Minute)).To(HaveLen(1))
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
		Expect(s.ReapTurns(ctx, -time.Hour)).Error().To(HaveOccurred(), "ReapTurns should fail when the restore fails")
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
