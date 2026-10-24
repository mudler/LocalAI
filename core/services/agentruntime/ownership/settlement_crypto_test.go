// SPDX-License-Identifier: MIT
package ownership_test

import (
	"bytes"
	"context"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	pc "github.com/mudler/LocalAI/core/services/agentruntime/privatecheckpoint"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Only authority/key/retention admission is a host fixture. Encoding, AES,
// lease predicates, transaction persistence and fresh-pool reads are real.
type sealHost struct{ now time.Time }

func (f sealHost) Now() time.Time { return f.now }
func (f sealHost) Check(_ context.Context, r pc.AuthorizationRequest) (pc.Authorization, error) {
	return pc.Authorization{Request: r, ReadUse: true, IndirectRetain: true, ExpiresAt: f.now.Add(time.Hour)}, nil
}
func (f sealHost) Active(context.Context, pc.KeyScope) (pc.KeyVersion, error) {
	return pc.KeyVersion{ID: "test-key", Version: 1}, nil
}
func (f sealHost) Lookup(context.Context, pc.KeyScope, pc.KeyVersion) (pc.KeyMaterial, error) {
	return pc.NewKeyMaterial(bytes.Repeat([]byte{42}, 32), pc.KeyActive), nil
}

type sealRetention struct{}

func (sealRetention) Check(context.Context, pc.Binding, time.Time) error { return nil }

type sealCleanup struct{}

func (sealCleanup) State(context.Context, pc.CleanupBinding) (pc.CleanupState, error) {
	return pc.SealedPresent, nil
}
func (sealCleanup) Tombstone(context.Context, pc.CleanupBinding) error { return nil }
func (sealCleanup) Purge(context.Context, pc.CleanupBinding) error     { return nil }

var _ = Describe("Sealed atomic settlement", func() {
	It("persists the real private codec AES envelope with only fixed safe status events", func() {
		ctx := context.Background()
		now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
		path := filepath.Join(GinkgoT().TempDir(), "sealed.db")
		open := func() *gorm.DB {
			d, e := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			Expect(e).NotTo(HaveOccurred())
			pool, e := d.DB()
			Expect(e).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(pool.Close()).To(Succeed()) })
			return d
		}
		db := open()
		s, e := store.New(db)
		Expect(e).NotTo(HaveOccurred())
		m, e := ownership.New(open())
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Create(ctx, "alice", "chat")).To(Succeed())
		a, e := s.Admit(ctx, "alice", "chat", "turn", []byte("safe input"))
		Expect(e).NotTo(HaveOccurred())
		lease, e := m.Acquire(ctx, ownership.Key{Principal: "alice", Conversation: "chat"}, "worker", now, time.Hour)
		Expect(e).NotTo(HaveOccurred())
		host := sealHost{now}
		svc, e := pc.New(pc.Dependencies{Clock: host, Authorizer: host, Reader: s, Leases: m, Keyring: host, Encryptor: pc.AES256GCM{}, Retention: sealRetention{}, Cleanup: sealCleanup{}})
		Expect(e).NotTo(HaveOccurred())
		native := nibadapter.Checkpoint{Version: nibadapter.CheckpointVersion, Context: []byte(`[{"role":"user","content":"PRIVATE-CONTEXT"}]`), History: []byte(`[{"role":"user","content":"PRIVATE-HISTORY"}]`), Artifacts: []byte(`[{"id":1,"content":"PRIVATE-ARTIFACT"}]`), Pruning: []byte(`{"compressed":{"0":{"level":1,"content":"PRIVATE-PRUNING"}}}`)}
		payload, e := pc.NewPrivatePayload(native, []byte("PRIVATE-CURSOR"))
		Expect(e).NotTo(HaveOccurred())
		target := pc.Scope{Principal: "alice", Conversation: "chat", Turn: "turn", Attempt: 1, Epoch: lease.Epoch}
		sealed, e := svc.Seal(ctx, pc.SaveRequest{Target: target, Lease: lease, Revision: a.Revision, Quiescent: true, Binding: pc.Binding{Source: target, SourceRevision: a.Revision, AuthRevision: 1, TaintRevision: 1, RetentionRevision: 1, ExpiresAt: now.Add(time.Hour)}}, payload)
		Expect(e).NotTo(HaveOccurred())
		checkpoint := sealed.StoreCheckpoint()
		Expect(checkpoint.Context).To(ContainSubstring("AES-256-GCM"))
		Expect(checkpoint.Context).NotTo(ContainSubstring("PRIVATE-"))
		out, e := m.Settle(ctx, lease, now, store.SettlementWrite{ExpectedRevision: a.Revision, TurnID: "turn", Mode: store.HoldTurn, Checkpoint: checkpoint, Events: []store.Event{{Kind: "status", Payload: []byte(`{"status":"waiting"}`)}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Next).To(BeNil())
		fresh := open()
		reader, e := store.New(fresh)
		Expect(e).NotTo(HaveOccurred())
		snap, e := reader.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		Expect(snap.Checkpoint).To(Equal(checkpoint))
		var columns struct{ CheckpointContext, CheckpointArtifacts, CheckpointPruning, CheckpointTranscriptCursor []byte }
		Expect(fresh.Table("runtime_conversations").Take(&columns).Error).To(Succeed())
		Expect(columns.CheckpointContext).To(Equal(checkpoint.Context))
		Expect(columns.CheckpointArtifacts).To(BeEmpty())
		Expect(columns.CheckpointPruning).To(BeEmpty())
		Expect(columns.CheckpointTranscriptCursor).To(BeEmpty())
		events, e := reader.Events(ctx, "alice", "chat", 0, 10)
		Expect(e).NotTo(HaveOccurred())
		Expect(events).To(Equal([]store.Event{{Sequence: 1, TurnID: "turn", Kind: "status", Payload: []byte(`{"status":"waiting"}`)}}))
		restore, e := pc.New(pc.Dependencies{Clock: host, Authorizer: host, Reader: reader, Leases: m, Keyring: host, Encryptor: pc.AES256GCM{}, Retention: sealRetention{}, Cleanup: sealCleanup{}})
		Expect(e).NotTo(HaveOccurred())
		recovered, e := restore.Restore(ctx, pc.ReadRequest{Target: target, Lease: lease, Revision: out.Revision, SourceRevision: a.Revision, Hash: pc.EnvelopeHash(checkpoint)}, snap.Checkpoint)
		Expect(e).NotTo(HaveOccurred())
		Expect(recovered.NativeCheckpoint()).To(Equal(native))
		Expect(recovered.Cursor()).To(Equal([]byte("PRIVATE-CURSOR")))
	})
})
