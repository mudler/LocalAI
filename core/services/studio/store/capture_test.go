// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var _ = Describe("durable capture", func() {
	for _, backend := range []string{"JSON", "SQLite", "PostgreSQL"} {
		backend := backend
		Describe(backend, func() {
			var open func() store.Store
			var s store.Store
			var ctx context.Context
			var dbHandle *gorm.DB
			var diskPath string
			var postgresURL string
			BeforeEach(func() {
				ctx = context.Background()
				path := filepath.Join(GinkgoT().TempDir(), "store")
				diskPath = path
				if backend == "PostgreSQL" {
					raw := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
					if raw == "" {
						Skip("LOCALAI_TEST_POSTGRES_URL is not set")
					}
					admin, e := gorm.Open(postgres.Open(raw), &gorm.Config{})
					Expect(e).NotTo(HaveOccurred())
					schema := fmt.Sprintf("capture_test_%d", time.Now().UnixNano())
					Expect(admin.Exec("CREATE SCHEMA " + schema).Error).To(Succeed())
					DeferCleanup(func() {
						Expect(admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error).To(Succeed())
						sqlDB, e := admin.DB()
						Expect(e).NotTo(HaveOccurred())
						Expect(sqlDB.Close()).To(Succeed())
					})
					u, e := url.Parse(raw)
					Expect(e).NotTo(HaveOccurred())
					q := u.Query()
					q.Set("search_path", schema)
					u.RawQuery = q.Encode()
					postgresURL = u.String()
				}
				open = func() store.Store { v, e := store.NewJSON(path); Expect(e).NotTo(HaveOccurred()); return v }
				if backend != "JSON" {
					open = func() store.Store {
						var db *gorm.DB
						if backend == "SQLite" {
							db = openSQLite(path)
						} else {
							url := postgresURL
							if url == "" {
								Skip("LOCALAI_TEST_POSTGRES_URL is not set")
							}
							var e error
							db, e = gorm.Open(postgres.Open(url), &gorm.Config{})
							Expect(e).NotTo(HaveOccurred())
							sqlDB, e := db.DB()
							Expect(e).NotTo(HaveOccurred())
							DeferCleanup(func() { _ = sqlDB.Close() })
						}
						dbHandle = db
						v, e := store.NewGORM(db)
						Expect(e).NotTo(HaveOccurred())
						return v
					}
				}
				s = open()
				// Every PostgreSQL case owns a schema; claims scan all chats in that schema.
				id = "capture-" + time.Now().Format("150405.000000000")
				Expect(s.CreateChat(ctx, session.ChatRecord{ID: id, Principal: "alice", AgentID: "agent", HarnessState: []byte(`{}`)})).To(Succeed())
			})
			It("upgrades a pre-capture store without losing chat state", func() {
				if backend == "JSON" {
					raw, e := os.ReadFile(filepath.Join(diskPath, "chats", base64.RawURLEncoding.EncodeToString([]byte(id))+".json"))
					Expect(e).NotTo(HaveOccurred())
					var legacy map[string]json.RawMessage
					Expect(json.Unmarshal(raw, &legacy)).To(Succeed())
					delete(legacy, "capture")
					raw, e = json.Marshal(legacy)
					Expect(e).NotTo(HaveOccurred())
					Expect(os.WriteFile(filepath.Join(diskPath, "chats", base64.RawURLEncoding.EncodeToString([]byte(id))+".json"), raw, 0600)).To(Succeed())
				} else {
					Expect(dbHandle.Exec("DROP TABLE studio_capture_keys").Error).To(Succeed())
					Expect(dbHandle.Exec("DROP TABLE studio_capture_states").Error).To(Succeed())
				}
				s = open()
				chat, e := s.GetChat(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(chat.AgentID).To(Equal("agent"))
				_, e = s.AcceptMemoryInput(ctx, acceptance(store.CaptureIncoming))
				Expect(e).NotTo(HaveOccurred())
				records, e := s.ListCaptures(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(records).To(HaveLen(1))
				if backend != "JSON" {
					Expect(dbHandle.Exec("INSERT INTO studio_capture_keys (id, chat_id) VALUES (?, ?)", records[0].ID, id).Error).To(HaveOccurred())
				}
			})
			It("preserves acceptance and frozen routing across reopen and duplicates", func() {
				in := acceptance(store.CaptureIncoming)
				first, e := s.AcceptMemoryInput(ctx, in)
				Expect(e).NotTo(HaveOccurred())
				s = open()
				in.Text = "changed"
				in.Policy.Destination = "other"
				second, e := s.AcceptMemoryInput(ctx, in)
				Expect(e).NotTo(HaveOccurred())
				Expect(second).To(Equal(first))
				records, e := s.ListCaptures(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(records).To(HaveLen(1))
				Expect(records[0].RawText).To(Equal("remember"))
				Expect(records[0].Identity.Destination).To(Equal("collection"))
				claim, e := s.ClaimCapture(ctx, "owner", time.Now(), time.Minute)
				Expect(e).NotTo(HaveOccurred())
				Expect(claim.Record.ID).To(Equal(records[0].ID))
			})
			It("hands off a short expired lease with a new owner and rejects the old owner", func() {
				_, e := s.AcceptMemoryInput(ctx, acceptance(store.CaptureIncoming))
				Expect(e).NotTo(HaveOccurred())
				now := time.Now().Truncate(time.Second)
				a, e := s.ClaimCapture(ctx, "first", now, time.Second)
				Expect(e).NotTo(HaveOccurred())
				other := s
				if backend != "JSON" {
					other = open()
				}
				b, e := other.ClaimCapture(ctx, "second", now.Add(time.Second), time.Second)
				Expect(e).NotTo(HaveOccurred())
				Expect(b.Record.Owner).To(Equal("second"))
				Expect(b.Record.Generation).To(BeNumerically(">", a.Record.Generation))
				state, e := a.Record.ReducerState()
				Expect(e).NotTo(HaveOccurred())
				w := state.(memory.Authorizing).Work
				tr := memory.Reduce(state, memory.AuthorizationGranted{Envelope: w.Fence})
				a.Now = now.Add(time.Second)
				Expect(s.ApplyCapture(ctx, a, tr)).To(MatchError(store.ErrCaptureConflict))
			})
			It("does not recreate deleted captures or resurrect harness state on reopen", func() {
				in := acceptance(store.CaptureIncoming)
				_, e := s.AcceptMemoryInput(ctx, in)
				Expect(e).NotTo(HaveOccurred())
				Expect(s.DeleteChat(ctx, "alice", id)).To(Succeed())
				s = open()
				_, e = s.GetChat(ctx, "alice", id)
				Expect(e).To(MatchError(session.ErrNotFound))
				Expect(s.CreateChat(ctx, session.ChatRecord{ID: id, Principal: "alice", AgentID: "agent"})).To(Succeed())
				_, e = s.AcceptMemoryInput(ctx, in)
				Expect(e).To(MatchError(store.ErrCaptureConflict))
				records, e := s.ListCaptures(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(records).To(BeEmpty())
			})

			for _, mode := range []store.CaptureMode{store.CaptureOff, store.CaptureIncoming, store.CaptureReplies, store.CaptureSummary} {
				mode := mode
				It("captures only the accepted text and successful final outcome for "+string(mode), func() {
					in := acceptance(mode)
					_, e := s.AcceptMemoryInput(ctx, in)
					Expect(e).NotTo(HaveOccurred())
					Expect(s.ClaimTurn(ctx, "alice", id, "run")).To(Succeed())
					completion := store.MemoryTurnCompletion{Principal: "alice", ChatID: id, RunID: "run", ReplyID: "reply", ReplyText: "answer", Status: store.TurnDone, IncomingIDs: []string{"message"}}
					Expect(s.CompleteMemoryTurn(ctx, completion)).To(Succeed())
					Expect(s.CompleteMemoryTurn(ctx, completion)).To(Succeed())
					s = open()
					records, e := s.ListCaptures(ctx, "alice", id)
					Expect(e).NotTo(HaveOccurred())
					count := map[store.CaptureMode]int{store.CaptureOff: 0, store.CaptureIncoming: 1, store.CaptureReplies: 2, store.CaptureSummary: 1}[mode]
					Expect(records).To(HaveLen(count))
					for _, r := range records {
						Expect(r.Identity.Destination).To(Equal("collection"))
						if mode == store.CaptureSummary {
							Expect(r.RawText).To(Equal("remember\n\nanswer"))
							Expect(r.InputType).To(Equal("SummaryInput"))
						}
					}
				})
			}
			It("excludes paused and failed replies and empty incoming summaries", func() {
				in := acceptance(store.CaptureSummary)
				in.Text = " "
				_, e := s.AcceptMemoryInput(ctx, in)
				Expect(e).NotTo(HaveOccurred())
				for _, status := range []store.TurnStatus{store.TurnPaused, store.TurnFailed, store.TurnDone} {
					Expect(s.ClaimTurn(ctx, "alice", id, "run")).To(Succeed())
					Expect(s.CompleteMemoryTurn(ctx, store.MemoryTurnCompletion{Principal: "alice", ChatID: id, RunID: "run", ReplyID: string(status), ReplyText: "answer", Status: status, IncomingIDs: []string{"message"}})).To(Succeed())
				}
				records, e := s.ListCaptures(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(records).To(BeEmpty())
			})
			It("persists frozen payload before write and preserves it through recovery", func() {
				_, e := s.AcceptMemoryInput(ctx, acceptance(store.CaptureIncoming))
				Expect(e).NotTo(HaveOccurred())
				now := time.Now().Truncate(time.Second)
				claim, e := s.ClaimCapture(ctx, "worker", now, time.Second)
				Expect(e).NotTo(HaveOccurred())
				Expect(claim.Record.Identity.Chat).To(Equal(id))
				Expect(claim.Record.ID).To(Equal(store.CaptureKey(claim.Record.Identity)))
				state, e := claim.Record.ReducerState()
				Expect(e).NotTo(HaveOccurred())
				auth := state.(memory.Authorizing)
				Expect(claim.Transition.State).To(Equal(state))
				Expect(claim.Transition.Effects).To(ContainElement(memory.CheckAuthorization{Work: auth.Work}))
				tr := memory.Reduce(state, memory.AuthorizationGranted{Envelope: auth.Work.Fence})
				Expect(s.ApplyCapture(ctx, claim, tr)).To(Succeed())
				records, e := s.ListCaptures(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				claim.Record = records[0]
				state, e = claim.Record.ReducerState()
				Expect(e).NotTo(HaveOccurred())
				payload := state.(memory.PersistingPayload)
				tr = memory.Reduce(state, memory.PayloadPersisted{Envelope: payload.Work.Fence})
				Expect(s.ApplyCapture(ctx, claim, tr)).To(Succeed())
				s = open()
				recovered, e := s.ClaimCapture(ctx, "new", now.Add(time.Second), time.Second)
				Expect(e).NotTo(HaveOccurred())
				state, e = recovered.Record.ReducerState()
				Expect(e).NotTo(HaveOccurred())
				Expect(state.(memory.Authorizing).Work.Job.Input).To(Equal(memory.FrozenPayload{Text: payload.Payload.Text, Hash: payload.Payload.Hash}))
			})
			It("rolls acceptance and completion back when the durable boundary fails", func() {
				fail := func(operation string) func() {
					if backend == "JSON" {
						Expect(os.Rename(filepath.Join(diskPath, "chats"), filepath.Join(diskPath, "hold"))).To(Succeed())
						Expect(os.WriteFile(filepath.Join(diskPath, "chats"), []byte("blocked"), 0600)).To(Succeed())
						return func() {
							Expect(os.Remove(filepath.Join(diskPath, "chats"))).To(Succeed())
							Expect(os.Rename(filepath.Join(diskPath, "hold"), filepath.Join(diskPath, "chats"))).To(Succeed())
						}
					}
					if backend == "SQLite" {
						Expect(dbHandle.Exec(`CREATE TRIGGER fail_capture BEFORE ` + operation + ` ON studio_capture_states BEGIN SELECT RAISE(ABORT,'forced capture failure'); END`).Error).To(Succeed())
						return func() { Expect(dbHandle.Exec(`DROP TRIGGER fail_capture`).Error).To(Succeed()) }
					}
					Expect(dbHandle.Exec(`CREATE OR REPLACE FUNCTION fail_capture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced capture failure'; END $$`).Error).To(Succeed())
					Expect(dbHandle.Exec(`CREATE TRIGGER fail_capture BEFORE INSERT OR UPDATE ON studio_capture_states FOR EACH ROW EXECUTE FUNCTION fail_capture()`).Error).To(Succeed())
					return func() {
						Expect(dbHandle.Exec(`DROP TRIGGER fail_capture ON studio_capture_states`).Error).To(Succeed())
						Expect(dbHandle.Exec(`DROP FUNCTION fail_capture()`).Error).To(Succeed())
					}
				}
				restore := fail("INSERT")
				_, e := s.AcceptMemoryInput(ctx, acceptance(store.CaptureReplies))
				Expect(e).To(HaveOccurred())
				restore()
				pending, e := s.Pending(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(pending).To(BeEmpty())
				records, e := s.ListCaptures(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(records).To(BeEmpty())
				_, e = s.AcceptMemoryInput(ctx, acceptance(store.CaptureReplies))
				Expect(e).NotTo(HaveOccurred())
				Expect(s.ClaimTurn(ctx, "alice", id, "run")).To(Succeed())
				completion := store.MemoryTurnCompletion{Principal: "alice", ChatID: id, RunID: "run", ReplyID: "reply", ReplyText: "answer", Status: store.TurnDone, IncomingIDs: []string{"message"}}
				restore = fail("UPDATE")
				Expect(s.CompleteMemoryTurn(ctx, completion)).To(HaveOccurred())
				restore()
				turn, e := s.Turn(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(turn.Running).To(BeTrue())
				records, e = s.ListCaptures(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(records).To(HaveLen(1))
				Expect(s.CompleteMemoryTurn(ctx, completion)).To(Succeed())
				records, e = s.ListCaptures(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(records).To(HaveLen(2))
			})
			It("has exactly one winner across two store handles", func() {
				if backend == "JSON" {
					return
				}
				_, e := s.AcceptMemoryInput(ctx, acceptance(store.CaptureIncoming))
				Expect(e).NotTo(HaveOccurred())
				other := open()
				now := time.Now()
				var wg sync.WaitGroup
				errs := make(chan error, 2)
				claims := make(chan store.CaptureClaim, 2)
				records, e := s.ListCaptures(ctx, "alice", id)
				Expect(e).NotTo(HaveOccurred())
				Expect(records).To(HaveLen(1))
				for i, handle := range []store.Store{s, other} {
					wg.Add(1)
					go func(h store.Store, owner string) {
						defer wg.Done()
						claim, err := h.ClaimCapture(ctx, owner, now, time.Minute)
						if err == nil {
							claims <- claim
						}
						errs <- err
					}(handle, []string{"one", "two"}[i])
				}
				wg.Wait()
				close(errs)
				won := 0
				for e := range errs {
					if e == nil {
						won++
					} else {
						Expect(e).To(MatchError(store.ErrNoCapture))
					}
				}
				Expect(won).To(Equal(1))
				close(claims)
				for claim := range claims {
					Expect(claim.Record.ID).To(Equal(records[0].ID))
				}
			})
		})
	}
})
var id string

func acceptance(mode store.CaptureMode) store.MemoryAcceptance {
	return store.MemoryAcceptance{Principal: "alice", ChatID: id, SourceID: "message", Text: "remember", Policy: store.CapturePolicy{AgentID: "agent", Destination: "collection", Mode: mode}}
}
