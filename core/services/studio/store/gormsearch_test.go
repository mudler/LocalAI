package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/chatsearch"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// waitIndexed waits for the background backfill of the text index.
func waitIndexed(s store.Store) {
	GinkgoHelper()
	Eventually(func() bool {
		r, err := s.(chatsearch.Index).SearchText(context.Background(), chatsearch.Query{Principal: "alice", Text: "zzzzzz"})
		Expect(err).NotTo(HaveOccurred())
		return !r.Progress.Indexing
	}, "20s", "20ms").Should(BeTrue())
}

// seedLegacy makes chats with messages and then removes every trace of the
// text index, as a database from before the index looks.
func seedLegacy(db *gorm.DB, chats int) {
	GinkgoHelper()
	s, err := store.NewGORM(db)
	Expect(err).NotTo(HaveOccurred())
	waitIndexed(s)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i := range chats {
		id := fmt.Sprintf("old%03d", i)
		Expect(s.CreateChat(ctx, session.ChatRecord{ID: id, Principal: "alice", Title: id, Created: at, Updated: at})).To(Succeed())
		_, err := s.AppendEntry(ctx, id, session.Entry{Role: session.RoleUser, Text: "legacy marmot " + id, At: at})
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(db.Exec("DROP TABLE IF EXISTS studio_entry_texts_fts").Error).To(Succeed())
	Expect(db.Exec("DROP TABLE studio_entry_texts").Error).To(Succeed())
	Expect(db.Exec("DROP TABLE studio_entry_text_state").Error).To(Succeed())
}

var _ = Describe("the text index of the GORM store on SQLite", func() {
	It("uses FTS5 with the trigram tokenizer", func() {
		s, err := store.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db")))
		Expect(err).NotTo(HaveOccurred())
		Expect(store.TextEngine(s)).To(Equal("fts5"))
	})

	It("indexes the messages of a database from before the index, in the background", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		seedLegacy(db, 130)
		s, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		waitIndexed(s)
		res, err := s.(chatsearch.Index).SearchText(context.Background(), chatsearch.Query{Principal: "alice", Text: "marmot", Limit: 200, Now: time.Now()})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Hits).To(HaveLen(130))
		Expect(res.Progress.Indexing).To(BeFalse())
		Expect(res.Progress.Done).To(Equal(130))
	})

	It("resumes where the backfill stopped and indexes messages written meanwhile once", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		seedLegacy(db, 20)
		first, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		waitIndexed(first)
		// A second start finds the work done and changes nothing.
		second, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		_, err = second.AppendEntry(context.Background(), "old000", session.Entry{Role: session.RoleAssistant, Text: "fresh marmot"})
		Expect(err).NotTo(HaveOccurred())
		waitIndexed(second)
		res, err := second.(chatsearch.Index).SearchText(context.Background(), chatsearch.Query{Principal: "alice", Text: "marmot", Limit: 100, Now: time.Now()})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Hits).To(HaveLen(21))
	})

	It("starts without waiting for the backfill", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		seedLegacy(db, 300)
		start := time.Now()
		s, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
		waitIndexed(s)
	})

	It("drops the index rows with the chat in one transaction", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		s, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		Expect(s.CreateChat(ctx, session.ChatRecord{ID: "c1", Principal: "alice", Title: "t", Created: time.Now(), Updated: time.Now()})).To(Succeed())
		_, err = s.AppendEntry(ctx, "c1", session.Entry{Role: session.RoleUser, Text: "hello"})
		Expect(err).NotTo(HaveOccurred())
		var n int64
		Expect(db.Table("studio_entry_texts").Count(&n).Error).To(Succeed())
		Expect(n).To(Equal(int64(1)))
		Expect(s.DeleteChat(ctx, "alice", "c1")).To(Succeed())
		Expect(db.Table("studio_entry_texts").Count(&n).Error).To(Succeed())
		Expect(n).To(BeZero())
		Expect(db.Raw("SELECT count(*) FROM studio_entry_texts_fts WHERE studio_entry_texts_fts MATCH '\"hello\"'").Scan(&n).Error).To(Succeed())
		Expect(n).To(BeZero())
	})
})
