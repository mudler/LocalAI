// SPDX-License-Identifier: MIT
package ownership

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Host execution persistence", func() {
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	var m *Manager
	var db *gorm.DB
	var s *store.Store
	var lease Lease
	var path string
	var metadata ExecutionMetadata
	open := func() (*Manager, *store.Store, *gorm.DB) {
		d, e := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(e).NotTo(HaveOccurred())
		p, e := d.DB()
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(p.Close()).To(Succeed()) })
		mm, e := New(d)
		Expect(e).NotTo(HaveOccurred())
		ss, e := store.New(d)
		Expect(e).NotTo(HaveOccurred())
		return mm, ss, d
	}
	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "executions.db")
		m, s, db = open()
		Expect(s.Create(ctx, "alice", "chat")).To(Succeed())
		_, e := s.Admit(ctx, "alice", "chat", "active", []byte("input"))
		Expect(e).NotTo(HaveOccurred())
		lease, e = m.Acquire(ctx, Key{"alice", "chat"}, "host", now, time.Hour)
		Expect(e).NotTo(HaveOccurred())
		metadata, e = NewExecutionMetadata("active", "invocation", "protocol", "helper", "registration", "agent", "tool", 1, 1, [7]string{"catalog", "config", "authorization", "resource", "proposal", "parent", "taint"}, 0, time.Minute, false, 1, strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64))
		Expect(e).NotTo(HaveOccurred())
	})
	It("durably admits a complete immutable row and converges after reopen", func() {
		a, e := m.AdmitExecution(ctx, lease, now, metadata)
		Expect(e).NotTo(HaveOccurred())
		Expect(a.RequestID()).To(HavePrefix("he1_"))
		Expect(a.Digest()).To(HaveLen(64))
		other, _, _ := open()
		b, e := other.AdmitExecution(ctx, lease, now, metadata)
		Expect(e).NotTo(HaveOccurred())
		Expect(b.RequestID()).To(Equal(a.RequestID()))
		Expect(b.Digest()).To(Equal(a.Digest()))
		var count int64
		Expect(db.Table("runtime_execution_admissions").Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
	})

	admit := func() ExecutionAdmission {
		a, e := m.AdmitExecution(ctx, lease, now, metadata)
		Expect(e).NotTo(HaveOccurred())
		return a
	}
	prepared := func(a ExecutionAdmission) {
		_, e := m.Prepare(ctx, lease, now, a.Call())
		Expect(e).NotTo(HaveOccurred())
	}
	unavailable := func(a ExecutionAdmission) {
		out, e := m.ReadCompletedExecution(ctx, lease, now, "active", 1, a)
		Expect(e).To(MatchError(ErrExecutionUnavailable))
		Expect(out.Bytes()).To(BeNil())
	}
	It("converges concurrent exact admission across independent pools to one row ID and digest", func() {
		other, _, _ := open()
		var wg sync.WaitGroup
		start := make(chan struct{})
		out := make([]ExecutionAdmission, 2)
		errs := make([]error, 2)
		for i, x := range []*Manager{m, other} {
			wg.Add(1)
			go func(i int, x *Manager) {
				defer wg.Done()
				<-start
				out[i], errs[i] = x.AdmitExecution(ctx, lease, now, metadata)
			}(i, x)
		}
		close(start)
		wg.Wait()
		Expect(errs).To(Equal([]error{nil, nil}))
		Expect(out[0].RequestID()).To(Equal(out[1].RequestID()))
		Expect(out[0].Digest()).To(Equal(out[1].Digest()))
		var count int64
		Expect(db.Table("runtime_execution_admissions").Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
	})
	It("matches an independent typed tuple identity oracle", func() {
		a := admit()
		Expect(a.RequestID()).To(Equal("he1_" + func() string {
			b, _ := json.Marshal([]any{"host-execution-request/v1", "alice", "chat", "active", uint64(1), uint64(1), "invocation"})
			h := sha256.Sum256(b)
			return hex.EncodeToString(h[:])
		}()))
	})
	for _, field := range []string{"Protocol", "Helper", "Registration", "Agent", "Tool", "Catalog", "Config", "Authorization", "Resource", "Proposal", "ParentGrant", "Taint", "Mode", "TTL", "Tainted", "Codec", "Arguments", "Binding", "Provenance", "Revision"} {
		It("conflicts or rejects immutable mutation of "+field, func() {
			a := admit()
			f := metadata.open()
			v := reflect.ValueOf(&f).Elem().FieldByName(field)
			switch v.Kind() {
			case reflect.String:
				if v.Len() == 64 {
					v.SetString(strings.Repeat("d", 64))
				} else {
					v.SetString(v.String() + "2")
				}
			case reflect.Uint8, reflect.Uint64:
				v.SetUint(v.Uint() + 1)
			case reflect.Int64:
				v.SetInt(v.Int() + 1)
			case reflect.Bool:
				v.SetBool(!v.Bool())
			}
			changed := ExecutionMetadata{open: func() executionFields { return f }}
			out, e := m.AdmitExecution(ctx, lease, now, changed)
			Expect(e).To(HaveOccurred())
			Expect(out.open).To(BeNil())
			same, e := m.AdmitExecution(ctx, lease, now, metadata)
			Expect(e).NotTo(HaveOccurred())
			Expect(same.Digest()).To(Equal(a.Digest()))
		})
	}
	It("revalidates every persisted immutable field before admitting or dispatching", func() {
		a := admit()
		prepared(a)
		original := a.open()
		typ := reflect.TypeOf(original)
		for i := 0; i < typ.NumField(); i++ {
			r := original
			v := reflect.ValueOf(&r).Elem().Field(i)
			switch v.Kind() {
			case reflect.String:
				v.SetString(v.String() + "x")
			case reflect.Int64:
				v.SetInt(v.Int() + 1)
			case reflect.Uint8:
				v.SetUint(v.Uint() + 1)
			case reflect.Bool:
				v.SetBool(!v.Bool())
			}
			Expect(db.Table("runtime_execution_admissions").Where("request_id = ?", original.RequestID).Delete(nil).Error).To(Succeed())
			Expect(db.Create(&r).Error).To(Succeed())
			out, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
			Expect(e).To(HaveOccurred(), typ.Field(i).Name)
			Expect(out.open).To(BeNil())
			out, e = m.AdmitExecution(ctx, lease, now, metadata)
			// Moving a key to a different scope removes the original reservation;
			// a new admission there is valid, but cannot hide the failed dispatch.
			if typ.Field(i).Name != "Principal" && typ.Field(i).Name != "Conversation" && typ.Field(i).Name != "Turn" {
				Expect(e).To(HaveOccurred(), typ.Field(i).Name)
				Expect(out.open).To(BeNil())
			}

			Expect(db.Table("runtime_execution_admissions").Where("1=1").Delete(nil).Error).To(Succeed())
			Expect(db.Create(&original).Error).To(Succeed())
		}
	})
	It("separates regenerated invocations despite identical protocol and argument digests", func() {
		a := admit()
		f := metadata.open()
		f.Invocation += "2"
		b, e := m.AdmitExecution(ctx, lease, now, ExecutionMetadata{open: func() executionFields { return f }})
		Expect(e).NotTo(HaveOccurred())
		Expect(a.RequestID()).NotTo(Equal(b.RequestID()))
		Expect(a.Digest()).NotTo(Equal(b.Digest()))
	})
	It("does not guess origin identities after a uniqueness witness conflicts", func() {
		a := admit()
		Expect(db.Table("runtime_execution_admissions").Where("request_id = ?", a.RequestID()).Update("request_id", "he1_forged").Error).To(Succeed())
		out, e := m.AdmitExecution(ctx, lease, now, metadata)
		Expect(e).To(MatchError(ErrConflict))
		Expect(out.open).To(BeNil())
	})
	for _, kind := range []string{"owner", "epoch", "persisted expiry", "released"} {
		It("rejects "+kind+" authority for all operations", func() {
			a := admit()
			prepared(a)
			l := lease
			switch kind {
			case "owner":
				l.Owner = "other"
			case "epoch":
				l.Epoch++
			case "persisted expiry":
				Expect(db.Table("runtime_ownership_leases").Where("principal = ?", "alice").Update("expires", now.UnixNano()).Error).To(Succeed())
			case "released":
				Expect(m.Release(ctx, l, now)).To(Succeed())
			}
			out, e := m.AdmitExecution(ctx, l, now, metadata)
			Expect(e).To(MatchError(ErrLeaseLost))
			Expect(out.open).To(BeNil())
			out, e = m.DispatchAdmittedExecution(ctx, l, now, 1, a)
			Expect(e).To(MatchError(ErrLeaseLost))
			Expect(out.open).To(BeNil())
			result, e := m.ReadCompletedExecution(ctx, l, now, "active", 1, a)
			Expect(e).To(MatchError(ErrLeaseLost))
			Expect(result.Bytes()).To(BeNil())
		})
	}
	for _, kind := range []string{"tombstone", "revision", "turn", "missing input", "settled input", "ordinal overflow", "sequence overflow", "revision overflow"} {
		It("rejects live target with "+kind+" in every seam", func() {
			a := admit()
			prepared(a)
			switch kind {
			case "tombstone":
				Expect(s.Delete(ctx, "alice", "chat")).To(Succeed())
			case "revision":
				Expect(db.Exec("UPDATE runtime_conversations SET revision=2").Error).To(Succeed())
			case "turn":
				Expect(db.Exec("UPDATE runtime_conversations SET running_turn='other'").Error).To(Succeed())
			case "missing input":
				Expect(db.Exec("DELETE FROM runtime_inputs").Error).To(Succeed())
			case "settled input":
				Expect(db.Exec("UPDATE runtime_inputs SET settled=1").Error).To(Succeed())
			case "ordinal overflow":
				Expect(db.Exec("UPDATE runtime_conversations SET last_ordinal=-1").Error).To(Succeed())
			case "sequence overflow":
				Expect(db.Exec("UPDATE runtime_conversations SET last_sequence=-1").Error).To(Succeed())
			case "revision overflow":
				Expect(db.Exec("UPDATE runtime_conversations SET revision=-1").Error).To(Succeed())
			}
			out, e := m.AdmitExecution(ctx, lease, now, metadata)
			Expect(e).To(HaveOccurred())
			Expect(out.open).To(BeNil())
			out, e = m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
			Expect(e).To(HaveOccurred())
			Expect(out.open).To(BeNil())
			r, e := m.ReadCompletedExecution(ctx, lease, now, "active", 1, a)
			Expect(e).To(HaveOccurred())
			Expect(r.Bytes()).To(BeNil())
		})
	}
	It("validates bounded identifiers numeric policy digest and codec domains", func() {
		for _, mutate := range []func(*executionFields){func(f *executionFields) { f.Invocation = strings.Repeat("x", 513) }, func(f *executionFields) { f.Invocation = "\x00" }, func(f *executionFields) { f.Invocation = string([]byte{255}) }, func(f *executionFields) { f.Attempt = 0 }, func(f *executionFields) { f.Attempt = math.MaxUint64 }, func(f *executionFields) { f.Revision = 0 }, func(f *executionFields) { f.Revision = math.MaxUint64 }, func(f *executionFields) { f.Mode = 4 }, func(f *executionFields) { f.TTL = 0 }, func(f *executionFields) { f.TTL = int64(MaxTTL) + 1 }, func(f *executionFields) { f.Codec = 2 }, func(f *executionFields) { f.Binding = strings.Repeat("G", 64) }, func(f *executionFields) { f.Arguments = "a" }, func(f *executionFields) { f.Provenance = strings.Repeat("A", 64) }} {
			f := metadata.open()
			mutate(&f)
			out, e := m.AdmitExecution(ctx, lease, now, ExecutionMetadata{open: func() executionFields { return f }})
			Expect(e).To(MatchError(ErrInvalidInput))
			Expect(out.open).To(BeNil())
		}
		l := lease
		l.Epoch = math.MaxUint64
		out, e := m.AdmitExecution(ctx, l, now, metadata)
		Expect(e).To(MatchError(ErrInvalidInput))
		Expect(out.open).To(BeNil())
	})
	It("accepts exact maximum identity numeric and TTL metadata domains", func() {
		f := metadata.open()
		f.Invocation = strings.Repeat("x", 512)
		f.Attempt = math.MaxInt64
		f.TTL = int64(MaxTTL)
		out, e := m.AdmitExecution(ctx, lease, now, ExecutionMetadata{open: func() executionFields { return f }})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.RequestID()).To(HaveLen(68))
	})
	It("redacts opaque values through all fmt verbs private containers and JSON", func() {
		a := admit()
		result := CompletedExecution{open: func() string { return "raw-private-result-sentinel" }}
		for _, value := range []any{metadata, a, result, struct{ private any }{metadata}, struct{ private any }{a}, struct{ private any }{result}} {
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
				text := fmt.Sprintf(verb, value)
				for _, secret := range []string{"alice", "invocation", "raw-private-result-sentinel", "registration"} {
					Expect(text).NotTo(ContainSubstring(secret))
				}
			}
			b, e := json.Marshal(value)
			Expect(e).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("sentinel"))
		}
	})
	It("sanitizes injected SQL errors and rolls back admission", func() {
		Expect(db.Exec("CREATE TRIGGER fail_admit BEFORE INSERT ON runtime_execution_admissions BEGIN SELECT RAISE(ABORT,'private SQL sentinel'); END").Error).To(Succeed())
		out, e := m.AdmitExecution(ctx, lease, now, metadata)
		Expect(e).To(MatchError(errExecution))
		Expect(out.open).To(BeNil())
		var count int64
		Expect(db.Table("runtime_execution_admissions").Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})
	It("returns zero after actual outer COMMIT failure", func() {
		pool, e := db.DB()
		Expect(e).NotTo(HaveOccurred())
		pool.SetMaxOpenConns(1)
		Expect(db.Exec("PRAGMA foreign_keys=ON").Error).To(Succeed())
		Expect(db.Exec("CREATE TABLE execution_parent(id INTEGER PRIMARY KEY)").Error).To(Succeed())
		Expect(db.Exec("CREATE TABLE execution_child(id INTEGER REFERENCES execution_parent(id) DEFERRABLE INITIALLY DEFERRED)").Error).To(Succeed())
		Expect(db.Exec("CREATE TRIGGER fail_execution_commit AFTER INSERT ON runtime_execution_admissions BEGIN INSERT INTO execution_child VALUES(1); END").Error).To(Succeed())
		calls := 0
		Expect(db.Callback().Create().After("gorm:create").Register("observe_execution_commit", func(tx *gorm.DB) {
			if tx.Statement.Table == "runtime_execution_admissions" && tx.Error == nil {
				calls++
			}
		})).To(Succeed())
		DeferCleanup(func() { Expect(db.Callback().Create().Remove("observe_execution_commit")).To(Succeed()) })
		out, e := m.AdmitExecution(ctx, lease, now, metadata)
		Expect(e).To(MatchError(errExecution))
		Expect(out.open).To(BeNil())
		Expect(calls).To(Equal(1))
		_, _, fresh := open()
		var count int64
		Expect(fresh.Table("runtime_execution_admissions").Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})
	It("normalizes automatic rollback winning COMMIT to context cancellation", func() {
		canceled, cancel := context.WithCancel(ctx)
		defer cancel()
		calls := 0
		Expect(db.Callback().Query().After("gorm:query").Register("execution_cancel", func(tx *gorm.DB) {
			if tx.Statement.Table != "runtime_execution_admissions" || tx.Error != nil {
				return
			}
			calls++
			cancel()
			transaction, ok := tx.Statement.ConnPool.(*sql.Tx)
			Expect(ok).To(BeTrue())
			Eventually(func() error { _, e := transaction.ExecContext(ctx, "SELECT 1"); return e }, time.Second).Should(MatchError(sql.ErrTxDone))
		})).To(Succeed())
		DeferCleanup(func() { Expect(db.Callback().Query().Remove("execution_cancel")).To(Succeed()) })
		out, e := m.AdmitExecution(canceled, lease, now, metadata)
		Expect(e).To(MatchError(context.Canceled))
		Expect(out.open).To(BeNil())
		Expect(calls).To(Equal(1))
		_, _, fresh := open()
		var count int64
		Expect(fresh.Table("runtime_execution_admissions").Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})
	It("preserves unrelated failures despite simultaneous cancellation", func() {
		canceled, cancel := context.WithCancel(ctx)
		defer cancel()
		Expect(db.Callback().Create().After("gorm:create").Register("execution_unrelated", func(tx *gorm.DB) {
			if tx.Statement.Table == "runtime_execution_admissions" {
				cancel()
				tx.AddError(errors.New("private unrelated"))
			}
		})).To(Succeed())
		DeferCleanup(func() { Expect(db.Callback().Create().Remove("execution_unrelated")).To(Succeed()) })
		out, e := m.AdmitExecution(canceled, lease, now, metadata)
		Expect(e).To(MatchError(errExecution))
		Expect(out.open).To(BeNil())
	})
	for _, status := range []ToolStatus{Prepared, Dispatched, Unknown} {
		It("never returns private result bytes for "+string(status), func() {
			a := admit()
			prepared(a)
			Expect(db.Table("runtime_ownership_tools").Where("request_id = ?", a.RequestID()).Updates(map[string]any{"status": status, "result": []byte("raw-private-result-sentinel")}).Error).To(Succeed())
			unavailable(a)
		})
	}
	It("returns only a durable sealed completed envelope without decoding", func() {
		a := admit()
		prepared(a)
		_, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
		Expect(e).NotTo(HaveOccurred())
		envelope := []byte(`{"version":1,"admission":"` + a.Digest() + `","provenance":"` + strings.Repeat("c", 64) + `","projection":"` + strings.Repeat("e", 64) + `","text":"authorized projected text"}`)
		_, e = m.Complete(ctx, lease, now, a.Call(), envelope)
		Expect(e).NotTo(HaveOccurred())
		out, e := m.ReadCompletedExecution(ctx, lease, now, "active", 1, a)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Bytes()).To(Equal(envelope))
		copy := out.Bytes()
		copy[0] = 'x'
		Expect(out.Bytes()).To(Equal(envelope))
	})
	It("reads exact completed origin across attempts epochs and target revision after reopen", func() {
		a := admit()
		prepared(a)
		_, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Complete(ctx, lease, now, a.Call(), []byte("sealed-envelope"))
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Release(ctx, lease, now)).To(Succeed())
		other, _, _ := open()
		current, e := other.Acquire(ctx, lease.Key, "next", now, time.Hour)
		Expect(e).NotTo(HaveOccurred())
		Expect(db.Exec("UPDATE runtime_conversations SET revision=2").Error).To(Succeed())
		out, e := other.ReadCompletedExecution(ctx, current, now, "active", 2, a)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(out.Bytes())).To(Equal("sealed-envelope"))
		denied, e := other.DispatchAdmittedExecution(ctx, current, now, 2, a)
		Expect(e).To(HaveOccurred())
		Expect(denied.open).To(BeNil())
	})
	It("requires exact origin scope attempt epoch binding and completion dispatch epoch", func() {
		a := admit()
		prepared(a)
		_, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Complete(ctx, lease, now, a.Call(), []byte("sealed"))
		Expect(e).NotTo(HaveOccurred())
		for _, mutate := range []func(*executionRow){func(r *executionRow) { r.Principal = "other" }, func(r *executionRow) { r.Conversation = "other" }, func(r *executionRow) { r.Turn = "other" }, func(r *executionRow) { r.Attempt++ }, func(r *executionRow) { r.Epoch++ }, func(r *executionRow) { r.Binding = strings.Repeat("d", 64) }, func(r *executionRow) { r.RequestID = "he1_forged" }} {
			r := a.open()
			mutate(&r)
			unavailable(ownExecution(r))
		}
		Expect(db.Table("runtime_ownership_tools").Where("request_id = ?", a.RequestID()).Update("dispatch_epoch", 2).Error).To(Succeed())
		unavailable(a)
	})
	It("uses one unavailable error for absent mismatched and zero origin", func() {
		a := admit()
		unavailable(a)
		unavailable(ExecutionAdmission{})
		prepared(a)
		Expect(db.Table("runtime_ownership_tools").Where("request_id = ?", a.RequestID()).Updates(map[string]any{"status": Completed, "arguments_hash": "wrong", "result": []byte("raw-private-result-sentinel")}).Error).To(Succeed())
		unavailable(a)
	})
	for _, status := range []ToolStatus{Dispatched, Unknown} {
		It("atomically blocks legacy prior attempt and epoch "+string(status)+" without admission", func() {
			a := admit()
			prepared(a)
			Expect(db.Create(&toolRow{Principal: "alice", Conversation: "chat", TurnID: "active", RequestID: "legacy", ArgumentsHash: "legacy", Attempt: 7, DispatchEpoch: 7, Status: status, Result: []byte("private")}).Error).To(Succeed())
			out, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
			Expect(e).To(MatchError(ErrConflict))
			Expect(out.open).To(BeNil())
			r, e := lookup(db, lease.Key, a.Call())
			Expect(e).NotTo(HaveOccurred())
			Expect(r.Status).To(Equal(Prepared))
		})
	}
	It("allows other prepared completed and other-turn unresolved journal rows", func() {
		a := admit()
		prepared(a)
		for i, status := range []ToolStatus{Prepared, Completed, Unknown} {
			turn := "active"
			if status == Unknown {
				turn = "old"
			}
			Expect(db.Create(&toolRow{Principal: "alice", Conversation: "chat", TurnID: turn, RequestID: fmt.Sprint(i), Status: status}).Error).To(Succeed())
		}
		_, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
		Expect(e).NotTo(HaveOccurred())
		out, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
		Expect(e).To(MatchError(ErrConflict))
		Expect(out.open).To(BeNil())
	})
	It("serializes two distinct fresh dispatches to exactly one winner across pools", func() {
		a := admit()
		f := metadata.open()
		f.Invocation = "second"
		b, e := m.AdmitExecution(ctx, lease, now, ExecutionMetadata{open: func() executionFields { return f }})
		Expect(e).NotTo(HaveOccurred())
		prepared(a)
		prepared(b)
		other, _, _ := open()
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		out := make([]ExecutionAdmission, 2)
		for i, x := range []*Manager{m, other} {
			wg.Add(1)
			go func(i int, x *Manager) {
				defer wg.Done()
				<-start
				out[i], errs[i] = x.DispatchAdmittedExecution(ctx, lease, now, 1, []ExecutionAdmission{a, b}[i])
			}(i, x)
		}
		close(start)
		wg.Wait()
		wins := 0
		for i, e := range errs {
			if e == nil {
				wins++
			} else {
				Expect(e).To(MatchError(ErrConflict))
				Expect(out[i].open).To(BeNil())
			}
		}
		Expect(wins).To(Equal(1))
	})
	It("rolls back failed dispatch journal update and returns zero", func() {
		a := admit()
		prepared(a)
		Expect(db.Exec("CREATE TRIGGER fail_execution_dispatch BEFORE UPDATE ON runtime_ownership_tools BEGIN SELECT RAISE(ABORT,'private dispatch sentinel'); END").Error).To(Succeed())
		out, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
		Expect(e).To(MatchError(errExecution))
		Expect(out.open).To(BeNil())
		r, e := lookup(db, lease.Key, a.Call())
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(Prepared))
	})
	It("reconstructs an explicit exact origin without retaining the admission object", func() {
		a := admit()
		prepared(a)
		_, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Complete(ctx, lease, now, a.Call(), []byte("sealed projection envelope"))
		Expect(e).NotTo(HaveOccurred())
		other, _, _ := open()
		ref, e := ExactExecutionOrigin(lease, metadata, lease.Epoch)
		Expect(e).NotTo(HaveOccurred())
		out, e := other.ReadCompletedExecution(ctx, lease, now, "active", 1, ref)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(out.Bytes())).To(Equal("sealed projection envelope"))
	})
	for _, operation := range []string{"dispatch", "read"} {
		It("returns zero on "+operation+" outer COMMIT failure", func() {
			a := admit()
			prepared(a)
			if operation == "read" {
				_, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
				Expect(e).NotTo(HaveOccurred())
				_, e = m.Complete(ctx, lease, now, a.Call(), []byte("raw-private-result-sentinel"))
				Expect(e).NotTo(HaveOccurred())
			}
			pool, e := db.DB()
			Expect(e).NotTo(HaveOccurred())
			pool.SetMaxOpenConns(1)
			Expect(db.Exec("PRAGMA foreign_keys=ON").Error).To(Succeed())
			Expect(db.Exec("CREATE TABLE execution_parent(id INTEGER PRIMARY KEY)").Error).To(Succeed())
			Expect(db.Exec("CREATE TABLE execution_child(id INTEGER REFERENCES execution_parent(id) DEFERRABLE INITIALLY DEFERRED)").Error).To(Succeed())
			table := "runtime_conversations"
			if operation == "dispatch" {
				table = "runtime_ownership_tools"
			}
			Expect(db.Exec("CREATE TRIGGER fail_execution_commit AFTER UPDATE ON " + table + " BEGIN INSERT INTO execution_child VALUES(1); END").Error).To(Succeed())
			if operation == "dispatch" {
				out, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
				Expect(e).To(MatchError(errExecution))
				Expect(out.open).To(BeNil())
				r, e := lookup(db, lease.Key, a.Call())
				Expect(e).NotTo(HaveOccurred())
				Expect(r.Status).To(Equal(Prepared))
			} else {
				out, e := m.ReadCompletedExecution(ctx, lease, now, "active", 1, a)
				Expect(e).To(MatchError(errExecution))
				Expect(out.Bytes()).To(BeNil())
			}
		})
		It("returns zero when "+operation+" context rollback wins COMMIT", func() {
			a := admit()
			prepared(a)
			if operation == "read" {
				_, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
				Expect(e).NotTo(HaveOccurred())
				_, e = m.Complete(ctx, lease, now, a.Call(), []byte("raw-private-result-sentinel"))
				Expect(e).NotTo(HaveOccurred())
			}
			canceled, cancel := context.WithCancel(ctx)
			defer cancel()
			wait := func(tx *gorm.DB) {
				cancel()
				transaction, ok := tx.Statement.ConnPool.(*sql.Tx)
				Expect(ok).To(BeTrue())
				Eventually(func() error { _, e := transaction.ExecContext(ctx, "SELECT 1"); return e }, time.Second).Should(MatchError(sql.ErrTxDone))
			}
			if operation == "dispatch" {
				Expect(db.Callback().Update().After("gorm:update").Register("execution_cancel_dispatch", func(tx *gorm.DB) {
					if tx.Statement.Table == "runtime_ownership_tools" && tx.Error == nil {
						wait(tx)
					}
				})).To(Succeed())
				DeferCleanup(func() { Expect(db.Callback().Update().Remove("execution_cancel_dispatch")).To(Succeed()) })
				out, e := m.DispatchAdmittedExecution(canceled, lease, now, 1, a)
				Expect(e).To(MatchError(context.Canceled))
				Expect(out.open).To(BeNil())
			} else {
				Expect(db.Callback().Query().After("gorm:query").Register("execution_cancel_read", func(tx *gorm.DB) {
					if tx.Statement.Table == "runtime_ownership_tools" && tx.Error == nil {
						wait(tx)
					}
				})).To(Succeed())
				DeferCleanup(func() { Expect(db.Callback().Query().Remove("execution_cancel_read")).To(Succeed()) })
				out, e := m.ReadCompletedExecution(canceled, lease, now, "active", 1, a)
				Expect(e).To(MatchError(context.Canceled))
				Expect(out.Bytes()).To(BeNil())
			}
		})
	}
	It("does not grant dispatch before preparation or change legacy Dispatch serialization", func() {
		a := admit()
		out, e := m.DispatchAdmittedExecution(ctx, lease, now, 1, a)
		Expect(e).To(MatchError(ErrNotFound))
		Expect(out.open).To(BeNil())
		prepared(a)
		legacy := ToolCall{"active", "legacy", "legacy", 9}
		_, e = m.Prepare(ctx, lease, now, legacy)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Dispatch(ctx, lease, now, legacy)
		Expect(e).NotTo(HaveOccurred())
		_, e = m.Dispatch(ctx, lease, now, a.Call())
		Expect(e).NotTo(HaveOccurred())
	})

	It("does not mask input errors with canceled contexts", func() {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		out, e := m.AdmitExecution(canceled, Lease{}, now, metadata)
		Expect(e).To(MatchError(ErrInvalidInput))
		Expect(out.open).To(BeNil())
	})
	It("normalizes expired deadline without returning any candidate", func() {
		expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		defer cancel()
		a := admit()
		prepared(a)
		out, e := m.AdmitExecution(expired, lease, now, metadata)
		Expect(e).To(MatchError(context.DeadlineExceeded))
		Expect(out.open).To(BeNil())
		out, e = m.DispatchAdmittedExecution(expired, lease, now, 1, a)
		Expect(e).To(MatchError(context.DeadlineExceeded))
		Expect(out.open).To(BeNil())
		r, e := m.ReadCompletedExecution(expired, lease, now, "active", 1, a)
		Expect(e).To(MatchError(context.DeadlineExceeded))
		Expect(r.Bytes()).To(BeNil())
	})
	It("rejects a journal attempt mismatch even for a completed binding", func() {
		a := admit()
		prepared(a)
		Expect(db.Table("runtime_ownership_tools").Where("request_id = ?", a.RequestID()).Updates(map[string]any{"status": Completed, "attempt": 2, "dispatch_epoch": 1, "result": []byte("raw-private-result-sentinel")}).Error).To(Succeed())
		unavailable(a)
	})
	It("ignores stale client expiry hints but honors persisted renewal", func() {
		a := admit()
		lease.ExpiresAt = now.Add(-time.Hour)
		_, e := m.Renew(ctx, lease, now, 2*time.Hour)
		Expect(e).NotTo(HaveOccurred())
		out, e := m.AdmitExecution(ctx, lease, now.Add(90*time.Minute), metadata)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Digest()).To(Equal(a.Digest()))
	})

	It("elects one immutable witness for conflicting concurrent admission", func() {
		other, _, _ := open()
		f := metadata.open()
		f.Protocol = "conflicting"
		changed := ExecutionMetadata{open: func() executionFields { return f }}
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		out := make([]ExecutionAdmission, 2)
		for i, x := range []*Manager{m, other} {
			wg.Add(1)
			go func(i int, x *Manager) {
				defer wg.Done()
				<-start
				out[i], errs[i] = x.AdmitExecution(ctx, lease, now, []ExecutionMetadata{metadata, changed}[i])
			}(i, x)
		}
		close(start)
		wg.Wait()
		wins := 0
		for i, e := range errs {
			if e == nil {
				wins++
				witness, e := m.AdmitExecution(ctx, lease, now, []ExecutionMetadata{metadata, changed}[i])
				Expect(e).NotTo(HaveOccurred())
				Expect(witness.Digest()).To(Equal(out[i].Digest()))
			} else {
				Expect(e).To(MatchError(ErrConflict))
				Expect(out[i].open).To(BeNil())
			}
		}
		Expect(wins).To(Equal(1))
		var count int64
		Expect(db.Table("runtime_execution_admissions").Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
	})

})
