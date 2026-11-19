+++
title = "The owner"
description = "The singleton owner, turning it on or off, running it as its own workload and upgrades."
weight = 30
+++

Look up how the one process that owns the add-ons works, and how to turn it on or off, run it on its own and upgrade it. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

## Singleton owner and its limits

Addon state lives in one place. Exactly one LocalAI instance is the **owner** of an
addon root (its storage identifier and `Root` directory): it holds the journal of
installed packages, the resource policy store, the proposals, the artifact store, the
backups and the data of every addon, and it supervises the addon processes. The other
instances are **frontends**: they authenticate the caller against the shared account
store and forward the typed operation to the owner. The consequences are by design:

- **No second owner.** A second process that opens the same root is refused while the
  first holds it; two owners are never active. There is no election and no automatic
  failover. Moving the owner to another machine is an operator action: stop the owner,
  move or mount the root, configure the new owner and start it.
- **An owner outage is an addon outage.** While the owner is down, every frontend
  answers `503` for every response type (JSON, progress, status, file, asset and
  proposal calls) with the same typed error, and executes nothing locally. Nothing is
  queued or applied later. A mutation whose reply was lost is reported as
  `outcome_unknown` with its original operation ID and is resolved by receipt lookup
  after the owner is back, never by sending it again under a new ID.
- **No state on frontends.** A frontend keeps no addon data, no addon process and no
  sticky session. Any frontend can serve any request, and a request may use another
  frontend than the previous one.
- **One writer per database.** A Tables database is one SQLite file on the owner's
  disk with a single writer. Throughput is bounded by that machine, and the data is
  only as available as that disk and its backups.
- **The addon process is local to the owner.** It listens on loopback only. The
  carrier between a frontend and the owner is the only network path, and it is
  authenticated in both directions by pinned certificates and signed requests.

A deployment with one instance is the owner and has no frontend; everything above
still holds.

The `--addons-role` flag chooses how a process takes part. `combined` is the
default and is both the owner and the only frontend. `owner` and `frontend` split
them, as described in [Running the owner as its own workload](#running-the-owner-as-its-own-workload).

**Distributed state.** In [distributed mode]({{% relref "/administration/distributed-mode" %}}),
the owner is a single runner by design: the operator runs one owner replica, and the
lock file in its root is the guard against a mistake, not a way to scale. The
frontends are stateless for add-ons. The one exception is the add-on pins of a
frontend that has no account database, which are per-instance state (see
[Frontend limits](#frontend-limits)).

## Turning the owner on or off

A normal `local-ai run` starts the addon owner, so Apps, Add-ons and Tables work
without extra setup. The node is its own owner: it keeps addon state under the
`addons` folder of the data path (`--data-path`) and reaches the owner over a
loopback carrier with keys that are made at each start and never written to disk.

| Flag | Environment variable | Default | What it does |
|---|---|---|---|
| `--addons-enabled` | `LOCALAI_ADDONS_ENABLED` | `true` | Runs the addon owner. `--addons-enabled=false` turns it off completely: no owner starts, no addon runs, the UI hides the add-on entries, and every add-on, fine-tuning and quantization route answers `503`. See [The addons feature gate](#the-addons-feature-gate). |
| `--addons-ownership-mode` | `LOCALAI_ADDONS_OWNERSHIP_MODE` | `local-owner` when authentication is off, `named-user` when it is on | `named-user` gives each account its own access. `local-owner` makes every caller share one owner. A value you set always wins over the default. Any other value stops startup with an error. |
| `--addons-official-library` | `LOCALAI_ADDONS_OFFICIAL_LIBRARY` | `true` | Registers the [official library]({{% relref "/addons/install-and-update#the-official-library" %}}) on the first start. `--addons-official-library=false` does not register it. |
| `--addons-backend-library` | `LOCALAI_ADDONS_BACKEND_LIBRARY` | `true` | Registers the [backend library]({{% relref "/addons/install-and-update#the-backend-library" %}}) on the first start, like the official library. A build from master reads it as the backend source; a tagged build reads the backend gallery until the release cutover. `--addons-backend-library=false` starts without it and reads the backend gallery on every build. A library you remove stays removed. |
| `--official-libraries` | `LOCALAI_OFFICIAL_LIBRARIES` | `true` | Use the default library sources of add-ons, backends and models. `--official-libraries=false` leaves them listed but unread. It is also the runtime setting `official_libraries`. See [Library sources]({{% relref "/addons/install-and-update#library-sources" %}}). |
| `--addons-backend-max-bytes` | `LOCALAI_ADDONS_BACKEND_MAX_BYTES` | `8589934592` (8 GiB) | Largest compressed size of a backend image installed from a library. Add-ons keep their own limit of 512 MiB. |
| `--addons-wsl-distro` | `LOCALAI_ADDONS_WSL_DISTRO` | empty (the default distribution) | Windows only. The WSL 2 distribution that runs the add-ons whose manifest has `osSettings.windows.execution` set to `"wsl2"`, such as the Sandbox. See [Install on Windows]({{% relref "/start/install/windows" %}}#sandbox-add-on). |
| `--addons-role` | `LOCALAI_ADDONS_ROLE` | `combined` | `combined` is the single node described above. `owner` runs only the owner and serves the carrier on the network. `frontend` runs no owner and forwards every add-on request to it. Any other value stops startup with an error. |
| `--addons-owner-address` | `LOCALAI_ADDONS_OWNER_ADDRESS` | empty | `https://host:port` of the owner carrier, with no path. Use a stable service name, never a pod address. Required for `owner` and `frontend`; an error with `combined`. |
| `--addons-owner-listen` | `LOCALAI_ADDONS_OWNER_LISTEN` | empty | `owner` only. The address the carrier listens on. Empty listens on all interfaces, on the port of `--addons-owner-address`. |
| `--addons-carrier-dir` | `LOCALAI_ADDONS_CARRIER_DIR` | empty | Directory with the carrier key set made by `local-ai util addons-carrier-keys`. Required for `owner` and `frontend`; an error with `combined`. |

`named-user` needs authentication. With authentication on and no mode set, LocalAI
uses `named-user`. With authentication off there are no accounts, so LocalAI uses
`local-owner` by default: every caller that can reach the server shares one owner and
can install, run and remove add-ons. See
[Explicit local-owner mode]({{% relref "/addons/contract-reference/owner-forwarding#explicit-local-owner-mode" %}}) for what that means. To keep
add-ons off on a server without authentication, set
`--addons-enabled=false`, or set `--addons-ownership-mode=named-user`; with
authentication off, `named-user` leaves the owner off and LocalAI logs a warning.

The `owner` and `frontend` roles split the owner from the frontends. The `frontend`
role does not create or open the `addons` folder of the data path. The key set for
both roles comes from `local-ai util addons-carrier-keys`; see
[Running the owner as its own workload](#running-the-owner-as-its-own-workload).

### The addons feature gate

Fine-tuning and quantization are add-ons, so `--addons-enabled=false` turns off all
three. `GET /api/features` reports one key, `addons`, and the keys `fine_tuning`
and `quantization` carry the same value. The key is `true` when add-ons are
available on this instance:

- In the `combined` and `owner` roles it is `true` when add-ons are enabled.
- In the `frontend` role it is `true` when add-ons are enabled and the owner
  answered a reachability call within 2 seconds. The answer is kept for 10 seconds,
  so `/api/features` does not contact the owner on every request.

While the key is `false`:

- The UI shows no Add-ons entry, no pinned apps, and no fine-tuning or
  quantization entry. `/app/addons` and canonical add-on page addresses lead back
  to `/app`. The UI does not call `/api/addons/pins` or `/api/addons/builtin`. A
  server that sends no `addons` key counts as `true` in the UI.
- Every path under `/api/addons`, `/api/fine-tuning` and `/api/quantization`
  answers `503` with the same body, also for a path that does not exist. The
  answer does not show which routes exist. The body is the LocalAI error envelope
  (`{"error": {"message", "type", "code"}}`, code `unavailable`). The message is
  "add-ons are turned off on this instance" when the feature is off and "the
  add-on owner is not reachable" when a frontend cannot reach the owner.
- With `--addons-enabled=false` the handlers of these routes are not registered at
  all. A `frontend` with `--addons-enabled=false` needs no `--addons-owner-address`
  and no `--addons-carrier-dir`. It loads no keys and builds no carrier. If you set
  `--addons-role`, `--addons-owner-address`, `--addons-owner-listen` or
  `--addons-carrier-dir`, LocalAI ignores them and logs one line.

This gate is for the whole instance. It does not replace the permission of each
user: the `addons`, fine-tuning and quantization permissions of a user are checked
after the gate.

The reachability answer of a frontend is a per-instance observation and is not
shared. Two frontends can disagree for up to 10 seconds after the owner goes up or
down. A request through a frontend that still reports `true` gets the `503` of the
carrier while the owner is down.

## Running the owner as its own workload

A normal `local-ai run` is both the owner and the only frontend (the `combined`
role). In a deployment with several frontend replicas, run the owner as one
separate workload and let the frontends forward to it. The owner keeps all its
state in files under one root, as before. Frontends keep none and scale freely.

- **One owner, one volume.** Run exactly one owner replica (`--addons-role=owner`)
  with its own volume for the data path. Use a recreate-style rollout, so two
  owner processes never overlap: the old one must stop before the new one starts.
  A second owner on the same root stops at once with "addon storage already owned"
  and the process exits with an error. It does not wait.
- **Fixed mount path.** The journal stores absolute paths. Mount the volume at the
  same path on every start. If the data path or the mount point changes, every
  installed add-on points at a directory that does not exist.
- **A local volume.** The root needs a local file system with working `flock`,
  atomic rename and directory `fsync`. A network block volume that one node mounts
  at a time is fine. A shared network file system is not supported.
- **A stable owner address.** Frontends reach the owner at `--addons-owner-address`.
  The owner does not store it, so you can change it between starts. Use a stable
  service name, never a pod address.
- **Frontends have no add-on volume.** A frontend (`--addons-role=frontend`) does
  not create or open the `addons` folder. Give it no volume for it.
- **Existing add-ons.** A root written by a single node loads under the owner role
  when the owner uses the same owner and storage identity. The stored address does
  not matter. The journal keeps absolute paths, so the owner must see the same data
  path and mount point as the node did. Otherwise the installed add-ons point at
  missing directories. For an empty volume, the owner starts fresh.

{{% callout style="warning" %}}
The owner is a node-bound singleton. If the owner is down, add-ons are down: the
frontends stay up and answer `503` for every add-on request. Failover is manual.
There is no election and no standby owner.
{{% /callout %}}

### What runs where

The three roles split the work of a LocalAI process as follows.

| Work | `combined` (default) | `owner` | `frontend` |
|---|---|---|---|
| Add-on processes, the gateway and the host operations | Yes, in this process | Yes | No. It forwards to the owner. |
| The carrier | Loopback, with keys made at each start | Served on the network | Client only |
| Chat turns | Yes | No | Yes |
| Model loading | In this process, or on workers in distributed mode | No models. In distributed mode it routes loads to workers. | In this process, or on workers in distributed mode |
| Connector polling (Telegram, Slack, Discord) | Yes | No | Yes, on the one leader frontend |
| Triggers, loops and agent jobs | Yes | No | Yes |
| API and UI for users | Yes | No. Send users to a frontend. | Yes |

A single node is `combined`: the add-ons run in the same process as the chats and
the models, and nothing else is needed. Use `owner` and `frontend` only when you
run several frontend replicas.

### The owner in distributed mode

When the frontends run in [distributed mode]({{% relref "/administration/distributed-mode" %}}),
the owner must join distributed mode too. Give it the same settings as the
frontends:

- the same NATS URL (`--nats-url`) and registration token (`--registration-token`);
- the same PostgreSQL database (`--auth-database-url`), with `--auth-enabled` on;
- `--distributed`.

With these, the owner takes part in the leader locks and sends model loads to the
workers. Without them, the owner acts as a separate single node in the cluster:

- It tries to load models in its own process. It has no backends, so the load fails
  with `backend not found`.
- It does not see the leader lock of the connectors. It polls the same bots as the
  leader frontend, so a Slack message gets two answers.

An owner should not run chats at all. In the release that contains pull request
13052, an owner that has no distributed settings skips connector polling,
triggers, loops and agent jobs, and refuses the push operation `connector.inbound`
with an error. In earlier releases, give the owner the distributed settings, or
turn the connectors off on it with `LOCALAI_DISABLE_CONNECTORS=true`.

### Make the carrier keys

The frontends and the owner trust each other through a static key set. Make it
once, anywhere you can run `local-ai`:

```bash
local-ai util addons-carrier-keys --out ./carrier-keys --owner-host owner.example
```

`--owner-host` is the name the frontends dial, and it must match the host in
`--addons-owner-address`. The command prints which files go where:

| Role | Mounts |
|---|---|
| Owner | `ca.pem`, `owner.pem`, `owner.key`, `frontend.pem`, `frontend-sign.pub` |
| Frontend | `ca.pem`, `owner.pem`, `frontend.pem`, `frontend.key`, `frontend-sign.key` |

Store the set as a secret and mount only the files of each role. Point
`--addons-carrier-dir` at the mount. LocalAI refuses key files that group or other
users can read, so give them mode `0600` or tighter.

All frontend replicas share one identity and one signing key, so a new or
rescheduled frontend is accepted at once and the owner needs no list of replicas.
Anyone who holds the frontend files can act as a frontend. A forwarded request
still carries the user, and the owner checks that user against the account
database, so the key set does not give a user any authority on its own. The command
does not keep the CA key, so nobody can make a third certificate from the set.

{{% callout style="note" %}}
Rotation is a coordinated restart. The frontends pin the exact certificate of the
owner and the owner pins the one of the frontends. To change the key set, make a
new one, update the secret, and restart the owner and every frontend. There is no
overlap window and no automatic renewal.
{{% /callout %}}

### Example

This Docker Compose file shows the add-on flags of one owner and two frontends.
Add the other flags of your deployment (the database, NATS and the rest of
[distributed mode]({{% relref "/administration/distributed-mode" %}})) to each service as
usual.

```yaml
services:
  owner:
    image: localai/localai:latest
    command: run
    hostname: owner.example
    environment:
      LOCALAI_ADDONS_ROLE: owner
      LOCALAI_ADDONS_OWNER_ADDRESS: https://owner.example:8443
      LOCALAI_ADDONS_CARRIER_DIR: /run/addon-keys
      LOCALAI_ADDONS_OWNERSHIP_MODE: named-user
      LOCALAI_DATA_PATH: /data
      LOCALAI_AUTH: "true"
      LOCALAI_AUTH_DATABASE_URL: postgres://localai:secret@db.example:5432/localai
    volumes:
      - owner-data:/data
      - ./carrier-keys-owner:/run/addon-keys:ro
    deploy:
      replicas: 1
      update_config:
        order: stop-first

  frontend:
    image: localai/localai:latest
    command: run
    environment:
      LOCALAI_ADDONS_ROLE: frontend
      LOCALAI_ADDONS_OWNER_ADDRESS: https://owner.example:8443
      LOCALAI_ADDONS_CARRIER_DIR: /run/addon-keys
      LOCALAI_ADDONS_OWNERSHIP_MODE: named-user
      LOCALAI_AUTH: "true"
      LOCALAI_AUTH_DATABASE_URL: postgres://localai:secret@db.example:5432/localai
    volumes:
      - ./carrier-keys-frontend:/run/addon-keys:ro
    deploy:
      replicas: 2

volumes:
  owner-data:
```

`./carrier-keys-owner` holds the owner files and `./carrier-keys-frontend` holds the
frontend files from the table above.

### Settings every role shares

- **The same ownership mode.** Every role of a deployment must use the same
  `--addons-ownership-mode`.
- **The same account database.** In `named-user` mode all roles must use the same
  account database. The owner checks the user of each forwarded request against it.
- `--addons-enabled=false` keeps its meaning in every role.

### What a restart looks like

When the owner stops, the kernel drops its lock and its add-on processes end with
it. The new owner takes the lock, checks its identity file, and reads the journal:
operations that had only started are marked failed, and operations that were
already prepared or applied are finished. Recovery journals and pending proposals
finish or wait for an administrator. Then the add-ons that were enabled start again.

While this happens, the frontends answer `503`. A frontend that starts while the
owner is down starts normally and answers `503` until the owner is up. Some state
is in memory only and is lost: an upgrade that was being canceled becomes failed,
and the memory of failed automatic upgrades is cleared. A mutation whose reply was
lost is reported as `outcome_unknown` with its operation ID, and relays that were
in progress (progress, file and asset) are cut. The owner picks a new audience at
each start. A request signed for the old owner is refused, and the frontend gets
the new audience on its next call without any action.

### The models path

Fine-tuning and quantization export models into the models path of the owner.
The owner and every frontend must mount the same models storage at the same
absolute `--models-path`. LocalAI checks this with a probe file:

- At start, the owner writes `.localai-addons-probe.json` into its models path.
  It holds a random nonce that changes at every start. Do not delete it, and do
  not exclude it from the shared volume. LocalAI does not list it as a model.
- Each frontend checks at start, and then every 60 seconds, that it sees the
  same file with the same nonce at the same absolute path. If it does not, the
  frontend logs an error, reports `model_exports: false` in `/api/features`,
  and answers the export and import routes with `503`.
- The owner refuses an export or import while its own probe file is missing,
  also for the add-on host operations. If the owner cannot write the probe at
  start, it keeps running and refuses exports.
- If the owner is unreachable, or is an older version without the probe, the
  state is `unknown`. LocalAI logs a warning and leaves exports on. The state
  clears within 60 seconds after the owner is back.

The `/api/fine-tuning` and `/api/quantization` routes of a frontend still write
to the models path of that frontend. The probe is what blocks them when that
path is not the shared storage.

### Frontend limits

A process with `--addons-role=frontend` holds no add-on state. This has these
consequences:

- **Library sources live on the owner and are changed through it.** The frontend
  never opens, creates or writes `sources.json` or anything else under the `addons`
  folder of the data path. Every `/api/library/sources` route is forwarded to the
  owner, which is the only writer. The caller must be a named account and an
  administrator: the frontend checks the account, and the owner checks the
  administrator right again, as it does for its own routes. A legacy API key has no
  named user and gets `401` on a frontend, in `local-owner` mode too. The routes
  answer the same statuses as on a single node (`404` for an unknown source, `409` for
  a duplicate, `400` for an invalid request). With add-ons off a frontend has no owner
  to reach, and these routes answer `503` with the message "library sources are
  managed on the owner". When the owner is down they answer `503` with
  "the add-on owner is not reachable". A change whose reply was lost answers `503`
  with the operation ID in the message; read the list again before you repeat it.
- **The gallery lists follow the owner.** A frontend asks the owner for its source
  list every 60 seconds (a read over the pinned carrier that needs no signed-in user)
  and, only when the list or the `official_libraries` setting
  changed, rebuilds its model and backend gallery lists from the galleries named by
  `--galleries` and `--backend-galleries` plus the owner's sources. A change made on
  the owner shows up on every frontend after at most 60 seconds, and a change made
  through a frontend shows up on that frontend at once. Until the owner has answered
  once, a frontend reads only its own flags. While the owner does not answer, the
  frontend keeps its last lists and logs that they may be stale. This is a
  per-instance observation: two frontends can show different lists for up to 60
  seconds.
- **`official_libraries` is set on the owner.** The owner's value is the value of
  the deployment, and a frontend takes it at each refresh. `POST /api/settings` with a
  different `official_libraries` value answers `409` on a frontend. Set it on the
  owner.
- **Backend installs run on the frontend node.** `POST /backends/library/install`
  and `GET /backends/library/updates` work on a frontend. The owner resolves the
  entry (including the choice of a meta backend for the capability of the frontend)
  and pins the image by digest. The frontend then installs it into its own backends
  path, verifies the signature there, and applies its own size limit and
  `--require-backend-integrity`. The owner's backends path stays unchanged. The
  update check compares the local backends with the releases the owner lists.
  The background update check has no user, so it works in named-user mode through
  one read-only system call (`POST /addons/library/system`, capability
  `library-system`). The owner accepts it only from a pinned frontend, only for the
  release lookup, and without an actor. Every call a user starts still needs the
  administrator check on the owner.
- **The owner and the frontends must run on the same OS and architecture.** The
  owner resolves entries for its own platform, so a frontend checks the platform the
  owner reports (`GET /addons/capabilities`) and refuses every library call to an owner
  on another one with a `503`. A frontend also refuses library calls to an owner that
  does not serve them, with "the add-on owner does not support library operations;
  upgrade the owner". Upgrade the owner before the frontends.
- **Pins are per frontend without an account database.** With an account database,
  the pins of the Apps entry are shared by all frontends. Without one, each
  frontend keeps its own pins in a file in its own data path, so two frontends can
  show different pins. A frontend logs a warning at start when it has no account database.

## Operations and reads during an upgrade

An install, an upgrade or a removal runs on the owner and can take a minute or
more: it fetches and checks the package, backs up the data, publishes the new
generation and starts it. The owner runs one such operation per add-on at a
time. A second upgrade of an add-on that is already changing is refused with a
conflict; it is never queued or run in parallel.

Reading never waits for an operation. The apps list, the host access overview
and the installed list answer from a snapshot of the last journal write, so an
upgrade in progress does not make the Add-ons page slow or empty. While an
operation runs, each add-on that it touches carries an `operation` object in
the apps list:

```json
{"id": "sandbox", "state": "running", "generation": 5,
 "operation": {"kind": "upgrade", "phase": "downloading", "since": "2026-11-04T17:18:00Z"}}
```

`phase` is `queued`, `downloading` (fetch and check the package), `backing-up`,
`installing`, `migrating`, `starting` or `working`. `state` and `generation` are
still those of the running add-on until the new generation is published.

A frontend that forwards a read to the owner waits at most eight seconds. When
the owner does not answer in that time, the route fails closed with the same
typed `503` from every frontend, and never with an older list of its own:

- `owner_busy`: the owner is up but did not answer in time.
- `owner_unavailable`: the owner could not be reached at all.

A frontend keeps no copy of the apps list, because two frontends with different
copies would give different answers for the same request. `GET /api/addons/pins`
answers the same way instead of returning pins without the add-ons it could not
check. When the owner is reachable and busy, it still answers from its snapshot,
so the apps list is a normal `200` with the `operation` fields above.

The Add-ons page keeps the last list it received in the browser (in memory and,
for a page loaded during an upgrade, in the session storage of the tab). On a
`503` it keeps showing that list, shows a notice such as "The add-on server is
busy. Showing the last known state.", and asks again with a growing delay. It
only says that no add-ons are installed when the owner answered with an empty
list.
