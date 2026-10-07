# Distributed seams

Distributed mode talks to other processes through a few interfaces. Code above
them must not name NATS, and a new transport is a new implementation of them.
Today the fan-out seam has two carriers: NATS, and `core/services/pgbus`, which
uses PostgreSQL LISTEN and NOTIFY. Every other seam has one: NATS, or a direct
dial for the gRPC and HTTP connections to a worker. Only the carrier files import
the NATS libraries (`core/services/messaging/client.go`, `tls.go`,
`core/services/nodes/control_nats.go` and `pkg/natsauth`). Only the pgbus package
imports `github.com/jackc/pgx`, and only `core/services/carrier/pgbus.go` imports
the pgbus package. Only `core/services/tunnel` imports yamux. A spec in
`core/services/carrier` fails when any other
non-test file imports one of them; it reads the import lines of every Go file, so
a build constraint does not hide one. A new carrier adds its library and its
files to that spec.

| Seam | Interface | Lives in | Today |
|---|---|---|---|
| Fan-out | `messaging.Broadcaster` | `core/services/messaging` | NATS client |
| Queues | `messaging.WorkQueue` (producer), `messaging.WorkConsumer` (worker), keyed by `messaging.WorkKind` | `core/services/messaging` (`workqueue.go`) | `NewNATSWorkQueue`, `NewNATSWorkConsumer` |
| Control verbs, frontend half | `nodes.NodeCommandSender`, `nodes.FileStager` | `core/services/nodes` | `RemoteUnloaderAdapter`, `S3NATSFileStager` over NATS request/reply; `HTTPFileStager` over HTTP |
| Control verbs, worker half | unexported `controlServer` (`handle`, `handleWithProgress`) | `core/services/worker` | `natsControlServer` |
| Agent RPC, frontend half | `AgentControl` (package `mcp`) | `core/http/endpoints/mcp` | `nodes.NATSAgentControl` |
| Agent RPC, worker half | unexported `agentRPCServer` | `core/cli/agent_worker.go` | `nodes.NATSAgentRPCServer` |
| Dial to a worker | `nodes.BackendClientFactory`, `nodes.ModelProber`, `nodes.WorkerNetDialerFor` | `core/services/nodes` | direct dial |

Request and reply payloads of the control verbs live in
`core/services/workerctl`, so the frontend half, the worker half and any
carrier share one wire format without importing each other.

## Holders and the carrier row

Code above the seams does not hold a carrier. It holds a forwarding holder from
`core/services/carrier`, one per seam: `Broadcaster`, `WorkQueue`, `Commands`
(`nodes.NodeControl`), `Files`, `Clients`, `NewWorkerDialer` and `Agents`. Each
call loads an `atomic.Pointer[carrier.Set]` once and calls the same method on
that set. A `carrier.Set` is the implementation of every seam for one carrier.
Build a set completely and call `Validate` before you store it. The holders take
no lock and allocate nothing, and a call that is already running is never moved.
`carrier.NewNATSSet` is the one place that builds the NATS set.

`NodeControl` is the whole control surface a carrier gives the frontend:
`NodeCommandSender`, the process lister, the three unload interfaces the model
loader reads by type assertion, and the install timeout, model file deletion and
forced install that the managers and the reconciler call. Take it, not a
concrete sender. The `Files` holder implements `RequestFileReleaser` because
`FileStagingClient` looks for it, and `carrier.FileCarrier` makes every set
provide it.

`carrier.Broadcaster` also keeps a registry of every subscription, because a
subscription lives on one carrier. A swap is `Listen(next)` (each handler now
listens on both carriers), then a store of `next` in the pointer (publishes go
to `next`), then `Release(old)`. No message is published on two carriers.
`NotifyReconnect` runs the reconnect hooks that `syncstate` and the failover
sync register, because messages across the flip are not ordered.

`carrier.NewPgbusFanout` builds the fan-out member of a set for the pgbus
carrier. It opens the LISTEN connection when it is called and not before, so a
deployment on NATS holds no LISTEN session until a change of carrier asks for
one, and `Close` frees it. The set still needs the other members from the other
parts of that carrier before `Validate` accepts it.

The active carrier is one row of table `cluster_carrier`
(`cluster.CarrierStore`). Each transition is a compare-and-set on its epoch. At
startup a replica reads the row, or inserts it if it is absent, and builds the
set the row names. A replica whose row names a carrier it does not implement
fails to start. It never falls back to another carrier.

A NATS URL that points at a server which is not up does not stop the start:
`messaging.New` retries on a failed connect, so a frontend can start before its
broker. A URL it cannot parse does.

## The worker tunnel

A worker that has no address that others can reach holds one outbound websocket
to a frontend. `core/services/tunnel` has the wire format (a request frame names
a tag and a target, and a reply frame always follows), the session, the registry
of the sessions that a replica holds, and `Splice`. The connect endpoint is
`GET /api/cluster/connect` (`core/http/endpoints/cluster`) and the client is
`core/services/worker/tunnel.go`.

- Every websocket of the tunnel comes from `tunnel.NewUpgrader` or
  `tunnel.NewDialer`. They set 64 KiB buffers. The 4 KiB default of gorilla makes
  a transfer from the worker five times slower than the other direction. A spec
  reads the source files and fails on a literal `websocket.Upgrader{` or
  `websocket.Dialer{`.
- A worker has two sessions, called lanes. The inference lane carries model
  calls, control and health, with the yamux windows at their defaults. The bulk
  lane carries file transfers, with windows of 4 MiB and 32 MiB. The caller
  chooses the lane in `Registry.Open`. A large transfer on the inference lane
  delays every small call on it, because yamux queues up to one window ahead of
  them. `Registry.Open` falls back to the inference lane when a node has no bulk
  session.
- The inference lane owns the claim in `node_connections`. The bulk lane is held
  under that claim, only on the replica that holds the inference lane, and it is
  closed with it. A bulk dial that reaches another replica gets 409 and the
  worker dials again.
- `Splice` passes the end of one direction on with `CloseWrite` and goes on
  copying the other direction. It closes both streams at once when a direction
  fails or when a stream cannot half-close.
- A node has its own tunnel credential. Registration mints it, returns it once,
  and stores only its SHA-256 (`BackendNode.TunnelTokenHash`). The registration
  token of the deployment never opens a tunnel. Every registration mints a new
  credential, so the client reads it at every dial.
- A refusal of a stream is one of four. `tunnel.IsWorkerAnswer` is true for the
  three that are evidence about a backend. `ErrStreamNotServed` says that the
  worker learned nothing, and it must never count as evidence.

## Queues

`WorkKind` names the work (`WorkTask`, `WorkMCPCI`, `WorkAgentRun`).
`natsRoute` in `workqueue_nats.go` is the one place a kind becomes a subject and
a queue group. A nil error from `Enqueue` means the carrier accepted the
payload, not that a consumer exists. The NATS carrier ignores the ctx of
`Enqueue`.

`Consume(ctx, kind, maxInFlight, h)` keeps two concurrency models on purpose.
With `maxInFlight` 1 the handler runs inline on the NATS delivery goroutine and
a panic is not recovered. Any other value spawns a recovered goroutine per
delivery; when bounded, the slot is taken on the delivery goroutine. 0 and
negative values are unbounded. `Unsubscribe` stops delivery, then waits for
running handlers, so a handler must not call it. The agent worker asks for
MCP CI with 1 (`startMCPCIConsumer`) and for agent runs with the dispatcher's
`maxConcurrent` (0 from the CLI); specs pin both.
`WithAgentRunRoute` lets an agent worker move the agent-run subject and group.
An empty subject keeps `agent.execute`; an empty queue is kept and makes a
plain subscription, as an explicitly empty `LOCALAI_AGENT_QUEUE` always did.

## Control verbs

Frontend half: `NodeCommandSender` sends the lifecycle verbs; `FileStager`
moves files. `S3NATSFileStager` returns `nodes.ErrNoRoute` when nothing is
listening for the node. `HTTPFileStager` reports connection failures as
ordinary errors.

`NodeCommandSender` embeds `LoadOperationControl`: the load operation verbs
(`InstallBackendOp`, `StopLoadOperation`, `OperationControl` for renewals and
completions, `UnloadReplica`, `StopModelReplica`). A carrier implements all of
them. Its timing and error contract is the doc comment on the interface in
`core/services/nodes/interfaces.go`, and `load_operation_control_conformance_test.go`
runs every carrier in `loadOperationCarriers` against it. Whether a worker
names operations is the worker's own report (`BackendInstallReply.ReportsOperations`),
not a property of the carrier.

Worker half: each verb is a `controlVerb`. A handler is typed with `unary`,
`withProgress` or `noReply` and registered with `handle` (one request of the
verb at a time on NATS, panic not recovered) or `handleWithProgress` (a
goroutine per request, progress published on the install-progress subject).
An undecodable body is still answered with the verb's typed refusal. The
`undecodable` error a `controlHandler` returns is read only by tests today: the
NATS server drops it. It is a recorded exception to the no-dead-code rule, kept
as the hook a carrier that signals a malformed request out of band (HTTP 400)
needs.

## Agent RPC

`AgentControl` carries MCP tool and discovery requests to one agent worker. A
decoded reply is returned with a nil error even when its `Error` field is set.
The NATS implementation reads only the deadline of ctx, never its
cancellation, and uses the default MCP timeouts when there is none.
`NATSAgentRPCServer` serves both in the agent-workers queue group and answers an
undecodable body with an `unmarshal error: ` reply. It also serves the node's
backend stop as `func(backend string)` and never replies to it.

## Dial

`BackendClientFactory.NewClient(nodeID, address, parallel)` builds the gRPC
client for SmartRouter, HealthMonitor and the reconciler's default
`ModelProber`. The node id is there because a dialer that must know which node
it reaches, as a tunnel does, cannot recover it from the address; the direct
factory ignores it. `ModelProber.Probe(ctx, nodeID, address)` likewise.

`WorkerNetDialerFor` returns the dial function for one worker's own HTTP
server. `DirectWorkerNetDialer` dials the address it is handed. It serves
`HTTPFileStager` and the backend-logs proxy (HTTP and WebSocket).
`HTTPFileStager.clientFor` keeps one HTTP client per node, because the idle
pool is keyed by host and port and two workers can report the same address.

## Rules a carrier must keep

- Payload: a broadcast above `messaging.MaxBroadcastBytes` is refused with
  `messaging.ErrPayloadTooLarge` by every carrier.
- Subjects: one closed set of roots, the `broadcastRoots` and `controlRoots`
  maps in `core/services/messaging/subject_rules.go`. Add a root there, with a
  row in the roots table in `subject_rules_test.go`, not in a carrier.
  `messaging.ValidateSubject` refuses anything else with
  `messaging.ErrUnservedSubject`. A carrier with no request and reply, such as
  pgbus, uses `messaging.ValidateBroadcastSubject`, which refuses the control
  roots with the same error. `messagingtest.BroadcastRootSubjects` has one
  subject for each broadcast root, and a spec keeps it equal to the rules.
- Wildcards: only a whole single token `*`, never the root. `>` is refused with
  `messaging.ErrUnsupportedWildcard`.
- Delivery is at-most-once. Anything that must survive a gap belongs in a table.
- A subscriber that is reconnecting misses messages. Do not read silence as
  evidence about a node.

## The cluster registry

`core/services/cluster` holds the facts that all replicas share through the
database. Table `instances` has one row for each live frontend replica
(`Registry`, kept fresh by `Membership`, which every replica runs). The
database clock stamps `last_seen`, and `instanceIsLive` is the one predicate
for "alive". The row also holds the epoch of the carrier row for which the
replica has built a carrier (`ReadyEpoch`) and the reason when it could not
(`ReadyReason`). Table `node_connections` records which live replica holds the
connection of a worker. `Registry.Claim` takes a new epoch from a sequence for
every claim, and `Release` needs both the owner and the epoch, so a replica that
lost a worker cannot clear the claim of the one that won it. Compare epochs for
equality and not for order. `Registry.Presence` tells a worker that is
reconnecting from one that is gone only when the departure is older than the
grace, and nothing but `PresenceGone` may be read as absence.

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
runner, where it fails so a runner without Docker cannot hide the check. The
end-to-end specs in `tests/e2e/distributed` run the NATS implementations of the
other seams against a real server, also through Docker.

## Open items for a second carrier

- `DistributedModelStore.Range` (`core/services/nodes/distributed_store.go`)
  builds a tokenless `model.Model` from `node.Address`. It dials nothing today,
  but it is the one direct construction site left outside the dial seam.
- The worker's `files.ensure` handler passes the first caller's ctx into a
  shared singleflight closure. The NATS server hands it `context.Background`.
  A carrier with a request ctx needs `context.WithoutCancel` there, or one
  cancelled caller fails the others.
- `HTTPFileStager` caches a client per node and never forgets one. There is no
  `ForgetNode`.
- `clientFor` returns no error; a carrier with no dialer for a node needs that
  path.
- The agent worker still uses NATS directly for its connection and for agent
  events. `agents.NewEventBridge` takes a `Broadcaster`, so the frontend's
  bridge goes through the holder.
- The backend-logs proxy (`proxyHTTPToWorker`) starts from
  `httpclient.HardenedTransport()`, which keeps
  `Proxy: http.ProxyFromEnvironment`. With `HTTP_PROXY` set, a dialer that
  routes by node would be handed the proxy address. Clear `Proxy` when the
  dialer is not the direct one.
- `natsControlServer.subject` refuses a verb it has no subject for, so
  registration fails. A verb that only one carrier serves needs a per-carrier
  opt-out where the verbs are registered.
- `WorkHandler` returns only an error. A carrier whose stream handler must send
  a terminal reply derives it from the `jobs.<id>.result` event the handler
  publishes on `events` (`handleMCPCIJob` does this).
- Not additive: the agent-run consumer (`NATSDispatcher.runDelivery`) ignores
  the per-delivery `events` publisher and publishes through the process-wide
  `EventBridge`, which is bound to one `Broadcaster`. A second carrier must
  change `NATSDispatcher` and `EventBridge`, for example by binding
  `handleJob`'s publishes to `events` through a bridge view that shares the
  cancel registry.
- `controlHandler`'s `undecodable` return is the recorded exception described
  under Control verbs.
- Agent cancel has no production sender (`EventBridge.CancelExecution` has no
  caller), so it is not part of the agent RPC seam.
