// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"math"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Store numeric domain", func() {
	ctx := context.Background()
	var s *Store
	var db *gorm.DB
	var path string
	open := func() {
		var err error
		db, err = gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).NotTo(HaveOccurred())
		sql, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(sql.Close()).To(Succeed()) })
		s, err = New(db)
		Expect(err).NotTo(HaveOccurred())
	}
	snapshot := func() Snapshot {
		v, err := s.Get(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		return v
	}
	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "numeric.db")
		open()
		Expect(s.Create(ctx, "alice", "chat")).To(Succeed())
		_, err := s.Admit(ctx, "alice", "chat", "a", []byte("a"))
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Admit(ctx, "alice", "chat", "b", []byte("b"))
		Expect(err).NotTo(HaveOccurred())
	})
	It("returns empty terminal event pages only after identity, ownership and tombstone checks", func() {
		v := snapshot()
		_, err := s.Commit(ctx, "alice", "chat", v.Revision, "a", Checkpoint{}, []Event{{Kind: "done"}})
		Expect(err).NotTo(HaveOccurred())
		for _, after := range []uint64{math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64} {
			events, err := s.Events(ctx, "alice", "chat", after, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(BeEmpty())
			_, err = s.Events(ctx, "bob", "chat", after, 10)
			Expect(err).To(MatchError(ErrNotFound))
			_, err = s.Events(ctx, "alice", "missing", after, 10)
			Expect(err).To(MatchError(ErrNotFound))
			_, err = s.Events(ctx, " ", "chat", after, 10)
			Expect(err).To(MatchError(ErrInvalidInput))
			_, err = s.Events(ctx, "alice", "chat", after, 0)
			Expect(err).To(MatchError(ErrInvalidInput))
		}
		Expect(s.Delete(ctx, "alice", "chat")).To(Succeed())
		for _, after := range []uint64{math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64} {
			_, err = s.Events(ctx, "alice", "chat", after, 10)
			Expect(err).To(MatchError(ErrDeleted))
			_, err = s.Events(ctx, "bob", "chat", after, 10)
			Expect(err).To(MatchError(ErrNotFound))
		}
	})
	It("rejects high-bit checkpoint versions before SQL without changing any records", func() {
		before := snapshot()
		for _, version := range []uint64{math.MaxInt64 + 1, math.MaxUint64} {
			_, err := s.Commit(ctx, "alice", "chat", before.Revision, "a", Checkpoint{Version: version, Context: []byte("new")}, []Event{{Kind: "done"}})
			Expect(err).To(MatchError(ErrInvalidInput))
			Expect(snapshot()).To(Equal(before))
			// A canceled context would fail on any SQL; numeric validation must precede it.
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = s.Commit(canceled, "alice", "chat", before.Revision, "a", Checkpoint{Version: version}, nil)
			Expect(err).To(MatchError(ErrInvalidInput))
		}
		events, err := s.Events(ctx, "alice", "chat", 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
	})
	It("round trips MaxInt64 checkpoint versions after reopen", func() {
		v := snapshot()
		cp := Checkpoint{Version: math.MaxInt64, Context: []byte("boundary")}
		_, err := s.Commit(ctx, "alice", "chat", v.Revision, "a", cp, nil)
		Expect(err).NotTo(HaveOccurred())
		open()
		Expect(snapshot().Checkpoint).To(Equal(cp))
	})
	It("treats unrepresentable expected revisions as conflicts after scoped lookup", func() {
		before := snapshot()
		for _, rev := range []uint64{math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64} {
			_, err := s.Commit(ctx, "alice", "chat", rev, "a", Checkpoint{}, nil)
			Expect(err).To(MatchError(ErrConflict))
			_, err = s.Commit(ctx, "bob", "chat", rev, "a", Checkpoint{}, nil)
			Expect(err).To(MatchError(ErrNotFound))
			Expect(snapshot()).To(Equal(before))
		}
		Expect(s.Delete(ctx, "alice", "chat")).To(Succeed())
		_, err := s.Commit(ctx, "alice", "chat", math.MaxUint64, "a", Checkpoint{}, nil)
		Expect(err).To(MatchError(ErrDeleted))
	})
	// Bounded existing-record fixtures exercise counters without billions of writes.
	for _, tc := range []struct{ name, column, operation string }{
		{"admission revision", "revision", "admit"},
		{"admission ordinal", "last_ordinal", "admit"},
		{"settlement revision", "revision", "commit"},
		{"outbox sequence", "last_sequence", "commit"},
		{"deletion revision", "revision", "delete"},
	} {
		It("fails closed at exhausted "+tc.name+" without changing records", func() {
			Expect(scope(db.Model(&conversationRow{}), "alice", "chat").Update(tc.column, uint64(math.MaxInt64)).Error).To(Succeed())
			before := snapshot()
			var rowBefore conversationRow
			Expect(scope(db, "alice", "chat").Take(&rowBefore).Error).To(Succeed())
			var inputsBefore []inputRow
			Expect(scope(db, "alice", "chat").Order("ordinal").Find(&inputsBefore).Error).To(Succeed())
			var err error
			switch tc.operation {
			case "admit":
				// Dedup is read-only and remains available even when counters are exhausted.
				a, e := s.Admit(ctx, "alice", "chat", "a", []byte("a"))
				Expect(e).NotTo(HaveOccurred())
				Expect(a.Revision).To(Equal(uint64(1)))
				_, err = s.Admit(ctx, "alice", "chat", "c", nil)
			case "commit":
				_, err = s.Commit(ctx, "alice", "chat", before.Revision, "a", Checkpoint{Version: 3}, []Event{{Kind: "done"}})
			case "delete":
				err = s.Delete(ctx, "alice", "chat")
			}
			Expect(err).To(MatchError(ErrConflict))
			Expect(snapshot()).To(Equal(before))
			var rowAfter conversationRow
			Expect(scope(db, "alice", "chat").Take(&rowAfter).Error).To(Succeed())
			Expect(rowAfter).To(Equal(rowBefore))
			var inputsAfter []inputRow
			Expect(scope(db, "alice", "chat").Order("ordinal").Find(&inputsAfter).Error).To(Succeed())
			Expect(inputsAfter).To(Equal(inputsBefore))
			events, e := s.Events(ctx, "alice", "chat", 0, 10)
			Expect(e).NotTo(HaveOccurred())
			Expect(events).To(BeEmpty())
		})
	}
	It("permits the final representable ordinal, revision and event sequence without wrapping", func() {
		Expect(scope(db.Model(&conversationRow{}), "alice", "chat").Updates(map[string]any{"revision": uint64(math.MaxInt64 - 2), "last_ordinal": uint64(math.MaxInt64 - 1), "last_sequence": uint64(math.MaxInt64 - 1)}).Error).To(Succeed())
		a, err := s.Admit(ctx, "alice", "chat", "c", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Revision).To(Equal(uint64(math.MaxInt64 - 1)))
		v := snapshot()
		Expect(v.Pending[1].Ordinal).To(Equal(uint64(math.MaxInt64)))
		out, err := s.Commit(ctx, "alice", "chat", a.Revision, "a", Checkpoint{}, []Event{{Sequence: math.MaxUint64, Kind: "last"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Revision).To(Equal(uint64(math.MaxInt64)))
		events, err := s.Events(ctx, "alice", "chat", math.MaxInt64-1, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Sequence).To(Equal(uint64(math.MaxInt64)))
		_, err = s.Commit(ctx, "alice", "chat", out.Revision, "b", Checkpoint{}, nil)
		Expect(err).To(MatchError(ErrConflict))
	})
	It("rejects a sequence batch that crosses the signed boundary atomically", func() {
		Expect(scope(db.Model(&conversationRow{}), "alice", "chat").Update("last_sequence", uint64(math.MaxInt64-1)).Error).To(Succeed())
		before := snapshot()
		_, err := s.Commit(ctx, "alice", "chat", before.Revision, "a", Checkpoint{}, []Event{{Kind: "one"}, {Kind: "two"}})
		Expect(err).To(MatchError(ErrConflict))
		Expect(snapshot()).To(Equal(before))
		events, err := s.Events(ctx, "alice", "chat", 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
		// The failed batch did not consume the last slot or settle the input.
		_, err = s.Commit(ctx, "alice", "chat", before.Revision, "a", Checkpoint{}, []Event{{Kind: "last"}})
		Expect(err).NotTo(HaveOccurred())
	})
})
