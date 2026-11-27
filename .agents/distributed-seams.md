# Distributed seams

Distributed mode talks to other processes through a few interfaces. Code above
them must not name NATS, and a new transport is a new implementation of them.
The fan-out seam has two buses: `core/services/pgbus`, which uses PostgreSQL
LISTEN and NOTIFY and which every frontend and chat worker has, and NATS when
the cluster has a NATS server. A process listens on both and publishes each
broadcast on one (see "The broadcast buses"). The seams to a worker have one
implementation for each kind of route: NATS for the verbs with a direct dial for
the gRPC and HTTP connections, or the tunnel. Request and reply between the
frontends and the chat workers go over the peer link. Only the carrier files import
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
| Fan-out | `messaging.Broadcaster` | `core/services/messaging` | `carrier.Broadcaster` over the pgbus bus and the NATS client |
| Control verbs, frontend half | `nodes.NodeCommandSender`, `nodes.FileStager` | `core/services/nodes` | `RemoteUnloaderAdapter` over NATS request/reply and `TunnelControl` over the HTTP control plane of a worker; `S3FileStager` over either (`NewS3NATSFileStager`, `NewS3TunnelFileStager`); `HTTPFileStager` over HTTP |
| Control verbs, worker half | unexported `controlServer` (`handle`, `handleConcurrent`, `handleWithProgress`) | `core/services/worker` | `natsControlServer`, `httpControlServer` |
| Agent RPC, frontend half | `AgentControl` (package `mcp`), and `nodes.NodeAgentControl` for a call that names the worker | `core/http/endpoints/mcp`, `core/services/nodes` | `nodes.NATSAgentControl`, `nodes.AgentControlClient` (tunnel) |
| Agent RPC, worker half | `agentworker.ServeConfig`, which serves every route of the worker: `nodes.NATSAgentRPCServer` on NATS, `agentworker.Handler` on the tunnel | `core/services/agentworker`, `core/services/nodes` | `nodes.NATSAgentRPCServer`, `agentworker.Config` |
| Chat start, frontend half | `studio.ChatWorkers` | `core/services/studio` | `nodes.ChatStarter`, over the queue request of the peer link (`messaging.ProcessReplies`): the frontend picks the chat worker |
| Chat start, worker half | `studio.Service.ServeStart`, served by `nodes.ChatStartHandler` | `core/application/studio_chatworker.go` | `nodes.ChatStartServer` |
| Dial to a worker | `nodes.BackendClientFactory`, `nodes.ModelProber`, `nodes.WorkerNetDialerFor` | `core/services/nodes` | direct dial |

Request and reply payloads of the control verbs live in
`core/services/workerctl`, so the frontend half, the worker half and any
carrier share one wire format without importing each other.

## Holders and the route table

Code above the seams does not hold a carrier. It holds a forwarding holder from
`core/services/carrier`, one per seam: `Broadcaster`, `Commands`
(`nodes.NodeControl`), `Files`, `Clients`, `NewRoutedWorkerDialer` and `Agents`.
The seams split in two:

- `carrier.WorkerRoute` is one way to reach a worker: `Commands`, `Files`,
  `Clients`, `Dialer`, `Agents` (nil for a route with no agent RPC),
  `ForgetNode` and `Close`. Its `Kind` is a `nodes.RouteKind`: `direct`
  (`carrier.NewNATSRoutes`: NATS for the verbs, a direct dial for the data) or
  `tunnel` (`carrier.NewTunnelRoutes`: the tunnel of the worker, over a
  websocket or over P2P).
- The broadcaster holder (`carrier.Broadcaster`) is what the frontends and chat
  workers broadcast on. It listens on the buses of the process. Workers never
  use it.
- Request and reply between those processes go over the peer link
  (`carrier.NewPeerReplies`, a `messaging.ProcessReplies`), on every
  deployment.

`Commands`, `Files`, `Clients`, the dialer and `Agents` take a
`carrier.RouteTable`. It holds one route of each kind and reads the routes of a
node from `nodes.RoutesOf` (`of(node)`, cached for a second, and read again once
before a call is refused). A call to a node tries, in order, the route that last
worked for the node (its sticky route), then `direct`, then `tunnel`, among the
routes that the node has and the frontend holds. None is `nodes.ErrNoRoute`. A
call moves to the next route only on a not-started error: `nodes.ErrNoRoute`
(NATS no responders, a tunnel the worker does not hold, a verb that the route
cannot send) or `tunnel.NotStarted`. It never moves after a timeout, a broken
stream or an answer of the worker, and a call that is already running is never
moved. The sticky route matters because NATS runs the verbs of a worker one at a
time and HTTP runs them in parallel. The sticky routes and the cache are hints
of one replica; `RouteTable.Forget` drops them with the node. A gRPC client is
built on the first route of the node and keeps it. The holders allocate nothing
on a call that succeeds.

`Agents` has no node to route by for a call that names no worker: it asks the
`direct` route (the NATS queue group), then the `tunnel` route (a worker that
the frontend picks), and moves on only on a not-started error. A call that names
the worker (`ExecuteMCPToolOn`, `DiscoverMCPToolsOn`) goes on the routes of that
node like any other call to a node: the NATS subject of the node on `direct`,
its tunnel on `tunnel`. A route whose `Agents` cannot name a node
(`nodes.NodeAgentControl`) is a not-started error. The fan-out verbs (`UnloadRemoteModel`,
`UnloadRemoteModelContext`, `DeleteModelFiles`) give each route the nodes whose
first route it is, through `nodes.ScopedFanout`; a node whose routes cannot be
read goes to the first route. A fan-out does not move a node to its next route,
because the scoped verb does not say which node failed and how.

A frontend builds its routes once at start and pins them in the table
(`RouteTable.Pin`), one of each kind, for as long as it runs: the tunnel route
(`carrier.NewTunnelRoutes`) on every frontend, and the direct route
(`carrier.NewNATSRoutes`) when the cluster has a NATS server. The direct route
shares the connection of the NATS bus (`NATSOptions.Shared`), and its `Close`
leaves it open. A frontend without NATS holds the tunnel route only, so a node
that holds NATS only has no route from it (`nodes.ErrNoRoute`). There is no
cluster-wide carrier: nothing moves a route in or out of the table while the
frontend runs. A new NATS address is read at the next start.

`NodeControl` is the whole control surface a route gives the frontend:
`NodeCommandSender`, the process lister, the three unload interfaces the model
loader reads by type assertion, and the install timeout and model file deletion
that the managers and the reconciler call. Take it, not a
concrete sender. The `Files` holder implements `RequestFileReleaser` because
`FileStagingClient` looks for it, and `carrier.FileCarrier` makes every route
provide it.

### The broadcast buses

`carrier.Broadcaster` is built with `BroadcasterOptions`: a `Floor` bus (pgbus,
required) and a `Primary` bus (NATS, nil when no NATS server is configured).
A `carrier.Bus` is a name, a broadcaster, a reconnect hook, `Connected` and
`Close`. The holder listens on both buses. `Publish` goes to the primary when
`Connected` says this process reaches it, and to the floor otherwise. A
broadcast is never published on two buses, so there is no dedup and no change to
the wire format. Every process listens on the floor, so a process without NATS
still reaches the others; it does not hear what they publish on NATS, so
`Broadcaster.Unready` (wrapping `carrier.ErrBusUnreachable`) is set and
`/readyz` answers 503 (`DistributedServices.Unready`,
`Application.Unavailable`). The bus is chosen from the configuration (the
setting `nats.url`, else `--nats-url`, read at start) and the connection; a new
NATS address needs a restart. The admin API reports the bus of the frontend
that answers from that frontend's own connection (`carrier.BusState`, the `bus`
of `GET /api/cluster/settings`), never from a row.

The holder keeps a registry of every subscription, because a subscription
lives on one bus (`listen`, `release`; the export of the tests calls them
`ListenBus` and `ReleaseBus`). A change of the publish bus runs
`NotifyReconnect` once, as the reconnect of a bus does: messages across two buses
are not ordered, so `syncstate` and the failover sync read their tables again.
The change is seen on the next publish. A process that stays connected and hears
a peer move between the buses runs no hook; the periodic `Reconcile` of
`syncstate` repairs that.

`carrier.NewPgbusFanout` opens the LISTEN connection of the floor, and
`Fanout.Bus` returns it as a bus. Every frontend and chat worker opens it once at
start, on every setting, and the holder's `Close` frees it.

### The NATS address

`application.adoptNATSURL` runs at start. The flag `--nats-url` is copied into
the cluster setting `nats.url` when none is stored, without its user name,
password and query string. A flag that names another server than the stored
address stops the start and names both: the stored address is what every
replica uses, and the operator who moves a broker stores the new address
(`PUT /api/cluster/settings`) and restarts. A replica whose flag names the
stored server dials the flag, because that is where it keeps its own
credential. A NATS URL that points at a server which is not up does not stop
the start: `messaging.New` retries on a failed connect, so a frontend can start
before its broker. A URL it cannot parse does. The setting can be written only
when the replica that serves the request reaches the server at the new address.

The cluster settings (`cluster.SettingsStore`, table `cluster_settings`) hold what
every replica must read the same way: `nats.url`, `nats.worker_url` and the
carrier weights. The runtime settings of the application are a file of one
process and cannot carry them. Credentials are never stored there: an address
with a user name, a password or a query string is refused on write, and every view
and log line drops them.

The carrier weights (`routing.carrier_weights.nats`, `.websocket`, `.p2p`, whole
numbers 0 to 1000, defaults 100, 50 and 25) are in the same table, one row each so
that a partial update never rewrites the others. The registry (`carrier_rank.go`)
reads them through `SmartRouterOptions.CarrierWeights` and keeps them, with the
rank of every node, for 2 seconds, so a change needs no restart. A weight is a
tie-break only: it is added to the ORDER BY after the keys that decide fit, never
before. The weight of a node is the highest weight among `RoutesOf`. A node whose
only route is a data-limited relay tunnel is a last resort in the scheduler and is
never chosen for a placement that pushes model files (`stagesOverRelay`).

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
- A worker that joined with a worker invite (`local-ai worker --join`) opens its
  lanes over P2P instead: one stream per lane on `p2pnet.ProtoTunnelInference`
  and `p2pnet.ProtoTunnelBulk`. A stream carries no HTTP request, so the node,
  the lane and the credential travel in a first frame (`tunnel.Hello`, a uvarint
  length and JSON of at most 4 KiB) and the answer in `tunnel.HelloReply` with
  the status codes of the connect endpoint; the worker reads them as it reads a
  refused websocket dial (`worker.TunnelRefusal`). The yamux session then runs on
  the stream (`ServerSessionConn`, `ClientSessionConn`), and the session goes to
  the same `Registry`, so nothing above the registry knows the door. Both doors
  run the checks of `admitTunnel` (`core/http/endpoints/cluster`), which adds
  one: `BackendNode.PeerID` must equal the peer that dialled, and is empty for a
  websocket. The host listens on the tunnel protocols only in mode `share` on a
  distributed frontend, and admits only peers paired as workers
  (`Application.IsWorkerPeer`). The bulk lane is dialled with
  `p2pnet.NeedBulk`, so a relay with a data limit never carries files.
- A worker over P2P registers and heartbeats over P2P HTTP. `nodeTokenAuth`
  and `RegisterNodeEndpoint` take its peer ID (`localai.WithWorkerPeers`) in
  place of the registration token, pin the node to it, keep it pending at its
  first registration, mint the tunnel credential when it asks for the tunnel
  route, and never mint a NATS credential. A registration
  under the name of a pinned node from anywhere else, and one of a P2P worker
  under the name of a token node, is `nodes.ErrNodePinned` (409).
- `Splice` passes the end of one direction on with `CloseWrite` and goes on
  copying the other direction. It closes both streams at once when a direction
  fails or when a stream cannot half-close.
- A worker joins with a join token (`cluster.JoinTokenStore`, table
  `join_tokens`, minted by `POST /api/cluster/join-tokens`) or, when the
  frontend has one, the shared registration token. Both are registration only.
  `RegisterNodeEndpoint` tries the own credential of the node, then the join
  token, then the registration token. A join token is checked with `Peek`, spent
  with `Consume` (one conditional UPDATE, so a race for the last use has one
  winner) after the request is valid, and given back with `Refund` when the
  registration then fails. It returns `node_token` once and stores only its hash
  (`BackendNode.NodeTokenHash`). `nodeCredentialAuth` accepts that credential and
  nothing else on the other `/api/node/:id/*` routes, for that node only. A new
  join or shared-token registration replaces it and ends the tunnel session. A
  node that joined with a token keeps to its connections (`JoinRoutes`). A
  registration with no proof does not replace the credential of a node that has
  one. The worker keeps the credential in `<data dir>/node-credential.json`
  (`workerregistry.FileCredential`).
- The data plane token between the frontends and the workers (gRPC auth and the
  file-transfer bearer) is `workerctl.WorkerAuthToken(registrationToken)`, a
  derivation: the registration token itself never leaves the frontends. The
  registration answer carries it as `worker_token`. A frontend with no
  registration token has none, and the data plane is open as it always was.
- The join scripts (`/join.sh`, `/join.ps1`, `/join.yaml`) are public and
  answer only for a token that `Peek` accepts; they never spend it. Anything
  that logs a request must leave out the query string of these paths, and the
  API trace skips `/api/cluster/join-tokens` and `/api/node/register`.
- A node has its own tunnel credential. A registration that asks for the
  tunnel route (`"routes": ["tunnel"]`) mints it, returns it once, and stores
  only its SHA-256 (`BackendNode.TunnelTokenHash`). The connect endpoint
  compares it in constant time. The registration token of the deployment never
  opens a tunnel. Every registration for the tunnel mints a new credential, so
  the client reads it at every dial. A registration for NATS only, and a
  heartbeat, never mint one: a new credential ends the session of the old one.
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

There are none. The agent-run, MCP CI and job queues were removed with the old
agents and jobs; chat runs and triggers use the database and advisory locks
(`.agents/distributed-state.md`). A new queue is a new interface in
`core/services/messaging`, with its own subject root added to
`broadcastRoots` in `subject_rules.go`.

The chat start is not a queue either: it is a request to one chat worker that
the frontend picks. Nothing is buffered, and the reply says whether the turn
started.

## Chat start

`studio.ChatWorkers.StartChat` hands the start of an unattended run (trigger,
job, connector message) to one chat worker on `chats.start`, over the peer link
(`nodes.ChatStarter` over `DistributedServices.Replies`). The frontend lists the
live chat workers from the node list (`chatWorkerInstances`: healthy `agent`
nodes with `node.role=chat-worker`, by `node.instance`, among the live
replicas) and tries up to three, as described below. The payloads are `workerctl.ChatStartRequest` and
`ChatStartReply`. Only the deadline of ctx bounds the wait, as for the agent
RPC. Without one the start's budget does: `LOCALAI_CHAT_START_TIMEOUT`
(`nodes.WithChatStartTimeout`, default `nodes.DefaultChatStartTimeout`). The
worker gives the handler the same budget.

- `ErrNoRoute` (wrapped): no worker listens. `Service.SendBackground` runs the
  turn on the frontend.
- `messaging.ErrNotDelivered` (wrapped): the start reached no worker (none
  could be reached). Nothing started, so the frontend runs the turn too. It is
  not `ErrNoRoute`.
- A timeout or a transport error: the worker may have started the turn. The
  start fails and nothing runs on the frontend, so a message cannot run twice.
- A reply with `Error` set: the worker's refusal. The start fails with
  `studio.ErrWorkerRefused`. A reply with `Full` set is a refusal that proves
  that nothing started, so the frontend tries the next worker first.

The worker half runs the handler inline on the delivery goroutine. A start
only claims the turn and hands the message to the loop, so it is short; the
turn runs in the background. Events and stop of the turn use the existing
`chats.events.*` and `chats.control.*` subjects.

A client whose chat event stream reconnects to a frontend that does not run
the turn gets the missed events from the process that does, by a request over
the peer link to that instance (`studio.NewReplayCarrier`; each process serves
the subject `chats.replay.<instance>`, which only picks the handler on the far
process). The turn records its
instance on the chat (`store.Store.SetTurnOwner`). No answer in 2 s, or an
answer of `resync`, means the client reads the transcript; nothing else acts on
it.

### Request and reply between processes

The chat start, the chat replay and the background jobs of a turn are request
and reply between the frontends and the chat workers. The peer link is the one
transport for them on every deployment, NATS included:
`DistributedServices.Replies` is a `messaging.ProcessReplies` built once at start
by `carrier.NewPeerReplies` (`SubscribeReply`, `RequestInstance`,
`RequestQueue`). It has no per-carrier branch. There is no broker, so the asking process names the process that
answers: the replay and the job asks name the instance of the owner
(`RequestInstance`), and the chat start names a queue (`RequestQueue`). A request
goes over the peer link to the process it names (`tunnel.PeerPool.Request`), and
the far process runs the handler that it registered for the subject
(`tunnel.Replies`; the tunnel package names no subject). A request for this
process runs in the process. A queue request lists the members
(`PeerRepliesOptions.Queues`: for `chats.start`, the live replicas that a
healthy `agent` node with `node.role=chat-worker` names in `node.instance`),
tries them in a random order, at most three, and moves on only when the failure
proves that the member ran nothing. Each try may spend only its share of what is
left of the budget to open its stream (the dial of the peer link included), so
one member that does not answer leaves time for the others; a member that took
the request has the rest of the budget for its handler.

Every frontend and chat worker therefore needs a peer address that the others
reach (`peerAddress` at start, `LOCALAI_PEER_ADDRESS` overrides). A process with
none is unready (`application.ErrNoPeerAddress`, from
`DistributedServices.Unready`). Model workers open no new port.

- **Not started** (`tunnel.NotStarted`): the request was not written whole, the
  replica serves nothing for the subject, it is not the replica named, it
  serves too many requests (`tunnel.ErrReplicaBusy`), or it predates request
  and reply. Or the member answered with a refusal that `moveOn` reads as
  proof, such as `ChatStartReply.Full`.
- **Unknown**: the request was written and the reply did not arrive, or the
  handler ran and failed. A reply above the bound is a handler that ran
  (`tunnel.ErrHandlerFailed` with `tunnel.ErrReplyTooLarge`), wherever the
  handler ran. It is returned and never sent again, so a message is never run
  twice.

The handler gets no context: the stream is closed when the caller gives up, and the
reply is dropped. The budget of the caller crosses in the request frame and
bounds the wait of the far replica. A request and a reply are bounded by
`messaging.MaxBroadcastBytes`. A body is read into a buffer that grows with the
bytes that arrive, never to the size that the peer declared. A replica serves at
most 64 requests at once for one peer link and 256 in all, counted from before
the body is read; it refuses the next as busy.

`carrier.mapReplyError` is where the errors of the tunnel become messaging
errors. Absence (`messaging.ErrNoResponders`) exists only for a queue request,
when every member tried is not in the deployment or serves nothing for the
subject. A request to a named replica (`RequestInstance`) is never absent: a
replica with no row may be a live one whose heartbeats stalled past the reap
window, and one that serves nothing may not have registered its handler yet, so
both are `messaging.ErrNotDelivered`. So are a peer that does not answer, an
older release, a busy replica and a replica that is not the one named.
`ErrNotDelivered` is never absence: no caller may read it as a process that
left. A request above the bound is `messaging.ErrPayloadTooLarge`; a reply above
it is not. A subject that no queue lists answers `ErrNotDelivered` too.

A background job is therefore never marked lost because its node did not
answer: only a node that answers that it does not know the job makes it
lost. A job whose node left for good stays running in the view, and an ask about
it says that the node could not be reached.

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

`model.prefetch` (`workerctl.ModelPrefetchRequest`, actions `stage`, `commit`,
`discard`) is outside `NodeCommandSender`: `nodes.ModelFilePrefetcher` sends it
(`nodeControl.PrefetchModelFiles` on either link, `carrier.Commands` routes it on
the routes of the node) and `nodes.UpgradeFiles` drives it for a model upgrade, so a
worker downloads and verifies its own files. An older worker does not serve it
(`ErrNoRoute`, also for a 404 on the HTTP link); the upgrade logs that and the
worker gets the files from the frontend at its next load. Nothing on that path
fails the upgrade except a failed object storage delete at commit. The worker
runs its requests one at a time on every carrier.

Worker half: each verb is a `controlVerb`, named by the constants of
`core/services/workerctl` (`workerctl.AllVerbs` lists them). A handler is typed
with `unary`, `withProgress` or `noReply` and registered with `handle` (one
request of the verb at a time on NATS, panic not recovered),
`handleConcurrent` (a goroutine per request on NATS too, for a verb whose
handler may wait: `model.stop` waits for a process that another stop is
ending) or `handleWithProgress` (a goroutine per request, progress published
on the install-progress subject). An undecodable body is still answered with the verb's
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
stop that was done. A 404 is a worker that does not serve the verb. For
`backend.upgrade` it becomes `nodes.ErrWorkerTooOld`: the worker is older than
the verb, and the upgrade fails on it. It is the worker's own answer, never
`ErrNoRoute`: the node is not marked unhealthy, and its pending upgrade waits
without counting a failed attempt. There is no fallback to a forced install; an
old worker must be upgraded with the frontend. A 404 is a plain error for every
other verb.

## Agent RPC

`AgentControl` carries MCP tool and discovery requests to one agent worker. A
decoded reply is returned with a nil error even when its `Error` field is set.
The NATS implementation reads only the deadline of ctx, never its
cancellation, and uses the default MCP timeouts when there is none.
`NATSAgentRPCServer` serves both in the agent-workers queue group and on the
subjects of its own node (`messaging.SubjectNodeMCPToolExecute`,
`SubjectNodeMCPDiscovery`: `mcp.<node>.tools.execute`, `mcp.<node>.discovery`),
for a call that names the worker (`NATSAgentControl.ExecuteMCPToolOn`). A mutex
keeps one tool call (and one discovery) at a time across the two subjects. The
minted JWT of an agent node allows the subjects of that node only. It answers
an undecodable body with an `unmarshal error: ` reply. It also serves the node's
backend stop as `func(backend string)` and never replies to it.

On the tunnel the frontend picks the worker. `nodes.AgentSelector` lists the agent
nodes that may take work (not pending, not draining; a failed health probe does
not exclude one) and asks which of them hold a tunnel, preferring one that this
replica holds because that call needs no relay. `nodes.AgentControlClient` sends
the request over the control client, with the same contract as NATS: a decoded
reply is returned with a nil error, no agent worker wraps `nodes.ErrNoRoute`, and
only the deadline of ctx applies. A request goes to a second worker (up to three)
only when the failure proves that the first did not start it: no route, or a
worker too old to serve the verb. A broken stream or a
timeout is returned, because the tool may have run.

`agentworker.Handler` is the worker half on the tunnel: one HTTP server that
binds loopback only and is reached through the tunnel (its tunnel offers the http
tag alone). It serves the MCP tool call, the discovery and the backend stop.

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
relays onward. A stream whose frame has `reply` where the lane goes is a
request for the replica itself (replica, `reply`, budget, subject), followed by
the body; the relay hands it to `tunnel.Replies`. A replica of an older release
refuses that frame with a relay code, which the caller reads as
`tunnel.ErrRequestUnsupported`.

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
  the request and reply clients of the worker routes.
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
for "alive". `cluster.Migrate` also drops what an older release left there: the
table `cluster_carrier` and the readiness columns of `instances`; the node
registry drops `backend_nodes.attached_epoch`. Table `node_connections` records
which live replica holds the connection of a worker. `Registry.Claim` takes a new epoch from a sequence for
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
around the node (the scheduler skips it). An upgrade has no fallback: it fails
on that node. Never delete `node_models` rows on it. Timeouts are not
`ErrNoRoute`.

A pending backend op that fails with `ErrNoRoute` is still recorded as a failed
attempt (`RecordPendingBackendOpFailure`, the reconciler's attempt count, and
the dead-letter after the maximum attempts). That is accounting for the op, not
a verdict about the node.

The carrier's own sentinel (`nats.ErrNoResponders`, `messaging.ErrNoResponders`)
is mapped onto `ErrNoRoute` in `core/services/nodes/control_nats.go`
(`mapRequestError`) and nowhere else; a caller outside `nodes` that needs the
mapping asks through `nodes.RequestInstance`, as the job carrier does. A
consumer that matches on the carrier error reads absence as a fact about the
node, which is the mistake this contract exists to prevent.
`messaging.ErrNotDelivered` is never mapped onto `ErrNoRoute`.

## Testing a new carrier

Run it against `messagingtest.RunBroadcasterConformance`
(`core/services/messaging/messagingtest`) in its own package. `FakeBus` runs it
in `core/services/testutil`. The NATS run is in `core/services/messaging`; it
needs Docker. Without Docker it skips, except when `CI` is set on a non-macOS
runner, where it fails so a runner without Docker cannot hide the check. The
end-to-end specs in `tests/e2e/distributed` run the NATS implementations of the
other seams against a real server, also through Docker.

## Workers hold their routes

A worker does not choose a carrier and does not follow one. Its operator gives
it routes (`worker.Route`: `nats`, `tunnel`), from its flags (`Config.routes`
for a backend worker, `AgentWorkerCMD.routes` for an agent worker, or
`--routes`). A NATS URL gives the NATS route, the frontend URL or the invite
gives the tunnel, and a worker that joined over P2P holds the tunnel only
(`--routes nats` is refused with `--join`). The registration answer names no
carrier.

- `worker.RouteHolder` holds every route at once. Each route has its own loop:
  it gets the credential of the route (`workerregistry.CredentialManager` with
  the route, whose register function asks for that route only), attaches it
  (`worker.Attacher`, from `BackendPlane.Attachers` or
  `agentworker.ServeConfig.Attachers`), and attaches it again when the
  attachment fails for good (`Failed`, for example a NATS credential that
  cannot be renewed). A route that cannot be attached is tried again with a wait
  of 1 to 30 seconds and never holds back the other routes. A NATS server that
  is down still attaches (the client reconnects by itself) and is reported as
  not connected.
- At start the worker registers once with no route, for the identity of the
  node. A registration for NATS gets the NATS user JWT (approved nodes only)
  and the handover (`nats_url` from `nats.worker_url` else `nats.url`, the CA as
  PEM, `nats_client_tls`); the refresh of the JWT asks for NATS only. A
  registration for the tunnel gets the tunnel token. A local `--nats-url`, CA or
  credential still wins over the handover.
- Every heartbeat carries `routes` (the routes the worker was given),
  `attached` (the routes it holds and is connected on), `route_error` (why each
  other route is not held) and, with a tunnel, `tunnel_transport`,
  `tunnel_path` and `tunnel_limited`. `HeartbeatUpdate.RouteReport` reads them,
  and `NodeRegistry.SetRouteReport` stores them in `attached`, `follow`,
  `follow_error` and the tunnel columns, which `nodes.RoutesOf` and
  `nodes.RouteJudge` read. A heartbeat is skipped while no route is connected.
- The heartbeat answer carries nothing about a carrier.

## Workers that hold only a tunnel

The route table reaches a worker that holds the tunnel and not NATS through the
tunnel route: the route is pinned on every replica. A worker holds one tunnel, over a websocket or over P2P, so the two
kinds never compete. The health monitor judges such a worker by its tunnel,
see "Health of a node". The router places a model on it by the routes of the node, see below.
A worker over P2P is tunnel-only. An agent worker over P2P is not supported.

## Routes of a node

The router does not know a carrier. `nodes.RoutesOf(node)` returns the usable
routes of one node from the columns the worker reported: `direct` (NATS control
and a direct dial of the address; the worker is attached to NATS, or has not
reported its routes yet, and has an address, or is an agent worker, which needs
none; never for a worker that joined over P2P) and `tunnel` (the worker is
attached to the tunnel; transport, path and limited are attributes of the
route). `SmartRouter.narrowByRoutable` keeps the nodes with at least one route,
so a node is schedulable whatever the other nodes use. A node with no route is
skipped, and no row changes. A limited relay is still a route. The router takes
no option for this. Ties between equally fit nodes are broken by the carrier
weights (see the cluster settings above). The holders route each call by the
same list (see "Holders and the route table").

## Health of a node

`nodes.RouteJudge` says, for each route of a node, if it is live
(`nodes.RouteState`: `live`, `reconnecting` or `down`, with a reason). It reads
the node row and, for the tunnel, `Registry.Presence`. It keeps no state, so every replica gives the same answer for the
same rows. The node list (`GET /api/nodes`, `GET /api/nodes/:id`, option
`localai.WithRouteStates`) and the MCP tool `list_nodes` show its answer as
`route_state` (`routes` in the tool).

- `heartbeat`: live while the last heartbeat is younger than the stale
  threshold. It proves that the worker process runs and carries the route
  report; it does not carry work.
- `nats`: for a worker given or holding NATS. Live when the last report lists
  it in `attached` and the heartbeat is fresh (the report rides on the
  heartbeat). The reason comes from `route_error`.
- `tunnel`: for a worker given or holding the tunnel, over a websocket or P2P.
  `PresenceConnected` is live. `PresenceReconnecting` is `reconnecting`, which
  counts as live while the heartbeat is fresh. `PresenceGone` is down, past the
  grace already. With no presence answer (no row, or a failed read) the report
  decides while the heartbeat is fresh.
- A node that has not sent its route report yet has the heartbeat only.

`HealthMonitor` (`UsePresence(reader, grace)`, grace
`cluster.DefaultReconnectGrace`) acts on that answer at each tick:

- A route that carries work (`nats` or `tunnel`) is live: the node is healthy,
  and is promoted if it was not. A lost route next to a live one, or a lost
  heartbeat next to a held tunnel, is logged once and shown in `route_state`;
  the status does not change.
- No such route is live and the heartbeat is stale: the stale rules apply as
  before (`MarkOffline`, or `MarkUnhealthy` with auto-offline off).
- No such route is live and the heartbeat is fresh: the node is demoted with
  the status-only `MarkUnhealthy` once it has been without a live route for the
  grace (at once when the only routes are tunnels presence reports gone). It is
  neither promoted nor probed while it stays so. A route that comes back inside
  the grace changes nothing, so a reconnect does not flap the node.

The time a node has been without a live route (`noRouteSince`) is kept in each
replica that runs the checks (the advisory lock gives each tick to one
replica). A replica that has not seen the node for more than three check
intervals starts the grace again, so a route that came back meanwhile is not
missed; this only delays a demotion.

## Open items

- A worker that registers an address and holds a tunnel (a worker that holds
  both routes) is reached by the backend-logs proxy through the host that its
  address names, and the proxy of the environment is used for it. Only a worker
  without an address skips the proxy.
- A chat worker (`local-ai run` with `LOCALAI_CHAT_WORKER`) connects as a
  frontend does, with the frontend's NATS credentials. It registers itself in
  the node registry through the database (an approved `agent` node with
  `node.role=chat-worker`, see `registerChatWorkerNode`), so the node list
  shows it and the scheduler skips it. A drain does not reach it, and it gets
  no minted JWT: that needs the `chats.events.*` / `chats.control.*` subjects
  in `pkg/natsauth`. Its chat starts come over the peer link, so it needs no
  NATS subject for them.
- A chat worker refuses a start that would take it past
  `LOCALAI_CHAT_WORKER_MAX_TURNS` running turns (`studio.ErrWorkerFull`,
  `ChatStartReply.Full`). The frontend sends the start to another worker, on
  every deployment.
- The requests between replicas share the peer link with the bulk traffic that
  it relays to workers (one session with the windows of the bulk lane). A
  512 KiB replay page behind one model file transfer takes about 0.13 s at
  1 Gbit/s and 0.4 s at 100 Mbit/s, but 3.8 s (median) to 7.5 s at 10 Mbit/s, past the 2 s of
  the replay (benchmark in `core/services/tunnel/replay_latency_test.go`). A
  second peer session with the default windows for requests, as the worker
  tunnel has two lanes, would keep them out of that queue.
- A start that carries files runs on the frontend: the uploads are not sent
  over the bus.
- The P2P door of the tunnel. The invite names the frontend that made it, and a
  worker over P2P always dials that frontend: another replica reaches it only
  through the peer link. A worker cannot fall back to another frontend when that
  one is gone.
