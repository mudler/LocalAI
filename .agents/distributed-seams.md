# Distributed seams

Distributed mode talks to other processes through a few interfaces. Code above
them must not name NATS, and any new transport is an implementation of them.

| Seam | Interface | Lives in | Today |
|---|---|---|---|
| Fan-out | `messaging.Broadcaster` | `core/services/messaging` | NATS client |
| Control verbs | `nodes.NodeCommandSender`, `nodes.FileStager` | `core/services/nodes` | NATS request/reply (`RemoteUnloaderAdapter`, `S3NATSFileStager`), or HTTP for files (`HTTPFileStager`) |
| Worker gRPC dial | `nodes.BackendClientFactory` (the factory takes the node id) | `core/services/nodes` | direct dial |

## Caveats in the current tree

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

On `ErrNoRoute` the only change to the node's own state is the status-only
`MarkUnhealthy`, which the next heartbeat reverses. A caller may also route
around the node (the scheduler skips it, and an upgrade falls back to the older
install subject). Never delete `node_models` rows on it. Timeouts are not
`ErrNoRoute`.

A pending backend op that fails with `ErrNoRoute` is still recorded as a failed
attempt (`RecordPendingBackendOpFailure`, the reconciler's attempt count, and
the dead-letter after the maximum attempts). That is accounting for the op, not
a verdict about the node.

The carrier's own sentinel (`nats.ErrNoResponders`) is mapped onto
`ErrNoRoute` in `core/services/nodes/control_nats.go` and nowhere else. A
consumer that matches on the carrier error reads absence as a fact about the
node, which is the mistake this contract exists to prevent.

## Testing a new carrier

Run it against `messagingtest.RunBroadcasterConformance`
(`core/services/messaging/messagingtest`) in its own package. `FakeBus` runs it
in `core/services/testutil`. The NATS run is in `core/services/messaging`; it
needs Docker. Without Docker it skips, except when `CI` is set on a non-macOS
runner, where it fails so a runner without Docker cannot hide the check.
