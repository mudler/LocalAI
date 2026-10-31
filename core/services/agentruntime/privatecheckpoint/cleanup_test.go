// SPDX-License-Identifier: MIT
package privatecheckpoint

import (
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateCheckpoint(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Private checkpoint boundary")
}

var _ = Describe("Cleanup model", func() {
	It("defines every state/event pair without resurrection or premature purge", func() {
		for s := SealedPresent; s <= Purged; s++ {
			for e := RequestCleanup; e <= StaleBinding; e++ {
				tr, err := ReduceCleanup(s, e)
				Expect(err).NotTo(HaveOccurred())
				Expect(tr.Rule).NotTo(BeEmpty())
				if tr.Effect == Purge {
					Expect(s == Tombstoned || e == TombstoneSucceeded).To(BeTrue())
				}
				if s == Purged {
					Expect(tr.State).To(Equal(Purged))
					Expect(tr.Effect).NotTo(Equal(Tombstone))
					Expect(tr.Effect).NotTo(Equal(Purge))
				}
				if s == SealedPresent && e != TombstoneSucceeded {
					Expect(tr.State).NotTo(Equal(Purged))
				}
			}
		}
	})
	It("holds stale bindings and keeps failed purges tombstoned for retries", func() {
		tr, e := ReduceCleanup(Tombstoned, PurgeFailed)
		Expect(e).NotTo(HaveOccurred())
		Expect(tr.State).To(Equal(Tombstoned))
		tr, e = ReduceCleanup(tr.State, Retry)
		Expect(e).NotTo(HaveOccurred())
		Expect(tr.Effect).To(Equal(Purge))
		tr, e = ReduceCleanup(SealedPresent, StaleBinding)
		Expect(e).NotTo(HaveOccurred())
		Expect(tr.Effect).To(Equal(Hold))
	})
	It("rejects values outside the finite alphabet", func() {
		_, e := ReduceCleanup(99, Retry)
		Expect(e).To(HaveOccurred())
		_, e = ReduceCleanup(SealedPresent, 99)
		Expect(e).To(HaveOccurred())
	})
})

// diskCleanupFixture is a test-only host adapter. Reopening the file demonstrates
// durable tombstone consultation, not production WAL/backup erasure or fencing.
type diskCleanupFixture struct {
	path      string
	authority bool
	failPurge bool
	purges    int
}
type cleanupRecord struct {
	Binding CleanupBinding
	State   CleanupState
}

func (f *diskCleanupFixture) load(b CleanupBinding) (cleanupRecord, error) {
	var r cleanupRecord
	data, err := os.ReadFile(f.path)
	if err != nil || json.Unmarshal(data, &r) != nil || r.Binding != b {
		return cleanupRecord{}, ErrDenied
	}
	return r, nil
}
func (f *diskCleanupFixture) persist(r cleanupRecord) error {
	if err := os.WriteFile(f.path+".tmp", wire(r), 0600); err != nil {
		return ErrDenied
	}
	if err := os.Rename(f.path+".tmp", f.path); err != nil {
		return ErrDenied
	}
	return nil
}
func (f *diskCleanupFixture) State(_ context.Context, b CleanupBinding) (CleanupState, error) {
	r, err := f.load(b)
	return r.State, err
}
func (f *diskCleanupFixture) Tombstone(_ context.Context, b CleanupBinding) error {
	r, err := f.load(b)
	if err != nil || !f.authority {
		return ErrDenied
	}
	if r.State != SealedPresent {
		return nil
	}
	r.State = Tombstoned
	return f.persist(r)
}
func (f *diskCleanupFixture) Purge(_ context.Context, b CleanupBinding) error {
	r, err := f.load(b)
	if err != nil || !f.authority || r.State == SealedPresent {
		return ErrDenied
	}
	if r.State == Purged {
		return nil
	}
	if f.failPurge {
		return ErrDenied
	}
	f.purges++
	r.State = Purged
	return f.persist(r)
}

var _ = Describe("Test-only cleanup CAS adapter", func() {
	var f *diskCleanupFixture
	var b CleanupBinding
	ctx := context.Background()
	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "private-checkpoint-cleanup-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
		b = CleanupBinding{Principal: "alice", Conversation: "chat", Hash: [32]byte{1}, Revision: 3, PolicyRevision: 1}
		f = &diskCleanupFixture{path: filepath.Join(dir, "cleanup.json"), authority: true}
		Expect(f.persist(cleanupRecord{b, SealedPresent})).To(Succeed())
	})
	It("requires host cleanup authority and durable tombstoning before purge", func() {
		Expect(f.Purge(ctx, b)).To(MatchError(ErrDenied))
		f.authority = false
		Expect(f.Tombstone(ctx, b)).To(MatchError(ErrDenied))
		state, err := f.State(ctx, b)
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(SealedPresent))
	})
	It("reopens durable tombstones after purge failure and retries idempotently", func() {
		Expect(f.Tombstone(ctx, b)).To(Succeed())
		f.failPurge = true
		Expect(f.Purge(ctx, b)).To(MatchError(ErrDenied))
		reopened := &diskCleanupFixture{path: f.path, authority: true}
		state, err := reopened.State(ctx, b)
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(Tombstoned))
		Expect(reopened.Tombstone(ctx, b)).To(Succeed())
		Expect(reopened.Purge(ctx, b)).To(Succeed())
		Expect(reopened.Purge(ctx, b)).To(Succeed())
		Expect(reopened.Tombstone(ctx, b)).To(Succeed())
		Expect(reopened.purges).To(Equal(1))
		state, err = reopened.State(ctx, b)
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(Purged))
	})
	DescribeTable("holds stale cleanup bindings without modifying a replacement", func(change func(*CleanupBinding)) {
		old := b
		change(&b)
		Expect(f.persist(cleanupRecord{b, SealedPresent})).To(Succeed())
		Expect(f.Tombstone(ctx, old)).To(MatchError(ErrDenied))
		Expect(f.Purge(ctx, old)).To(MatchError(ErrDenied))
		_, err := f.State(ctx, old)
		Expect(err).To(MatchError(ErrDenied))
		state, err := f.State(ctx, b)
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(SealedPresent))
	}, Entry("hash", func(b *CleanupBinding) { b.Hash[0]++ }),
		Entry("revision", func(b *CleanupBinding) { b.Revision++ }),
		Entry("policy revision", func(b *CleanupBinding) { b.PolicyRevision++ }),
		Entry("principal", func(b *CleanupBinding) { b.Principal = "bob" }),
		Entry("conversation", func(b *CleanupBinding) { b.Conversation = "other" }))
})
