// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var _ = Describe("document OFF preferences", func() {
	for _, backend := range []string{"JSON", "SQLite", "PostgreSQL"} {
		Context(backend, func() {
			var s store.Store
			var db *gorm.DB
			var dir string
			ctx := context.Background()
			legacy := []string{"builtin:docx", "addon:docx", "builtin:pptx", "pdf", "builtin:pdf", "builtin:xlsx", "builtin:writing-skills", "other", "other"}
			clean := []string{"addon:docx", "addon:pptx", "pdf", "addon:pdf", "addon:xlsx", "builtin:writing-skills", "other", "other"}
			BeforeEach(func() {
				var err error
				dir = GinkgoT().TempDir()
				switch backend {
				case "JSON":
					s, err = store.NewJSON(dir)
				case "SQLite":
					db = openSQLite(filepath.Join(dir, "studio.db"))
					s, err = store.NewGORM(db)
				case "PostgreSQL":
					url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
					if url == "" {
						Skip("LOCALAI_TEST_POSTGRES_URL is not set")
					}
					db, err = gorm.Open(postgres.Open(url), &gorm.Config{})
					Expect(err).NotTo(HaveOccurred())
					sqlDB, e := db.DB()
					Expect(e).NotTo(HaveOccurred())
					DeferCleanup(func() { _ = sqlDB.Close() })
					s, err = store.NewGORM(db)
					Expect(err).NotTo(HaveOccurred())
					for _, table := range []string{"studio_pending", "studio_entries", "studio_chats"} {
						Expect(db.Exec("DELETE FROM " + table).Error).To(Succeed())
					}
				}
				Expect(err).NotTo(HaveOccurred())
				Expect(s.CreateChat(ctx, session.ChatRecord{ID: "prefs", Principal: "alice", Title: "unchanged", SkillsOff: legacy, AddonsOff: []string{"sandbox"}, BuiltinsOn: []string{"read_file"}, McpOff: []string{"private"}})).To(Succeed())
			})
			reload := func() {
				var err error
				if db == nil {
					s, err = store.NewJSON(dir)
				} else {
					s, err = store.NewGORM(db)
				}
				Expect(err).NotTo(HaveOccurred())
			}
			raw := func() string {
				if db == nil {
					b, err := os.ReadFile(chatFile(dir, "prefs", ".json"))
					Expect(err).NotTo(HaveOccurred())
					return string(b)
				}
				var value string
				Expect(db.Raw("SELECT skills_off FROM studio_chats WHERE id = ?", "prefs").Scan(&value).Error).To(Succeed())
				return value
			}
			It("persists legacy OFF once and preserves explicit re-enable and unrelated preferences", func() {
				original := raw()
				_, err := s.GetChat(ctx, "mallory", "prefs")
				Expect(err).To(MatchError(session.ErrNotFound))
				Expect(raw()).To(Equal(original))
				rec, err := s.GetChat(ctx, "alice", "prefs")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.SkillsOff).To(Equal(clean))
				Expect(rec.Title).To(Equal("unchanged"))
				Expect(rec.AddonsOff).To(Equal([]string{"sandbox"}))
				Expect(rec.BuiltinsOn).To(Equal([]string{"read_file"}))
				Expect(rec.McpOff).To(Equal([]string{"private"}))
				Expect(raw()).NotTo(ContainSubstring("builtin:docx"))
				persisted := raw()
				rec.SkillsOff[0] = "mutated"
				rec.AddonsOff[0] = "mutated"
				reload()
				rec, err = s.GetChat(ctx, "alice", "prefs")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.SkillsOff).To(Equal(clean))
				Expect(rec.AddonsOff).To(Equal([]string{"sandbox"}))
				Expect(raw()).To(Equal(persisted))
				Expect(s.SetSkillsOff(ctx, "alice", "prefs", nil)).To(Succeed())
				reload()
				rec, err = s.GetChat(ctx, "alice", "prefs")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.SkillsOff).To(BeEmpty())
				Expect(s.SetSkillsOff(ctx, "alice", "prefs", legacy)).To(Succeed())
				Expect(raw()).NotTo(ContainSubstring("builtin:docx"))
			})
			It("serializes migration with concurrent replacement without resurrecting OFF", func() {
				var wg sync.WaitGroup
				errs := make(chan error, 20)
				for i := 0; i < 10; i++ {
					wg.Add(2)
					go func() { defer wg.Done(); _, err := s.GetChat(ctx, "alice", "prefs"); errs <- err }()
					go func() { defer wg.Done(); errs <- s.SetSkillsOff(ctx, "alice", "prefs", nil) }()
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					Expect(err).NotTo(HaveOccurred())
				}
				reload()
				rec, err := s.GetChat(ctx, "alice", "prefs")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.SkillsOff).To(BeEmpty())
			})
			It("rolls back failed migration and leaves legacy data available for retry", func() {
				original := raw()
				if db == nil {
					path := chatFile(dir, "prefs", ".json")
					Expect(os.Rename(path, path+".saved")).To(Succeed())
					Expect(os.Mkdir(path, 0700)).To(Succeed())
					_, err := s.GetChat(ctx, "alice", "prefs")
					Expect(err).To(HaveOccurred())
					// A failed save must not publish normalized preferences in memory.
					_, err = s.GetChat(ctx, "alice", "prefs")
					Expect(err).To(HaveOccurred())
					Expect(os.Remove(path)).To(Succeed())
					Expect(os.Rename(path+".saved", path)).To(Succeed())
				} else {
					if backend == "SQLite" {
						Expect(db.Exec(`CREATE TRIGGER reject_migration BEFORE UPDATE OF skills_off ON studio_chats WHEN NEW.skills_off <> OLD.skills_off BEGIN SELECT RAISE(ABORT, 'forced migration failure'); END`).Error).To(Succeed())
						DeferCleanup(func() { Expect(db.Exec("DROP TRIGGER IF EXISTS reject_migration").Error).To(Succeed()) })
					} else {
						Expect(db.Exec(`CREATE OR REPLACE FUNCTION reject_document_migration() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.skills_off <> OLD.skills_off THEN RAISE EXCEPTION 'forced migration failure'; END IF; RETURN NEW; END $$`).Error).To(Succeed())
						Expect(db.Exec(`CREATE TRIGGER reject_migration BEFORE UPDATE OF skills_off ON studio_chats FOR EACH ROW EXECUTE FUNCTION reject_document_migration()`).Error).To(Succeed())
						DeferCleanup(func() {
							Expect(db.Exec("DROP TRIGGER IF EXISTS reject_migration ON studio_chats").Error).To(Succeed())
							Expect(db.Exec("DROP FUNCTION reject_document_migration()").Error).To(Succeed())
						})
					}
					_, err := s.GetChat(ctx, "alice", "prefs")
					Expect(err).To(HaveOccurred())
					if backend == "SQLite" {
						Expect(db.Exec("DROP TRIGGER reject_migration").Error).To(Succeed())
					} else {
						Expect(db.Exec("DROP TRIGGER reject_migration ON studio_chats").Error).To(Succeed())
					}
				}
				Expect(raw()).To(Equal(original))
				rec, err := s.GetChat(ctx, "alice", "prefs")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.SkillsOff).To(Equal(clean))
			})
			if backend == "PostgreSQL" {
				It("reads after the lock so an explicit re-enable wins over a waiting migration", func() {
					type barrierKey struct{}
					entered, release := make(chan struct{}), make(chan struct{})
					Expect(db.Callback().Update().Before("gorm:update").Register("document:barrier", func(tx *gorm.DB) {
						if tx.Statement.Context.Value(barrierKey{}) != nil {
							close(entered)
							<-release
						}
					})).To(Succeed())
					DeferCleanup(func() { Expect(db.Callback().Update().Remove("document:barrier")).To(Succeed()) })
					done := make(chan error, 1)
					go func() {
						_, err := s.GetChat(context.WithValue(ctx, barrierKey{}, true), "alice", "prefs")
						done <- err
					}()
					<-entered
					err := s.SetSkillsOff(ctx, "alice", "prefs", nil)
					close(release)
					Expect(err).NotTo(HaveOccurred())
					Expect(<-done).To(Succeed())
					reload()
					rec, err := s.GetChat(ctx, "alice", "prefs")
					Expect(err).NotTo(HaveOccurred())
					Expect(rec.SkillsOff).To(BeEmpty())
				})
			}
			if backend != "JSON" {
				It("propagates locking update errors without changing ownership or counters", func() {
					snapshot := func() map[string]interface{} {
						var row map[string]interface{}
						Expect(db.Table("studio_chats").Where("id = ?", "prefs").Take(&row).Error).To(Succeed())
						return row
					}
					before := snapshot()
					forced := errors.New("forced locking update failure")
					Expect(db.Callback().Update().Before("gorm:update").Register("document:reject-lock", func(tx *gorm.DB) {
						_ = tx.AddError(forced)
					})).To(Succeed())
					DeferCleanup(func() { _ = db.Callback().Update().Remove("document:reject-lock") })
					_, err := s.GetChat(ctx, "alice", "prefs")
					Expect(err).To(MatchError(forced))
					Expect(snapshot()).To(Equal(before))
					Expect(db.Callback().Update().Remove("document:reject-lock")).To(Succeed())
					_, err = s.GetChat(ctx, "mallory", "prefs")
					Expect(err).To(MatchError(session.ErrNotFound))
					Expect(snapshot()).To(Equal(before))
					_, err = s.GetChat(ctx, "alice", "prefs")
					Expect(err).NotTo(HaveOccurred())
					after := snapshot()
					delete(before, "skills_off")
					delete(after, "skills_off")
					Expect(after).To(Equal(before))
				})
			}

			It("does not rewrite malformed stored preferences", func() {
				if db == nil {
					path := chatFile(dir, "prefs", ".json")
					Expect(os.WriteFile(path, []byte("{"), 0600)).To(Succeed())
					reopened, err := store.NewJSON(dir)
					Expect(err).NotTo(HaveOccurred()) // damaged chats are skipped, not repaired
					_, err = reopened.GetChat(ctx, "alice", "prefs")
					Expect(err).To(MatchError(session.ErrNotFound))
					data, err := os.ReadFile(path)
					Expect(err).NotTo(HaveOccurred())
					Expect(string(data)).To(Equal("{"))
				} else {
					Expect(db.Exec("UPDATE studio_chats SET skills_off = ? WHERE id = ?", "{", "prefs").Error).To(Succeed())
					_, err := s.GetChat(ctx, "alice", "prefs")
					Expect(err).To(HaveOccurred())
					Expect(raw()).To(Equal("{"))
				}
			})
		})
	}
})
