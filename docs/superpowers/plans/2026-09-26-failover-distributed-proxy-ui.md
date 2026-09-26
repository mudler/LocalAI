# Failover: Distributed Mode, localai-proxy and WebUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make failover chains consistent across distributed frontends, add a `localai-proxy` backend that serves every LocalAI API (including live transcription) from a remote LocalAI, add the chain UI, and add the distributed-state contributor rule — all in PR #12285.

**Architecture:** The failover `Manager` gets a `StateSync` dependency (no-op standalone; three `syncstate.SyncedMap`s in distributed mode) and a leader gate (PostgreSQL advisory lock per tick) so one frontend probes and owns chain state. `localai-proxy` is a new Go gRPC backend mapping each backend method to the upstream LocalAI REST endpoint, with a WebSocket bridge to the upstream realtime API for live transcription. The React UI adds an editor field, a template, a live health strip, a badge and an overview page on the existing failover REST/SSE API.

**Tech Stack:** Go (echo, gRPC, gorm, NATS via `syncstate`, gorilla/websocket), Ginkgo/Gomega, React 19 + Vite, Playwright.

**Spec:** `docs/superpowers/specs/2026-09-26-failover-distributed-proxy-ui-design.md` (builds on `docs/superpowers/specs/2026-09-26-model-failover-chains-design.md`)

## Global Constraints

- Worktree `/home/mudler/_git/LocalAI/.wt/failover-chains`, branch `feat/failover-chains`, PR #12285. Push only at the end (the controller does it).
- Commit trailer exactly `Assisted-by: Claude:claude-opus-5-5`. Never `Co-Authored-By` or `Signed-off-by`. `docs/superpowers/` needs `git add -f`.
- Build scope: `go build ./core/... ./pkg/... ./tests/... ./backend/go/localai-proxy/...` — never `go build ./...` (CGo launcher/backends need X11).
- Root `core/http` tests need `LOCALAI_TEST_HTTP_PORT=19391`.
- Logging `github.com/mudler/xlog`; `any` not `interface{}`; comments explain why.
- Standalone mode (no NATS/DB) must behave exactly as before this plan.
- SyncedMap names exactly: `failover.pins`, `failover.targets`, `failover.chains`. Leader republish interval 10 s. Advisory lock key constant `KeyFailoverProber` = 108.
- Backend name exactly `localai-proxy`; backend option `realtime_pipeline:<name>`; `Unimplemented` message format `localai-proxy: <method> has no upstream counterpart`.
- UI: no new inline `style={{}}` (inline-style ratchet); `StatusPill` tones: healthy/primary → success, recovering/fallback → warning, down/degraded → error, missing → muted; strings in i18n for all 8 locales (en, it, es, de, zh-CN, id, ko, pt-BR); pin controls only for `isAdmin`.
- Coverage baselines (`coverage-baseline.txt` 54.2, `core/http/react-ui/coverage-baseline.txt` 40.0) must not go down; never edit them.

## Review Focus

1. A NATS echo of a frontend's own publish must not deadlock or double-emit events (Task 1: "echo of own publish is a no-op").
2. A frontend that joins late (no deltas yet) must still serve requests from local state, then converge on the leader's republish (Task 2: "late joiner converges on heartbeat").
3. Leadership moving between frontends must not reset fail-back timing (shared `activeSince`) (Task 3: "new leader keeps activeSince").
4. A `localai-proxy` target that returns `Unimplemented` must move the request to the next chain target without tripping (Task 4: "Unimplemented skips without trip").
5. An upstream disconnect mid live-transcription must end the gRPC stream with `Unavailable`, not hang (Task 8: "upstream disconnect ends the stream").

---

## Part C — Distributed-aware failover

### Task 1: Manager state-sync hooks and leader gate

**Files:**
- Create: `core/services/failover/statesync.go`, `core/services/failover/statesync_test.go`
- Modify: `core/services/failover/manager.go`, `core/services/failover/schedule.go`

**Interfaces:**
- Consumes: existing `Manager` internals (`setTargetLocked`, `recomputeLocked`, `Pin`, `Unpin`, `emitLocked`, `chainState`, `targetState`, `takeWarmLocked`).
- Produces:
  ```go
  type TargetSnapshot struct {
      Target        string      `json:"target"`
      State         TargetState `json:"state"`
      Reason        Reason      `json:"reason"`
      Error         string      `json:"error,omitempty"`
      ConsecutiveOK int         `json:"consecutive_ok"`
      Since         time.Time   `json:"since"`
  }
  type ChainSnapshot struct {
      Chain       string     `json:"chain"`
      Active      string     `json:"active"`
      ActiveSince time.Time  `json:"active_since"`
      State       ChainState `json:"state"`
      Reason      Reason     `json:"reason"`
  }
  type StateSync interface {
      PublishTarget(TargetSnapshot)
      PublishChain(ChainSnapshot)
      SetPin(chain, target string) error
      ClearPin(chain string) error
      Pins() map[string]string
  }
  type LeaderGate func(ctx context.Context, fn func()) bool
  func WithLeaderGate(g LeaderGate) Option
  func (m *Manager) SetStateSync(s StateSync)
  func (m *Manager) ApplyTarget(s TargetSnapshot)
  func (m *Manager) ApplyChain(s ChainSnapshot)
  func (m *Manager) ApplyPin(chain, target string) // target "" = unpinned
  func (m *Manager) IsLeader() bool
  func (m *Manager) Republish() // leader: publish every target and chain snapshot
  ```

Design rules (binding):
- Publishing happens **outside `m.mu`**: a real or fake bus delivers the frontend's own publish back synchronously to `ApplyTarget`/`ApplyChain`/`ApplyPin`, which take `m.mu`. Queue publishes in `m.pending []func()` while locked; every exported mutating method drains and runs them after unlocking (helper `m.unlockAndFlush()`).
- `ApplyTarget` sets state through `setTargetLocked` with `m.applying = true`, so it emits the local `target.state` event but does not publish. An echo whose state equals the local state is a no-op (existing early return).
- Local transitions (`setTargetLocked` with `!m.applying` and `m.sync != nil`) queue `PublishTarget`.
- Chains: when `m.sync != nil && !m.leader`, `recomputeLocked` does not change `ch.active` once the chain has adopted leader state (`ch.adopted`); before adoption it computes locally. `ApplyChain` sets `active`, `activeSince`, `state`, `adopted = true` and emits `chain.switched` (with the snapshot's reason) when `active` or `state` changed. When the leader's recompute changes a chain, it queues `PublishChain`.
- Pins: `Pin`/`Unpin` set local state immediately (read-your-writes), then call `sync.SetPin`/`ClearPin` outside the lock. `ApplyPin` sets/clears `ch.pinned` and recomputes with `ReasonManual`. `SetStateSync` hydrates pins from `s.Pins()`.
- Leader gate: in `Tick`, after `Sync()`, call `gate(ctx, fn)` where `fn` runs probe scheduling + `Reevaluate()`; set `m.leader` to the returned bool. Without a gate (standalone) the manager is always leader and `Tick` behaves exactly as today. Followers still run `Reevaluate()` (local-only when not adopted). On a false→true leader transition set `m.warmPending = true` so `onWarm` fires on the new leader.
- `Republish()` queues `PublishTarget` for every target and `PublishChain` for every chain (leader only; no-op otherwise). `Run` calls it every 10 ticks when leader.

- [ ] **Step 1: Write the failing tests** (`statesync_test.go`, package `failover`, reuse `fakeClock`, `fakeSource`, `local`, `remote`, `chainCfg`, `t`, `errBoom`, `drain` from existing test files)

```go
package failover

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// loopSync is an in-process StateSync that delivers every publish to all
// managers synchronously, including the publisher (like NATS echo).
type loopSync struct {
	mu    sync.Mutex
	peers []*Manager
	pins  map[string]string
}

func (l *loopSync) add(m *Manager) { l.mu.Lock(); l.peers = append(l.peers, m); l.mu.Unlock() }
func (l *loopSync) each(f func(*Manager)) {
	l.mu.Lock()
	ps := append([]*Manager(nil), l.peers...)
	l.mu.Unlock()
	for _, p := range ps {
		f(p)
	}
}
func (l *loopSync) PublishTarget(s TargetSnapshot) { l.each(func(m *Manager) { m.ApplyTarget(s) }) }
func (l *loopSync) PublishChain(s ChainSnapshot)   { l.each(func(m *Manager) { m.ApplyChain(s) }) }
func (l *loopSync) SetPin(c, t string) error {
	l.mu.Lock(); l.pins[c] = t; l.mu.Unlock()
	l.each(func(m *Manager) { m.ApplyPin(c, t) })
	return nil
}
func (l *loopSync) ClearPin(c string) error {
	l.mu.Lock(); delete(l.pins, c); l.mu.Unlock()
	l.each(func(m *Manager) { m.ApplyPin(c, "") })
	return nil
}
func (l *loopSync) Pins() map[string]string {
	l.mu.Lock(); defer l.mu.Unlock()
	out := map[string]string{}
	for k, v := range l.pins {
		out[k] = v
	}
	return out
}

var _ = Describe("Manager state sync", func() {
	var (
		clock      *fakeClock
		src        *fakeSource
		bus        *loopSync
		a, b       *Manager
		leaderIsA  bool
		gateFor    func(isA bool) LeaderGate
		ctx        = context.Background()
	)

	BeforeEach(func() {
		clock = newFakeClock()
		src = newFakeSource(remote("x"), local("y"), chainCfg("chain", nil, t("x"), t("y")))
		bus = &loopSync{pins: map[string]string{}}
		leaderIsA = true
		gateFor = func(isA bool) LeaderGate {
			return func(_ context.Context, fn func()) bool {
				if isA != leaderIsA {
					return false
				}
				fn()
				return true
			}
		}
		a = New(src, WithClock(clock), WithLeaderGate(gateFor(true)))
		b = New(src, WithClock(clock), WithLeaderGate(gateFor(false)))
		bus.add(a); bus.add(b)
		a.SetStateSync(bus); b.SetStateSync(bus)
		a.Tick(ctx); b.Tick(ctx)
	})

	It("echo of own publish is a no-op and emits one event", func() {
		events, cancel := a.Subscribe(16)
		defer cancel()
		a.ReportFailure("x", errBoom)
		n := 0
		for _, e := range drain(events) {
			if e.Type == EventTargetState && e.Target == "x" {
				n++
			}
		}
		Expect(n).To(Equal(1))
	})

	It("a trip on one frontend is skipped by the other's plan", func() {
		b.ReportFailure("x", errBoom)
		att, err := a.Plan("chain")
		Expect(err).ToNot(HaveOccurred())
		Expect(att.Target()).To(Equal("y"))
	})

	It("followers adopt the leader's chain state and emit the switch", func() {
		events, cancel := b.Subscribe(16)
		defer cancel()
		a.ReportFailure("x", errBoom) // leader recomputes and publishes chain state
		st, _ := b.ChainStatus("chain")
		Expect(st.Active).To(Equal("y"))
		var sw []Event
		for _, e := range drain(events) {
			if e.Type == EventChainSwitched {
				sw = append(sw, e)
			}
		}
		Expect(sw).ToNot(BeEmpty())
	})

	It("a pin on one frontend applies on all", func() {
		Expect(b.Pin("chain", "y")).To(Succeed())
		st, _ := a.ChainStatus("chain")
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("y"))
		Expect(a.Unpin("chain")).To(Succeed())
		st, _ = b.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
	})

	It("hydrates pins when the sync is attached", func() {
		bus.pins["chain"] = "y"
		c := New(src, WithClock(clock), WithLeaderGate(gateFor(false)))
		c.SetStateSync(bus)
		st, _ := c.ChainStatus("chain")
		Expect(st.Pinned).ToNot(BeNil())
	})

	It("only the leader probes", func() {
		pa, pb := &fakeProber{fail: map[string]error{}}, &fakeProber{fail: map[string]error{}}
		a = New(src, WithClock(clock), WithProber(pa), WithLeaderGate(gateFor(true)))
		b = New(src, WithClock(clock), WithProber(pb), WithLeaderGate(gateFor(false)))
		a.SetStateSync(bus); b.SetStateSync(bus)
		a.Tick(ctx); b.Tick(ctx)
		Eventually(func() int { return len(pa.take()) }).Should(BeNumerically(">", 0))
		Consistently(func() int { return len(pb.take()) }, 200*time.Millisecond).Should(Equal(0))
		Expect(a.IsLeader()).To(BeTrue())
		Expect(b.IsLeader()).To(BeFalse())
	})

	It("new leader keeps activeSince across a leadership move", func() {
		a.ReportFailure("x", errBoom)
		before, _ := b.ChainStatus("chain")
		leaderIsA = false
		clock.Advance(5 * time.Second)
		a.Tick(ctx); b.Tick(ctx)
		after, _ := b.ChainStatus("chain")
		Expect(after.ActiveSince).To(Equal(before.ActiveSince))
		Expect(b.IsLeader()).To(BeTrue())
	})

	It("republish sends every target and chain", func() {
		c := New(src, WithClock(clock), WithLeaderGate(gateFor(false)))
		bus.add(c)
		c.SetStateSync(bus)
		a.ReportFailure("x", errBoom)
		a.Republish()
		st, _ := c.ChainStatus("chain")
		Expect(st.Active).To(Equal("y"))
	})

	It("standalone manager (no sync, no gate) is always leader", func() {
		m := New(src, WithClock(clock))
		m.Tick(ctx)
		Expect(m.IsLeader()).To(BeTrue())
	})
})
```

(`fakeProber` and its `take()` exist in `schedule_test.go`; if the in-flight probe tracking needs waiting, use `Eventually` as shown.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/services/failover/... 2>&1 | tail -5`
Expected: compile failure (`undefined: TargetSnapshot`).

- [ ] **Step 3: Implement** `statesync.go` (types, interface, `WithLeaderGate`, `SetStateSync`, `Apply*`, `IsLeader`, `Republish`, `unlockAndFlush`) and the hooks in `manager.go`/`schedule.go` following the design rules above. Add fields to `Manager`: `sync StateSync`, `gate LeaderGate`, `leader bool` (guarded by `mu`), `applying bool`, `pending []func()`, `ticks int`; to `chainState`: `adopted bool`. Keep every existing test green.

- [ ] **Step 4: Run tests**

Run: `go test -race -count=3 ./core/services/failover/... 2>&1 | tail -10`
Expected: PASS, no races.

- [ ] **Step 5: Commit**

```bash
git add core/services/failover
git commit -m "feat(failover): share state through a sync hook and gate probes on a leader

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 2: syncstate-backed StateSync with a durable pin store

**Files:**
- Create: `core/services/failover/distsync/distsync.go`, `core/services/failover/distsync/pinstore.go`, `core/services/failover/distsync/distsync_suite_test.go`, `core/services/failover/distsync/distsync_test.go`

**Interfaces:**
- Consumes: Task 1 `StateSync`, `TargetSnapshot`, `ChainSnapshot`, `Manager.ApplyTarget/ApplyChain/ApplyPin`; `syncstate.New/Config/Store`; `messaging.MessagingClient`; `advisorylock.WithLockCtx`, `advisorylock.KeySchemaMigrate`; `testutil.NewFakeBus`.
- Produces:
  ```go
  type PinRecord struct {
      Chain     string    `gorm:"primaryKey" json:"chain"`
      Target    string    `json:"target"`
      UpdatedAt time.Time `json:"updated_at"`
  }
  func (PinRecord) TableName() string { return "failover_pins" }
  func NewPinStore(db *gorm.DB) (*PinStore, error) // migrates under KeySchemaMigrate
  func New(ctx context.Context, nats messaging.MessagingClient, pins syncstate.Store[string, PinRecord], m *failover.Manager) (*Sync, error)
  func (s *Sync) Close() error
  // *Sync implements failover.StateSync
  ```

Rules:
- Three maps: `failover.pins` (key chain, `Store: pins` when non-nil — guard against a typed-nil interface as `finetune/service.go` does), `failover.targets` (key target, NATS only), `failover.chains` (key chain, NATS only). No `Reconcile` on NATS-only maps (it would re-hydrate them empty); the leader's `Republish` covers late joiners.
- `OnApply` for targets/chains calls `m.ApplyTarget`/`m.ApplyChain`; for pins `m.ApplyPin(chain, target)` on "set" and `m.ApplyPin(chain, "")` on "delete".
- `New` starts all three maps, then calls `m.SetStateSync(s)` (which hydrates pins from `s.Pins()`).

- [ ] **Step 1: Write the failing tests** — two managers on one `testutil.NewFakeBus()`, each with its own `distsync.New`, sharing an in-memory `syncstate.Store` fake for pins (a map with a mutex implementing `List/Upsert/Delete`). Specs:
  - "a pin on A is visible on B and survives a new instance C built from the same store" (C's `ChainStatus` shows the pin right after `New`).
  - "a trip on B makes A's plan skip the target".
  - "the leader's chain switch reaches the follower".
  - "late joiner converges on heartbeat": build C after A tripped a target; before `A.Republish()` C shows the primary active; after it, the fallback.
  - "PinStore round-trips" using a sqlite gorm DB (`gorm.io/driver/sqlite`, `file::memory:`) if the repo already depends on it (check `go.mod`); otherwise skip this spec and test the adapter with the in-memory store only, and say so in the report.

- [ ] **Step 2: Run to verify failure** — `go test ./core/services/failover/distsync/...` → compile failure.
- [ ] **Step 3: Implement** `distsync.go` and `pinstore.go` per the rules (PinStore: `List` = `db.Find`, `Upsert` = `db.Save`, `Delete` = `db.Delete(&PinRecord{Chain: k})`; migration via `advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error { return db.AutoMigrate(&PinRecord{}) })`).
- [ ] **Step 4: Run tests** — `go test -race ./core/services/failover/...` → PASS.
- [ ] **Step 5: Commit**

```bash
git add core/services/failover/distsync
git commit -m "feat(failover): sync pins, target health and chain state over NATS

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 3: Distributed wiring, warm pins on workers, docs

**Files:**
- Modify: `core/services/advisorylock/keys.go` (add `KeyFailoverProber = 108` with a comment)
- Modify: `core/application/startup.go` (after `application.distributed = distSvc`, ~L294, and before `Run` ~L570), `core/application/distributed.go` (~L380-395, L458: pinned resolver), `core/application/failover.go` (preload only when leader)
- Create: `core/application/failover_distributed.go`, `core/application/failover_distributed_test.go`
- Modify: `docs/content/features/model-failover.md` (replace the per-instance limit with a "Distributed mode" section)

**Interfaces:**
- Consumes: Tasks 1-2; `advisorylock.TryWithLockCtx(ctx, db, key, fn func() error) (bool, error)`; `a.IsDistributed()`, `a.Distributed().Nats`, `a.distributedDB()`; `nodes.PinnedModelResolver` (`GetPinnedModelNames() []string`); `failover.MergePinned`.
- Produces: `func failoverLeaderGate(db *gorm.DB) failover.LeaderGate`; `type failoverPinnedResolver struct{ base nodes.PinnedModelResolver; fm *failover.Manager }` implementing `GetPinnedModelNames()`.

Rules:
- Leader gate: `func(ctx, fn) bool { ok, err := advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyFailoverProber, func() error { fn(); return nil }); if err != nil { xlog.Warn(...); return false }; return ok }`. The failover manager is constructed before distributed init (startup.go ~L257), so give it the gate through a setter `(*Manager).SetLeaderGate(LeaderGate)` added in this task (mirror of `WithLeaderGate`), then call `distsync.New(ctx, distSvc.Nats, pinStore, application.failoverManager)`; on error log and continue standalone.
- Pinned resolver: wrap `configLoader` so `GetPinnedModelNames()` returns `failover.MergePinned(configLoader.GetPinnedModelNames(), fm.WarmTargets())`, and pass it to both the SmartRouter and the ReplicaReconciler options. Adapt `initDistributed`'s signature minimally (add a `pinned nodes.PinnedModelResolver` parameter or set it after construction — choose the smaller change and explain).
- Warm preload: `applyFailoverWarmTargets` keeps the pin sync, and runs the preload goroutine only when `!a.IsDistributed() || a.failoverManager.IsLeader()`.
- `Run` calls `Republish()` every 10 ticks when leader (implemented in Task 1; verify here).

- [ ] **Step 1: Write the failing tests** (`failover_distributed_test.go`, in the application package's existing test suite):
  - `failoverPinnedResolver` merges config pins and warm targets without duplicates.
  - `failoverLeaderGate` with a gorm DB that is not PostgreSQL (the advisorylock package falls back to an in-process lock for non-Postgres DBs): two gates on the same DB — while one is inside `fn`, the other returns false; afterwards the other returns true. Use the same DB setup other `core/application` or `advisorylock` tests use (read `core/services/advisorylock/*_test.go` first).
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement** per the rules; add the `keys.go` constant; update docs: in `model-failover.md`, replace the "Failover state is kept in memory by each LocalAI instance" limit with a `## Distributed mode` section: pins are cluster-wide and persisted; target health and chain state are shared; one frontend (the probe leader) probes, fails back and preloads warm targets; warm targets are pinned on workers; a frontend that joins late converges within 10 s.
- [ ] **Step 4: Run tests** — `go test -race ./core/application/... ./core/services/failover/... ./core/services/advisorylock/...` and `go build ./core/... ./pkg/... ./tests/...` → PASS.
- [ ] **Step 5: Commit**

```bash
git add core/services/advisorylock core/application core/services/failover docs/content/features/model-failover.md
git commit -m "feat(failover): run one prober per cluster and pin warm targets on workers

Assisted-by: Claude:claude-opus-5-5"
```

---

## Part A — localai-proxy backend

### Task 4: Core support — Rerank for Go backends, Unimplemented skip, proxy options

**Files:**
- Modify: `pkg/grpc/interface.go` (add `RerankModel`), `pkg/grpc/server.go` (add `Rerank` handler), `pkg/grpc/model_identity_modalities_test.go` (update the note/test that says Go servers have no Rerank)
- Modify: `core/services/failover/classify.go` (add `IsCapabilityGap`), `core/services/failover/manager.go` (`Do` uses it), `core/http/middleware/failover.go` (retry uses it)
- Modify: `core/backend/options.go:545` (proxy options for `localai-proxy`), `core/config/model_config_loader.go` (load-time warnings)
- Test: `pkg/grpc/server_rerank_test.go` (or the package's existing server test file), `core/services/failover/classify_test.go`, `core/services/failover/manager_test.go`, `core/http/middleware/failover_test.go`, `core/config/model_config_loader_test.go`

**Interfaces:**
- Produces:
  ```go
  // pkg/grpc/interface.go
  type RerankModel interface {
      Rerank(context.Context, *pb.RerankRequest) (*pb.RerankResult, error)
  }
  // core/services/failover/classify.go
  func IsCapabilityGap(err error) bool // gRPC Unimplemented anywhere in the chain
  ```

- [ ] **Step 1: Write the failing tests**
  - `pkg/grpc`: a fake model embedding `base.Base` and implementing `RerankModel` is served by `server.Rerank` (use the package's existing in-process server test pattern for `Score`); a model without it returns `codes.Unimplemented`.
  - `classify_test.go`: `IsCapabilityGap(grpcstatus.Error(codes.Unimplemented, "x"))` true; wrapped with `fmt.Errorf("%w")` true; `codes.Unavailable` false; nil false.
  - `manager_test.go` ("Unimplemented skips without trip"): `m.Do` where target `a` returns `grpcstatus.Error(codes.Unimplemented, "localai-proxy: X has no upstream counterpart")` and `b` succeeds → `tried == [a b]`, `a` stays `StateHealthy`.
  - `failover_test.go` (middleware): handler for `a` returns the same Unimplemented error → served by `b`, `a` healthy.
  - `model_config_loader_test.go`: a `backend: localai-proxy` config with `proxy.mode: translate` logs a warning and still loads; one without `known_usecases` loads (warning). Use the loader test's existing log-capture approach if any; otherwise assert only that loading succeeds and note it.
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement**
  ```go
  // pkg/grpc/server.go — copy of the Score handler shape
  func (s *server) Rerank(ctx context.Context, in *pb.RerankRequest) (*pb.RerankResult, error) {
      if err := s.checkModelIdentity(in); err != nil {
          return nil, err
      }
      rm, ok := s.llm.(RerankModel)
      if !ok {
          return nil, status.Errorf(codes.Unimplemented, "method Rerank not implemented")
      }
      if s.llm.Locking() {
          s.llm.Lock()
          defer s.llm.Unlock()
      }
      return rm.Rerank(ctx, in)
  }
  ```
  (If `checkModelIdentity` does not accept `*pb.RerankRequest`, add the `GetModelIdentity()` method set it needs — `RerankRequest` has field `ModelIdentity`.)
  ```go
  // classify.go
  // IsCapabilityGap reports a target that cannot serve this kind of request at
  // all. The next target may serve it, and this target is not broken.
  func IsCapabilityGap(err error) bool {
      if err == nil {
          return false
      }
      st, ok := grpcstatus.FromError(err)
      return ok && st.Code() == codes.Unimplemented
  }
  ```
  In `Manager.Do`, before the retryable check: `if IsCapabilityGap(err) && !committed.Load() { if !att.Skip() { return err }; continue }`. In `failoverRetry`, treat `failover.IsCapabilityGap(err)` exactly like the admission-rejection branch (spill with `att.Skip()`, no trip). In `options.go` change the condition to `if c.Backend == "cloud-proxy" || c.Backend == "localai-proxy"`. In the loader's load-time pass, `xlog.Warn` for `localai-proxy` configs that set `proxy.mode`/`proxy.provider` (ignored) or have no `known_usecases`.
- [ ] **Step 4: Run tests** — `go test -race ./pkg/grpc/... ./core/services/failover/... ./core/http/middleware/... ./core/config/...` → PASS.
- [ ] **Step 5: Commit**

```bash
git add pkg/grpc core/services/failover core/http/middleware core/backend/options.go core/config
git commit -m "feat(grpc): serve Rerank from Go backends and skip Unimplemented targets

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 5: localai-proxy backend — skeleton, packaging, text methods

**Files:**
- Create: `backend/go/localai-proxy/{main.go,proxy.go,client.go,text.go,Makefile,package.sh,run.sh}`, `backend/go/localai-proxy/{localai_proxy_suite_test.go,fake_upstream_test.go,text_test.go}`
- Modify: root `Makefile` (6 places, mirroring cloud-proxy: `.NOTPARALLEL`, `TEST_PATHS`, `BACKEND_LOCALAI_PROXY = localai-proxy|golang|.|false|true`, `$(eval $(call generate-docker-build-target,$(BACKEND_LOCALAI_PROXY)))`, `docker-build-localai-proxy` in `docker-build-backends`, `build-localai-proxy-backend`/`clean-localai-proxy-backend` e2e helpers building to `tests/e2e/mock-backend/localai-proxy`), `backend/index.yaml` (meta + cpu/metal latest/development images, mirroring cloud-proxy), `.github/backend-matrix.yml` (amd64, arm64, darwin entries mirroring cloud-proxy), `.gitignore` (the e2e binary)

**Interfaces:**
- Consumes: `pkg/grpc` (`StartServer`, `AIModelRich`, `RerankModel`, `ScoreModel`), `pkg/grpc/base.Base`, `pkg/httpclient.New`.
- Produces: `type LocalAIProxy struct { base.Base; cfg atomic.Pointer[proxyConfig]; client *http.Client }`, `func NewLocalAIProxy() *LocalAIProxy`, `type proxyConfig struct { base, upstreamModel, apiKey, realtimePipeline string; timeout time.Duration }`, helpers `func (p *LocalAIProxy) postJSON(ctx, path string, body, out any) error`, `func (p *LocalAIProxy) postMultipart(ctx, path string, fields map[string]string, fileField, filePath string, out any) error`, `func (p *LocalAIProxy) postStream(ctx, path string, body any) (*http.Response, error)`, `func (p *LocalAIProxy) model(req string) string` (upstream model name), `func unimplemented(method string) error` returning `status.Errorf(codes.Unimplemented, "localai-proxy: %s has no upstream counterpart", method)`.

Rules:
- `Load`: requires `opts.GetProxy()` non-nil and a valid `upstream_url` (base URL; strip a trailing `/`); resolves the API key with the same rules as cloud-proxy's `resolveAPIKey` (copy the function); warns if `mode`/`provider` set; reads `realtime_pipeline:<name>` from `opts.GetOptions()`; `request_timeout_seconds` becomes a per-request context timeout for non-streaming calls; model name = `upstream_model`, else `opts.GetModel()`.
- Auth: `Authorization: Bearer <key>` when a key is set. HTTP client from `httpclient.New()` (no redirects).
- Upstream non-2xx → a gRPC error: 5xx/transport → `codes.Unavailable`; 4xx → `codes.InvalidArgument` (so failover does not trip on client errors); body text in the message (truncated to 500 chars).
- Text methods in `text.go`: `PredictRich`/`PredictStreamRich` via `/v1/chat/completions` when `opts.GetMessages()` is non-empty, else `/v1/completions` with `opts.GetPrompt()` (map tokens, temperature, top_p, top_k, stop, seed; stream parses SSE `data:` lines and sends `pb.Reply{Message}` per delta; do not close the channel); legacy `Predict`/`PredictStream` wrap them; `Embeddings` → `/v1/embeddings` (`input` = `opts.GetEmbeddings()`, returns `data[0].embedding`); `Rerank` → `/v1/rerank`; `TokenizeString` → `/v1/tokenize`; `Score` → `/api/score` (read `core/http/endpoints` for the request shape).
- Methods with no counterpart return `unimplemented("<Method>")`: `AudioEncode`, `AudioDecode`, `AudioToAudioStream`, `TokenClassify`, `ModelMetadata`, fine-tune and quantization methods. `Status` keeps the base implementation.

- [ ] **Step 1: Write the failing tests** — `fake_upstream_test.go`: an `httptest.Server` recording method, path, `Authorization`, and JSON/multipart body, with per-path scripted responses (JSON or SSE). `text_test.go` specs: Load rejects missing proxy options and a bad URL; Load parses `realtime_pipeline`; `PredictRich` hits `/v1/chat/completions` with the upstream model and returns the content; `PredictStreamRich` streams SSE deltas in order; `Embeddings`, `Rerank`, `TokenizeString` hit their paths and map results; a 503 upstream → `codes.Unavailable`; a 400 → `codes.InvalidArgument`; `AudioEncode` → `Unimplemented` with the exact message.
- [ ] **Step 2: Run to verify failure** — `go test ./backend/go/localai-proxy/...`.
- [ ] **Step 3: Implement** per the rules; packaging per the Files list (copy cloud-proxy's `Makefile`/`package.sh`/`run.sh` with the binary renamed).
- [ ] **Step 4: Run tests** — `go test -race ./backend/go/localai-proxy/...`; `make -C backend/go/localai-proxy build`; `make build-localai-proxy-backend` → OK. Validate YAML: `python3 -c "import yaml,sys; yaml.safe_load(open('backend/index.yaml')); yaml.safe_load(open('.github/backend-matrix.yml'))"`.
- [ ] **Step 5: Commit**

```bash
git add backend/go/localai-proxy Makefile backend/index.yaml .github/backend-matrix.yml .gitignore
git commit -m "feat(localai-proxy): add a backend that serves text APIs from a remote LocalAI

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 6: localai-proxy — audio methods

**Files:**
- Create: `backend/go/localai-proxy/audio.go`, `backend/go/localai-proxy/audio_test.go`

**Interfaces:** consumes Task 5 helpers.

Mapping (request → upstream → result):

| Method | Upstream | Notes |
|---|---|---|
| `TTS(req)` | `POST /tts` JSON `{model, input: req.Text, voice, language}` | write the response bytes to `req.Dst` |
| `TTSStream(req, out)` | `POST /tts` with `stream: true` | copy the chunked `audio/wav` body to `out` as it arrives (header + PCM, unchanged); close `out` per the base contract |
| `SoundGeneration(req)` | `POST /v1/sound-generation` | write bytes to `req.Dst` |
| `AudioTranscription(ctx, req)` | `POST /v1/audio/transcriptions` multipart: `file` from `req.Dst` (input audio path), `model`, `language`, `translate`, `prompt`, `diarize` | map `TranscriptionResult{text, segments, words, language, duration}` to `pb.TranscriptResult` |
| `AudioTranscriptionStream(ctx, req, out)` | same, plus `stream=true` | SSE `transcript.text.delta` → `TranscriptStreamResponse{Delta}`; `transcript.text.done` → `FinalResult`; `error` event → return `codes.Unavailable` |
| `Diarize` | `POST /v1/audio/diarization` multipart | map segments |
| `VAD(req)` | `POST /v1/vad` JSON `{model, audio: req.Audio}` | map `segments[{start,end}]` |
| `SoundDetection(ctx, req)` | `POST /v1/audio/classification` multipart `file` from `req.Src`, `top_k`, `threshold` | map `detections[{index,label,score}]` |
| `AudioTransform` | `POST /audio/transformations` | read the handler for the request shape; write output to the path the request carries |

Read each `pb` request/response message in `backend/backend.proto` and each REST schema in `core/schema` before mapping; keep field names exact.

- [ ] **Step 1: Write the failing tests** — one spec per row using the fake upstream: path, multipart fields (file bytes equal the input file), JSON body, and result mapping; `TTS` writes the upstream bytes to `Dst`; `TTSStream` forwards chunks in order and the first chunk starts with `RIFF`; `AudioTranscriptionStream` emits deltas then the final result; upstream disconnect mid-stream → `codes.Unavailable`.
- [ ] **Step 2: Verify failure.** **Step 3: Implement.** **Step 4:** `go test -race ./backend/go/localai-proxy/...` → PASS.
- [ ] **Step 5: Commit** — `feat(localai-proxy): serve speech, transcription and audio APIs remotely`.

---

### Task 7: localai-proxy — image, video, 3D and vision methods

**Files:**
- Create: `backend/go/localai-proxy/media.go`, `backend/go/localai-proxy/media_test.go`

Mapping:

| Method | Upstream | Notes |
|---|---|---|
| `GenerateImage(req)` | `POST /v1/images/generations` `{model, prompt, negative_prompt, size: "WxH", step, seed, response_format: "b64_json"}`; `req.Src`/`ref_images` sent as base64 in `files`/`ref_images` per `core/schema` | decode `data[0].b64_json` into `req.Dst` |
| `UpscaleImage` | `POST /v1/images/upscale` | same output handling |
| `GenerateVideo` | `POST /video` | write the returned file (b64 or URL download relative to the upstream base) to `Dst` |
| `Generate3D`, `Animate3D` | `POST /3d/generations`, `/3d/animate` | same output handling; implement `AnimationMetadataModel` only if the upstream response carries the metadata |
| `Detect`, `Depth` | `POST /v1/detection`, `/v1/depth` | map results |
| `FaceVerify`, `FaceAnalyze` | `POST /v1/face/verify`, `/v1/face/analyze` | map results |
| `VoiceVerify`, `VoiceAnalyze`, `VoiceEmbed` | `POST /v1/voice/verify`, `/v1/voice/analyze`, `/v1/voice/embed` | map results |
| `StoresSet/Get/Delete/Find` | `POST /stores/set`, `/stores/get`, `/stores/delete`, `/stores/find` | map keys/values |

When the upstream returns a URL instead of b64, download it with the same client (same auth) and write it to `Dst`.

- [ ] **Step 1: Failing tests** — one spec per row with the fake upstream (path, key request fields, `Dst` written from b64 and from a URL). **Step 2** verify failure. **Step 3** implement. **Step 4** `go test -race ./backend/go/localai-proxy/...` → PASS.
- [ ] **Step 5: Commit** — `feat(localai-proxy): serve image, video, 3D and vision APIs remotely`.

---

### Task 8: localai-proxy — live transcription bridge

**Files:**
- Create: `backend/go/localai-proxy/live.go`, `backend/go/localai-proxy/live_test.go`

**Interfaces:** `func (p *LocalAIProxy) AudioTranscriptionLive(in <-chan *pb.TranscriptLiveRequest, out chan<- *pb.TranscriptLiveResponse) error`. Contract (from `pkg/grpc/server.go:477-541`): the backend closes `out`; `in` closes on client EOF; return errors immediately (callers wait for the ready ack).

Protocol (upstream `github.com/gorilla/websocket`):
1. No `realtime_pipeline` → `close(out); return grpcerrors.LiveTranscriptionUnsupported("localai-proxy", "set the realtime_pipeline backend option")` (same helper `base.Base` uses).
2. Read the first `in` message; it must be `config` (else `codes.InvalidArgument`). Rate = `config.sample_rate` or 16000.
3. Dial `ws(s)://<base>/v1/realtime?model=<realtime_pipeline>` with the bearer header. Read `session.created`.
4. Send `{"type":"session.update","session":{"type":"transcription","audio":{"input":{"format":{"type":"audio/pcm","rate":<rate>},"transcription":{"model":"<realtime_pipeline>","language":"<lang>"},"turn_detection":{"type":"server_vad"}}}}}`. On `session.updated` send `TranscriptLiveResponse{Ready: true}`; on `error` return `codes.Unavailable` with its message.
5. Writer goroutine: each `audio.pcm` (float32 in [-1,1]) → PCM16 LE → base64 → `{"type":"input_audio_buffer.append","audio":"..."}`.
6. Reader goroutine: `conversation.item.input_audio_transcription.delta` → `{Delta}`; `...completed` → `{Delta: <transcript minus text already sent for this item>, Eou: true}` and append to the running final text; `...failed` or `error` → end with `codes.Unavailable`.
7. When `in` closes: wait up to 5 s for an in-flight `completed` (track `speech_started` without a matching completion), then send `{FinalResult: {Text: <all completed text>}}`, close the socket, close `out`, return nil.
8. Socket read error before step 7 → close `out`, return `status.Error(codes.Unavailable, ...)`.

- [ ] **Step 1: Failing tests** with an `httptest` WebSocket server (gorilla `Upgrader`) scripting the upstream: unsupported without the option; ready after `session.updated`; audio frames arrive base64-PCM16 of the right length; deltas and a completion map to `Delta`/`Eou`; closing `in` yields `FinalResult` with the concatenated text; "upstream disconnect ends the stream" (server closes mid-session → `Unavailable`, `out` closed, no goroutine left blocked — assert the call returns within 2 s).
- [ ] **Step 2** verify failure. **Step 3** implement. **Step 4** `go test -race ./backend/go/localai-proxy/...` → PASS.
- [ ] **Step 5: Commit** — `feat(localai-proxy): bridge live transcription to the upstream realtime API`.

---

### Task 9: localai-proxy end to end, and docs

**Files:**
- Modify: `tests/e2e/e2e_suite_test.go` (build/locate the `localai-proxy` binary like cloud-proxy), create `tests/e2e/e2e_localai_proxy_test.go`, extend `tests/e2e/realtime_ws_test.go` (label `failover`)
- Modify: docs — the page that documents `cloud-proxy` (find with `grep -rln "cloud-proxy" docs/content`) gets a `localai-proxy` section; `docs/content/features/model-failover.md` gets a remote-LocalAI example with per-stage chains.

Rules:
- Point `localai-proxy` models at the test server itself (`proxy.upstream_url` = the suite's base URL, `upstream_model` = an existing mock model), registered at runtime the way the cloud-proxy/failover e2e specs register models.
- Specs: chat, embeddings, TTS and transcription through `localai-proxy` return 2xx with the upstream model's answer; a chain `[proxy-target, local mock]` where the proxy target's upstream model is `fail-load-…` fails over to the local target; a realtime pipeline whose `llm` stage is a chain `[localai-proxy → mock LLM, mock LLM]` completes a turn and, after the proxy target is made to fail (point it at a `fail-load` upstream model or stop routing), switches stage with a `localai.model.failover` event. Live transcription through the bridge is covered by Task 8's unit specs; add an e2e only if the suite has a pipeline with streaming transcription available.
- Run `make build-localai-proxy-backend build-mock-backend` first.

- [ ] Steps: write specs → run (fail) → implement registration + docs → run `go run github.com/onsi/ginkgo/v2/ginkgo --label-filter=failover -v ./tests/e2e` and the full `!real-models` e2e → commit `test(localai-proxy): proxy APIs and realtime stages end to end`.

---

## Part B — WebUI

### Task 10: Failover data layer and health strip

**Files:**
- Modify: `core/http/react-ui/src/utils/config.js` (endpoints), `src/utils/api.js` (`failoverApi`), `src/components/StatusPill.jsx` (tones)
- Create: `src/hooks/useFailoverChains.js`, `src/components/FailoverChainStatus.jsx`
- Modify: `src/pages/ModelEditor.jsx` (strip after the `me-head` block, before the template selector, when `!isCreateMode` and the chain exists), `src/App.css` (classes), `public/locales/*/models.json` (strings, 8 locales)
- Test: `e2e/failover-health.spec.js`

**Interfaces — Produces:**
```js
// utils/config.js endpoints
failoverChains: '/api/failover',
failoverChain: (name) => `/api/failover/${encodeURIComponent(name)}`,
failoverEvents: '/api/failover/events',
failoverPin: (name) => `/api/failover/${encodeURIComponent(name)}/pin`,
// utils/api.js
export const failoverApi = {
  list: () => fetchJSON(API_CONFIG.endpoints.failoverChains),
  get: (name) => fetchJSON(API_CONFIG.endpoints.failoverChain(name)),
  pin: (name, target) => postJSON(API_CONFIG.endpoints.failoverPin(name), { target }),
  unpin: (name) => fetchJSON(API_CONFIG.endpoints.failoverPin(name), { method: 'DELETE' }),
  eventsUrl: () => API_CONFIG.endpoints.failoverEvents,
}
// hooks/useFailoverChains.js
export default function useFailoverChains() // → { chains: ChainStatus[], byName: {[name]: ChainStatus}, loading, error, refresh }
```
Hook rules: initial `failoverApi.list()`; `new EventSource(apiUrl(failoverApi.eventsUrl()))`; `snapshot` → replace `chains` from `data.chains`; `chain.switched` → patch that chain's `active`, `state`, `active_since = data.at`; `target.state` → patch that target's `state`, `last_error = data.error` in every chain containing it; `onerror` no-op; poll `list()` every 15 s; close on unmount.

`StatusPill` STATUS map additions: `primary: 'success'`, `fallback: 'warning'`, `recovering: 'warning'`, `degraded: 'error'`, `down: 'error'`, `missing: 'muted'` (`healthy` already maps to success).

`FailoverChainStatus({ chain, onPin, onUnpin, canPin })` renders: chain `StatusPill` + active target + relative "since"; a compact table of targets (model, kind, warm, `StatusPill` state, last probe, last error truncated with title); per-target **Pin** button and a chain-level **Unpin** when `canPin`, each behind `ConfirmDialog`. Classes only.

- [ ] **Step 1: Failing Playwright spec** `e2e/failover-health.spec.js` (import `test` from `./coverage-fixtures.js`; mock `**/api/auth/status`, config metadata, `**/api/failover` with one chain `chain` [a healthy active, b healthy], `**/api/failover/chain`, and `**/api/failover/events` fulfilled with `text/event-stream` body `event: snapshot\ndata: {"chains":[...]}\n\nevent: chain.switched\ndata: {"chain":"chain","from":"a","to":"b","state":"fallback","reason":"trip","at":"2026-09-26T10:00:00Z"}\n\n`). Assertions: `/app/model-editor/chain` shows the strip with the chain state; after the event the active target is `b` and the pill reads fallback; the Pin button is visible with auth disabled (admin) and hidden when auth status reports a non-admin user (mock `/api/auth/me` the way `users-tab-gating.spec.js` does); clicking Pin + confirm POSTs `{target}`.
- [ ] **Step 2** run `cd core/http/react-ui && npx playwright test e2e/failover-health.spec.js` (after `bun run build` if the harness serves the build; follow the repo's UI test instructions in `Makefile` `test-ui*` targets) → FAIL.
- [ ] **Step 3** implement; **Step 4** re-run → PASS; `npm run lint` and `npm run lint:inline-styles` (or the scripts in `package.json`) clean.
- [ ] **Step 5: Commit** — `feat(ui): show live failover chain health in the model editor`.

---

### Task 11: Chain editor field and template

**Files:**
- Create: `core/http/react-ui/src/components/FailoverTargetsEditor.jsx`
- Modify: `src/components/ConfigFieldRenderer.jsx` (branch `component === 'failover-targets'`, same `list-row` wrapper as `router-candidates`), `src/utils/modelTemplates.js` (template), `src/pages/ModelEditor.jsx` (`SECTION_ICONS.failover = 'fa-shuffle'`, `SECTION_COLORS.failover = 'var(--color-accent)'`), `core/config/meta/registry.go` (`failover.targets` `Component: "failover-targets"`), `core/config/meta/registry_test.go` (assert the component), `public/locales/*/modelEditor.json`
- Test: `e2e/failover-editor.spec.js`

`FailoverTargetsEditor({ value, onChange })`: modelled on `RouterCandidatesEditor` — items `{model, warm}`; row = `SearchableModelSelect` (value/onChange), `Toggle` for warm (disabled with a title when the selected model's backend is a proxy — look it up from `useModels()` data if it carries the backend; otherwise leave enabled and rely on the load warning, and say so), move up/down, remove; "Add target" button; inline errors (fewer than 2 targets, duplicate model, the edited model's own name) from `useFormContext()` `formData.name`.

Template entry:
```js
{
  id: 'failover',
  label: 'Failover Chain',
  icon: 'fa-shuffle',
  description: 'Serve one model name from an ordered list of models. The first healthy one answers; the next takes over when it fails.',
  fields: {
    'name': '',
    'failover.targets': [{ model: '' }, { model: '' }],
  },
},
```

- [ ] Steps: failing spec (template card visible; `?template=failover` shows two target rows; adding/removing/moving rows; duplicate and too-few errors; saving sends `failover.targets` in the PATCH/import body — mock the save endpoint and assert the JSON) → implement → `npx playwright test e2e/failover-editor.spec.js` PASS, `go test ./core/config/meta/...` PASS → commit `feat(ui): edit failover chain targets with a dedicated field`.

---

### Task 12: Chain badge and Failover overview page

**Files:**
- Create: `core/http/react-ui/src/pages/Failover.jsx`
- Modify: `src/pages/InstalledModels.jsx` (chain badge next to the alias badge: `badge badge-info`, icon `fa-shuffle`, text `chain → <active>`; data from `useFailoverChains()`), `src/router.jsx` (`const Failover = page('failover', () => import('./pages/Failover'))`; route `{ path: 'failover', element: <Admin><Failover /></Admin> }`), `src/components/console/consoleConfig.js` (`operate.runtime` item `{ path: '/app/failover', icon: 'fas fa-shuffle', labelKey: 'items.failover', adminOnly: true }`), `public/locales/*/nav.json`, `public/locales/*/models.json`, `src/App.css`
- Test: `e2e/failover-overview.spec.js`

Overview: `useFailoverChains()`; a dense table (chain name link → `/app/model-editor/<name>`, chain `StatusPill`, active target, one small pill per target, time since `active_since`); empty state with a link to `/app/model-editor?template=failover`.

- [ ] Steps: failing spec (nav entry visible for admin; table rows from mocked `/api/failover`; SSE patch updates a row; empty state link; installed-models badge `chain → a`) → implement → specs PASS; run the full UI suite with coverage: `make test-ui-coverage-check` (UI coverage ≥ baseline) → commit `feat(ui): list failover chains and badge chain models`.

---

## Part D — Contributor rule and final verification

### Task 13: distributed-state rule, docs sweep, final verification

**Files:**
- Create: `.agents/distributed-state.md`
- Modify: `AGENTS.md` (Topics table row; Quick Reference bullet), `.agents/api-endpoints-and-auth.md` (checklist line), `docs/content/features/model-failover.md` (UI section: editor, health strip, overview; confirm distributed section from Task 3)

`.agents/distributed-state.md` content (write it in full, following the style of the other `.agents/*.md` guides):
- Title "Distributed-aware state". Why: frontends are stateless replicas; in-memory state diverges silently (the failover chains example).
- The rule: a feature that keeps runtime state (in-memory maps, caches, pins, schedulers, background loops, probes) chooses one mode and documents it on its docs page:
  1. **Shared** — `syncstate.SyncedMap` (`core/services/syncstate`); add a `Store` when the state must survive a restart. Example: finetune jobs (`core/services/finetune/service.go`), failover pins (`core/services/failover/distsync`). Gotcha: `Reconcile` without a `Store`/`Loader` re-hydrates the map empty — republish from a leader instead.
  2. **Single-runner** — `advisorylock.RunLeaderLoop` / `TryWithLockCtx` (`core/services/advisorylock`); new keys go in `keys.go`. Example: node health monitor (`core/services/nodes/health.go`), failover prober.
  3. **Stateless per request** — nothing to share.
  4. **Per-instance** — allowed only with the reason written in the feature's docs.
- Tests: shared and single-runner features include a two-instance test on `testutil.NewFakeBus()` (`core/services/testutil/fakebus.go`); note that the fake bus delivers synchronously, including the publisher's own message, so never publish while holding a lock the apply path takes.
- Checklist for PRs.

AGENTS.md Quick Reference bullet:
`- **Distributed-aware state**: any feature that keeps runtime state (maps, caches, pins, schedulers, background loops) must choose shared (syncstate), single-runner (advisorylock), stateless, or documented per-instance behaviour for multi-frontend clusters. See [.agents/distributed-state.md](.agents/distributed-state.md).`

AGENTS.md Topics row:
`| [.agents/distributed-state.md](.agents/distributed-state.md) | Features that keep runtime state — how they must behave with several frontends (syncstate, advisory-lock leaders, fakebus tests) |`

api-endpoints-and-auth.md checklist line (under Quality):
`- [ ] Stateful feature: distributed mode chosen and documented (see [distributed-state.md](distributed-state.md))`

- [ ] Steps: write the files → final verification, in order, reading each output:
```bash
make protogen-go build-mock-backend build-cloud-proxy-backend build-localai-proxy-backend
go vet ./core/services/failover/... ./backend/go/localai-proxy/... ./pkg/grpc/...
go test -race ./core/services/failover/... ./core/application/... ./core/config/... ./core/http/middleware/... ./pkg/grpc/... ./pkg/mcp/localaitools/... ./backend/go/localai-proxy/... ./core/http/endpoints/openai/...
LOCALAI_TEST_HTTP_PORT=19391 go test ./core/http/...
go run github.com/onsi/ginkgo/v2/ginkgo --label-filter='!real-models' -v ./tests/e2e
make test-ui-coverage-check
LOCALAI_TEST_HTTP_PORT=19391 make test-coverage-check
```
Expected: all green; both coverage checks at or above baseline. Record any pre-existing failure (e.g. `make swagger`) with its output tail.
- [ ] Commit — `docs: require distributed-aware state for stateful features`.
