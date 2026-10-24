// SPDX-License-Identifier: MIT
package ownership

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestOwnership(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Ownership (SQLite only)") }

var _ = Describe("Transactional ownership", func() {
	var m *Manager
	var db *gorm.DB
	var path string
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	key := Key{"alice", "chat"}
	call := ToolCall{"turn", "request", "sha256:abc", 1}
	open := func() (*Manager, *gorm.DB) {
		d, e := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(e).NotTo(HaveOccurred())
		s, e := d.DB()
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close()).To(Succeed()) })
		x, e := New(d)
		Expect(e).NotTo(HaveOccurred())
		return x, d
	}
	acquire := func() Lease {
		l, e := m.Acquire(ctx, key, "one", now, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		return l
	}
	BeforeEach(func() { path = filepath.Join(GinkgoT().TempDir(), "ownership.db"); m, db = open(); _ = db })
	It("grants exactly one of two simultaneous owners across database handles", func() {
		other, _ := open()
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		leases := make([]Lease, 2)
		for i, x := range []*Manager{m, other} {
			wg.Add(1)
			go func(i int, x *Manager) {
				defer wg.Done()
				<-start
				leases[i], errs[i] = x.Acquire(ctx, key, []string{"one", "two"}[i], now, time.Minute)
			}(i, x)
		}
		close(start)
		wg.Wait()
		wins := 0
		for i, e := range errs {
			if e == nil {
				wins++
				Expect(leases[i].Epoch).To(Equal(uint64(1)))
			} else {
				Expect(e).To(MatchError(ErrConflict))
			}
		}
		Expect(wins).To(Equal(1))
	})
	It("persists epochs and never renews or releases expired or replaced authority", func() {
		l := acquire()
		same, e := m.Acquire(ctx, key, "one", now.Add(time.Second), time.Hour)
		Expect(e).NotTo(HaveOccurred())
		Expect(same).To(Equal(l))
		renewed, e := m.Renew(ctx, l, now.Add(time.Second), 2*time.Minute)
		Expect(e).NotTo(HaveOccurred())
		Expect(renewed.ExpiresAt).To(Equal(now.Add(121 * time.Second)))
		Expect(m.Check(ctx, l, now.Add(90*time.Second))).To(Succeed())
		_, e = m.Renew(ctx, renewed, renewed.ExpiresAt, time.Minute)
		Expect(e).To(MatchError(ErrLeaseLost))
		Expect(m.Release(ctx, renewed, renewed.ExpiresAt)).To(MatchError(ErrLeaseLost))
		m, _ = open()
		next, e := m.Acquire(ctx, key, "two", renewed.ExpiresAt, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		Expect(next.Epoch).To(Equal(uint64(2)))
		Expect(m.Check(ctx, l, renewed.ExpiresAt)).To(MatchError(ErrLeaseLost))
		Expect(m.Release(ctx, l, renewed.ExpiresAt)).To(MatchError(ErrLeaseLost))
		Expect(m.Release(ctx, next, renewed.ExpiresAt)).To(Succeed())
		m, _ = open()
		final, e := m.Acquire(ctx, key, "two", renewed.ExpiresAt, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		Expect(final.Epoch).To(Equal(uint64(3)))
	})
	It("persists prepared intent and grants dispatch only once across reopen", func() {
		l := acquire()
		r, e := m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(Prepared))
		again, e := m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(r))
		changed := call
		changed.ArgumentsHash = "changed"
		_, e = m.Prepare(ctx, l, now, changed)
		Expect(e).To(MatchError(ErrConflict))
		changed = call
		changed.Attempt++
		_, e = m.Prepare(ctx, l, now, changed)
		Expect(e).To(MatchError(ErrConflict))
		r, e = m.Dispatch(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(Dispatched))
		m, _ = open()
		_, e = m.Dispatch(ctx, l, now, call)
		Expect(e).To(MatchError(ErrConflict))
		rs, e := m.Records(ctx, l, now, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rs).To(HaveLen(1))
		Expect(rs[0].Status).To(Equal(Dispatched))
	})
	It("recovers only prior dispatched work as Unknown without replay and requires verified reconciliation", func() {
		l := acquire()
		_, e := m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Dispatch(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		prepared := call
		prepared.RequestID = "prepared"
		_, e = m.Prepare(ctx, l, now, prepared)
		Expect(e).NotTo(HaveOccurred())
		done := call
		done.RequestID = "done"
		_, e = m.Prepare(ctx, l, now, done)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Dispatch(ctx, l, now, done)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Complete(ctx, l, now, done, []byte("done"))
		Expect(e).NotTo(HaveOccurred())
		// A live owner's own in-flight intent is not a crash recovery candidate.
		rs, e := m.Recover(ctx, l, now, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rs[2].Status).To(Equal(Dispatched))
		later := now.Add(time.Minute)
		m, _ = open()
		next, e := m.Acquire(ctx, key, "two", later, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Dispatch(ctx, l, later, prepared)
		Expect(e).To(MatchError(ErrLeaseLost))
		_, e = m.Complete(ctx, l, later, call, nil)
		Expect(e).To(MatchError(ErrLeaseLost))
		_, e = m.Reconcile(ctx, l, later, call, "proof", nil)
		Expect(e).To(MatchError(ErrLeaseLost))
		rs, e = m.Recover(ctx, next, later, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rs).To(HaveLen(3))
		Expect(rs[0].Status).To(Equal(Completed))
		Expect(rs[1].Status).To(Equal(Prepared))
		Expect(rs[2].Status).To(Equal(Unknown))
		m, _ = open()
		again, e := m.Recover(ctx, next, later, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(rs))
		_, e = m.Dispatch(ctx, next, later, call)
		Expect(e).To(MatchError(ErrConflict))
		_, e = m.Complete(ctx, next, later, call, nil)
		Expect(e).To(MatchError(ErrConflict))
		changed := call
		changed.Attempt++
		_, e = m.Prepare(ctx, next, later, changed)
		Expect(e).To(MatchError(ErrConflict))
		_, e = m.Reconcile(ctx, next, later, call, "", nil)
		Expect(e).To(MatchError(ErrInvalidInput))
		r, e := m.Reconcile(ctx, next, later, call, "provider-evidence-1", []byte("verified"))
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(Completed))
		Expect(r.ResolutionID).To(Equal("provider-evidence-1"))
		reused, e := m.Prepare(ctx, next, later, call)
		Expect(e).NotTo(HaveOccurred())
		Expect(reused).To(Equal(r))
		_, e = m.Dispatch(ctx, next, later, call)
		Expect(e).To(MatchError(ErrConflict))
	})
	It("scopes leases and every journal lookup by principal and conversation", func() {
		l := acquire()
		_, e := m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Dispatch(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Complete(ctx, l, now, call, []byte("alice"))
		Expect(e).NotTo(HaveOccurred())
		for _, k := range []Key{{"bob", "chat"}, {"alice", "other"}} {
			other, e := m.Acquire(ctx, k, "one", now, time.Minute)
			Expect(e).NotTo(HaveOccurred())
			rows, e := m.Records(ctx, other, now, "turn")
			Expect(e).NotTo(HaveOccurred())
			Expect(rows).To(BeEmpty())
			rows, e = m.Recover(ctx, other, now, "turn")
			Expect(e).NotTo(HaveOccurred())
			Expect(rows).To(BeEmpty())
			_, e = m.Dispatch(ctx, other, now, call)
			Expect(e).To(MatchError(ErrNotFound))
			_, e = m.Complete(ctx, other, now, call, nil)
			Expect(e).To(MatchError(ErrNotFound))
			_, e = m.Reconcile(ctx, other, now, call, "proof", nil)
			Expect(e).To(MatchError(ErrNotFound))
			_, e = m.Prepare(ctx, other, now, call)
			Expect(e).NotTo(HaveOccurred())
		}
		forged := l
		forged.Principal = "mallory"
		Expect(m.Check(ctx, forged, now)).To(MatchError(ErrLeaseLost))
		rows, e := m.Records(ctx, l, now, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rows[0].Result).To(Equal([]byte("alice")))
	})
	It("binds callbacks to immutable hash and attempt and isolates result buffers across reopen", func() {
		l := acquire()
		_, e := m.Dispatch(ctx, l, now, call)
		Expect(e).To(MatchError(ErrNotFound))
		_, e = m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Complete(ctx, l, now, call, nil)
		Expect(e).To(MatchError(ErrConflict))
		_, e = m.Reconcile(ctx, l, now, call, "evidence", nil)
		Expect(e).To(MatchError(ErrConflict))
		_, e = m.Dispatch(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		for _, c := range []ToolCall{{"turn", "request", "wrong", 1}, {"turn", "request", "sha256:abc", 2}} {
			_, e = m.Dispatch(ctx, l, now, c)
			Expect(e).To(MatchError(ErrConflict))
			_, e = m.Complete(ctx, l, now, c, nil)
			Expect(e).To(MatchError(ErrConflict))
			_, e = m.Reconcile(ctx, l, now, c, "proof", nil)
			Expect(e).To(MatchError(ErrConflict))
		}
		data := []byte("result")
		r, e := m.Complete(ctx, l, now, call, data)
		Expect(e).NotTo(HaveOccurred())
		data[0] = 'X'
		r.Result[0] = 'Y'
		m, _ = open()
		r, e = m.Complete(ctx, l, now, call, []byte("result"))
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Result).To(Equal([]byte("result")))
		_, e = m.Complete(ctx, l, now, call, []byte("changed"))
		Expect(e).To(MatchError(ErrConflict))
		rows, e := m.Records(ctx, l, now, "turn")
		Expect(e).NotTo(HaveOccurred())
		rows[0].Result[0] = 'Z'
		r, e = m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Result).To(Equal([]byte("result")))
		empty := call
		empty.RequestID = "empty"
		_, e = m.Prepare(ctx, l, now, empty)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Dispatch(ctx, l, now, empty)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Complete(ctx, l, now, empty, nil)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Complete(ctx, l, now, empty, []byte{})
		Expect(e).NotTo(HaveOccurred())
	})
	It("reuses reconciled results immutably and keeps unknown blocked across further epochs", func() {
		l := acquire()
		_, e := m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Dispatch(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		later := now.Add(time.Minute)
		next, e := m.Acquire(ctx, key, "two", later, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Recover(ctx, next, later, "turn")
		Expect(e).NotTo(HaveOccurred())
		later = later.Add(time.Minute)
		next, e = m.Acquire(ctx, key, "three", later, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		rs, e := m.Recover(ctx, next, later, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rs[0].Status).To(Equal(Unknown))
		_, e = m.Dispatch(ctx, next, later, call)
		Expect(e).To(MatchError(ErrConflict))
		r, e := m.Reconcile(ctx, next, later, call, "verified", nil)
		Expect(e).NotTo(HaveOccurred())
		again, e := m.Reconcile(ctx, next, later, call, "verified", []byte{})
		Expect(e).NotTo(HaveOccurred())
		Expect(again.Status).To(Equal(r.Status))
		_, e = m.Reconcile(ctx, next, later, call, "other", nil)
		Expect(e).To(MatchError(ErrConflict))
		_, e = m.Reconcile(ctx, next, later, call, "verified", []byte("changed"))
		Expect(e).To(MatchError(ErrConflict))
	})
	It("rolls back failed acquisition, intent, completion and multi-row recovery SQL atomically", func() {
		Expect(db.Exec("CREATE TRIGGER fail_lease BEFORE UPDATE OF owner ON runtime_ownership_leases BEGIN SELECT RAISE(ABORT, 'injected acquisition'); END").Error).To(Succeed())
		out, e := m.Acquire(ctx, key, "one", now, time.Minute)
		Expect(e).To(HaveOccurred())
		Expect(out).To(Equal(Lease{}))
		var count int64
		Expect(db.Model(&leaseRow{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
		Expect(db.Exec("DROP TRIGGER fail_lease").Error).To(Succeed())
		l := acquire()
		_, e = m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		Expect(db.Exec("CREATE TRIGGER fail_tool BEFORE UPDATE ON runtime_ownership_tools BEGIN SELECT RAISE(ABORT, 'injected tool'); END").Error).To(Succeed())
		r, e := m.Dispatch(ctx, l, now, call)
		Expect(e).To(HaveOccurred())
		Expect(r).To(Equal(ToolRecord{}))
		rows, e := m.Records(ctx, l, now, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rows[0].Status).To(Equal(Prepared))
		Expect(db.Exec("DROP TRIGGER fail_tool").Error).To(Succeed())
		_, e = m.Dispatch(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		Expect(db.Exec("CREATE TRIGGER fail_complete BEFORE UPDATE ON runtime_ownership_tools WHEN NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'injected completion'); END").Error).To(Succeed())
		r, e = m.Complete(ctx, l, now, call, []byte("result"))
		Expect(e).To(HaveOccurred())
		Expect(r).To(Equal(ToolRecord{}))
		rows, e = m.Records(ctx, l, now, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rows[0].Status).To(Equal(Dispatched))
		Expect(rows[0].Result).To(BeEmpty())
		Expect(db.Exec("DROP TRIGGER fail_complete").Error).To(Succeed())
		second := call
		second.RequestID = "second"
		_, e = m.Prepare(ctx, l, now, second)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Dispatch(ctx, l, now, second)
		Expect(e).NotTo(HaveOccurred())
		later := now.Add(time.Minute)
		next, e := m.Acquire(ctx, key, "two", later, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		Expect(db.Exec("CREATE TRIGGER fail_recover BEFORE UPDATE ON runtime_ownership_tools WHEN NEW.request_id = 'second' BEGIN SELECT RAISE(ABORT, 'injected recovery'); END").Error).To(Succeed())
		rows, e = m.Recover(ctx, next, later, "turn")
		Expect(e).To(HaveOccurred())
		Expect(rows).To(BeNil())
		rows, e = m.Records(ctx, next, later, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2))
		for _, r := range rows {
			Expect(r.Status).To(Equal(Dispatched))
		}
		Expect(db.Exec("DROP TRIGGER fail_recover").Error).To(Succeed())
		rows, e = m.Recover(ctx, next, later, "turn")
		Expect(e).NotTo(HaveOccurred())
		for _, r := range rows {
			Expect(r.Status).To(Equal(Unknown))
		}
	})
	It("serializes duplicate dispatch races and fences callbacks against takeover in the write transaction", func() {
		l := acquire()
		_, e := m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		other, _ := open()
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, x := range []*Manager{m, other} {
			wg.Add(1)
			go func(i int, x *Manager) { defer wg.Done(); <-start; _, errs[i] = x.Dispatch(ctx, l, now, call) }(i, x)
		}
		close(start)
		wg.Wait()
		wins := 0
		for _, e := range errs {
			if e == nil {
				wins++
			} else {
				Expect(e).To(MatchError(ErrConflict))
			}
		}
		Expect(wins).To(Equal(1))
		// Hold a write transaction while the callback enters; take over authority
		// before releasing it. The callback must observe the committed new epoch.
		tx := db.Begin()
		Expect(tx.Error).NotTo(HaveOccurred())
		Expect(scope(tx, key).Model(&leaseRow{}).Updates(map[string]any{"owner": "two", "epoch": 2}).Error).To(Succeed())
		entered := make(chan struct{})
		result := make(chan error, 1)
		go func() { close(entered); _, e := other.Complete(ctx, l, now, call, []byte("late")); result <- e }()
		<-entered
		Expect(tx.Commit().Error).To(Succeed())
		Expect(<-result).To(MatchError(ErrLeaseLost))
		next := l
		next.Owner = "two"
		next.Epoch = 2
		rows, e := m.Records(ctx, next, now, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rows[0].Status).To(Equal(Dispatched))
		Expect(rows[0].Result).To(BeEmpty())
	})
	It("rejects invalid identities, times, TTLs and unsigned numeric overflow before SQL", func() {
		_, e := New(nil)
		Expect(e).To(MatchError(ErrInvalidInput))
		for _, k := range []Key{{"", "chat"}, {" ", "chat"}, {"alice", ""}, {strings.Repeat("a", 513), "chat"}} {
			_, e = m.Acquire(ctx, k, "one", now, time.Minute)
			Expect(e).To(MatchError(ErrInvalidInput))
		}
		for _, ttl := range []time.Duration{0, -1, MaxTTL + 1} {
			_, e = m.Acquire(ctx, key, "one", now, ttl)
			Expect(e).To(MatchError(ErrInvalidInput))
		}
		for _, n := range []time.Time{{}, now.In(time.FixedZone("other", 3600)), time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC)} {
			_, e = m.Acquire(ctx, key, "one", n, time.Minute)
			Expect(e).To(MatchError(ErrInvalidInput))
		}
		_, e = m.Acquire(ctx, key, " ", now, time.Minute)
		Expect(e).To(MatchError(ErrInvalidInput))
		l := acquire()
		for _, epoch := range []uint64{0, uint64(math.MaxInt64) + 1, math.MaxUint64} {
			bad := l
			bad.Epoch = epoch
			Expect(m.Check(ctx, bad, now)).To(MatchError(ErrInvalidInput))
			Expect(m.Release(ctx, bad, now)).To(MatchError(ErrInvalidInput))
			_, e = m.Prepare(ctx, bad, now, call)
			Expect(e).To(MatchError(ErrInvalidInput))
		}
		for _, c := range []ToolCall{{"", "r", "h", 1}, {"t", " ", "h", 1}, {"t", "r", "", 1}, {"t", "r", "h", 0}, {"t", "r", "h", uint64(math.MaxInt64) + 1}, {"t", "r", "h", math.MaxUint64}} {
			_, e = m.Prepare(ctx, l, now, c)
			Expect(e).To(MatchError(ErrInvalidInput))
			_, e = m.Dispatch(ctx, l, now, c)
			Expect(e).To(MatchError(ErrInvalidInput))
			_, e = m.Complete(ctx, l, now, c, nil)
			Expect(e).To(MatchError(ErrInvalidInput))
		}
		_, e = m.Records(ctx, l, now, " ")
		Expect(e).To(MatchError(ErrInvalidInput))
		_, e = m.Recover(ctx, l, now, strings.Repeat("t", 513))
		Expect(e).To(MatchError(ErrInvalidInput))
		// Both positive signed maxima are supported; exhaustion fails explicitly.
		maximum := call
		maximum.Attempt = math.MaxInt64
		_, e = m.Prepare(ctx, l, now, maximum)
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Release(ctx, l, now)).To(Succeed())
		Expect(scope(db, key).Model(&leaseRow{}).UpdateColumn("epoch", int64(math.MaxInt64)).Error).To(Succeed())
		_, e = m.Acquire(ctx, key, "two", now, time.Minute)
		Expect(e).To(MatchError(ErrInvalidInput))
		var row leaseRow
		Expect(scope(db, key).First(&row).Error).To(Succeed())
		Expect(row.Epoch).To(Equal(int64(math.MaxInt64)))
		Expect(row.Owner).To(BeEmpty())
	})

	It("allows new authority to dispatch known-not-dispatched work but rejects all expired journal access", func() {
		l := acquire()
		_, e := m.Prepare(ctx, l, now, call)
		Expect(e).NotTo(HaveOccurred())
		later := l.ExpiresAt
		_, e = m.Prepare(ctx, l, later, call)
		Expect(e).To(MatchError(ErrLeaseLost))
		_, e = m.Dispatch(ctx, l, later, call)
		Expect(e).To(MatchError(ErrLeaseLost))
		_, e = m.Records(ctx, l, later, "turn")
		Expect(e).To(MatchError(ErrLeaseLost))
		_, e = m.Recover(ctx, l, later, "turn")
		Expect(e).To(MatchError(ErrLeaseLost))
		next, e := m.Acquire(ctx, key, "two", later, MaxTTL)
		Expect(e).NotTo(HaveOccurred())
		rows, e := m.Recover(ctx, next, later, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rows[0].Status).To(Equal(Prepared))
		_, e = m.Dispatch(ctx, next, later, call)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Complete(ctx, next, later, call, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Release(ctx, next, later)).To(Succeed())
		Expect(scope(db, key).Model(&leaseRow{}).UpdateColumn("epoch", int64(math.MaxInt64-1)).Error).To(Succeed())
		last, e := m.Acquire(ctx, key, "three", later, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		Expect(last.Epoch).To(Equal(uint64(math.MaxInt64)))
		Expect(m.Check(ctx, last, later)).To(Succeed())
	})

})
