# Add-on owner as its own role

Status: proposed. No code change yet.

## Problem

In distributed mode the add-on owner runs inside each frontend process. The owner
takes an exclusive `flock` on `owner.lock` in the add-on state root. Frontends
that share one state volume therefore cannot all run an owner. Two effects follow:

- Scaling frontends does not scale add-ons, and a second replica on the same
  volume fails to start with `addon storage already owned`.
- A rolling update overlaps the old and new pods. The new pod cannot take the lock
  until the old one stops, so a rollout either stalls or leaves two builds serving.

Today the workaround is one frontend replica with a `Recreate` strategy. That is
a deployment fix, not a design.

## What already exists

- Frontends forward every add-on request to the owner over a pinned mutual-TLS
  carrier (`core/services/addons/gateway/carrier.go`, configured by
  `OwnerAddress`). The split between frontend and owner is already in the code.
- The owner is a singleton by design: one owner per state root, no election, no
  automatic failover (`docs/content/features/addons.md`, "Singleton owner and its
  limits").
- Owner state is spread over files in the state root: `identity.json`,
  `journal.json`, `metadata/<addon>/grants.json`, `sources.json`, and the package
  store under `packages/`. Tables keeps its own SQLite database, chosen by the
  add-on itself.

So the carrier and the singleton rule stay. What changes is where the owner runs
and where its state lives.

## Decisions

1. **The owner can run as its own process.** A role, not a new binary, selected
   at start. Single mode keeps running frontend and owner in one process over
   loopback. Distributed mode runs the owner as a separate workload and the
   frontends reach it through the existing carrier. The owner code path is the
   same in both modes.
2. **Durable owner state moves to a database when one is configured.** The
   identity, journal, grants, sources and package records are stored behind a
   storage interface. The default backend is the current local file layout, so
   single mode needs no setup. A database backend is selected in configuration and
   is the normal choice for distributed mode.
3. **Single owner is enforced by a lock that matches the storage.** On the file
   backend, `owner.lock` keeps two owners from running on the same state root. It
   is a node-local guard, not a scaling mechanism. On the database backend, the
   owner takes a sticky leader lock with `advisorylock.HeldLock`
   (`core/services/advisorylock`, see `.agents/distributed-state.md`). That lock
   is held on a dedicated session with keepalives, so a dead owner frees it in
   about 30 seconds, and it works across nodes.
4. **Storage is per add-on.** An add-on decides its own store. Tables may use
   SQLite, or accept MySQL or PostgreSQL credentials. The owner only provides the
   interface and the lifecycle. It does not force one engine on add-ons.
5. **Failover is manual at first.** Moving the owner means starting it elsewhere
   against the same database. A lease-based election can follow later, on top of
   the same storage.

## Consequences

- Frontends need the owner address and its pinned identity. That is one extra
  hop and one more service to run in distributed mode.
- An owner outage is still an add-on outage. The frontends stay up and report it.
- Database-backed state removes the node-pinned volume. The owner can start on any
  node that can reach the database.
- The same storage interface serves single mode, so both modes exercise the same
  code.

## Open questions

- Role naming and its flag: `--addon-owner` on the existing command, or a
  subcommand.
- Whether the owner gets its own volume when the database option is not used.
- Which state moves first. Grants and sources are the smallest slice.
- How the frontend discovers the owner address in a deployment.

## Phases

1. Storage interface behind the current file layout. No behaviour change, covered
   by the existing tests.
2. Owner role and separate-process startup, reusing the carrier. Single mode
   unchanged.
3. Database backend for the owner state, selected in configuration.
4. Deployment guidance: owner as a single-replica workload, frontends stateless
   and scalable.
