// SPDX-License-Identifier: MIT
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	pc "github.com/mudler/LocalAI/core/services/agentruntime/privatecheckpoint"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	"github.com/mudler/nib/chat"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Fixture authority is deliberately synthetic. It proves seam use, not a
// production key provider, complete provenance or two-owner database fencing.
type cryptoHost struct {
	mu      sync.Mutex
	now     time.Time
	snap    store.Snapshot
	scope   pc.Scope
	req     pc.SaveRequest
	saves   int
	deny    bool
	settled chan struct{}
}

func (h *cryptoHost) Now() time.Time { return h.now }
func (h *cryptoHost) Check(_ context.Context, r pc.AuthorizationRequest) (pc.Authorization, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deny || r.Target.Principal != h.scope.Principal || r.Target.Conversation != h.scope.Conversation || r.Target.Turn != h.scope.Turn || r.Target.Attempt != h.scope.Attempt || r.Target.Epoch != h.scope.Epoch {
		return pc.Authorization{}, errShell
	}
	return pc.Authorization{Request: r, ReadUse: true, IndirectRetain: true, ExpiresAt: h.now.Add(time.Hour)}, nil
}
func (h *cryptoHost) Get(context.Context, string, string) (store.Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v := h.snap
	v.Checkpoint.Context = bytes.Clone(v.Checkpoint.Context)
	return v, nil
}
func (h *cryptoHost) Active(context.Context, pc.KeyScope) (pc.KeyVersion, error) {
	return pc.KeyVersion{ID: "fixture", Version: 1}, nil
}
func (h *cryptoHost) Lookup(context.Context, pc.KeyScope, pc.KeyVersion) (pc.KeyMaterial, error) {
	return pc.NewKeyMaterial(bytes.Repeat([]byte{73}, 32), pc.KeyActive), nil
}

type cryptoLease struct{ h *cryptoHost }

func (l cryptoLease) Check(_ context.Context, v ownership.Lease, n time.Time) error {
	l.h.mu.Lock()
	defer l.h.mu.Unlock()
	if v.Epoch != l.h.scope.Epoch || !n.Before(v.ExpiresAt) {
		return errShell
	}
	return nil
}

type cryptoRetention struct{}

func (cryptoRetention) Check(_ context.Context, b pc.Binding, n time.Time) error {
	if b.RetentionRevision != 1 || !n.Before(b.ExpiresAt) {
		return errShell
	}
	return nil
}

type cryptoCleanup struct{}

func (cryptoCleanup) State(context.Context, pc.CleanupBinding) (pc.CleanupState, error) {
	return pc.SealedPresent, nil
}
func (cryptoCleanup) Tombstone(context.Context, pc.CleanupBinding) error { return nil }
func (cryptoCleanup) Purge(context.Context, pc.CleanupBinding) error     { return nil }
func (h *cryptoHost) SaveRequest(context.Context, identity) (pc.SaveRequest, error) {
	return h.req, nil
}
func (h *cryptoHost) Settle(_ context.Context, r settlementRequest) (*handoff, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := r.id.fields()
	if h.deny || f.attempt != h.scope.Attempt || f.epoch != h.scope.Epoch || f.turn != h.snap.Running.InputID || h.snap.Revision != 3 || r.checkpoint.Version != 1 || len(r.checkpoint.Context) == 0 || len(r.checkpoint.Artifacts)+len(r.checkpoint.Pruning)+len(r.checkpoint.TranscriptCursor) != 0 {
		return nil, errShell
	}
	h.snap.Checkpoint = r.checkpoint
	h.saves++
	close(h.settled)
	return nil, nil
}
func cryptoFixture() (*cryptoHost, *pc.Service) {
	d := fixture().fields().id.fields()
	n := time.Unix(1000, 0).UTC()
	scope := pc.Scope{Principal: d.principal, Conversation: d.conversation, Turn: d.turn, Attempt: d.attempt, Epoch: d.epoch}
	h := &cryptoHost{now: n, scope: scope, snap: store.Snapshot{Revision: 3, Running: &store.Input{InputID: d.turn}}, settled: make(chan struct{})}
	h.req = pc.SaveRequest{Target: scope, Revision: 3, Lease: ownership.Lease{Key: ownership.Key{Principal: d.principal, Conversation: d.conversation}, Owner: "fixture", Epoch: 1, ExpiresAt: n.Add(time.Hour)}, Binding: pc.Binding{Source: scope, SourceRevision: 2, AuthRevision: 1, TaintRevision: 1, RetentionRevision: 1, ExpiresAt: n.Add(time.Hour)}}
	s, e := pc.New(pc.Dependencies{Clock: h, Authorizer: h, Reader: h, Leases: cryptoLease{h}, Keyring: h, Encryptor: pc.AES256GCM{}, Retention: cryptoRetention{}, Cleanup: cryptoCleanup{}})
	Expect(e).NotTo(HaveOccurred())
	return h, s
}

var _ = Describe("Actual tool-disabled nib turn", func() {
	It("denies every mandatory tool request and excludes all executable integrations", func() {
		for _, r := range []chat.ToolCallRequest{{}, {Name: "bash", Arguments: "private"}, {Name: "spawn_agent", AgentID: "helper"}} {
			Expect(denyNative(r)).To(HaveOccurred())
		}
		cfg, e := (localNativeFactory{endpoint: "http://127.0.0.1:1/v1", stateDir: GinkgoT().TempDir()}).config()
		Expect(e).NotTo(HaveOccurred())
		Expect(cfg.BuiltinTools).To(Equal([]string{"__runner_deny_all__"}))
		Expect(cfg.MCPServers).To(BeEmpty())
		Expect(cfg.Hooks).To(BeEmpty())
		Expect(cfg.Skills).To(BeEmpty())
		Expect(cfg.Agents).To(BeEmpty())
		Expect(cfg.LSP).To(BeEmpty())
		Expect(*cfg.LSPAutoDetect).To(BeFalse())
		Expect(cfg.TraceDir).To(BeEmpty())
	})
	It("runs SendMessage joins checkpoint writers seals ciphertext and restores an authorized fresh attempt", func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		var turns atomic.Int32
		var toolSchemas atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/chat/completions" {
				http.NotFound(w, r)
				return
			}
			var body struct {
				Tools  []json.RawMessage `json:"tools"`
				Stream bool              `json:"stream"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(400)
				return
			}
			toolSchemas.Add(int32(len(body.Tools)))
			if body.Stream {
				w.WriteHeader(400)
				return
			}
			turns.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"fixture","object":"chat.completion","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"PRIVATE-NATIVE-ANSWER"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`)
		}))
		defer server.Close()
		h, codec := cryptoFixture()
		host := &shellHost{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c, e := newCoordinator(ctx, fixture(), shellDependencies{Factory: localNativeFactory{endpoint: server.URL + "/v1", stateDir: GinkgoT().TempDir()}, Authority: host, Provenance: h, Settlement: h, Handoff: host, Clock: host, Codec: codec}, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(c.begin()).To(Succeed())
		Eventually(h.settled, 10*time.Second).Should(BeClosed())
		Eventually(c.phase).Should(Equal("Idle"))
		Expect(turns.Load()).To(Equal(int32(1)))
		Expect(toolSchemas.Load()).To(BeZero())
		c.mu.Lock()
		native := c.session.(*localNativeSession)
		c.mu.Unlock()
		cp, cursor, e := native.Export()
		Expect(e).NotTo(HaveOccurred())
		Expect(cp.Context).To(ContainSubstring("PRIVATE-NATIVE-ANSWER"))
		Expect(cp.History).To(ContainSubstring("PRIVATE-NATIVE-ANSWER"))
		Expect(cp.Artifacts).To(MatchJSON(`[]`))
		Expect(cp.Pruning).NotTo(BeEmpty())
		Expect(cursor).To(HaveLen(8))
		// Yield repeatedly while the cancelled watcher can still run. Source audit
		// proves no exported writers; this race probe exercises repeated exports.
		for i := 0; i < 100; i++ {
			again, cur, e := native.Export()
			Expect(e).NotTo(HaveOccurred())
			Expect(again).To(Equal(cp))
			Expect(cur).To(Equal(cursor))
		}
		h.mu.Lock()
		stored := h.snap.Checkpoint
		h.scope.Attempt = 2
		h.scope.Epoch = 2
		h.mu.Unlock()
		Expect(stored.Context).NotTo(ContainSubstring("PRIVATE-NATIVE-ANSWER"))
		Expect(stored.Context).NotTo(ContainSubstring("private-input"))
		Expect(stored.Artifacts).To(BeEmpty())
		Expect(stored.Pruning).To(BeEmpty())
		Expect(stored.TranscriptCursor).To(BeEmpty())
		read := pc.ReadRequest{Target: h.scope, Lease: h.req.Lease, Revision: 3, Hash: pc.EnvelopeHash(stored), SourceRevision: 2}
		read.Lease.Epoch = 2
		p, e := codec.Restore(ctx, read, stored)
		Expect(e).NotTo(HaveOccurred())
		Expect(p.NativeCheckpoint()).To(Equal(cp))
		Expect(p.Cursor()).To(Equal(cursor))
		id := fixture().fields().id.clone()
		id.fields().attempt = 2
		id.fields().epoch = 2
		fresh, e := (localNativeFactory{endpoint: server.URL + "/v1", stateDir: GinkgoT().TempDir()}).New(ctx, id, &p, func() {})
		Expect(e).NotTo(HaveOccurred())
		Expect(fresh.AwaitQuiescence(ctx)).To(Succeed())
		restored, cur, e := fresh.Export()
		Expect(e).NotTo(HaveOccurred())
		Expect(restored).To(Equal(cp))
		Expect(cur).To(Equal(cursor))
		Expect(fresh.Close()).To(Succeed())
		Expect(native.Close()).To(Succeed())
		cancel()
		c.waitWorkers()
	})
	It("rejects external provider URLs before constructing a session", func() {
		_, e := (localNativeFactory{endpoint: "https://example.com/v1", stateDir: "private"}).config()
		Expect(e).To(HaveOccurred())
	})
	It("redacts session and factory diagnostics", func() {
		f := localNativeFactory{endpoint: "PRIVATE-URL", stateDir: "PRIVATE-PATH"}
		Expect(fmt.Sprintf("%#v", f)).NotTo(ContainSubstring("PRIVATE-"))
		Expect(fmt.Sprintf("%+v", &localNativeSession{})).NotTo(ContainSubstring("session:"))
	})
})

var _ = nibadapter.CheckpointVersion
