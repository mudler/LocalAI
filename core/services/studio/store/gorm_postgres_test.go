package store_test

import (
	"context"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/chatsearch"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/store/storetest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
var _ = Describe("the GORM store on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("runs the contract suite", func() {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		})
		return
	}
	storetest.Run(func() store.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		s, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		for _, tbl := range []string{"studio_pending", "studio_entry_texts", "studio_entry_text_state", "studio_entries", "studio_chats"} {
			Expect(db.Exec("DELETE FROM " + tbl).Error).NotTo(HaveOccurred())
		}
		return s
	})
})

var _ = Describe("the text index of the GORM store on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("indexes and backfills", func() { Skip("LOCALAI_TEST_POSTGRES_URL is not set") })
		return
	}
	open := func() *gorm.DB {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		for _, tbl := range []string{"studio_pending", "studio_entry_texts", "studio_entry_text_state", "studio_entries", "studio_chats"} {
			_ = db.Exec("DELETE FROM " + tbl).Error
		}
		return db
	}

	It("uses the trigram index when the extension is there", func() {
		s, err := store.NewGORM(open())
		Expect(err).NotTo(HaveOccurred())
		Expect(store.TextEngine(s)).To(Equal("pg_trgm"))
	})

	It("indexes the messages of a database from before the index", func() {
		db := open()
		seedLegacy(db, 40)
		s, err := store.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		waitIndexed(s)
		res, err := s.(chatsearch.Index).SearchText(context.Background(), chatsearch.Query{Principal: "alice", Text: "marmot", Limit: 100, Now: time.Now()})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Hits).To(HaveLen(40))
	})
})
