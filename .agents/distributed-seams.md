# Distributed seams

Distributed mode talks to other processes through a few interfaces. Code above
them must not name NATS, and any new transport is an implementation of them.

| Seam | Interface | Lives in | Today |
|---|---|---|---|
| Fan-out | `messaging.Broadcaster` | `core/services/messaging` | NATS client |
| Control verbs | `nodes.NodeCommandSender`, `nodes.FileStager` | `core/services/nodes` | NATS request/reply (`RemoteUnloaderAdapter`, `S3NATSFileStager`), or HTTP for files (`HTTPFileStager`) |
| Worker gRPC dial | `nodes.BackendClientFactory`, optional `nodes.NodeBackendClientFactory` | `core/services/nodes` | direct dial, with one exception (below) |

## Caveats in the current tree

- The dial seam does not cover every dial yet. Every `BackendClientFactory`
  call goes through `newBackendClient` in `core/services/nodes/interfaces.go`,
  but `grpcModelProber.Probe` in `core/services/nodes/reconciler.go` dials the
  backend address directly and bypasses the factory. Before a non-direct
  dialer (for example a tunnel) is added, `ModelProber.Probe` needs a node id
  and must be routed through the factory, or the reconciler's liveness probe
  will try to reach an address that is not routable from the frontend.
- When a factory implements both `NewClient` and `NewNodeClient`,
  `NewNodeClient` wins. A wrapper around a factory must override both.
- `FileStager` implementations do not report absence the same way.
  `S3NATSFileStager` returns `nodes.ErrNoRoute` when nothing is listening for
  the node. `HTTPFileStager` reports connection failures as ordinary errors.

## Rules a carrier must keep

- Subjects: one closed set of roots, the `broadcastRoots` and `controlRoots`
  maps in `core/services/messaging/subject_rules.go` (read them with
  `messaging.BroadcastRoots()` and `messaging.ControlRoots()`). Add a root
  there, with a test row, not in a carrier. `messaging.ValidateSubject` refuses
  anything else with `messaging.ErrUnservedSubject`.
- Wildcards: only a whole single token `*`, never the root. `>` is refused with
  `messaging.ErrUnsupportedWildcard`. `messaging.MatchSubject` is the one
  spelling of the matching rule.
- Delivery is at-most-once. Anything that must survive a gap belongs in a table.
- A subscriber that is reconnecting misses messages. Do not read silence as
  evidence about a node.

## Four conditions that are never reported as each other

1. A routing fact: no route from here right now (`nodes.ErrNoRoute`).
2. A connection absent within the reconnect grace: nothing acts on it.
3. An unreachable peer: not a verdict about the worker.
4. The worker's own answer, including a refusal: the worker is present.

The only state change allowed on `ErrNoRoute` is the status-only
`MarkUnhealthy`, which the next heartbeat reverses. A caller may also route
around the node (the scheduler skips it, and an upgrade falls back to the older
install subject). Never delete `node_models` rows on it. Timeouts are not
`ErrNoRoute`.

The carrier's own sentinel (`nats.ErrNoResponders`) is mapped onto
`ErrNoRoute` in `core/services/nodes/control_nats.go` and nowhere else. A
consumer that matches on the carrier error reads absence as a fact about the
node, which is the mistake this contract exists to prevent.

## Testing a new carrier

Run it against `messagingtest.RunBroadcasterConformance`
(`core/services/messaging/messagingtest`) in its own package. `FakeBus` runs it
in `core/services/testutil`. The NATS run is in `core/services/messaging`; it
needs Docker and skips without it.
