package proposals_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/proposals/proposalstest"
)

func openSQLite(path string) *gorm.DB {
	db, err := auth.OpenSQLiteFile(path)
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("the memory store", func() {
	proposalstest.Run(func() proposals.Store {
		s, err := proposals.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		return s
	})
})

var _ = Describe("the JSON file store", func() {
	proposalstest.Run(func() proposals.Store {
		s, err := proposals.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		return s
	})

	It("survives a reopen", func() {
		dir := GinkgoT().TempDir()
		first, _ := proposals.NewJSON(dir)
		Expect(first.Create(context.Background(), proposalstest.Sample("p1", "u1", "", 0), proposals.Limits{})).To(Succeed())
		second, err := proposals.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		_, err = second.Get(context.Background(), "u1", "p1")
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("the GORM store on SQLite", func() {
	proposalstest.Run(func() proposals.Store {
		s, err := proposals.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db")))
		Expect(err).NotTo(HaveOccurred())
		return s
	})

	It("survives a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "studio.db")
		first, _ := proposals.NewGORM(openSQLite(path))
		Expect(first.Create(context.Background(), proposalstest.Sample("p1", "u1", "", 0), proposals.Limits{})).To(Succeed())
		second, err := proposals.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		got, err := second.Get(context.Background(), "u1", "p1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Source.ID).To(Equal("b1"))
	})

	It("reads a row written before proposers existed as an own proposal", func() {
		path := filepath.Join(GinkgoT().TempDir(), "studio.db")
		db := openSQLite(path)
		// The table as an older version made it, without the new columns.
		Expect(db.Exec(`CREATE TABLE studio_change_proposals (id text PRIMARY KEY, owner text, agent text, kind text, scope text,
			target text, summary text, content text, base_hash text, source text, status text, rev integer, revert_rev integer,
			created datetime, expires datetime, answered datetime, auto numeric NOT NULL DEFAULT false)`).Error).To(Succeed())
		Expect(db.Exec(`INSERT INTO studio_change_proposals (id, owner, kind, scope, target, summary, content, source, status, created, expires)
			VALUES ('old1', 'u1', 'memory', 'user', 'Memory/index.md', 's', 'c', '{}', 'pending', ?, ?)`, time.Now(), time.Now().Add(time.Hour)).Error).To(Succeed())
		s, err := proposals.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		got, err := s.Get(context.Background(), "u1", "old1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Proposer).To(BeEmpty())
		Expect(got.ProjectNode).To(BeZero())
		// It counts as the owner's own proposal for the caps.
		Expect(s.Create(context.Background(), proposalstest.Sample("new1", "u1", "", 0), proposals.Limits{MaxPending: 2})).To(Succeed())
		Expect(s.Create(context.Background(), proposalstest.Sample("new2", "u1", "", 0), proposals.Limits{MaxPending: 2})).To(MatchError(proposals.ErrTooManyPending))
	})
})
