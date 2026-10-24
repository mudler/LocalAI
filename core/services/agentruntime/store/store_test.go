// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Transactional runtime store (SQLite only)")
}

var _ = Describe("Store", func() {
	var s *Store
	var db *gorm.DB
	var path string
	ctx := context.Background()
	open := func(path string) (*Store, *gorm.DB) {
		d, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).NotTo(HaveOccurred())
		sql, err := d.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(sql.Close()).To(Succeed()) })
		st, err := New(d)
		Expect(err).NotTo(HaveOccurred())
		return st, d
	}
	get := func() Snapshot { v, e := s.Get(ctx, "alice", "chat"); Expect(e).NotTo(HaveOccurred()); return v }
	admit := func(id string) Admission {
		a, e := s.Admit(ctx, "alice", "chat", id, []byte(id))
		Expect(e).NotTo(HaveOccurred())
		return a
	}
	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "runtime.db")
		s, db = open(path)
		Expect(s.Create(ctx, "alice", "chat")).To(Succeed())
	})
	It("scopes every operation to the principal even for identical IDs", func() {
		Expect(s.Create(ctx, "bob", "chat")).To(Succeed())
		a := admit("same")
		b, e := s.Admit(ctx, "bob", "chat", "same", []byte("other"))
		Expect(e).NotTo(HaveOccurred())
		Expect(b.RunningTurn).To(Equal(a.RunningTurn))
		_, e = s.Get(ctx, "mallory", "chat")
		Expect(e).To(MatchError(ErrNotFound))
		_, e = s.Commit(ctx, "mallory", "chat", a.Revision, a.RunningTurn, Checkpoint{}, nil)
		Expect(e).To(MatchError(ErrNotFound))
		Expect(s.Delete(ctx, "mallory", "chat")).To(MatchError(ErrNotFound))
		_, e = s.Events(ctx, "mallory", "chat", 0, 10)
		Expect(e).To(MatchError(ErrNotFound))
	})
	It("deduplicates original admissions across settlement and reopen and rejects changed bytes", func() {
		first := admit("one")
		second := admit("two")
		Expect(first.Queued).To(BeFalse())
		Expect(second.Queued).To(BeTrue())
		_, e := s.Commit(ctx, "alice", "chat", second.Revision, "one", Checkpoint{}, nil)
		Expect(e).NotTo(HaveOccurred())
		s, _ = open(path)
		a, e := s.Admit(ctx, "alice", "chat", "one", []byte("one"))
		Expect(e).NotTo(HaveOccurred())
		Expect(a).To(Equal(first))
		a, e = s.Admit(ctx, "alice", "chat", "two", []byte("two"))
		Expect(e).NotTo(HaveOccurred())
		Expect(a).To(Equal(second))
		_, e = s.Admit(ctx, "alice", "chat", "one", []byte("changed"))
		Expect(e).To(MatchError(ErrConflict))
		Expect(get().Running.InputID).To(Equal("two"))
		Expect(get().Pending).To(BeEmpty())
	})
	It("persists FIFO and atomically checkpoints every opaque field, events, settlement and next claim", func() {
		admit("a")
		admit("b")
		a := admit("c")
		s, _ = open(path)
		cp := Checkpoint{Version: 7, Context: []byte("private context"), Artifacts: []byte("artifacts"), Pruning: []byte("pruning"), TranscriptCursor: []byte("cursor")}
		out, e := s.Commit(ctx, "alice", "chat", a.Revision, "a", cp, []Event{{Sequence: 999, TurnID: "a", Kind: "done", Payload: []byte("safe")}, {Kind: "checkpoint"}})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Revision).To(Equal(a.Revision + 1))
		Expect(out.Next.InputID).To(Equal("b"))
		v := get()
		Expect(v.Checkpoint).To(Equal(cp))
		Expect(v.Running.InputID).To(Equal("b"))
		Expect(v.Pending).To(HaveLen(1))
		Expect(v.Pending[0].InputID).To(Equal("c"))
		Expect(v.Pending[0].Ordinal).To(BeNumerically(">", v.Running.Ordinal))
		events, e := s.Events(ctx, "alice", "chat", 0, 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Sequence).To(Equal(uint64(1)))
		Expect(events[0].TurnID).To(Equal("a"))
		events, e = s.Events(ctx, "alice", "chat", 1, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Sequence).To(Equal(uint64(2)))
		Expect(s.Create(ctx, "bob", "chat")).To(Succeed())
		events, e = s.Events(ctx, "bob", "chat", 0, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
	})
	It("rejects stale revisions and wrong turns without changing data", func() {
		a := admit("a")
		b := admit("b")
		before := get()
		_, e := s.Commit(ctx, "alice", "chat", a.Revision, "a", Checkpoint{Version: 1}, []Event{{Kind: "done"}})
		Expect(e).To(MatchError(ErrConflict))
		_, e = s.Commit(ctx, "alice", "chat", b.Revision, "b", Checkpoint{}, nil)
		Expect(e).To(MatchError(ErrConflict))
		Expect(get()).To(Equal(before))
		_, e = s.Commit(ctx, "alice", "chat", b.Revision, "a", Checkpoint{}, nil)
		Expect(e).NotTo(HaveOccurred())
		_, e = s.Commit(ctx, "alice", "chat", b.Revision, "a", Checkpoint{}, nil)
		Expect(e).To(MatchError(ErrConflict))
	})
	It("rolls back checkpoint, outbox, queue and sequence on an injected database failure", func() {
		admit("a")
		a := admit("b")
		before := get()
		// SQLite triggers exercise a real failure after the checkpoint CAS, not a mock transaction.
		Expect(db.Exec("CREATE TRIGGER fail_runtime_event BEFORE INSERT ON runtime_events WHEN NEW.kind = 'fail' BEGIN SELECT RAISE(ABORT, 'injected failure'); END").Error).To(Succeed())
		_, e := s.Commit(ctx, "alice", "chat", a.Revision, "a", Checkpoint{Version: 2, Context: []byte("new"), Artifacts: []byte("new"), Pruning: []byte("new"), TranscriptCursor: []byte("new")}, []Event{{Kind: "done"}, {Kind: "fail"}})
		Expect(e).To(HaveOccurred())
		Expect(e.Error()).To(ContainSubstring("injected failure"))
		Expect(get()).To(Equal(before))
		events, e := s.Events(ctx, "alice", "chat", 0, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
		Expect(db.Exec("DROP TRIGGER fail_runtime_event").Error).To(Succeed())
		_, e = s.Commit(ctx, "alice", "chat", a.Revision, "a", Checkpoint{}, []Event{{Kind: "done"}})
		Expect(e).NotTo(HaveOccurred())
		events, e = s.Events(ctx, "alice", "chat", 0, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(events[0].Sequence).To(Equal(uint64(1)))
	})
	It("makes deletion absorbing without leaking tombstones or losing dedup authority", func() {
		a := admit("a")
		Expect(s.Delete(ctx, "alice", "chat")).To(Succeed())
		Expect(s.Delete(ctx, "alice", "chat")).To(Succeed())
		s, _ = open(path)
		v, e := s.Get(ctx, "alice", "chat")
		Expect(e).To(MatchError(ErrDeleted))
		Expect(v.Deleted).To(BeTrue())
		Expect(s.Create(ctx, "alice", "chat")).To(MatchError(ErrDeleted))
		_, e = s.Admit(ctx, "alice", "chat", "a", []byte("a"))
		Expect(e).To(MatchError(ErrDeleted))
		_, e = s.Commit(ctx, "alice", "chat", a.Revision, "a", Checkpoint{}, nil)
		Expect(e).To(MatchError(ErrDeleted))
		_, e = s.Get(ctx, "bob", "chat")
		Expect(e).To(MatchError(ErrNotFound))
		_, e = s.Events(ctx, "alice", "chat", 0, 10)
		Expect(e).To(MatchError(ErrDeleted))
	})
	It("isolates all returned and caller-owned byte buffers", func() {
		payload := []byte("payload")
		a, e := s.Admit(ctx, "alice", "chat", "a", payload)
		Expect(e).NotTo(HaveOccurred())
		payload[0] = 'X'
		v := get()
		Expect(string(v.Running.Payload)).To(Equal("payload"))
		v.Running.Payload[0] = 'Y'
		cp := Checkpoint{Context: []byte("ctx"), Artifacts: []byte("art"), Pruning: []byte("prune"), TranscriptCursor: []byte("cursor")}
		ev := []Event{{Kind: "done", Payload: []byte("event")}}
		_, e = s.Commit(ctx, "alice", "chat", a.Revision, "a", cp, ev)
		Expect(e).NotTo(HaveOccurred())
		cp.Context[0] = 'X'
		ev[0].Payload[0] = 'X'
		v = get()
		Expect(string(v.Checkpoint.Context)).To(Equal("ctx"))
		v.Checkpoint.Context[0] = 'Y'
		v.Checkpoint.Artifacts[0] = 'Y'
		v.Checkpoint.Pruning[0] = 'Y'
		v.Checkpoint.TranscriptCursor[0] = 'Y'
		Expect(string(get().Checkpoint.Context)).To(Equal("ctx"))
		Expect(string(get().Checkpoint.Artifacts)).To(Equal("art"))
		Expect(string(get().Checkpoint.Pruning)).To(Equal("prune"))
		Expect(string(get().Checkpoint.TranscriptCursor)).To(Equal("cursor"))
		events, e := s.Events(ctx, "alice", "chat", 0, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(events[0].Payload)).To(Equal("event"))
		events[0].Payload[0] = 'Y'
		events, e = s.Events(ctx, "alice", "chat", 0, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(events[0].Payload)).To(Equal("event"))
	})
	It("serializes independent file-backed handles with concurrent unique and duplicate inputs", func() {
		const n = 24
		other, _ := open(path)
		handles := []*Store{s, other}
		var wg sync.WaitGroup
		errs := make(chan error, n*2)
		admissions := make(chan Admission, n*2)
		for i := 0; i < n*2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id := fmt.Sprintf("input-%02d", i%n)
				a, e := handles[i%2].Admit(ctx, "alice", "chat", id, []byte(id))
				errs <- e
				admissions <- a
			}(i)
		}
		wg.Wait()
		close(errs)
		close(admissions)
		for e := range errs {
			Expect(e).NotTo(HaveOccurred())
		}
		originals := map[string]Admission{}
		starts := map[string]bool{}
		for a := range admissions {
			if old, ok := originals[a.InputID]; ok {
				Expect(a).To(Equal(old))
			}
			originals[a.InputID] = a
			if !a.Queued {
				starts[a.InputID] = true
			}
		}
		Expect(starts).To(HaveLen(1))
		Expect(originals).To(HaveLen(n))
		v := get()
		Expect(v.Pending).To(HaveLen(n - 1))
		ordered := append([]Input{*v.Running}, v.Pending...)
		for i, in := range ordered {
			Expect(in.Ordinal).To(Equal(uint64(i + 1)))
		}
		s, _ = open(path)
		for _, in := range ordered {
			v = get()
			Expect(v.Running.InputID).To(Equal(in.InputID))
			_, e := s.Commit(ctx, "alice", "chat", v.Revision, in.InputID, Checkpoint{}, nil)
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(get().Running).To(BeNil())
	})
	It("allows only one winner when two handles settle the same revision", func() {
		a := admit("a")
		other, _ := open(path)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, h := range []*Store{s, other} {
			wg.Add(1)
			go func(h *Store) {
				defer wg.Done()
				_, e := h.Commit(ctx, "alice", "chat", a.Revision, "a", Checkpoint{}, []Event{{Kind: "done"}})
				errs <- e
			}(h)
		}
		wg.Wait()
		close(errs)
		wins, conflicts := 0, 0
		for e := range errs {
			if e == nil {
				wins++
			} else if errors.Is(e, ErrConflict) {
				conflicts++
			} else {
				Expect(e).NotTo(HaveOccurred())
			}
		}
		Expect(wins).To(Equal(1))
		Expect(conflicts).To(Equal(1))
		events, e := s.Events(ctx, "alice", "chat", 0, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
	})
	It("validates nonempty bounded identities and pagination without anonymous aliases", func() {
		for _, id := range []string{"", " ", strings.Repeat("x", 257)} {
			Expect(s.Create(ctx, id, "c")).To(MatchError(ErrInvalidInput))
			Expect(s.Create(ctx, "p", id)).To(MatchError(ErrInvalidInput))
			_, e := s.Admit(ctx, "alice", "chat", id, nil)
			Expect(e).To(MatchError(ErrInvalidInput))
		}
		_, e := s.Events(ctx, "alice", "chat", 0, 0)
		Expect(e).To(MatchError(ErrInvalidInput))
		_, e = s.Events(ctx, "alice", "chat", 0, 1001)
		Expect(e).To(MatchError(ErrInvalidInput))
		a := admit("a")
		_, e = s.Commit(ctx, "alice", "chat", a.Revision, "a", Checkpoint{}, []Event{{Kind: ""}})
		Expect(e).To(MatchError(ErrInvalidInput))
		Expect(get().Revision).To(Equal(a.Revision))
	})
	It("retains hash and dedup rows after deletion and never returns private tombstone bytes", func() {
		a := admit("a")
		_, e := s.Commit(ctx, "alice", "chat", a.Revision, "a", Checkpoint{Context: []byte("private")}, []Event{{Kind: "done"}})
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Delete(ctx, "alice", "chat")).To(Succeed())
		var rows []inputRow
		Expect(scope(db, "alice", "chat").Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Settled).To(BeTrue())
		Expect(rows[0].PayloadHash).To(HaveLen(32))
		Expect(rows[0].admission()).To(Equal(a))
		v, e := s.Get(ctx, "alice", "chat")
		Expect(e).To(MatchError(ErrDeleted))
		Expect(v.Checkpoint).To(Equal(Checkpoint{}))
		Expect(v.Running).To(BeNil())
		Expect(v.Pending).To(BeEmpty())
	})
	It("replaces all checkpoint fields including zero values and keeps event sequences across reopen", func() {
		a := admit("a")
		cp := Checkpoint{Version: 9, Context: []byte("ctx"), Artifacts: []byte("art"), Pruning: []byte("prune"), TranscriptCursor: []byte("cursor")}
		_, e := s.Commit(ctx, "alice", "chat", a.Revision, "a", cp, []Event{{Kind: "first"}})
		Expect(e).NotTo(HaveOccurred())
		a = admit("b")
		Expect(a.Queued).To(BeFalse())
		Expect(a.RunningTurn).To(Equal("b"))
		_, e = s.Commit(ctx, "alice", "chat", a.Revision, "b", Checkpoint{}, []Event{{Kind: "second"}})
		Expect(e).NotTo(HaveOccurred())
		s, _ = open(path)
		Expect(get().Checkpoint).To(Equal(Checkpoint{}))
		events, e := s.Events(ctx, "alice", "chat", 0, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(2))
		Expect(events[1].Sequence).To(Equal(uint64(2)))
		Expect(events[1].TurnID).To(Equal("b"))
	})
	It("does not alias next-input, pending-input or hash buffers", func() {
		admit("a")
		a := admit("b")
		admit("c")
		v := get()
		v.Pending[0].Payload[0] = 'X'
		v.Pending[0].PayloadHash[0] ^= 255
		v2 := get()
		Expect(string(v2.Pending[0].Payload)).To(Equal("b"))
		Expect(v2.Pending[0].PayloadHash).NotTo(Equal(v.Pending[0].PayloadHash))
		out, e := s.Commit(ctx, "alice", "chat", v2.Revision, "a", Checkpoint{}, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Next.InputID).To(Equal(a.InputID))
		out.Next.Payload[0] = 'X'
		out.Next.PayloadHash[0] ^= 255
		Expect(string(get().Running.Payload)).To(Equal("b"))
		Expect(get().Running.PayloadHash).NotTo(Equal(out.Next.PayloadHash))
	})
	It("rejects mismatched event turns before any writes and handles canceled contexts", func() {
		a := admit("a")
		before := get()
		_, e := s.Commit(ctx, "alice", "chat", a.Revision, "a", Checkpoint{Version: 2}, []Event{{TurnID: "other", Kind: "done"}})
		Expect(e).To(MatchError(ErrInvalidInput))
		Expect(get()).To(Equal(before))
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, e = s.Admit(canceled, "alice", "chat", "b", nil)
		Expect(errors.Is(e, context.Canceled)).To(BeTrue())
		Expect(get()).To(Equal(before))
	})
	It("classifies retries narrowly and never retries arbitrary failures", func() {
		Expect(retryable(errors.New("database is locked"))).To(BeFalse())
		Expect(retryable(ErrConflict)).To(BeFalse())
		Expect(retryable(ErrInvalidInput)).To(BeFalse())
		calls := 0
		injected := errors.New("injected nonserialization error")
		e := s.transaction(ctx, func(tx *gorm.DB) error { calls++; return injected })
		Expect(e).To(MatchError(injected))
		Expect(calls).To(Equal(1))
	})
	It("validates principal and conversation on every read and mutation", func() {
		for _, ids := range [][2]string{{"", "chat"}, {"alice", ""}, {" ", "chat"}, {"alice", strings.Repeat("x", 257)}} {
			p, c := ids[0], ids[1]
			_, e := s.Get(ctx, p, c)
			Expect(e).To(MatchError(ErrInvalidInput))
			_, e = s.Admit(ctx, p, c, "a", nil)
			Expect(e).To(MatchError(ErrInvalidInput))
			_, e = s.Commit(ctx, p, c, 0, "a", Checkpoint{}, nil)
			Expect(e).To(MatchError(ErrInvalidInput))
			Expect(s.Delete(ctx, p, c)).To(MatchError(ErrInvalidInput))
			_, e = s.Events(ctx, p, c, 0, 10)
			Expect(e).To(MatchError(ErrInvalidInput))
		}
		Expect(s.Create(ctx, "alice", "chat")).To(Succeed())
		_, e := New(nil)
		Expect(e).To(MatchError(ErrInvalidInput))
	})

})
