// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/LocalAI/core/services/studio/agents"
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
			It("associates only a current claim and replays the stored fence", func() {
				_, err := s.AcceptMemoryInput(ctx, acceptance(store.CaptureIncoming))
				Expect(err).NotTo(HaveOccurred())
				now := time.Now().Truncate(time.Second)
				claim, err := s.ClaimCapture(ctx, "owner", now, time.Second)
				Expect(err).NotTo(HaveOccurred())
				fs := s.(store.CaptureMemoryStore)
				calls := 0
				reserve := func(_ context.Context, key, owner string) (lagi.MemoryFence, error) {
					calls++
					return lagi.MemoryFence{Key: key, Owner: owner, Generation: uint64(calls)}, nil
				}
				clock := func() time.Time { return now }
				fence, err := fs.ReserveCaptureMemory(ctx, claim, clock, reserve)
				Expect(err).NotTo(HaveOccurred())
				Expect(fence.Generation).To(Equal(uint64(1)))
				replay, err := fs.ReserveCaptureMemory(ctx, claim, clock, reserve)
				Expect(err).NotTo(HaveOccurred())
				Expect(replay).To(Equal(fence))
				Expect(calls).To(Equal(1))
				clock = func() time.Time { return now.Add(time.Second) }
				_, err = fs.ReserveCaptureMemory(ctx, claim, clock, reserve)
				Expect(err).To(MatchError(store.ErrCaptureConflict))
				Expect(calls).To(Equal(1))
			})
			It("rejects expiry during reservation and recovers the unassociated reservation", func() {
				_, err := s.AcceptMemoryInput(ctx, acceptance(store.CaptureIncoming))
				Expect(err).NotTo(HaveOccurred())
				now := time.Now().Truncate(time.Second)
				claim, err := s.ClaimCapture(ctx, "old", now, time.Second)
				Expect(err).NotTo(HaveOccurred())
				clock := now
				reserve := func(_ context.Context, key, owner string) (lagi.MemoryFence, error) {
					clock = now.Add(time.Second)
					return lagi.MemoryFence{Key: key, Owner: owner, Generation: 1}, nil
				}
				_, err = s.(store.CaptureMemoryStore).ReserveCaptureMemory(ctx, claim, func() time.Time { return clock }, reserve)
				Expect(err).To(MatchError(store.ErrCaptureConflict))
				records, err := s.ListCaptures(ctx, "alice", id)
				Expect(err).NotTo(HaveOccurred())
				Expect(records[0].BackendFence).To(BeNil())
				next, err := s.ClaimCapture(ctx, "new", clock, time.Minute)
				Expect(err).NotTo(HaveOccurred())
				fresh, err := s.(store.CaptureMemoryStore).ReserveCaptureMemory(ctx, next, func() time.Time { return clock }, func(_ context.Context, key, owner string) (lagi.MemoryFence, error) {
					return lagi.MemoryFence{Key: key, Owner: owner, Generation: 2}, nil
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(fresh.Generation).To(Equal(uint64(2)))
			})

			for _, engine := range []string{"chromem", "postgres"} {
				engine := engine
				for _, pause := range []string{"before", "after", "crash"} {
					pause := pause
					It("hands off real "+engine+" fences paused "+pause+" reservation", func() {
						if engine == "postgres" && os.Getenv("MEMORY_DATABASE_URL") == "" {
							Skip("MEMORY_DATABASE_URL is not set")
						}
						cfg := config.NewApplicationConfig()
						cfg.DataPath = GinkgoT().TempDir()
						cfg.Collections.VectorEngine = engine
						cfg.Collections.DatabaseURL = os.Getenv("MEMORY_DATABASE_URL")
						cfg.Collections.EmbeddingModel = "deterministic"
						cfg.Collections.APIURL = "http://embedding.invalid"
						client := &http.Client{Transport: captureEmbedding{}}
						svc, err := collections.New(cfg, nil, collections.WithHTTPClient(client))
						Expect(err).NotTo(HaveOccurred())
						DeferCleanup(svc.Close)
						collection := fmt.Sprintf("handoff_%d", time.Now().UnixNano())
						Expect(svc.CreateForUser("", collection)).To(Succeed())
						// PostgreSQL backend uses an independent pool, like another frontend.
						nextSvc := svc
						if engine == "postgres" {
							cfg2 := config.NewApplicationConfig()
							cfg2.Collections = cfg.Collections
							cfg2.DataPath = GinkgoT().TempDir()
							nextSvc, err = collections.New(cfg2, nil, collections.WithHTTPClient(client))
							Expect(err).NotTo(HaveOccurred())
							DeferCleanup(nextSvc.Close)
							Expect(nextSvc.CreateForUser("", collection)).To(Succeed())
						}
						other := s
						if backend != "JSON" {
							other = open()
						}
						_, err = s.AcceptMemoryInput(ctx, acceptance(store.CaptureIncoming))
						Expect(err).NotTo(HaveOccurred())
						now := time.Now().Truncate(time.Second)
						old, err := s.ClaimCapture(ctx, "old", now, time.Second)
						Expect(err).NotTo(HaveOccurred())
						var ticks atomic.Int64
						ticks.Store(now.UnixNano())
						clock := func() time.Time { return time.Unix(0, ticks.Load()) }
						entered, release := make(chan struct{}), make(chan struct{})
						done := make(chan error, 1)
						var orphan lagi.MemoryFence
						go func() {
							_, e := s.(store.CaptureMemoryStore).ReserveCaptureMemory(ctx, old, clock, func(c context.Context, key, owner string) (lagi.MemoryFence, error) {
								if pause == "before" {
									close(entered)
									<-release
								}
								f, e := svc.BeginMemoryAttempt(c, "", collection, key, owner)
								orphan = f
								if pause != "before" {
									close(entered)
									<-release
								}
								if pause == "crash" && e == nil {
									return f, errors.New("crash before association")
								}
								return f, e
							})
							done <- e
						}()
						<-entered
						ticks.Store(now.Add(time.Second).UnixNano())
						taking := make(chan struct{})
						type result struct {
							claim store.CaptureClaim
							err   error
						}
						taken := make(chan result, 1)
						go func() {
							close(taking)
							c, e := other.ClaimCapture(ctx, "new", clock(), time.Minute)
							taken <- result{c, e}
						}()
						<-taking
						close(release)
						e := <-done
						if pause == "crash" {
							Expect(e).To(MatchError("crash before association"))
						} else {
							Expect(e).To(MatchError(store.ErrCaptureConflict))
						}
						next := <-taken
						Expect(next.err).NotTo(HaveOccurred())
						Expect(next.claim.Record.BackendFence).To(BeNil())
						fresh, err := other.(store.CaptureMemoryStore).ReserveCaptureMemory(ctx, next.claim, clock, func(c context.Context, key, owner string) (lagi.MemoryFence, error) {
							return nextSvc.BeginMemoryAttempt(c, "", collection, key, owner)
						})
						Expect(err).NotTo(HaveOccurred())
						Expect(fresh.Generation).To(BeNumerically(">", orphan.Generation))
						replay, err := other.(store.CaptureMemoryStore).ReserveCaptureMemory(ctx, next.claim, clock, func(context.Context, string, string) (lagi.MemoryFence, error) {
							return lagi.MemoryFence{}, errors.New("must not reserve again")
						})
						Expect(err).NotTo(HaveOccurred())
						Expect(replay).To(Equal(fresh))
						_, err = s.(store.CaptureMemoryStore).ReserveCaptureMemory(ctx, old, clock, func(c context.Context, key, owner string) (lagi.MemoryFence, error) {
							return svc.BeginMemoryAttempt(c, "", collection, key, owner)
						})
						Expect(err).To(MatchError(store.ErrCaptureConflict))
						hash, err := lagi.MemoryPayloadHash("one memory", nil)
						Expect(err).NotTo(HaveOccurred())
						req := lagi.MemoryWrite{Key: old.Record.ID, Content: "one memory", PayloadHash: hash}
						allow := func(context.Context) error { return nil }
						_, err = svc.WriteMemoryFenced(ctx, "", collection, orphan, req, allow)
						Expect(err).To(MatchError(lagi.ErrMemoryStaleFence))
						err = s.(store.CaptureMemoryStore).WithCaptureMemory(ctx, old, clock, func(context.Context, lagi.MemoryFence) error { return errors.New("stale callback invoked") })
						Expect(err).To(MatchError(store.ErrCaptureConflict))
						var agentStore agents.Store
						if backend != "JSON" {
							agentStore, err = agents.NewGORM(dbHandle)
							Expect(err).NotTo(HaveOccurred())
							Expect(agentStore.Put(ctx, agents.Agent{ID: "agent", Principal: "alice", Name: "Current agent"})).To(Succeed())
						}
						// A callback authorization read must share the held SQL transaction,
						// even with a single pool connection; otherwise it deadlocks.
						if backend != "JSON" {
							pool, e := dbHandle.DB()
							Expect(e).NotTo(HaveOccurred())
							pool.SetMaxOpenConns(1)
						}
						writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
						defer cancel()
						authorize := func(c context.Context) error {
							if _, e := other.GetChat(c, "bob", id); !errors.Is(e, session.ErrNotFound) {
								return fmt.Errorf("cross-principal chat read: %v", e)
							}
							if agentStore != nil {
								if _, e := agentStore.Get(c, "bob", "agent"); !errors.Is(e, agents.ErrNotFound) {
									return fmt.Errorf("cross-principal agent read: %v", e)
								}
								if _, e := agentStore.Get(c, "alice", "agent"); e != nil {
									return e
								}
							}
							_, e := other.GetChat(c, "alice", id)
							return e
						}
						var receipt lagi.MemoryReceipt
						err = other.(store.CaptureMemoryStore).WithCaptureMemory(writeCtx, next.claim, clock, func(c context.Context, f lagi.MemoryFence) error {
							var e error
							receipt, e = nextSvc.WriteMemoryFenced(c, "", collection, f, req, authorize)
							return e
						})
						Expect(err).NotTo(HaveOccurred())
						Expect(receipt.EntryID).NotTo(BeEmpty())
						again, err := nextSvc.WriteMemoryFenced(ctx, "", collection, fresh, req, allow)
						Expect(err).NotTo(HaveOccurred())
						Expect(again.EntryID).To(Equal(receipt.EntryID))
						got, err := nextSvc.ReconcileMemory(ctx, "", collection, req.Key)
						Expect(err).NotTo(HaveOccurred())
						Expect(got.EntryID).To(Equal(receipt.EntryID))
						b, err := nextSvc.Backend("")
						Expect(err).NotTo(HaveOccurred())
						entries, err := b.ListEntries(collection)
						Expect(err).NotTo(HaveOccurred())
						Expect(entries).To(HaveLen(1))
					})
				}
			}

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

			It("fences timeout handoff against early events, stale revisions and takeover", func() {
				_, err := s.AcceptMemoryInput(ctx, acceptance(store.CaptureIncoming))
				Expect(err).NotTo(HaveOccurred())
				now := time.Now().Truncate(time.Second)
				claim, err := s.ClaimCapture(ctx, "first", now, time.Minute)
				Expect(err).NotTo(HaveOccurred())
				timed := s.(store.CaptureMemoryStore)
				_, err = timed.ApplyCaptureTimeout(ctx, claim)
				Expect(err).To(MatchError(store.ErrCaptureConflict))
				state, err := claim.Record.ReducerState()
				Expect(err).NotTo(HaveOccurred())
				w := state.(memory.Authorizing).Work
				env := w.Fence
				env.Now = now.Unix()
				success := memory.Reduce(state, memory.AuthorizationGranted{Envelope: env})
				claim.Now = claim.Record.Deadline.Add(3 * time.Second)
				// Even a caller-supplied transition derived from a pre-expiry
				// success cannot use the ordinary apply path after expiry.
				Expect(s.ApplyCapture(ctx, claim, success)).To(MatchError(store.ErrCaptureConflict))
				stale := claim
				stale.Record.Revision++
				_, err = timed.ApplyCaptureTimeout(ctx, stale)
				Expect(err).To(MatchError(store.ErrCaptureConflict))
				stale = claim
				stale.Record.Owner = "impostor"
				_, err = timed.ApplyCaptureTimeout(ctx, stale)
				Expect(err).To(MatchError(store.ErrCaptureConflict))
				stale = claim
				stale.Record.Generation++
				_, err = timed.ApplyCaptureTimeout(ctx, stale)
				Expect(err).To(MatchError(store.ErrCaptureConflict))
				other := s
				if backend != "JSON" {
					other = open()
				}
				takeover, err := other.ClaimCapture(ctx, "second", claim.Now, time.Minute)
				Expect(err).NotTo(HaveOccurred())
				Expect(takeover.Record.Generation).To(BeNumerically(">", claim.Record.Generation))
				_, err = timed.ApplyCaptureTimeout(ctx, claim)
				Expect(err).To(MatchError(store.ErrCaptureConflict))
				records, err := s.ListCaptures(ctx, "alice", id)
				Expect(err).NotTo(HaveOccurred())
				Expect(records[0].Revision).To(Equal(takeover.Record.Revision))
				Expect(records[0].Owner).To(Equal(takeover.Record.Owner))
				Expect(records[0].Generation).To(Equal(takeover.Record.Generation))
				Expect(records[0].StateJSON).To(Equal(takeover.Record.StateJSON))
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

var _ = Describe("capture state decoding", func() {
	It("preserves all state constructors and input constructors", func() {
		for _, original := range memory.SampleStates() {
			v := reflect.ValueOf(original)
			j := v.FieldByName("Job")
			if !j.IsValid() {
				j = v.FieldByName("Work").FieldByName("Job")
			}
			job := j.Interface().(memory.Job)
			raw, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())
			r := store.CaptureRecord{Identity: job.Identity, State: v.Type().Name(), InputType: reflect.TypeOf(job.Input).Name(), StateJSON: raw}
			decoded, err := r.ReducerState()
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded).To(Equal(original))
		}
		for _, input := range []memory.Input{memory.ContentInput{Text: "content"}, memory.SummaryInput{Text: "summary"}, memory.PendingPayload{Payload: memory.FrozenPayload{Text: "pending"}}, memory.SampleStates()[4].(memory.Writing).Work.Job.Input} {
			original := memory.SampleStates()[0].(memory.Queued)
			original.Job.Input = input
			raw, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())
			r := store.CaptureRecord{Identity: original.Job.Identity, State: "Queued", InputType: reflect.TypeOf(input).Name(), StateJSON: raw}
			decoded, err := r.ReducerState()
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded).To(Equal(original))
		}
	})
	It("rejects malformed queued and work states", func() {
		for _, state := range []string{"Queued", "Authorizing", "Transforming", "PersistingPayload", "Writing", "RetryWaiting", "Saved", "Failed", "Skipped"} {
			for _, job := range []string{`null`, `{}`, `{"Input":null}`, `{"Input":17}`, `{"Input":[]}`, `{"Input":{"Text":17}}`} {
				raw := `{"Job":` + job + `}`
				if state == "Authorizing" || state == "Transforming" || state == "PersistingPayload" || state == "Writing" {
					raw = `{"Work":` + raw + `}`
				}
				r := store.CaptureRecord{State: state, InputType: "ContentInput", StateJSON: json.RawMessage(raw)}
				var err error
				Expect(func() { _, err = r.ReducerState() }).NotTo(Panic(), state+raw)
				Expect(err).To(HaveOccurred(), state+raw)
			}
		}
		for _, raw := range []string{`null`, `{}`, `{"Job":`, `{"Work":null}`, `{"Work":{}}`, `[]`} {
			for _, state := range []string{"Queued", "Authorizing"} {
				r := store.CaptureRecord{State: state, InputType: "ContentInput", StateJSON: json.RawMessage(raw)}
				var err error
				Expect(func() { _, err = r.ReducerState() }).NotTo(Panic())
				Expect(err).To(HaveOccurred())
			}
		}
	})
	It("rejects unknown constructors, broken invariants and identity substitution", func() {
		original := memory.SampleStates()[0].(memory.Queued)
		raw, err := json.Marshal(original)
		Expect(err).NotTo(HaveOccurred())
		r := store.CaptureRecord{Identity: original.Job.Identity, State: "Queued", InputType: "ContentInput", StateJSON: raw}
		r.State = "Unknown"
		_, err = r.ReducerState()
		Expect(err).To(HaveOccurred())
		r.State, r.InputType = "Queued", "Unknown"
		_, err = r.ReducerState()
		Expect(err).To(HaveOccurred())
		r.InputType = "ContentInput"
		r.Identity.Principal = "another"
		_, err = r.ReducerState()
		Expect(err).To(MatchError(store.ErrCaptureConflict))
		r.Identity = original.Job.Identity
		original.Job.Expires = 0
		r.StateJSON, err = json.Marshal(original)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.ReducerState()
		Expect(err).To(HaveOccurred())
	})

	It("rejects a null input without panicking", func() {
		r := store.CaptureRecord{State: "Queued", InputType: "ContentInput", StateJSON: json.RawMessage(`{"Job":{"Input":null}}`)}
		var err error
		Expect(func() { _, err = r.ReducerState() }).NotTo(Panic())
		Expect(err).To(HaveOccurred())
	})
})

// Only the model endpoint is synthetic; LocalRecall reservations and writes are real.
type captureEmbedding struct{}

func (captureEmbedding) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"embedding":[1,0,0],"index":0}]}`)), Request: r}, nil
}
