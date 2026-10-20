// SPDX-License-Identifier: MIT
package privatecheckpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"path/filepath"
	"time"
)

const secret = "PRIVATE-SENTINEL"

type fixture struct {
	now                              time.Time
	revoked, tomb                    bool
	status                           KeyStatus
	snap                             store.Snapshot
	key                              []byte
	onRead, onLookup, onOpen, onAuth func()
	opens, seals                     int
	allowed                          map[Operation]bool
	denyResource                     string
}

func (f *fixture) Now() time.Time { return f.now }
func (f *fixture) Check(ctx context.Context, r AuthorizationRequest) (Authorization, error) {
	if f.onAuth != nil {
		f.onAuth()
	}
	if f.revoked || !f.allowed[r.Operation] {
		return Authorization{}, errors.New(secret)
	}
	for _, v := range r.Binding.Resources {
		if v.CollectionID == f.denyResource {
			return Authorization{}, errors.New(secret)
		}
	}
	return Authorization{Request: r, ReadUse: true, IndirectRetain: true, Export: true, Download: true, Cleanup: true, ExpiresAt: f.now.Add(time.Hour)}, nil
}
func (f *fixture) Get(context.Context, string, string) (store.Snapshot, error) {
	if f.onRead != nil {
		f.onRead()
	}
	return f.snap, nil
}

type leaseFixture struct{ f *fixture }

func (l leaseFixture) Check(_ context.Context, v ownership.Lease, n time.Time) error {
	if v.Epoch != 1 || !n.Before(v.ExpiresAt) {
		return errors.New(secret)
	}
	return nil
}

type retentionFixture struct{ f *fixture }

func (r retentionFixture) Check(_ context.Context, b Binding, n time.Time) error {
	if !n.Before(b.ExpiresAt) || b.RetentionRevision != 1 {
		return errors.New(secret)
	}
	return nil
}
func (f *fixture) Active(context.Context, KeyScope) (KeyVersion, error) {
	return KeyVersion{ID: "k", Version: 1}, nil
}
func (f *fixture) Lookup(context.Context, KeyScope, KeyVersion) (KeyMaterial, error) {
	if f.onLookup != nil {
		f.onLookup()
	}
	return NewKeyMaterial(f.key, f.status), nil
}
func (f *fixture) Seal(k, p, a []byte) ([]byte, []byte, error) {
	f.seals++
	return AES256GCM{}.Seal(k, p, a)
}
func (f *fixture) Open(k, n, c, a []byte) ([]byte, error) {
	f.opens++
	p, e := (AES256GCM{}).Open(k, n, c, a)
	if f.onOpen != nil {
		f.onOpen()
	}
	return p, e
}

type cleanupFixture struct{ f *fixture }

func (c cleanupFixture) State(context.Context, CleanupBinding) (CleanupState, error) {
	if c.f.tomb {
		return Tombstoned, nil
	}
	return SealedPresent, nil
}
func (c cleanupFixture) Tombstone(context.Context, CleanupBinding) error { return nil }
func (c cleanupFixture) Purge(context.Context, CleanupBinding) error     { return nil }

var _ = Describe("Private checkpoint", func() {
	var f *fixture
	var s *Service
	var req SaveRequest
	var read ReadRequest
	var payload PrivatePayload
	var cp store.Checkpoint
	ctx := context.Background()
	seal := func() {
		v, e := s.Seal(ctx, req, payload)
		Expect(e).NotTo(HaveOccurred())
		cp = v.StoreCheckpoint()
		f.snap.Checkpoint = cp
		read = ReadRequest{Target: req.Target, Lease: req.Lease, Revision: 3, Hash: EnvelopeHash(cp), SourceRevision: 2}
	}
	restore := func() error { _, e := s.Restore(ctx, read, cp); return e }
	BeforeEach(func() {
		f = &fixture{now: time.Unix(1000, 0).UTC(), status: KeyActive, key: bytes.Repeat([]byte{42}, 32), snap: store.Snapshot{Revision: 3, Running: &store.Input{InputID: "turn"}}, allowed: map[Operation]bool{Save: true, Restore: true, Export: true, Download: true}}
		var e error
		s, e = New(Dependencies{Clock: f, Authorizer: f, Reader: f, Leases: leaseFixture{f}, Keyring: f, Encryptor: f, Retention: retentionFixture{f}, Cleanup: cleanupFixture{f}})
		Expect(e).NotTo(HaveOccurred())
		scope := Scope{Principal: "alice", Conversation: "chat", Turn: "turn", Attempt: 1, Epoch: 1}
		req = SaveRequest{Target: scope, Lease: ownership.Lease{Key: ownership.Key{Principal: "alice", Conversation: "chat"}, Owner: "worker", Epoch: 1, ExpiresAt: f.now.Add(time.Hour)}, Revision: 3, Quiescent: true, Binding: Binding{Source: scope, SourceRevision: 2, Resources: []Resource{{"a", 1}, {"b", 2}}, AuthRevision: 1, TaintRevision: 1, RetentionRevision: 1, ExpiresAt: f.now.Add(time.Hour)}}
		payload, e = NewPrivatePayload(nibadapter.Checkpoint{Version: nibadapter.CheckpointVersion, Context: []byte(`[{"role":"user","content":"` + secret + ` context"}]`), History: []byte(`[{"role":"user","content":"` + secret + ` history"}]`), Artifacts: []byte(`[{"id":1,"content":"` + secret + ` artifact"},{"id":2,"content":"OTHER-BODY"}]`), Pruning: []byte(`{"compressed":{"0":{"level":1,"content":"` + secret + ` pruning"}}}`)}, []byte(secret+" cursor"))
		Expect(e).NotTo(HaveOccurred())
	})

	// These hooks run synchronously inside real host callbacks, not before the
	// operation. No sleeps or substitute crypto hide the boundary ordering.
	for _, op := range []Operation{Save, Restore, Export, Download} {
		op := op
		invoke := func(c context.Context) error {
			switch op {
			case Save:
				v, e := s.Seal(c, req, payload)
				if e != nil {
					Expect(v.StoreCheckpoint().Context).To(BeEmpty())
				}
				return e
			case Restore, Export:
				var p PrivatePayload
				var e error
				if op == Restore {
					p, e = s.Restore(c, read, cp)
				} else {
					p, e = s.Export(c, read, cp)
				}
				if e != nil {
					Expect(p.native).To(BeEmpty())
					Expect(p.cursor).To(BeEmpty())
				}
				return e
			default:
				v, e := s.DownloadArtifact(c, DownloadRequest{ReadRequest: read, ArtifactID: 1}, cp)
				if e != nil {
					Expect(v.Bytes()).To(BeEmpty())
				}
				return e
			}
		}
		for _, phase := range []int{2, 3} {
			for _, status := range []KeyStatus{KeyRevoked, KeyDestroyed} {
				phase, status := phase, status
				It(fmt.Sprintf("denies %s key status %d during authorization phase %d", op, status, phase), func() {
					seal()
					f.seals = 0
					calls := 0
					f.onAuth = func() {
						calls++
						if calls == phase {
							f.status = status
						}
					}
					// The authorizer still returns allowed: only key status changes.
					err := invoke(ctx)
					if phase == 2 {
						Expect(f.opens+f.seals).To(BeZero(), "no crypto after observed revocation")
					}
					Expect(calls).To(Equal(phase))
					Expect(err).To(MatchError(ErrDenied))
				})
			}
		}
		It(fmt.Sprintf("denies %s when key lookup revokes authorization", op), func() {
			seal()
			f.seals = 0
			f.onLookup = func() { f.revoked = true }
			Expect(invoke(ctx)).To(MatchError(ErrDenied))
			Expect(f.opens + f.seals).To(BeZero())
		})
		for _, phase := range []int{2, 3} {
			phase := phase
			It(fmt.Sprintf("denies %s changed key material during authorization phase %d", op, phase), func() {
				seal()
				f.seals = 0
				calls := 0
				f.onAuth = func() {
					calls++
					if calls == phase {
						f.key[0]++
					}
				}
				err := invoke(ctx)
				if phase == 2 {
					Expect(f.opens + f.seals).To(BeZero())
				}
				Expect(err).To(MatchError(ErrDenied))
			})
		}
		for _, cancelInstead := range []bool{false, true} {
			cancelInstead := cancelInstead
			It(fmt.Sprintf("denies %s slow final lookup cancellation=%t", op, cancelInstead), func() {
				seal()
				c, cancel := context.WithCancel(ctx)
				defer cancel()
				auths := 0
				f.onAuth = func() { auths++ }
				f.onLookup = func() {
					if auths == 3 {
						if cancelInstead {
							cancel()
						} else {
							f.now = req.Binding.ExpiresAt
						}
					}
				}
				Expect(invoke(c)).To(MatchError(ErrDenied))
			})
		}
	}
	DescribeTable("round trips canonical resource manifests", func(resources []Resource) {
		req.Binding.Resources = resources
		seal()
		p, e := s.Restore(ctx, read, cp)
		Expect(e).NotTo(HaveOccurred())
		Expect(p.NativeCheckpoint()).To(Equal(payload.NativeCheckpoint()))
		Expect(p.Cursor()).To(Equal(payload.Cursor()))
		Expect(s.Export(ctx, read, cp)).Error().NotTo(HaveOccurred())
		Expect(s.DownloadArtifact(ctx, DownloadRequest{ReadRequest: read, ArtifactID: 1}, cp)).Error().NotTo(HaveOccurred())
	}, Entry("nil", []Resource(nil)), Entry("empty", []Resource{}), Entry("unsorted nonempty", []Resource{{"b", 2}, {"a", 1}}))
	It("rejects duplicate resource entries without discarding them", func() {
		req.Binding.Resources = []Resource{{"a", 1}, {"a", 2}}
		_, e := s.Seal(ctx, req, payload)
		Expect(e).To(MatchError(ErrDenied))
		Expect(f.seals).To(BeZero())
	})
	It("requires all injected dependencies including typed nils", func() {
		_, e := New(Dependencies{})
		Expect(e).To(MatchError(ErrDenied))
		var nilF *fixture
		_, e = New(Dependencies{Clock: nilF})
		Expect(e).To(MatchError(ErrDenied))
	})
	It("round trips all native fields and cursor through real AES", func() {
		seal()
		p, e := s.Restore(ctx, read, cp)
		Expect(e).NotTo(HaveOccurred())
		Expect(p.NativeCheckpoint()).To(Equal(payload.NativeCheckpoint()))
		Expect(p.Cursor()).To(Equal(payload.Cursor()))
	})
	It("stores only a version one ciphertext context without sensitive native fields", func() {
		seal()
		Expect(cp.Version).To(Equal(uint64(1)))
		Expect(cp.Artifacts).To(BeEmpty())
		Expect(cp.Pruning).To(BeEmpty())
		Expect(cp.TranscriptCursor).To(BeEmpty())
		Expect(string(cp.Context)).NotTo(ContainSubstring(secret))
	})
	It("uses nondeterministic nonces", func() { seal(); one := cp.Context; seal(); Expect(cp.Context).NotTo(Equal(one)) })
	It("keeps restore export and download grants separate", func() {
		seal()
		f.allowed[Export] = false
		_, e := s.Export(ctx, read, cp)
		Expect(e).To(HaveOccurred())
		Expect(restore()).To(Succeed())
		f.allowed[Download] = false
		_, e = s.DownloadArtifact(ctx, DownloadRequest{ReadRequest: read, ArtifactID: 1}, cp)
		Expect(e).To(HaveOccurred())
	})
	It("returns only the requested artifact body", func() {
		seal()
		a, e := s.DownloadArtifact(ctx, DownloadRequest{ReadRequest: read, ArtifactID: 1}, cp)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(a.Bytes())).To(Equal(secret + " artifact"))
		Expect(string(a.Bytes())).NotTo(ContainSubstring("OTHER-BODY"))
	})
	It("rejects an absent artifact", func() {
		seal()
		_, e := s.DownloadArtifact(ctx, DownloadRequest{ReadRequest: read, ArtifactID: 99}, cp)
		Expect(e).To(HaveOccurred())
	})
	It("requires indirect retain on save rather than read alone", func() { f.allowed[Save] = false; _, e := s.Seal(ctx, req, payload); Expect(e).To(HaveOccurred()) })
	It("rejects the entire manifest when one resource is unauthorized", func() {
		seal()
		f.denyResource = "b"
		Expect(restore()).To(HaveOccurred())
		Expect(f.opens).To(BeZero())
	})
	It("rechecks revocation after a slow Reader before decrypt", func() {
		seal()
		f.onRead = func() { f.revoked = true }
		Expect(restore()).To(HaveOccurred())
		Expect(f.opens).To(BeZero())
	})
	It("rechecks revocation after key lookup before decrypt", func() {
		seal()
		f.onLookup = func() { f.revoked = true }
		Expect(restore()).To(HaveOccurred())
		Expect(f.opens).To(BeZero())
	})
	It("rechecks revocation during Open before plaintext release", func() {
		seal()
		f.onOpen = func() { f.revoked = true }
		p, e := s.Restore(ctx, read, cp)
		Expect(e).To(HaveOccurred())
		Expect(p.NativeCheckpoint()).To(Equal(nibadapter.Checkpoint{}))
	})
	It("accepts explicitly authorized old source into a newer target attempt", func() { seal(); read.Target.Attempt = 2; Expect(restore()).To(Succeed()) })
	DescribeTable("rejects target and snapshot mismatch", func(change func()) { seal(); change(); Expect(restore()).To(HaveOccurred()) },
		Entry("principal", func() { read.Target.Principal = "bob" }), Entry("conversation", func() { read.Target.Conversation = "else" }), Entry("turn", func() { read.Target.Turn = "else" }), Entry("epoch", func() { read.Target.Epoch = 2 }), Entry("zero attempt", func() { read.Target.Attempt = 0 }), Entry("stale conversation revision", func() { read.Revision++ }), Entry("source revision", func() { read.SourceRevision++ }), Entry("replaced envelope", func() { f.snap.Checkpoint.Context = []byte("other") }), Entry("deleted snapshot", func() { f.snap.Deleted = true }), Entry("expired lease", func() { read.Lease.ExpiresAt = f.now }), Entry("tombstoned purge failure", func() { f.tomb = true }))
	DescribeTable("rejects unusable key material", func(status KeyStatus, size int) {
		seal()
		f.status = status
		f.key = make([]byte, size)
		Expect(restore()).To(HaveOccurred())
	}, Entry("revoked", KeyRevoked, 32), Entry("destroyed", KeyDestroyed, 32), Entry("unknown status", KeyStatus(99), 32), Entry("short", KeyActive, 31), Entry("oversized", KeyActive, 33))
	It("permits decrypt only old rotation keys but never seals with them", func() {
		seal()
		f.status = KeyDecryptOnly
		Expect(restore()).To(Succeed())
		_, e := s.Seal(ctx, req, payload)
		Expect(e).To(HaveOccurred())
	})
	It("denies an incorrect cryptographic key", func() { seal(); f.key[0]++; Expect(restore()).To(HaveOccurred()) })
	It("denies exact expiry and clock advancement during authorization", func() {
		seal()
		f.onAuth = func() { f.now = req.Binding.ExpiresAt }
		Expect(restore()).To(HaveOccurred())
		Expect(f.opens).To(BeZero())
	})
	It("denies expiry reached inside Open", func() {
		seal()
		f.onOpen = func() { f.now = req.Binding.ExpiresAt }
		Expect(restore()).To(HaveOccurred())
	})
	It("denies an unapproved retention revision", func() {
		req.Binding.RetentionRevision = 2
		_, e := s.Seal(ctx, req, payload)
		Expect(e).To(HaveOccurred())
	})
	It("denies cancellation inside the final authorization before releasing plaintext", func() {
		seal()
		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		calls := 0
		f.onAuth = func() {
			calls++
			if calls == 3 {
				cancel()
			}
		}
		p, e := s.Restore(cancelCtx, read, cp)
		Expect(calls).To(Equal(3))
		Expect(e).To(MatchError(ErrDenied))
		Expect(p.NativeCheckpoint()).To(Equal(nibadapter.Checkpoint{}))
	})
	It("denies reads from a reopened durable tombstone after purge failure and retry", func() {
		seal()
		dir, err := os.MkdirTemp("", "private-checkpoint-denial-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
		b := CleanupBinding{Principal: read.Target.Principal, Conversation: read.Target.Conversation, Hash: read.Hash, Revision: read.Revision, PolicyRevision: 1}
		cleanup := &diskCleanupFixture{path: filepath.Join(dir, "cleanup.json"), authority: true, failPurge: true}
		Expect(cleanup.persist(cleanupRecord{b, SealedPresent})).To(Succeed())
		s.d.Cleanup = cleanup
		Expect(restore()).To(Succeed())
		f.opens = 0
		Expect(cleanup.Tombstone(ctx, b)).To(Succeed())
		Expect(cleanup.Purge(ctx, b)).To(MatchError(ErrDenied))
		reopened := &diskCleanupFixture{path: cleanup.path, authority: true}
		s.d.Cleanup = reopened
		Expect(restore()).To(MatchError(ErrDenied))
		_, err = s.Export(ctx, read, cp)
		Expect(err).To(MatchError(ErrDenied))
		_, err = s.DownloadArtifact(ctx, DownloadRequest{ReadRequest: read, ArtifactID: 1}, cp)
		Expect(err).To(MatchError(ErrDenied))
		Expect(f.opens).To(BeZero())
		Expect(reopened.Purge(ctx, b)).To(Succeed())
		Expect(reopened.Purge(ctx, b)).To(Succeed())
		Expect(restore()).To(MatchError(ErrDenied))
		Expect(reopened.purges).To(Equal(1))
	})

	It("rejects nonquiescent source exports", func() { req.Quiescent = false; _, e := s.Seal(ctx, req, payload); Expect(e).To(HaveOccurred()) })
	It("does not alias payload accessors or ciphertext storage", func() {
		seal()
		n := payload.NativeCheckpoint()
		n.Context[0] = '!'
		Expect(payload.NativeCheckpoint().Context[0]).To(Equal(byte('[')))
		v, e := s.Seal(ctx, req, payload)
		Expect(e).NotTo(HaveOccurred())
		x := v.StoreCheckpoint()
		x.Context[0] = '!'
		Expect(v.StoreCheckpoint().Context[0]).To(Equal(byte('{')))
	})
	It("redacts default fmt and JSON including malicious dependency errors", func() {
		seal()
		for _, v := range []any{payload, req, req.Binding, read, NewKeyMaterial([]byte(secret), KeyActive), AuthorizationRequest{Binding: req.Binding}, cpEnvelopeForTest(cp), PrivateArtifact{body: []byte(secret)}} {
			Expect(fmt.Sprintf("%v %+v %#v", v, v, v)).NotTo(ContainSubstring(secret))
			b, e := json.Marshal(v)
			Expect(e).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring(secret))
		}
		f.revoked = true
		Expect(restore()).To(MatchError(ErrDenied))
	})
	It("rejects malformed native input with fixed safe errors", func() {
		_, e := NewPrivatePayload(nibadapter.Checkpoint{Version: secret}, nil)
		Expect(e).To(MatchError(ErrDenied))
	})
	DescribeTable("rejects strict envelope corruption", func(mutate func([]byte) []byte) {
		seal()
		cp.Context = mutate(cp.Context)
		f.snap.Checkpoint = cp
		read.Hash = EnvelopeHash(cp)
		Expect(restore()).To(HaveOccurred())
		Expect(f.opens).To(BeZero())
	},
		Entry("duplicate", func(b []byte) []byte { return append([]byte(`{"Version":1,`), b[1:]...) }), Entry("trailing", func(b []byte) []byte { return append(b, []byte(` {}`)...) }), Entry("unknown property", func(b []byte) []byte { return append([]byte(`{"extra":1,`), b[1:]...) }), Entry("version", func(b []byte) []byte {
			var root map[string]json.RawMessage
			Expect(json.Unmarshal(b, &root)).To(Succeed())
			root["Version"] = json.RawMessage(`2`)
			out, err := json.Marshal(root)
			Expect(err).NotTo(HaveOccurred())
			return out
		}), Entry("algorithm", func(b []byte) []byte { return bytes.Replace(b, []byte("AES-256-GCM"), []byte("OTHER-CIPHER"), 1) }), Entry("oversized", func(b []byte) []byte { return bytes.Repeat([]byte{'x'}, MaxEnvelope+1) }))
	It("rejects authenticated key version tampering without weakening header authentication", func() {
		seal()
		var env envelope
		Expect(strict(cp.Context, &env)).To(Succeed())
		env.Header.Key.Version++
		cp.Context = wire(env)
		f.snap.Checkpoint = cp
		read.Hash = EnvelopeHash(cp)
		Expect(restore()).To(MatchError(ErrDenied))
		Expect(f.opens).To(Equal(1))
	})
	DescribeTable("denies cancellation in final authorization for each operation", func(op Operation) {
		seal()
		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		calls := 0
		f.onAuth = func() {
			calls++
			if calls == 3 {
				cancel()
			}
		}
		switch op {
		case Save:
			v, err := s.Seal(cancelCtx, req, payload)
			Expect(err).To(MatchError(ErrDenied))
			Expect(v.StoreCheckpoint().Context).To(BeEmpty())
		case Export:
			v, err := s.Export(cancelCtx, read, cp)
			Expect(err).To(MatchError(ErrDenied))
			Expect(v.NativeCheckpoint()).To(Equal(nibadapter.Checkpoint{}))
		case Download:
			v, err := s.DownloadArtifact(cancelCtx, DownloadRequest{ReadRequest: read, ArtifactID: 1}, cp)
			Expect(err).To(MatchError(ErrDenied))
			Expect(v.Bytes()).To(BeEmpty())
		}
		Expect(calls).To(Equal(3))
	}, Entry("seal", Save), Entry("export", Export), Entry("download", Download))
	DescribeTable("does not decrypt after cancellation in a slow dependency", func(phase string) {
		seal()
		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		switch phase {
		case "reader":
			f.onRead = cancel
		case "lookup":
			f.onLookup = cancel
		case "authorization":
			f.onAuth = cancel
		}
		_, err := s.Restore(cancelCtx, read, cp)
		Expect(err).To(MatchError(ErrDenied))
		Expect(f.opens).To(BeZero())
	}, Entry("reader", "reader"), Entry("lookup", "lookup"), Entry("authorization", "authorization"))

	It("detects authenticated ciphertext tampering", func() {
		seal()
		var e envelope
		Expect(strict(cp.Context, &e)).To(Succeed())
		e.Ciphertext[0] ^= 1
		cp.Context = wire(e)
		f.snap.Checkpoint = cp
		read.Hash = EnvelopeHash(cp)
		Expect(restore()).To(HaveOccurred())
	})
	It("detects authenticated header tampering", func() {
		seal()
		var e envelope
		Expect(strict(cp.Context, &e)).To(Succeed())
		e.Header.Binding.AuthRevision++
		cp.Context = wire(e)
		f.snap.Checkpoint = cp
		read.Hash = EnvelopeHash(cp)
		Expect(restore()).To(HaveOccurred())
	})
	It("bounds resources and IDs and rejects duplicate resources", func() {
		for _, rs := range [][]Resource{{{"a", 1}, {"a", 1}}, {{string(bytes.Repeat([]byte{'x'}, 1025)), 1}}, make([]Resource, 257)} {
			req.Binding.Resources = rs
			_, e := s.Seal(ctx, req, payload)
			Expect(e).To(HaveOccurred())
		}
	})
	It("persists only ciphertext in the existing file SQLite store without public events", func() {
		dir, e := os.MkdirTemp("/home/mudler/_git/control-room/assets/work/localai-unified-runtime/task6-attempt1", "sqlite-")
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
		path := filepath.Join(dir, "runtime.db")
		db, e := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(e).NotTo(HaveOccurred())
		sql, e := db.DB()
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(sql.Close()).To(Succeed()) })
		st, e := store.New(db)
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Create(ctx, "alice", "chat")).To(Succeed())
		a, e := st.Admit(ctx, "alice", "chat", "turn", []byte("public input"))
		Expect(e).NotTo(HaveOccurred())
		seal()
		_, e = st.Commit(ctx, "alice", "chat", a.Revision, "turn", cp, nil)
		Expect(e).NotTo(HaveOccurred())
		snap, e := st.Get(ctx, "alice", "chat")
		Expect(e).NotTo(HaveOccurred())
		Expect(snap.Checkpoint).To(Equal(cp))
		events, e := st.Events(ctx, "alice", "chat", 0, 100)
		Expect(e).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
		data, e := os.ReadFile(path)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring(secret))
	})
})

func cpEnvelopeForTest(cp store.Checkpoint) SealedCheckpoint {
	return SealedCheckpoint{data: cp.Context}
}
