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
| Queues | `messaging.WorkQueue` (producer), `messaging.WorkConsumer` (worker), keyed by `messaging.WorkKind` | `core/services/messaging` (`workqueue.go`) | `NewNATSWorkQueue`, `NewNATSWorkConsumer`; `jobs.ClaimQueue` and `jobs.ClaimConsumer` (the claim table); `agentworker.Work` (an agent worker on the tunnel) |
| Control verbs, frontend half | `nodes.NodeCommandSender`, `nodes.FileStager` | `core/services/nodes` | `RemoteUnloaderAdapter` over NATS request/reply and `TunnelControl` over the HTTP control plane of a worker; `S3FileStager` over either (`NewS3NATSFileStager`, `NewS3TunnelFileStager`); `HTTPFileStager` over HTTP |
| Control verbs, worker half | unexported `controlServer` (`handle`, `handleWithProgress`) | `core/services/worker` | `natsControlServer`, `httpControlServer` |
| Agent RPC, frontend half | `AgentControl` (package `mcp`) | `core/http/endpoints/mcp` | `nodes.NATSAgentControl`, `nodes.AgentControlClient` (tunnel) |
| Agent RPC, worker half | unexported `agentRPCServer` on NATS, `agentworker.Handler` on the tunnel | `core/cli/agent_worker.go`, `core/services/agentworker` | `nodes.NATSAgentRPCServer`, `agentworker.Config` and `agentworker.Work` |
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
`carrier.NewNATSSet` and `carrier.NewTunnelSet` are the places that build a set.

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
set the row names (`carrierRuntime.build` in `core/application`: `NewNATSSet` or
`NewTunnelSet`). A replica whose row names a carrier it does not implement fails
to start. It never falls back to another carrier, and its own flags never
override the row.

The row is seeded by the first replica that starts: NATS when it has a NATS URL
(the flag `--nats-url`, or a URL stored in the cluster settings), the tunnel
otherwise. `--nats-url` means what it always meant for a deployment on NATS, and
it is no longer required: a deployment with only PostgreSQL runs on the tunnel.
The flag is copied into the cluster setting `nats.url` when none is stored, and a
stored URL wins afterwards, so that every replica uses the same one. A NATS URL
that points at a server which is not up does not stop the start:
`messaging.New` retries on a failed connect, so a frontend can start before its
broker. A URL it cannot parse does.

## Changing the carrier

An admin changes the carrier of a running cluster with `POST /api/cluster/carrier`
(`{"target": "nats"|"tunnel", "dry_run": bool, "force": bool}`, or `{"abort": true}`).
`GET /api/cluster/carrier` reports the state. Both routes, and
`GET`/`PUT /api/cluster/settings`, answer to an admin only; the prefix
`/api/cluster/` is not public, and only the exact connect and peer paths skip the
global authentication. Saving a NATS URL stores it and checks that the serving
replica reaches it. It does not change the carrier.

The row has a state, and every move is a compare-and-set on the epoch:

| State | Meaning | Who moves it on |
|---|---|---|
| `stable` | One carrier is active. A previous carrier may still be attached (`draining`, `draining_until`). | the admin starts a change |
| `prepare` | Every replica builds the target and listens on it. Publishing stays on the old carrier. | the leader, when every live replica reported ready for this epoch |
| `commit` | The target is active. Every replica publishes on it and confirms with the epoch of the commit. | the leader, when every live replica confirmed |
| `stable` | The old carrier drains for `max_drain`. | the leader, at `draining_until` |

`cluster.Switch` holds the protocol and keeps no state outside the row and the
`instances` table, so a leader that dies between two moves is replaced by one that
reads the same row. The leader is whichever replica holds the advisory lock
`KeyCarrierSwitch` at a tick (`advisorylock.RunLeaderLoop`). Every timeout is
measured on the clock of the database (`CarrierStore.DBNow` and the change stamp),
never on the clock of a replica. A replica that cannot build the target reports
the reason, and the leader aborts naming it. A replica that is late for the prepare
timeout aborts the change, or is left behind by a forced one. `abort` is accepted
only in `prepare`: after the commit the target is the active carrier, and going
back is a change like any other.

The preflight (also the dry run) lists: the live replicas with their versions (old
frontends write no row and cannot be detected, so the admin confirms that the list
is complete, and every frontend must be upgraded before the first change); whether
each replica could build the target (`instances.availability`, written by the
replica when a dry run asks for it); every worker that cannot follow and why
(`nodes.SwitchWorkers`: a worker that reports no capabilities predates carrier
switching, a worker that reports an error says it); and the work in flight. A
blocker can be forced. A forced change commits without a replica that is not ready,
and names it in the note of the row.

`carrier.Swapper` is what a replica does. It polls the row every two seconds with a
jitter, and a hint on `state.carrier` makes it look at once; the poll decides. In
`prepare` it builds the target, attaches every subscription to it
(`Broadcaster.Listen`) and reports ready for the epoch. In `commit` it stores the
set, so that publishes, enqueues and new calls use it, starts the work of the set
(`Set.Start`: the claim loop of the tunnel) and confirms. In `stable` with a drain it
does nothing: the old set stays attached. When the leader ends the drain it stops
listening on the old set, cancels its work with `messaging.ErrCarrierReleased`,
runs `Set.Handoff` (the tunnel moves its pending claims to the queue of the new
carrier: update, then publish, so a job is lost and not run twice if the replica
dies in between; it runs again after `Settle` for the producers that were slow to
flip), closes the set, and runs the reconnect hooks once. A replica that polls
late, restarts or joins during a change acts on the row alone.

`carrier.Window` is the only per-worker routing, and it exists between the commit
and the end of the drain. A control verb, a file transfer, a dial or a client for
one worker goes to the active set when the worker is attached to it, else to the
previous set when it is attached to that, else it fails with `nodes.ErrNoRoute`.
The choice is read from the state of the worker (`BackendNode.Attached`, or a
tunnel held when it reports nothing) and never from the error of an earlier
attempt. A call that is running is never moved, so an install, a stream or a load
renewal ends on the carrier it began on while the old carrier is attached.

A run that the end of the drain cuts off is cancelled with
`messaging.ErrCarrierReleased`. The claim driver completes its claim and leaves the
job to the reaper (`ReapStuckJobs`), because the run did start and the carrier that
took over must not start it again. The model loads, installs and agent runs that
were started before the change finish where they began; whatever is still running
at `max_drain` is failed by the reaper, and a load whose lease is not renewed is
killed by the watchdog of the worker, as it is for a dead owner (`#12524`).

A worker that holds a tunnel has no address and binds its backends to loopback. The
NATS set therefore builds its clients with `nodes.NewGuardedDirectClientFactory`:
for a worker that registered no address, a loopback address is not dialled, and the
client reports a failure of the transport, which no code that decides whether a
backend is dead reads as an answer. A forced change to NATS cannot make the health
monitor or the reconciler reap a model that runs.

The cluster settings (`cluster.SettingsStore`, table `cluster_settings`) hold what
every replica must read the same way: `nats.url`, `nats.worker_url` and the waits
of a change (`switch.prepare_timeout`, `switch.transition_window`,
`switch.max_drain`; defaults 1m, 2m and 15m; the flags
`--carrier-prepare-timeout`, `--carrier-transition-window` and `--carrier-max-drain`
are the fallback). The runtime settings of the application are a file of one
process and cannot carry them. Credentials are never stored there.

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
- The frontend resets every stream that a worker opens (`MaxIncomingStreams` is
  0 on the server side of a session), because nothing reads such a stream and an
  accepted one would hold its window of unread data for as long as the session
  lives. The worker side keeps the default: it accepts the streams of the
  frontend.
- The credential is checked when a worker dials, and never again. A node that is
  deleted, or whose credential a registration replaces, loses the session that
  this replica holds (`Registry.Disconnect`), and a replica that shuts down
  closes its registry (`Registry.Close`) before it leaves the instances table. A
  session that another replica holds ends when its worker dials again and is
  refused. A worker whose dial is refused with 401 or 403 registers again
  (`TunnelConfig.Reauthorize`), once for each wait of its backoff.
- A claim has its own bound (10 seconds), and a claim that was written for a
  request that already ended is released.
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

`messagingtest.RunWorkQueueConformance` is the suite every carrier of the
queue passes: one consumer gets a payload and a publisher for its events,
competing consumers split the work once when nothing fails, the kinds stay apart,
`maxInFlight` holds, `Unsubscribe` waits for the running handler, and a payload
above `messaging.MaxWorkPayloadBytes` (1 MiB, the default `max_payload` of NATS)
is refused. It does not state what differs between carriers: the fate of work
whose consumer dies, and how often a failed handler is called again. A handler
must tolerate a repeat.

### The claim queue

The tunnel carrier has no broker, so its queue is a table. `jobs.ClaimQueue`
writes a row (`work_claims`) and sends a wake hint on `messaging.SubjectClaimWake`.
`jobs.ClaimConsumer` runs on a frontend replica. It polls every two seconds,
wakes at once on a hint for its kind, and claims into as many slots as it has with
`FOR UPDATE SKIP LOCKED`. The hint is at-most-once, so the poll is what guarantees
that work is found; with the poll alone a unit waits one second on average, and
with the hint the median wait is a few milliseconds. A row stays until the work
has an answer, so `Enqueue` succeeds while nobody consumes.

A replica claims and not an agent worker, because an agent worker has no database.
`jobs.AgentDriver` is the handler: it picks an agent worker with
`nodes.AgentSelector`, sends the payload to the verb of the kind
(`workerctl.VerbAgentExecute` or `VerbMCPCIRun`) and reads the stream that comes
back. `jobs.DispatchLoop` is a consumer for the three kinds with the driver as its
handler.

- The outcome of the handler settles the row. `nil` deletes it. An error, or a
  panic, releases it with a wait that doubles from two seconds to a minute (the
  wait keeps one poison row from being claimed ahead of every newer row on every
  tick). An error that wraps `jobs.ErrKeepClaim` leaves it with its replica: the
  work ran and its answer could not be recorded, and releasing would run it again.
- There is no dead letter. The only outcomes that release a row are those where
  nothing was learned about the work, and failing a job on them would report a
  missing connection as the verdict of a worker.
- A claim is settled only by the replica and the attempt that hold it, so a
  holder that was reaped and answers late cannot delete the claim of the replica
  that took the work over.
- `ReapAbandoned` returns the claims of replicas that are not live. It reads
  `cluster.LiveInstanceIDsSQL` and has no age in it: a live replica that holds a
  claim for an hour has a slow job. A replica whose own row is not live claims
  nothing.
- Delivery is at-least-once. A replica that dies in the middle of a job loses
  nothing and the job runs again. NATS loses the job of a consumer that dies.
- `MigratePending` takes the pending rows out of the queue once, for a change to a
  carrier that has its own queue. Held rows are driven to their end where they are.
- A plain task (`WorkTask`) has no worker on either carrier. The driver drops the
  claim and the job is left to the reaper (`ReapStuckJobs`), as it was on NATS.
- `Dispatcher.Cancel` publishes `jobs.<id>.cancel`. On NATS nothing subscribes, as
  on master, and a spec pins that. On this carrier the replica that holds the job
  ends its stream, the run on the worker ends with its request, and the job is
  closed as cancelled.

## Control verbs

Frontend half: `NodeCommandSender` sends the lifecycle verbs; `FileStager`
moves files. `S3FileStager` sends the file verbs over a control link, so the same
stager runs over NATS and over the HTTP control plane of the tunnel, and it
returns `nodes.ErrNoRoute` when no route to the node exists. `HTTPFileStager`
reports connection failures as ordinary errors.

`NodeCommandSender` embeds `LoadOperationControl`: the load operation verbs
(`InstallBackendOp`, `StopLoadOperation`, `OperationControl` for renewals and
completions, `UnloadReplica`, `StopModelReplica`). A carrier implements all of
them. Its timing and error contract is the doc comment on the interface in
`core/services/nodes/interfaces.go`, and `load_operation_control_conformance_test.go`
runs every carrier in `loadOperationCarriers` against it. Whether a worker
names operations is the worker's own report (`BackendInstallReply.ReportsOperations`),
not a property of the carrier.

Worker half: each verb is a `controlVerb`, named by the constants of
`core/services/workerctl` (`workerctl.AllVerbs` lists them). A handler is typed
with `unary`, `withProgress` or `noReply` and registered with `handle` (one
request of the verb at a time on NATS, panic not recovered) or
`handleWithProgress` (a goroutine per request, progress published on the
install-progress subject). An undecodable body is still answered with the verb's
typed refusal on NATS.

`httpControlServer` is the second server of the same handlers. It mounts each
verb at `workerctl.PathOf(verb)` below `/v1/control/`, on the HTTP server that the
worker already runs (`nodes.StartFileTransferServerWithControl`, behind the same
bearer token as the file routes). It delivers requests of a verb concurrently,
where NATS delivers them one by one, so a handler must be safe to call from
several goroutines. The answer of a verb is 200 with the reply, and the refusal
of the worker stays in the reply. A body that cannot be read, including a body
that does not decode (the `undecodable` return of a `controlHandler`), is 400. A
verb the worker does not serve is 404 with a body that says so, and a method other
than POST is 405. A verb with progress streams lines (`workerctl.Envelope`): any
number of progress lines and then exactly one reply line. A body that ends
without a reply line is a link that broke, and it says nothing about the work.
A verb that only one server can serve is simply not registered on the other
(`files.*` need object storage on both).

The frontend half is one implementation of the verbs over a `controlLink`
(`nodeControl` in `unloader.go`). The link holds how one request travels and how
its failure is told apart: `natsLink` for NATS and `httpLink` for the control
plane of a worker (`ControlClient` is the HTTP client). Adding a carrier of the
verbs is a new link, not a copy of the verbs. The link decides four things: what
is `ErrNoRoute`, whether a wait that ran out means that the worker may still be
installing (`waitExpired`), whether a missing acknowledgement of `backend.stop`
may be read as an older worker (`acknowledgementMissing`: true only for NATS), and
how the carrier is named in a log line. An HTTP link never reads silence as a
stop that was done. A 404 is a worker that does not serve the verb. It becomes
`ErrNoRoute` only for `backend.upgrade`, which keeps the fallback to the older
install, and it is a plain error for every other verb.

## Agent RPC

`AgentControl` carries MCP tool and discovery requests to one agent worker. A
decoded reply is returned with a nil error even when its `Error` field is set.
The NATS implementation reads only the deadline of ctx, never its
cancellation, and uses the default MCP timeouts when there is none.
`NATSAgentRPCServer` serves both in the agent-workers queue group and answers an
undecodable body with an `unmarshal error: ` reply. It also serves the node's
backend stop as `func(backend string)` and never replies to it.

On the tunnel the frontend picks the worker. `nodes.AgentSelector` lists the agent
nodes that may take work (not pending, not draining; a failed health probe does
not exclude one) and asks which of them hold a tunnel, preferring one that this
replica holds because that call needs no relay. `nodes.AgentControlClient` sends
the request over the control client, with the same contract as NATS: a decoded
reply is returned with a nil error, no agent worker wraps `nodes.ErrNoRoute`, and
only the deadline of ctx applies. A request goes to a second worker (up to three)
only when the failure proves that the first did not start it: no route, a worker
too old to serve the verb, or a worker with no free slot. A broken stream or a
timeout is returned, because the tool may have run.

`agentworker.Handler` is the worker half on the tunnel: one HTTP server that
binds loopback only and is reached through the tunnel (its tunnel offers the http
tag alone). `agentworker.Work` is its `WorkConsumer`. A run is a streaming
request: the events that the handler publishes are progress lines that name their
subject, the result it publishes becomes the reply line, and a body that ends
without a reply line is a link that broke. A worker that is full answers busy
(`workerctl.WriteBusy`, `workerctl.ErrWorkerBusy`) and the frontend offers the run
to another worker. The frontend publishes a line only if
`nodes.Rebroadcaster` allows the subject for the node type of the worker.

## Dial

`BackendClientFactory.NewClient(nodeID, address, parallel)` builds the gRPC
client for SmartRouter, HealthMonitor and the reconciler's default
`ModelProber`. The node id is there because a dialer that must know which node
it reaches, as a tunnel does, cannot recover it from the address; the direct
factory ignores it. `ModelProber.Probe(ctx, nodeID, address)` likewise.
`NewDialerClientFactory` builds clients through `grpc.NewClientWithDialer`: the
address is then the name of a backend process of the worker, and gRPC does not
resolve it.

`WorkerNetDialerFor` returns the dial function for one worker's own HTTP
server. `DirectWorkerNetDialer` dials the address it is handed. It serves
`HTTPFileStager`, `ControlClient` and the backend-logs proxy (HTTP and
WebSocket). `HTTPFileStager.clientFor` keeps one HTTP client per node, because
the idle pool is keyed by host and port and two workers can report the same
address, and `ForgetNode` drops one. A worker that holds a tunnel has no address.
`nodes.WorkerHTTPHost` gives it a name under `.invalid` for the URL, which is
never dialled, and the proxy of the environment is not used for such a host.

A dial that fails in the transport says nothing about the backend. gRPC reports
it as `codes.Unavailable`, the code of a backend that died, so the client keeps
the error of its last dial and `grpc.TransportFailureOf(client)` returns it,
looking through decorators (every decorator implements `Unwrap`). It is nil when
the dial worked and when the host answered about the backend (an error that
implements `grpc.BackendAnswer`). Code that decides a backend is dead must ask
it before it acts: the probe of the reconciler (`ProbeUnknown`), the per-model
check of the health monitor, the warm path of the router (`probeUnknown`), the
eviction of a remote model and the check of a cached remote model. None of them
may reap a row, count a miss or shut a model down on a transport failure.

## The peer link and the relay

A worker holds one tunnel and it lands on one replica. `tunnel.WorkerDialer` is
the one door to a worker: the replica that holds the tunnel opens the stream, and
any other replica relays through the owner over a link that the pool of peers
(`tunnel.PeerPool`) holds and the owner accepts at `GET /api/cluster/peer`
(`tunnel.PeerSessions`, `tunnel.Relay`). A replica publishes its address and the
hash of its own peer credential in its instances row. The credential of the
replica is the only secret that opens a peer link; the registration token opens
none. The link is plain `ws` unless `--peer-tls` is set (`tunnel.WithPeerTLS`),
and a replica that publishes an address that is not on its host warns at
start-up while it is plain. A relayed stream carries two request frames, the relay frame (worker, lane,
remaining budget of the caller) and then the frame of the tunnel. The relay never
relays onward.

The errors of a dial are kept apart. A route that does not exist carries
`tunnel.ErrNoRoute`, and the carrier maps it onto `nodes.ErrNoRoute` in one place
(`carrier.mapDialError`). Absence claims (`cluster.ErrNoConnection`,
`ErrInstanceNotFound`) never reach a caller. A refusal of the worker keeps its
identity and is no `ErrNoRoute`. A peer that does not answer is
`tunnel.ErrPeerUnreachable` and is not `ErrNoRoute`: a link between two frontends
says nothing about a worker, and the scheduler demotes a worker on `ErrNoRoute`. A
bulk lane that is down is `tunnel.ErrNoBulkSession` and is not `ErrNoRoute`
either. A path that is slow (a socket deadline, a worker that takes the stream and
says nothing) is `tunnel.ErrTransport`. A reply that breaks the protocol is
`tunnel.ErrProtocol`. A database that cannot say who holds the tunnel is
`tunnel.ErrInfrastructure`. An owner link that breaks before it answers is
`tunnel.ErrPeerUnreachable`. None of them is `ErrNoRoute`, and a failure that
`routeFailure` does not recognise is none of them either: only the listed routing
facts demote a worker. The budget of the caller is checked first and is never a
route failure.

A dial can ask for the bulk lane (`tunnel.WithBulkLane`) and then never falls
back to the lane of model calls. The file stager asks for it.

## Rules a carrier must keep

- The control verbs of a worker are never served unauthenticated to the network.
  `nodes.controlGate` checks the bearer token, and with no token it serves only a
  caller on the loopback address, which is the stream that the tunnel opens on
  the worker. The file routes keep their older behaviour on an empty token. An
  agent worker binds loopback only.

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
- The `carrier.Broadcaster` holder carries fan-out only. Its `Subscribe`
  refuses a subject outside the broadcast roots with
  `messaging.ErrUnservedSubject`, so a subscription that it accepted is
  servable by every carrier and `Listen` never fails for the subject. Control
  traffic (the `nodes` and `mcp` roots) does not go through the holder: it uses
  the request and reply clients of the set.
- Wildcards: only a whole single token `*`, never the root. `>` is refused with
  `messaging.ErrUnsupportedWildcard`.
- Delivery is at-most-once. Anything that must survive a gap belongs in a table.
- A subscriber that is reconnecting misses messages. Do not read silence as
  evidence about a node.
- Database connections of pgbus: one replica uses at most 1 pinned LISTEN
  connection, `pgbus.spillFetchers` (8) while it reads spilled rows, and
  `pgbus.Config.MaxPublishers` (16 by default) while it publishes. `Publish`
  waits for a free slot and never opens another connection, so a burst of
  publishers cannot exhaust `max_connections`. Add the 25 connections of each
  replica to the budget of the database.
- A read of a spilled row ends after `pgbus.Config.FetchTimeout` (8 seconds by
  default). The broadcast is dropped and counted with `stage=resolve`, so that
  one stuck read does not hold back the delivery on every subject.

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
lost a worker cannot clear the claim of the one that won it. Epochs are unique, and
they are not ordered: a claim that inserts after a `Release` can draw a number
that is lower than one already issued. Compare epochs for equality (`==`,
`!=`) and never for order. A spec (`core/services/cluster/epoch_order_test.go`)
fails on a `<`, `>`, `<=` or `>=` with an operand named like an epoch in code
under `core/services`. `Registry.Presence` tells a worker that is
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

- `WorkHandler` returns only an error. A carrier whose stream handler must send a
  terminal reply derives it from the `jobs.<id>.result` event the handler
  publishes on `events` (`handleMCPCIJob` does this, and `agentworker.Work`
  builds the reply line from it).
- The agent-run consumer (`NATSDispatcher.runDelivery`) binds the events of a run
  to the publisher of its delivery through `EventBridge.WithPublisher`, which
  shares the cancel registry with the bridge. On NATS that publisher is the NATS
  client, so the subjects and payloads are the ones as before.
- Agent cancel has no production sender (`EventBridge.CancelExecution` has no
  caller), so it is not part of the agent RPC seam, and the tunnel has no verb for
  it. The cancel of a run on the tunnel is its request.
- A worker that registers an address and holds a tunnel (a worker that can use
  either carrier) is reached by the backend-logs proxy through the host that its
  address names, and the proxy of the environment is used for it. Only a worker
  without an address skips the proxy.
- The health monitor reads the presence of each node (`HealthMonitor.UsePresence`)
  only while the tunnel is the active carrier: a node that heartbeats and whose
  tunnel has been gone longer than `cluster.DefaultReconnectGrace` is demoted with
  a status-only change, and is neither promoted nor probed until its tunnel is
  back.
