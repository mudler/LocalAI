# Distributed seams

Distributed mode talks to other processes through a few interfaces. Code above
them must not name NATS, and a new transport is a new implementation of them.
Today every seam has one carrier: NATS, or a direct dial for the gRPC and HTTP
connections to a worker. Only the carrier files import `nats.go`
(`core/services/messaging/client.go`, `tls.go` and
`core/services/nodes/control_nats.go`).

| Seam | Interface | Lives in | Today |
|---|---|---|---|
| Fan-out | `messaging.Broadcaster` | `core/services/messaging` | NATS client |
| Control verbs, frontend half | `nodes.NodeCommandSender`, `nodes.FileStager` | `core/services/nodes` | `RemoteUnloaderAdapter`, `S3NATSFileStager` over NATS request/reply; `HTTPFileStager` over HTTP |
| Control verbs, worker half | unexported `controlServer` (`handle`, `handleWithProgress`) | `core/services/worker` | `natsControlServer` |
| Agent RPC, frontend half | `AgentControl` (package `mcp`) | `core/http/endpoints/mcp` | `nodes.NATSAgentControl` |
| Agent RPC, worker half | unexported `agentRPCServer` | `core/cli/agent_worker.go` | `nodes.NATSAgentRPCServer` |
| Chat start, frontend half | `studio.ChatWorkers` | `core/services/studio` | `nodes.NATSChatStarter` |
| Chat start, worker half | `studio.Service.ServeStart`, served by `nodes.ChatStartHandler` | `core/application/studio_chatworker.go` | `nodes.NATSChatStartServer` |
| Dial to a worker | `nodes.BackendClientFactory`, `nodes.ModelProber`, `nodes.WorkerNetDialerFor` | `core/services/nodes` | direct dial |

Request and reply payloads of the control verbs live in
`core/services/workerctl`, so the frontend half, the worker half and any
carrier share one wire format without importing each other.

## Queues

There are none. The agent-run, MCP CI and job queues were removed with the old
agents and jobs; chat runs and triggers use the database and advisory locks
(`.agents/distributed-state.md`). A new queue is a new interface in
`core/services/messaging`, with its own subject root added to
`broadcastRoots` in `subject_rules.go`.

The chat start is not a queue: it is a request/reply in a queue group, like the
agent RPC. Nothing is buffered, and the reply says whether the turn started.

## Chat start

`studio.ChatWorkers.StartChat` hands the start of an unattended run (trigger,
job, connector message) to one chat worker on `chats.start`, queue group
`chat-workers`. The payloads are `workerctl.ChatStartRequest` and
`ChatStartReply`. Only the deadline of ctx bounds the wait, as for the agent
RPC. Without one the start's budget does: `LOCALAI_CHAT_START_TIMEOUT`
(`nodes.WithChatStartTimeout`, default `nodes.DefaultChatStartTimeout`). The
worker gives the handler the same budget.

- `ErrNoRoute` (wrapped): no worker listens. `Service.SendBackground` runs the
  turn on the frontend.
- A timeout or a transport error: the worker may have started the turn. The
  start fails and nothing runs on the frontend, so a message cannot run twice.
- A reply with `Error` set: the worker's refusal. The start fails with
  `studio.ErrWorkerRefused`.

The worker half runs the handler inline on the delivery goroutine. A start
only claims the turn and hands the message to the loop, so it is short; the
turn runs in the background. Events and stop of the turn use the existing
`chats.events.*` and `chats.control.*` subjects.

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

- Subjects: one closed set of roots, the `broadcastRoots` and `controlRoots`
  maps in `core/services/messaging/subject_rules.go`. Add a root there, with a
  row in the roots table in `subject_rules_test.go`, not in a carrier.
  `messaging.ValidateSubject` refuses anything else with
  `messaging.ErrUnservedSubject`.
- Wildcards: only a whole single token `*`, never the root. `>` is refused with
  `messaging.ErrUnsupportedWildcard`.
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
- The agent worker still uses NATS directly for its connection.
- A chat worker (`local-ai run` with `LOCALAI_CHAT_WORKER`) connects as a
  frontend does, with the frontend's NATS credentials. It does not register as
  a node, so the node list does not show it and the admin cannot approve or
  drain it. A minted per-node JWT for it needs `chats.start` (sub, queue) and
  the `chats.events.*` / `chats.control.*` subjects in `pkg/natsauth`.
- A chat worker refuses a start that would take it past
  `LOCALAI_CHAT_WORKER_MAX_TURNS` running turns (`studio.ErrWorkerFull`), and
  the run fails. A full worker cannot pass a start on to another one: NATS
  picks the worker of the queue group, not the frontend.
- A start that carries files runs on the frontend: the uploads are not sent
  over the bus.
- `ReplicaReconcilerOptions` has no `ClientFactory` field. Without a
  `Prober`, `NewReplicaReconciler` builds its own `tokenClientFactory`, so a
  carrier that dials through another factory must add the field.
- The backend-logs proxy (`proxyHTTPToWorker`) starts from
  `httpclient.HardenedTransport()`, which keeps
  `Proxy: http.ProxyFromEnvironment`. With `HTTP_PROXY` set, a dialer that
  routes by node would be handed the proxy address. Clear `Proxy` when the
  dialer is not the direct one.
- `natsControlServer.subject` refuses a verb it has no subject for, so
  registration fails. A verb that only one carrier serves needs a per-carrier
  opt-out where the verbs are registered.
- `controlHandler`'s `undecodable` return is the recorded exception described
  under Control verbs.
