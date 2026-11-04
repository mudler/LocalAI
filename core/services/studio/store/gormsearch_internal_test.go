package store

import (
	"context"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/chatsearch"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = ginkgo.Describe("the text search without FTS5 or pg_trgm", func() {
	ginkgo.It("scans the folded column and gives the same answers", func() {
		db, err := auth.OpenSQLiteFile(filepath.Join(ginkgo.GinkgoT().TempDir(), "studio.db"))
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		ginkgo.DeferCleanup(func() { _ = sqlDB.Close() })
		st, err := NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		s := st.(*gormStore)
		s.text.engine = engineScan
		ctx := context.Background()
		now := time.Now()
		Expect(s.CreateChat(ctx, session.ChatRecord{ID: "c1", Principal: "alice", Title: "t", Created: now, Updated: now})).To(Succeed())
		_, err = s.AppendEntry(ctx, "c1", session.Entry{Role: session.RoleUser, Text: "Qdrant port 6333, go is fun"})
		Expect(err).NotTo(HaveOccurred())
		for text, want := range map[string]int{"QDRANT port": 1, "go fun": 1, "qdrant nope": 0, "6333": 1} {
			res, err := s.SearchText(ctx, chatsearch.Query{Principal: "alice", Text: text, Now: now})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Engine).To(Equal(engineScan))
			Expect(res.Hits).To(HaveLen(want), text)
		}
	})
})
