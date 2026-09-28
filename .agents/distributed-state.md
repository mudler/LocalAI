# Distributed-aware state

Every frontend process is a stateless replica: any of them can take a
request, and any of them can be the one that starts, stops, or dies first.
Runtime state that lives only in a Go map on one frontend — an in-memory
cache, a set of pins, a scheduler's next-run time, a background probe loop —
diverges silently the moment there is more than one frontend. Each replica
believes its own copy, clients see different answers depending on which
frontend they hit, and nothing errors: the failure mode is quiet disagreement,
not a crash.

The failover chains feature is the concrete example this rule generalizes
from: chain pins, target health, and the active target of each chain used to
live in a plain map per frontend. Two frontends could disagree about which
target a chain was using, and a pin set through one frontend would be
invisible through another.

## The rule

Before you add runtime state (an in-memory map, a cache, a set of pins, a
scheduler, a background loop, a health prober, anything that is not re-derived
fresh from the database or the request itself), pick one of the four modes
below and write down which one you picked on the feature's own docs page
under `docs/content/`. "I didn't think about it" is not one of the modes.

### 1. Shared

Use `syncstate.SyncedMap` (`core/services/syncstate`) when every frontend
needs to see the same state and any frontend may be the one that reads or
writes it next. Deltas replicate over the message bus to every subscriber,
including the publisher, so all frontends converge without an extra polling
loop.

Add a `Store` (`syncstate.Config.Store`) when the state must survive a
restart of the whole cluster — the store is the durable source of truth, and
new frontends rehydrate from it instead of starting empty. Without a `Store`,
give a `Loader` for local, disk-backed rehydration in standalone mode.

Examples:
- Finetune jobs — `core/services/finetune/service.go`. A `Store` persists jobs
  across restarts; without one, a `Loader` reloads from disk.
- Failover pins, targets, and chains — `core/services/failover/distsync`.
  `failover.pins` is `Store`-backed (pins must survive a restart);
  `failover.targets` and `failover.chains` are ephemeral live-health state
  with no `Store`, republished by the leader every 10 s.

**Gotcha:** `Reconcile` with neither a `Store` nor a `Loader` does **nothing**
— there is nothing for it to pull from, so it cannot help a late joiner catch
up. If your map has no durable backing, a leader (or another privileged
writer) must republish its live state after a reconnect instead of relying on
`Reconcile` to recover it.

**Gotcha:** a hydrate (on `Start`, after a NATS reconnect, and on every
`Reconcile` tick) replaces the map's contents **without firing `OnApply`**.
If `OnApply` feeds derived state (the failover manager's pins, a cache, a
running process), that state stays stale after a reconnect or a repaired
missed delta. Re-sync the derived state from the map's `Snapshot()`: on a
periodic tick, and/or from an `OnReconnect` callback registered after the
map's `Start` (callbacks run in registration order, so the map has already
re-hydrated). `failover.Manager.ReconcilePins` is the example.

### 2. Single-runner

Use `advisorylock.RunLeaderLoop` or `advisorylock.TryWithLockCtx`
(`core/services/advisorylock`) when exactly one frontend in the cluster should
run some work on a schedule, and it does not matter which one. Add your lock
key to `keys.go` — keys are a single global namespace across the database, so
pick a number that is not already taken.

`RunLeaderLoop`/`TryWithLockCtx` leadership is **not sticky**: the lock is
acquired and released around each tick, so a different frontend can win it
next time. That is fine for idempotent, tick-shaped work (a cleanup pass, a
periodic scan) where it does not matter who ran the last one.

When leadership must be sticky — the same frontend keeps a role across many
ticks, for example because it holds live connections or in-memory session
state tied to that role — use `advisorylock.HeldLock` instead. It keeps a
dedicated database session and PostgreSQL TCP keepalives so a dead host's
lock is freed in about 30 s rather than after the OS's multi-hour keepalive
default. The failover prober (`core/application/failover_distributed.go`)
uses `HeldLock` for exactly this reason: it needs to stay the same leader
across probe ticks, not re-elect on every one.

Examples:
- Tick-shaped, non-sticky: the node health monitor
  (`core/services/nodes/health.go`), via `TryWithLockCtx`.
- Sticky: the failover prober (`core/application/failover_distributed.go`),
  via `HeldLock`.

### 3. Stateless per request

Nothing to share: the feature re-derives everything it needs from the
request, the database, or another already-distributed source, so there is no
in-memory state to diverge. Most handlers are this by default — no action
needed beyond noting it if a reviewer might otherwise ask.

### 4. Per-instance (documented exception)

Some state is legitimately local to a frontend — a warm in-process cache
that only saves work when it is warm, a local rate limiter for the process's
own outbound connections. This is allowed, but only when the feature's docs
page says so explicitly and states the consequence (which behavior differs
between frontends, and why that is acceptable). An undocumented per-instance
map is a bug, not a design choice.

## Tests

A shared or single-runner feature includes a **two-instance test** that
proves the two frontends actually agree, not just that each one works alone.
Build it on `testutil.NewFakeBus()` (`core/services/testutil/fakebus.go`):
construct two instances of the feature against the same fake bus, mutate
state through one, and assert the other observes it.

`FakeBus` delivers every publish **synchronously**, including back to the
publisher itself — that is what makes the two-instance test deterministic
without polling, but it is also a trap: **never publish while holding a lock
that the apply path also takes.** The publish call re-enters your own apply
handler on the same goroutine before `Publish` returns, so a lock held across
the publish deadlocks against itself. Release the lock, then publish.

## Checklist

When your PR adds or changes state that lives longer than a single request:

- [ ] Mode chosen: shared (`syncstate.SyncedMap`) / single-runner
      (`advisorylock`) / stateless / documented per-instance
- [ ] If shared: `Store` added if the state must survive a cluster restart,
      or a `Loader` for standalone rehydration; if neither, a leader
      republishes after reconnect instead of relying on bare `Reconcile`;
      state derived through `OnApply` re-syncs from `Snapshot()` after a
      hydrate (hydrate fires no `OnApply`)
- [ ] If single-runner: new lock key added to `keys.go`; `HeldLock` chosen
      over `RunLeaderLoop`/`TryWithLockCtx` if leadership must be sticky
      across ticks
- [ ] Publishing to the shared state never happens while holding a lock the
      apply path also takes (see the `FakeBus` gotcha above)
- [ ] Two-instance test on `testutil.NewFakeBus()` for shared/single-runner
      state
- [ ] The chosen mode (and, for per-instance, the reason) is written on the
      feature's docs page under `docs/content/`
- [ ] Standalone mode (no NATS/DB) still behaves correctly — most
      shared/single-runner code paths must degrade gracefully alone
