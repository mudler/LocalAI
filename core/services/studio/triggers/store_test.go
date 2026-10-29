package triggers_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/triggers/triggerstest"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func openSQLiteFile(path string) *gorm.DB {
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func sampleTrigger(id string) triggers.Trigger {
	return triggers.Trigger{ID: id, Owner: "u1", Name: "n", Prompt: "p", Enabled: true, Kind: triggers.KindSchedule, Schedule: "daily 08:00",
		TZ: "UTC", Since: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), AgentID: "a1", SourceChatID: "c1", Model: "m"}
}

var _ = Describe("Trigger stores", func() {
	Describe("JSON in memory", func() {
		triggerstest.RunStoreSuite(func() triggers.Store {
			s, err := triggers.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("JSON on disk", func() {
		triggerstest.RunStoreSuite(func() triggers.Store {
			s, err := triggers.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("GORM on SQLite", func() {
		triggerstest.RunStoreSuite(func() triggers.Store {
			s, err := triggers.NewGORM(openSQLiteFile(filepath.Join(GinkgoT().TempDir(), "t.db")))
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	// The PostgreSQL run needs a database that tests may wipe. Set
	// LOCALAI_TEST_POSTGRES_URL to enable it. Without it, one spec skips.
	Describe("GORM on PostgreSQL", func() {
		url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
		if url == "" {
			It("needs LOCALAI_TEST_POSTGRES_URL", func() {
				Skip("LOCALAI_TEST_POSTGRES_URL is not set")
			})
			return
		}
		triggerstest.RunStoreSuite(func() triggers.Store {
			db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
			Expect(err).NotTo(HaveOccurred())
			sqlDB, _ := db.DB()
			DeferCleanup(func() { _ = sqlDB.Close() })
			s, err := triggers.NewGORM(db)
			Expect(err).NotTo(HaveOccurred())
			for _, tbl := range []string{"studio_trigger_state", "studio_trigger_runs", "studio_triggers"} {
				Expect(db.Exec("DELETE FROM " + tbl).Error).To(Succeed())
			}
			return s
		}, triggerstest.WithResolution(time.Microsecond))
	})

	It("JSON survives a reopen", func() {
		ctx, dir := context.Background(), GinkgoT().TempDir()
		first, _ := triggers.NewJSON(dir)
		Expect(first.PutTrigger(ctx, sampleTrigger("t1"))).To(Succeed())
		ok, err := first.Claim(ctx, triggers.Run{ID: "r1", TriggerID: "t1", DueKey: "k1", Status: triggers.StatusRunning, ChatID: "chat9"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "claim")
		_ = first.SetCursor(ctx, 42)
		_ = first.SetLastDue(ctx, "t1", time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC))

		second, err := triggers.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		got, err := second.GetTrigger(ctx, "t1")
		Expect(err).NotTo(HaveOccurred(), "trigger after reopen")
		Expect(got.AgentID).To(Equal("a1"))
		Expect(got.SourceChatID).To(Equal("c1"))
		Expect(got.Model).To(Equal("m"))
		again, _ := second.Claim(ctx, triggers.Run{ID: "r2", TriggerID: "t1", DueKey: "k1"})
		Expect(again).To(BeFalse(), "a due key ran twice across a restart")
		r, err := second.RunByChat(ctx, "chat9")
		Expect(err).NotTo(HaveOccurred(), "run by chat")
		Expect(r.ID).To(Equal("r1"))
		rev, ok, _ := second.Cursor(ctx)
		Expect(ok).To(BeTrue())
		Expect(rev).To(BeEquivalentTo(42))
		d, _ := second.LastDue(ctx, "t1")
		Expect(d.IsZero()).To(BeFalse(), "last due lost")
	})

	// Two stores on one database are two frontends: a due key is claimed once.
	It("claims a due key once across two GORM stores", func() {
		path := filepath.Join(GinkgoT().TempDir(), "t.db")
		a, err := triggers.NewGORM(openSQLiteFile(path))
		Expect(err).NotTo(HaveOccurred())
		b, err := triggers.NewGORM(openSQLiteFile(path))
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		const rounds = 20
		for round := 0; round < rounds; round++ {
			var wg sync.WaitGroup
			wins := make(chan string, 8)
			for i, s := range []triggers.Store{a, b, a, b} {
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					id := fmt.Sprintf("r%d-%d", round, i)
					ok, err := s.Claim(ctx, triggers.Run{ID: id, TriggerID: "t1", DueKey: fmt.Sprintf("k%d", round), Status: triggers.StatusQueued})
					Expect(err).NotTo(HaveOccurred(), "claim")
					if ok {
						wins <- id
					}
				}()
			}
			wg.Wait()
			close(wins)
			n := 0
			for range wins {
				n++
			}
			Expect(n).To(Equal(1), "round %d: claims won", round)
		}
	})
})
