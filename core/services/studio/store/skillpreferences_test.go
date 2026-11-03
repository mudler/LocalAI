// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
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
			It("reads legacy OFF markers as their add-on names without writing", func() {
				original := raw()
				_, err := s.GetChat(ctx, "mallory", "prefs")
				Expect(err).To(MatchError(session.ErrNotFound))
				rec, err := s.GetChat(ctx, "alice", "prefs")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.SkillsOff).To(Equal(clean))
				Expect(rec.Title).To(Equal("unchanged"))
				Expect(rec.AddonsOff).To(Equal([]string{"sandbox"}))
				Expect(rec.BuiltinsOn).To(Equal([]string{"read_file"}))
				Expect(rec.McpOff).To(Equal([]string{"private"}))
				// A read never writes: it takes no write lock.
				Expect(raw()).To(Equal(original))
				rec.SkillsOff[0] = "mutated"
				rec.AddonsOff[0] = "mutated"
				rec, err = s.GetChat(ctx, "alice", "prefs")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.SkillsOff).To(Equal(clean))
				Expect(rec.AddonsOff).To(Equal([]string{"sandbox"}))
			})
			It("rewrites legacy OFF markers once when the store opens", func() {
				Expect(raw()).To(ContainSubstring("builtin:docx"))
				reload()
				Expect(raw()).NotTo(ContainSubstring("builtin:docx"))
				migrated := raw()
				rec, err := s.GetChat(ctx, "alice", "prefs")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.SkillsOff).To(Equal(clean))
				Expect(rec.Title).To(Equal("unchanged"))
				Expect(rec.AddonsOff).To(Equal([]string{"sandbox"}))
				// Opening again changes nothing, and an explicit re-enable stays.
				reload()
				Expect(raw()).To(Equal(migrated))
				Expect(s.SetSkillsOff(ctx, "alice", "prefs", nil)).To(Succeed())
				reload()
				rec, err = s.GetChat(ctx, "alice", "prefs")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.SkillsOff).To(BeEmpty())
				Expect(s.SetSkillsOff(ctx, "alice", "prefs", legacy)).To(Succeed())
				Expect(raw()).NotTo(ContainSubstring("builtin:docx"))
			})
			It("reads while a replacement runs without resurrecting OFF", func() {
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
			if backend == "SQLite" {
				It("opens and reads when the rewrite of legacy markers fails", func() {
					original := raw()
					Expect(db.Exec(`CREATE TRIGGER reject_migration BEFORE UPDATE OF skills_off ON studio_chats WHEN NEW.skills_off <> OLD.skills_off BEGIN SELECT RAISE(ABORT, 'forced migration failure'); END`).Error).To(Succeed())
					DeferCleanup(func() { Expect(db.Exec("DROP TRIGGER IF EXISTS reject_migration").Error).To(Succeed()) })
					reload()
					Expect(raw()).To(Equal(original))
					rec, err := s.GetChat(ctx, "alice", "prefs")
					Expect(err).NotTo(HaveOccurred())
					Expect(rec.SkillsOff).To(Equal(clean))
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
