// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"errors"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Trusted settlement participant", func() {
	ctx := context.Background()
	var db *gorm.DB
	var s *Store
	var path string
	var w SettlementWrite
	open := func() (*Store, *gorm.DB) {
		d, e := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(e).NotTo(HaveOccurred())
		pool, e := d.DB()
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(pool.Close()).To(Succeed()) })
		st, e := New(d)
		Expect(e).NotTo(HaveOccurred())
		return st, d
	}
	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "participant.db")
		s, db = open()
		Expect(s.Create(ctx, "alice", "chat")).To(Succeed())
		for _, id := range []string{"active", "next", "last"} {
			_, e := s.Admit(ctx, "alice", "chat", id, []byte(id))
			Expect(e).NotTo(HaveOccurred())
		}
		snap, e := s.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		w = SettlementWrite{ExpectedRevision: snap.Revision, TurnID: "active", Mode: HoldTurn, Checkpoint: Checkpoint{Version: 1, Context: []byte("opaque")}, Events: []Event{{Kind: "status", Payload: []byte("safe")}}}
	})
	It("rejects nil and autocommit handles before mutation", func() {
		for _, d := range []*gorm.DB{nil, db} {
			out, e := ApplySettlement(d, "alice", "chat", w)
			Expect(e).To(MatchError(ErrInvalidInput))
			Expect(out).To(Equal(Settlement{}))
		}
	})
	It("does not commit the caller transaction or use a separate pool", func() {
		before, e := s.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		other, _ := open()
		tx := db.Begin()
		Expect(tx.Error).NotTo(HaveOccurred())
		out, e := ApplySettlement(tx, "alice", "chat", w)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Revision).To(Equal(before.Revision + 1))
		visible, e := other.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		Expect(visible).To(Equal(before))
		Expect(tx.Rollback().Error).To(Succeed())
		fresh, _ := open()
		visible, e = fresh.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		Expect(visible).To(Equal(before))
	})
	It("propagates a canceled transaction context safely", func() {
		tx := db.Begin()
		Expect(tx.Error).NotTo(HaveOccurred())
		defer tx.Rollback()
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		out, e := ApplySettlement(tx.WithContext(canceled), "alice", "chat", w)
		Expect(e).To(MatchError(context.Canceled))
		Expect(out).To(Equal(Settlement{}))
	})
	It("preserves legacy unfenced completion and original admission deduplication", func() {
		out, e := s.Commit(ctx, "alice", "chat", w.ExpectedRevision, w.TurnID, w.Checkpoint, w.Events)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Next.InputID).To(Equal("next"))
		original, e := s.Admit(ctx, "alice", "chat", "active", []byte("active"))
		Expect(e).NotTo(HaveOccurred())
		Expect(original.RunningTurn).To(Equal("active"))
		duplicate, e := s.Commit(ctx, "alice", "chat", w.ExpectedRevision, w.TurnID, w.Checkpoint, w.Events)
		Expect(e).To(MatchError(ErrConflict))
		Expect(duplicate).To(Equal(Settlement{}))
	})
	for _, rollback := range []bool{false, true} {
		rollback := rollback
		It("aligns selected Hold then Complete SQL trace with model, rollback="+map[bool]string{false: "false", true: "true"}[rollback], func() {
			snap, e := s.Get(ctx, "alice", "chat")
			Expect(e).NotTo(HaveOccurred())
			state := fsOwnState([]fsRow{{Key: fsKey{"alice", "chat"}, Owner: "one", Epoch: 1, Expires: 100, Authority: fsLive, Revision: snap.Revision, Active: snap.Running, Pending: snap.Pending}})
			for _, mode := range []SettlementMode{HoldTurn, CompleteTurn, CompleteTurn, CompleteTurn} {
				w.Mode = mode
				w.ExpectedRevision = snap.Revision
				w.TurnID = snap.Running.InputID
				modelMode := fsHold
				if mode == CompleteTurn {
					modelMode = fsComplete
				}
				outcome := fsCommit
				if rollback {
					outcome = fsRollback
				}
				cmd := fsOwnCommand(fsRequest{Key: fsKey{"alice", "chat"}, Owner: "one", Epoch: 1, Now: 1, ExpectedRevision: w.ExpectedRevision, TurnID: w.TurnID, Mode: modelMode, Transaction: outcome, Checkpoint: w.Checkpoint, Events: w.Events})
				decision := fsReduce(state, cmd)
				Expect(fsCheckTransition(state, cmd, decision)).To(Succeed())
				predicted := decision.fields()
				var candidate Settlement
				sentinel := errors.New("caller rollback")
				err := db.Transaction(func(tx *gorm.DB) error {
					var e error
					candidate, e = ApplySettlement(tx, "alice", "chat", w)
					if e != nil {
						return e
					}
					if rollback {
						return sentinel
					}
					return nil
				})
				if rollback {
					Expect(err).To(MatchError(sentinel))
					Expect(predicted.Result).To(BeNil())
				} else {
					Expect(err).NotTo(HaveOccurred())
					Expect(&candidate).To(Equal(predicted.Result))
				}
				fresh, _ := open()
				snap, e = fresh.Get(ctx, "alice", "chat")
				Expect(e).NotTo(HaveOccurred())
				row := predicted.State.fields()[0]
				Expect(snap.Revision).To(Equal(row.Revision))
				Expect(snap.Running).To(Equal(row.Active)) /* SQL collections have no nil/empty distinction; compare ordered values. */
				Expect(append([]Input{}, snap.Pending...)).To(Equal(append([]Input{}, row.Pending...)))
				Expect(snap.Checkpoint).To(Equal(row.Checkpoint))
				events, e := fresh.Events(ctx, "alice", "chat", 0, 100)
				Expect(e).NotTo(HaveOccurred())
				Expect(append([]Event{}, events...)).To(Equal(append([]Event{}, row.Events...)))
				state = predicted.State
			}
		})
	}
})
