// SPDX-License-Identifier: MIT
package ownership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Atomic fenced settlement", func() {
	ctx := context.Background()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var path string
	var m *Manager
	var s *store.Store
	var db *gorm.DB
	var lease Lease
	var write store.SettlementWrite
	open := func() (*Manager, *store.Store, *gorm.DB) {
		d, e := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(e).NotTo(HaveOccurred())
		pool, e := d.DB()
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(pool.Close()).To(Succeed()) })
		mm, e := New(d)
		Expect(e).NotTo(HaveOccurred())
		ss, e := store.New(d)
		Expect(e).NotTo(HaveOccurred())
		return mm, ss, d
	}
	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "settlement.db")
		m, s, db = open()
		Expect(s.Create(ctx, "alice", "chat")).To(Succeed())
		a, e := s.Admit(ctx, "alice", "chat", "active", []byte("active"))
		Expect(e).NotTo(HaveOccurred())
		_, e = s.Admit(ctx, "alice", "chat", "next", []byte("next"))
		Expect(e).NotTo(HaveOccurred())
		snap, e := s.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		lease, e = m.Acquire(ctx, Key{"alice", "chat"}, "one", now, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		write = store.SettlementWrite{ExpectedRevision: snap.Revision, TurnID: a.RunningTurn, Mode: store.HoldTurn, Checkpoint: store.Checkpoint{Version: 1, Context: []byte("opaque")}, Events: []store.Event{{Kind: "status", Payload: []byte(`{"status":"waiting"}`)}}}
	})
	It("holds the existing active input and queue while atomically writing checkpoint and events", func() {
		out, e := m.Settle(ctx, lease, now, write)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Next).To(BeNil())
		Expect(out.Revision).To(Equal(write.ExpectedRevision + 1))
		_, fresh, _ := open()
		snap, e := fresh.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		Expect(snap.Running.InputID).To(Equal("active"))
		Expect(snap.Pending).To(HaveLen(1))
		Expect(snap.Checkpoint).To(Equal(write.Checkpoint))
	})
	It("completes exactly active and claims oldest FIFO across independent pools", func() {
		_, other, _ := open()
		_, e := other.Admit(ctx, "alice", "chat", "last", []byte("last"))
		Expect(e).NotTo(HaveOccurred())
		snap, e := other.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		write.ExpectedRevision = snap.Revision
		write.Mode = store.CompleteTurn
		out, e := m.Settle(ctx, lease, now, write)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Next.InputID).To(Equal("next"))
		snap, e = other.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		Expect(snap.Running.InputID).To(Equal("next"))
		Expect(snap.Pending[0].InputID).To(Equal("last"))
		events, e := other.Events(ctx, "alice", "chat", 0, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Sequence).To(Equal(uint64(1)))
		Expect(events[0].TurnID).To(Equal("active"))
	})
	It("completes an empty queue without a handoff", func() {
		Expect(db.Exec("DELETE FROM runtime_inputs WHERE input_id = 'next'").Error).To(Succeed())
		write.Mode = store.CompleteTurn
		out, e := m.Settle(ctx, lease, now, write)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Next).To(BeNil())
	})
	It("isolates identical IDs under different principals and conversations", func() {
		for _, k := range []Key{{"bob", "chat"}, {"alice", "other"}} {
			Expect(s.Create(ctx, k.Principal, k.Conversation)).To(Succeed())
			_, e := s.Admit(ctx, k.Principal, k.Conversation, "active", []byte("different"))
			Expect(e).NotTo(HaveOccurred())
		}
		out, e := m.Settle(ctx, lease, now, write)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Revision).To(Equal(write.ExpectedRevision + 1))
		for _, k := range []Key{{"bob", "chat"}, {"alice", "other"}} {
			snap, e := s.Get(ctx, k.Principal, k.Conversation)
			Expect(e).NotTo(HaveOccurred())
			Expect(snap.Checkpoint).To(Equal(store.Checkpoint{}))
			Expect(snap.Running.Payload).To(Equal([]byte("different")))
		}
	})
	for _, name := range []string{"expired", "exact expiry", "released", "stolen", "wrong owner", "wrong epoch", "zero epoch", "overflow epoch", "invalid trusted time"} {
		name := name
		It("rejects "+name+" with zero settlement and unchanged rows", func() {
			before, e := s.Get(ctx, "alice", "chat")
			Expect(e).NotTo(HaveOccurred())
			at := now
			want := ErrLeaseLost
			switch name {
			case "expired":
				at = lease.ExpiresAt.Add(time.Nanosecond)
			case "exact expiry":
				at = lease.ExpiresAt
			case "released":
				Expect(m.Release(ctx, lease, now)).To(Succeed())
			case "stolen":
				_, e = m.Acquire(ctx, lease.Key, "two", lease.ExpiresAt, time.Minute)
				Expect(e).NotTo(HaveOccurred())
				at = lease.ExpiresAt
			case "wrong owner":
				lease.Owner = "two"
			case "wrong epoch":
				lease.Epoch++
			case "zero epoch":
				lease.Epoch = 0
				want = ErrInvalidInput
			case "overflow epoch":
				lease.Epoch = math.MaxUint64
				want = ErrInvalidInput
			case "invalid trusted time":
				at = time.Time{}
				want = ErrInvalidInput
			}
			out, e := m.Settle(ctx, lease, at, write)
			Expect(e).To(MatchError(want))
			Expect(out).To(Equal(store.Settlement{}))
			_, fresh, _ := open()
			after, e := fresh.Get(ctx, "alice", "chat")
			Expect(e).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
		})
	}
	It("honors persisted renewal rather than token expiry", func() {
		_, e := m.Renew(ctx, lease, now.Add(time.Second), time.Hour)
		Expect(e).NotTo(HaveOccurred())
		other, _, _ := open()
		out, e := other.Settle(ctx, lease, lease.ExpiresAt, write)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Revision).To(Equal(write.ExpectedRevision + 1))
	})
	for _, name := range []string{"stale revision", "stale turn", "missing active input", "settled active input", "tombstone", "invalid mode", "unknown mode", "event turn", "checkpoint overflow", "expected overflow", "revision exhaustion", "event exhaustion"} {
		name := name
		It("rejects "+name+" without partial persistence", func() {
			want := store.ErrConflict
			switch name {
			case "stale revision":
				write.ExpectedRevision--
			case "stale turn":
				write.TurnID = "other"
			case "missing active input":
				Expect(db.Exec("DELETE FROM runtime_inputs WHERE input_id='active'").Error).To(Succeed())
			case "settled active input":
				Expect(db.Exec("UPDATE runtime_inputs SET settled=1 WHERE input_id='active'").Error).To(Succeed())
			case "tombstone":
				Expect(s.Delete(ctx, "alice", "chat")).To(Succeed())
				want = store.ErrDeleted
			case "invalid mode":
				write.Mode = 0
				want = store.ErrInvalidInput
			case "unknown mode":
				write.Mode = 99
				want = store.ErrInvalidInput
			case "event turn":
				write.Events[0].TurnID = "other"
				want = store.ErrInvalidInput
			case "checkpoint overflow":
				write.Checkpoint.Version = math.MaxUint64
				want = store.ErrInvalidInput
			case "expected overflow":
				write.ExpectedRevision = math.MaxUint64
			case "revision exhaustion":
				Expect(db.Exec("UPDATE runtime_conversations SET revision=?", int64(math.MaxInt64)).Error).To(Succeed())
				write.ExpectedRevision = math.MaxInt64
			case "event exhaustion":
				Expect(db.Exec("UPDATE runtime_conversations SET last_sequence=?", int64(math.MaxInt64)).Error).To(Succeed())
			}
			var before []map[string]any
			Expect(db.Table("runtime_conversations").Find(&before).Error).To(Succeed())
			out, e := m.Settle(ctx, lease, now, write)
			Expect(e).To(MatchError(want))
			Expect(out).To(Equal(store.Settlement{}))
			_, _, fresh := open()
			var after []map[string]any
			Expect(fresh.Table("runtime_conversations").Find(&after).Error).To(Succeed())
			Expect(after).To(Equal(before))
			var count int64
			Expect(fresh.Table("runtime_events").Count(&count).Error).To(Succeed())
			Expect(count).To(BeZero())
		})
	}
	It("accepts signed maxima and ignores caller event sequence", func() {
		Expect(db.Exec("UPDATE runtime_conversations SET revision=?,last_sequence=?", int64(math.MaxInt64-1), int64(math.MaxInt64-1)).Error).To(Succeed())
		write.ExpectedRevision = math.MaxInt64 - 1
		write.Checkpoint.Version = math.MaxInt64
		write.Events[0].Sequence = math.MaxUint64
		out, e := m.Settle(ctx, lease, now, write)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Revision).To(Equal(uint64(math.MaxInt64)))
		var seq int64
		Expect(db.Raw("SELECT sequence FROM runtime_events").Scan(&seq).Error).To(Succeed())
		Expect(seq).To(Equal(int64(math.MaxInt64)))
	})
	It("has exactly one concurrent winner across independently opened managers", func() {
		other, _, _ := open()
		write.Mode = store.CompleteTurn
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		outs := make([]store.Settlement, 2)
		for i, mm := range []*Manager{m, other} {
			wg.Add(1)
			go func(i int, mm *Manager) {
				defer wg.Done()
				<-start
				outs[i], errs[i] = mm.Settle(ctx, lease, now, write)
			}(i, mm)
		}
		close(start)
		wg.Wait()
		wins := 0
		for i, e := range errs {
			if e == nil {
				wins++
				Expect(outs[i].Next.InputID).To(Equal("next"))
			} else {
				Expect(e).To(MatchError(store.ErrConflict))
				Expect(outs[i]).To(Equal(store.Settlement{}))
			}
		}
		Expect(wins).To(Equal(1))
	})
	for _, name := range []string{"takeover", "release"} {
		name := name
		It("serializes settlement after a pending "+name+" transaction", func() {
			other, _, _ := open()
			tx := db.Begin()
			Expect(tx.Error).NotTo(HaveOccurred())
			vals := map[string]any{"owner": "two", "epoch": 2}
			if name == "release" {
				vals = map[string]any{"owner": "", "expires": 0}
			}
			Expect(scope(tx, lease.Key).Model(&leaseRow{}).Updates(vals).Error).To(Succeed())
			entered := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				close(entered)
				out, e := other.Settle(ctx, lease, now, write)
				if out != (store.Settlement{}) {
					result <- store.ErrConflict
					return
				}
				result <- e
			}()
			<-entered
			Expect(tx.Commit().Error).To(Succeed())
			Expect(<-result).To(MatchError(ErrLeaseLost))
		})
	}
	for _, stage := range []string{"conversation", "input", "first event", "second event"} {
		stage := stage
		It("rolls back and reopens after failure at "+stage, func() {
			write.Mode = store.CompleteTurn
			write.Events = append(write.Events, store.Event{Kind: "status", Payload: []byte("safe")})
			before, e := s.Get(ctx, "alice", "chat")
			Expect(e).NotTo(HaveOccurred())
			target := "UPDATE ON runtime_conversations"
			when := ""
			switch stage {
			case "input":
				target = "UPDATE ON runtime_inputs"
			case "first event":
				target = "INSERT ON runtime_events"
			case "second event":
				target = "INSERT ON runtime_events"
				when = " WHEN NEW.sequence=2"
			}
			Expect(db.Exec("CREATE TRIGGER fail_settle BEFORE " + target + when + " BEGIN SELECT RAISE(ABORT,'private injected content'); END").Error).To(Succeed())
			out, e := m.Settle(ctx, lease, now, write)
			Expect(e).To(MatchError(errSettlement))
			Expect(e.Error()).NotTo(ContainSubstring("private"))
			Expect(out).To(Equal(store.Settlement{}))
			_, fresh, d := open()
			after, e := fresh.Get(ctx, "alice", "chat")
			Expect(e).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
			var count int64
			Expect(d.Table("runtime_events").Count(&count).Error).To(Succeed())
			Expect(count).To(BeZero())
		})
	}
	It("returns zero on outer commit failure after all mutations", func() {
		// A deferred foreign key fails COMMIT, not the participant's last statement.
		pool, e := db.DB()
		Expect(e).NotTo(HaveOccurred())
		pool.SetMaxOpenConns(1)
		Expect(db.Exec("PRAGMA foreign_keys=ON").Error).To(Succeed())
		Expect(db.Exec("CREATE TABLE settlement_parent(id INTEGER PRIMARY KEY)").Error).To(Succeed())
		Expect(db.Exec("CREATE TABLE settlement_child(id INTEGER REFERENCES settlement_parent(id) DEFERRABLE INITIALLY DEFERRED)").Error).To(Succeed())
		Expect(db.Exec("CREATE TRIGGER fail_commit AFTER INSERT ON runtime_events BEGIN INSERT INTO settlement_child VALUES(1); END").Error).To(Succeed())

		created := 0
		Expect(db.Callback().Create().After("gorm:create").Register("observe_commit", func(tx *gorm.DB) {
			if tx.Statement.Table == "runtime_events" && tx.Error == nil {
				created++
			}
		})).To(Succeed())
		DeferCleanup(func() { Expect(db.Callback().Create().Remove("observe_commit")).To(Succeed()) })
		before, e := s.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		write.Mode = store.CompleteTurn
		out, e := m.Settle(ctx, lease, now, write)
		Expect(e).To(MatchError(errSettlement))
		Expect(out).To(Equal(store.Settlement{}))
		Expect(created).To(Equal(1))
		_, fresh, _ := open()
		after, e := fresh.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
	})
	for _, mode := range []store.SettlementMode{store.CompleteTurn, store.HoldTurn} {
		mode := mode
		It("rolls back cancellation after event mutation in mode "+map[store.SettlementMode]string{store.CompleteTurn: "complete", store.HoldTurn: "hold"}[mode], func() {
			before, e := s.Get(ctx, "alice", "chat")
			Expect(e).NotTo(HaveOccurred())
			canceled, cancel := context.WithCancel(ctx)
			defer cancel()
			calls := 0
			Expect(db.Callback().Create().After("gorm:create").Register("settlement_cancel", func(tx *gorm.DB) {
				if tx.Statement.Table == "runtime_events" {
					calls++
					cancel()
				}
			})).To(Succeed())
			DeferCleanup(func() { Expect(db.Callback().Create().Remove("settlement_cancel")).To(Succeed()) })
			write.Mode = mode
			out, e := m.Settle(canceled, lease, now, write)
			Expect(e).To(MatchError(context.Canceled))
			Expect(out).To(Equal(store.Settlement{}))
			Expect(calls).To(Equal(1))
			_, fresh, d := open()
			after, e := fresh.Get(ctx, "alice", "chat")
			Expect(e).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
			var count int64
			Expect(d.Table("runtime_events").Count(&count).Error).To(Succeed())
			Expect(count).To(BeZero())
		})
	}
	for _, mode := range []store.SettlementMode{store.CompleteTurn, store.HoldTurn} {
		for _, deadline := range []bool{false, true} {
			mode, deadline := mode, deadline
			It(fmt.Sprintf("preserves cancellation identity when automatic rollback wins COMMIT (mode=%d deadline=%t)", mode, deadline), func() {
				before, e := s.Get(ctx, "alice", "chat")
				Expect(e).NotTo(HaveOccurred())
				canceled, cancel := context.WithCancel(ctx)
				want := context.Canceled
				if deadline {
					cancel()
					canceled, cancel = context.WithTimeout(ctx, 5*time.Second)
					want = context.DeadlineExceeded
				}
				defer cancel()
				calls := 0
				Expect(db.Callback().Create().After("gorm:create").Register("rollback_before_commit", func(tx *gorm.DB) {
					if tx.Statement.Table != "runtime_events" || tx.Error != nil {
						return
					}
					calls++
					if !deadline {
						cancel()
					}
					Eventually(canceled.Done(), 10*time.Second).Should(BeClosed())
					// Wait for database/sql's rollback to mark the transaction done,
					// rather than hoping a sleep wins the COMMIT scheduling race.
					transaction, ok := tx.Statement.ConnPool.(*sql.Tx)
					Expect(ok).To(BeTrue())
					Eventually(func() error {
						_, err := transaction.ExecContext(ctx, "SELECT 1")
						return err
					}, 10*time.Second).Should(MatchError(sql.ErrTxDone))
				})).To(Succeed())
				DeferCleanup(func() { Expect(db.Callback().Create().Remove("rollback_before_commit")).To(Succeed()) })
				write.Mode = mode
				out, e := m.Settle(canceled, lease, now, write)
				Expect(out).To(Equal(store.Settlement{}))
				Expect(calls).To(Equal(1))
				_, fresh, d := open()
				after, readErr := fresh.Get(ctx, "alice", "chat")
				Expect(readErr).NotTo(HaveOccurred())
				Expect(after).To(Equal(before))
				var count int64
				Expect(d.Table("runtime_events").Count(&count).Error).To(Succeed())
				Expect(count).To(BeZero())
				Expect(e).To(MatchError(want))
			})
		}
	}
	It("rolls back hold checkpoint and earlier events after the second event fails", func() {
		before, e := s.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		write.Events = append(write.Events, store.Event{Kind: "status"})
		Expect(db.Exec("CREATE TRIGGER fail_hold BEFORE INSERT ON runtime_events WHEN NEW.sequence=2 BEGIN SELECT RAISE(ABORT,'private hold content'); END").Error).To(Succeed())
		out, e := m.Settle(ctx, lease, now, write)
		Expect(e).To(MatchError(errSettlement))
		Expect(out).To(Equal(store.Settlement{}))
		_, fresh, d := open()
		after, e := fresh.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
		var count int64
		Expect(d.Table("runtime_events").Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("does not replace an unrelated mutation failure with concurrent cancellation", func() {
		canceled, cancel := context.WithCancel(ctx)
		defer cancel()
		Expect(db.Callback().Create().After("gorm:create").Register("unrelated_failure", func(tx *gorm.DB) {
			if tx.Statement.Table == "runtime_events" && tx.Error == nil {
				cancel()
				tx.AddError(errors.New("private unrelated failure"))
			}
		})).To(Succeed())
		DeferCleanup(func() { Expect(db.Callback().Create().Remove("unrelated_failure")).To(Succeed()) })
		out, e := m.Settle(canceled, lease, now, write)
		Expect(out).To(Equal(store.Settlement{}))
		Expect(e).To(MatchError(errSettlement))
	})
	It("preserves domain errors even when the context is canceled", func() {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		out, e := m.Settle(canceled, Lease{}, now, write)
		Expect(out).To(Equal(store.Settlement{}))
		Expect(e).To(MatchError(ErrInvalidInput))
	})
	It("propagates cancellation without returning a candidate", func() {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		out, e := m.Settle(canceled, lease, now, write)
		Expect(e).To(MatchError(context.Canceled))
		Expect(out).To(Equal(store.Settlement{}))
	})

})
