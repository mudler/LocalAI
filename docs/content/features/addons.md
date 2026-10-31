---
title: Addon contracts
---

## Status and trust

The version-one contracts in `pkg/addons/contract` define managed-process addons.
The owner installs, verifies, starts and supervises them, and a deployment with
several frontends forwards every addon request to that owner over a pinned mutual
TLS carrier. The routes are described in [Public API](#public-api), the Apps and
Add-ons pages in [Apps](#apps), and Tables, the first first-party addon, in
[Tables](../tables/). The first-party package is described in
[First-party package and release channels](#first-party-package-and-release-channels);
it is not published to a registry by any test or by acceptance.

Trust has three parts, and they are different:

- **Package identity.** A package is installed only after its digest matches the
  catalog entry and, when the library requires it, its signature matches the
  identity policy of that library. A library, a channel and a trusted signer never
  change by themselves, and an upgrade is refused when the trusted identity of the
  new package differs from the installed one.
- **Executable code.** An installed executable is trusted code, not sandboxed
  code. It runs as the owner's operating system user, with the owner's file system
  and network. The host gives it a loopback port, a per-start credential and its
  own data directory; it does not confine it. Install only packages you trust.
- **Authority.** An addon never decides who a caller is or whether a change was
  approved. The host supplies the actor from the authenticated session or key,
  decides every role, and applies a mutation from an API client, an MCP session or
  the OpenAI-compatible chat route only after the proposer's own browser session
  confirms it. The one exception is the chats on the server (`/api/chats`), their
  agents and their triggers: turning an add-on on for a chat or an agent is the
  consent, and the change is applied at once, with the credential of the chat turn,
  for the user who turned the add-on on, and with an undo where the add-on keeps
  one (see [Proposals and confirmation](#proposals-and-confirmation)). Prompt text,
  a model and an addon cannot supply that approval.

Every addon requires an executable; a UI bundle alone is not an addon.

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

**Distributed state.** In [distributed mode]({{% relref "features/distributed-mode" %}}),
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
| `--addons-official-library` | `LOCALAI_ADDONS_OFFICIAL_LIBRARY` | `true` | Registers the [official library](#the-official-library) on the first start. `--addons-official-library=false` does not register it. |
| `--addons-backend-library` | `LOCALAI_ADDONS_BACKEND_LIBRARY` | `true` | Registers the [backend library](#the-backend-library) on the first start, like the official library. A build from master reads it as the backend source; a tagged build reads the backend gallery until the release cutover. `--addons-backend-library=false` starts without it and reads the backend gallery on every build. A library you remove stays removed. |
| `--official-libraries` | `LOCALAI_OFFICIAL_LIBRARIES` | `true` | Use the default library sources of add-ons, backends and models. `--official-libraries=false` leaves them listed but unread. It is also the runtime setting `official_libraries`. See [Library sources](#library-sources). |
| `--addons-backend-max-bytes` | `LOCALAI_ADDONS_BACKEND_MAX_BYTES` | `8589934592` (8 GiB) | Largest compressed size of a backend image installed from a library. Add-ons keep their own limit of 512 MiB. |
| `--addons-role` | `LOCALAI_ADDONS_ROLE` | `combined` | `combined` is the single node described above. `owner` runs only the owner and serves the carrier on the network. `frontend` runs no owner and forwards every add-on request to it. Any other value stops startup with an error. |
| `--addons-owner-address` | `LOCALAI_ADDONS_OWNER_ADDRESS` | empty | `https://host:port` of the owner carrier, with no path. Use a stable service name, never a pod address. Required for `owner` and `frontend`; an error with `combined`. |
| `--addons-owner-listen` | `LOCALAI_ADDONS_OWNER_LISTEN` | empty | `owner` only. The address the carrier listens on. Empty listens on all interfaces, on the port of `--addons-owner-address`. |
| `--addons-carrier-dir` | `LOCALAI_ADDONS_CARRIER_DIR` | empty | Directory with the carrier key set made by `local-ai util addons-carrier-keys`. Required for `owner` and `frontend`; an error with `combined`. |

`named-user` needs authentication. With authentication on and no mode set, LocalAI
uses `named-user`. With authentication off there are no accounts, so LocalAI uses
`local-owner` by default: every caller that can reach the server shares one owner and
can install, run and remove add-ons. See
[Explicit local-owner mode](#explicit-local-owner-mode) for what that means. To keep
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
  quantization entry. `/app/addons`, `/app/fine-tune` and `/app/quantize` lead back
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

{{% notice warning %}}
The owner is a node-bound singleton. If the owner is down, add-ons are down: the
frontends stay up and answer `503` for every add-on request. Failover is manual.
There is no election and no standby owner.
{{% /notice %}}

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

{{% notice note %}}
Rotation is a coordinated restart. The frontends pin the exact certificate of the
owner and the owner pins the one of the frontends. To change the key set, make a
new one, update the secret, and restart the owner and every frontend. There is no
overlap window and no automatic renewal.
{{% /notice %}}

### Example

This Docker Compose file shows the add-on flags of one owner and two frontends.
Add the other flags of your deployment (the database, NATS and the rest of
[distributed mode]({{% relref "features/distributed-mode" %}})) to each service as
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

## The official library

On the first start, when the owner has no `sources.json` in the `addons` folder yet,
LocalAI registers one library with the ID `official`. It is a YAML index of the
first-party add-ons (Tables, Voice, Face, Fine-tuning, Quantization, Studio, Talk, Office,
Sandbox and Web). The index has one entry for each enabled platform of each add-on, so an
install on Linux (amd64 or arm64) and on macOS (Apple silicon or Intel) finds the add-ons for
its own platform. The Sandbox add-on is Linux only. No add-on is built for Windows yet, so the
official library lists nothing there.

Which index the library reads depends on the build:

| Build | Index | Entries | Signer the library requires |
|---|---|---|---|
| A release (the version is a `v<semver>` tag) | `addons-index.yaml`, an asset of that GitHub release | Pinned by digest to the packages the release built | The package workflow run of that tag (`refs/tags/v<semver>`) |
| Any other build (master, a source build, a commit after a tag) | `gallery/addons/index.yaml` on the master branch | The mutable `development` builds | The package workflow run on master (`refs/heads/master`) |

```
https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/addons/index.yaml
https://github.com/mudler/LocalAI/releases/download/v<version>/addons-index.yaml
```

So a release installs the immutable, tag-pinned packages that the same release built and
signed, and never a development build signed by master. The first start writes the index
and signer of the running build into `sources.json`. After an upgrade, the next start moves
the `official` library to the index and signer of the new build; a library that you pointed at
another URL is left as it is.

The index has the same format as the other libraries (see
[Catalog document and limits](#catalog-document-and-limits)). A package that the named
workflow run did not build and sign is refused at install.

`gallery/addons/index.yaml` is generated, never edited by hand:

```bash
sh scripts/addons/catalog.sh --channel development \
  --repository quay.io/go-skynet/local-ai-addons --out gallery/addons/index.yaml
```

CI runs the same command with `--check` and fails when the committed file is stale, for
example when a platform is enabled in `addons/<id>/package/platforms.yaml` or a manifest
changes. The release index is rendered by the add-on package workflow when a `v<semver>` tag
is pushed and attached to the release when it is published (see
[Preparing a release](https://github.com/mudler/LocalAI/blob/master/.agents/preparing-a-release.md)).

### Add-ons that are not built in

Studio, Talk, Voice, Face, Fine-tuning, Quantization, Office and Web are not part of the LocalAI
binary or image. A fresh install has none of them. The old links to the pages they replaced
(see [Old links](#old-links)) open the add-on page, which says the add-on is not available
until it is installed.

To get them on a new install:

1. Sign in as an administrator (with authentication off, you already are one) and open
   **Add-ons**, then **Find add-ons**. The list is the official library for this machine's
   operating system and architecture.
2. Pick the add-on and choose **Install and turn on**. The install checks the signature
   against the library policy before anything runs. Studio is the usual first one: it holds the
   image, video, 3D, speech, sound and audio tools.
3. The add-on then appears in **My add-ons**. Use **Show in sidebar** to pin it.

An install needs the network to reach the library and the package registry. A machine
without it can run a private library (see [Private catalog service](#private-catalog-service)) that
mirrors the packages. `--addons-official-library=false` starts without the official library, and
`--addons-enabled=false` turns the add-on owner off, so nothing can be installed.
Installed add-ons live in the `addons` folder under the data path, so keep that path on a
volume in a container, or the add-ons must be installed again after the container is replaced.

The first start writes `sources.json`, and from then on that file is the record of
your choices. If you remove the `official` library (in the Add-ons page or through the
source routes), it stays removed, and a `sources.json` you already have is never
changed. To start without it, set `--addons-official-library=false` before the first
start. To get it back, add a source with the URL above, or enable it again (see
[Library sources](#library-sources)).

### Upgrading: Studio and Talk are add-ons now

Studio (image, video, 3D, speech, sound and audio) and Talk (realtime voice) used
to be pages of LocalAI. They are add-ons now, and two things change at the upgrade:

- **Nothing is installed for you.** No add-on is preinstalled. Studio and Talk
  appear only after an administrator installs them from the library (see
  [Add-ons that are not built in](#add-ons-that-are-not-built-in)). Until then the
  old links open a page that says the add-on is not available.
- **Users need the `addons` feature.** With authentication on, every add-on page is
  behind the `addons` feature, which is off by default for a user. A user who had
  the image, speech or realtime features but not `addons` loses Studio and Talk at
  the upgrade. The upgrade does not grant `addons` to anyone. An administrator
  always has it.

To restore access:

1. Install Studio and Talk: **Add-ons**, **Find add-ons**, then **Install and turn
   on** for each. A machine with no access to the network installs them from a
   private library that mirrors the packages (see
   [Private catalog service](#private-catalog-service) and
   [Library sources](#library-sources)).
2. Grant the `addons` feature to each user who should keep them, in
   **Settings → Users and keys** (see [Chats permissions](/features/chats/#permissions)
   for the same screen). The calls the pages make still need the user's own
   features: `images` for an image, `audio_speech` for speech, and so on.

With authentication off there are no user features: the one person is an
administrator and sees every installed add-on.

**Release notes.** The paragraph below is ready to copy into the release notes:

> **Studio and Talk are add-ons.** Studio (image, video, 3D, speech, sound and
> audio) and Talk (realtime voice) moved out of LocalAI into add-ons. Nothing is
> preinstalled: after the upgrade an administrator installs them from **Add-ons →
> Find add-ons** (an air-gapped host uses a private library that mirrors the
> packages). With authentication on, the add-on pages need the `addons` feature,
> which is off by default and is not granted at the upgrade, so users who used
> Studio or Talk lose access until an administrator grants `addons` to them in
> **Settings → Users and keys**.

## Library sources

A library source is a place LocalAI reads entries from. There are three kinds:

| Kind | What it provides | Read by |
|---|---|---|
| `addon` | Add-ons, as a library index | The Add-ons catalog |
| `backend` | Backends, as a library index or a backend gallery index | The Backends page, and the add-on catalog for a library |
| `model` | Models, as a library index or a model gallery index | The model gallery |

All sources of all kinds are in one store, `addons/sources.json` under the data path.
`local-ai library` (see the [CLI reference](/reference/cli-reference/#library-sources)), the
HTTP routes below and the Sources tab work on the same store with the same code, so
a change made in one place shows in the others.

A source can be a default: one that LocalAI ships (the official add-on library, the
model library and gallery index, and the backend library and gallery index). Of the two
model sources and the two backend sources only one is on by default, the one the build
reads (see [Which source a build reads]({{% relref "features/backends#which-source-a-build-reads" %}})). A default is marked `default`. If
you remove a default, LocalAI keeps a record of the removal (a tombstone, `removed:
true`), so the default does not come back at the next start. Turn the source on again
to bring it back. A source you added is deleted when you remove it.

### The Sources tab

The Sources tab of the Add-ons page lists the sources of all three kinds. Use the kind
filter to show only add-ons, backends or models. Each source shows its kind, a Default
badge for a shipped default, and its state: enabled, disabled, removed or, while the
official libraries are off, off. Administrators can add a source (kind, format, name and
address), turn a source on or off, and remove it. A removed default stays in the list
with a Restore button. An add-on source can also be edited, to set its signature policy
and credential reference, and refreshed. Backend and model sources have no signature
policy of their own.

### Conflicts

When two libraries list the same entry, LocalAI does not choose silently:

- A source you added wins over a default. The default is ignored for that entry.
- Two sources you added that list the same entry are a conflict. LocalAI refuses to
  install or resolve that entry until you turn one of the sources off or remove it.

The list shows the message next to each source that clashes. Only sources that LocalAI
fetches as a library index can clash, so gallery-format sources never show a conflict.

### Use official libraries

The setting `official_libraries` (default on) switches all default sources of all
three kinds at once. In the Settings page it is the toggle **Use official libraries**,
in the group Backends and galleries. On the command line it is
`--official-libraries` (`LOCALAI_OFFICIAL_LIBRARIES`).

When it is off, LocalAI does not read the default sources. The default add-on library,
model gallery index and backend gallery index are not used for search, install or
update checks. They stay in the store, still marked `default`, and the Sources tab shows
them as off (`suspended: true` in the routes). Each source keeps its own enabled state, so turning the
setting back on restores exactly what was there. Sources you added are not affected. The
change applies at once, with no restart. On a frontend the switch is read-only: the
owner's value applies, and a change there reaches the frontends within 60 seconds.

### Source routes

The routes are for administrators. A caller who is not an administrator gets `403`, and a
caller who is not signed in gets `401` when authentication is on. They are not add-on
routes, so they do not need the add-ons owner to run. On a frontend they go to the owner
(see [Frontend limits](#frontend-limits)).

| Method and path | What it does |
|---|---|
| `GET /api/library/sources` | List every source of all kinds, tombstones included |
| `POST /api/library/sources` | Add a source: `{"kind": "addon\|backend\|model", "url": "...", "id": "...", "name": "...", "format": "library\|gallery"}`. `id`, `name` and `format` are optional. The same kind and URL twice, or a taken id, is `409`. An add-on library at an `http` address of this machine is `400`: the owner refuses packages that do not come over `https`, so such a library would never list anything |
| `POST /api/library/sources/{id}/enable` | Turn a source on. This also brings back a removed default |
| `POST /api/library/sources/{id}/disable` | Turn a source off without removing it |
| `DELETE /api/library/sources/{id}` | Remove a source. A default leaves a tombstone |

The list answers `{"sources": [...], "officialLibraries": true}`. Each source has `kind`,
`id`, `name`, `url`, `format`, `enabled` (what LocalAI does with it), `default`, `removed`,
`suspended`, `conflict` (the message, when there is one), `policy` and, for an add-on
source, `refresh` (the last fetch). A source that does not exist is `404`. Changes to
model and backend sources apply to the running server. No MCP tool maps to these
routes.

## Backends in the library

A library lists more than add-ons. Every entry has one schema and an optional `kind`
field. The kinds are:

| Kind | What the entry installs | State |
|---|---|---|
| `addon` | A supervised add-on package. It is the default: an entry without `kind` is an add-on, so every index written before kinds existed stays valid. | Supported |
| `backend` | A backend image for the backends directory. | Supported. The backend library is the source of the backends. |
| `model` | A model. | Reserved. No reader installs it yet. |

The backend library (`gallery/backends/index.yaml`) is the source of truth of the
backends, the way the model library is for the models. The backend gallery
(`backend/index.yaml`) is a generated copy of it. A tagged build before the cutover reads
the gallery by default; a build from master reads the library. See
[The backend library](#the-backend-library). A library entry of kind `backend` can also be
installed by digest from the Find add-ons tab, as described below.

### How a reader treats an unknown kind

A reader keeps the entries of the kinds it knows and skips every other entry. It does
not read the fields of a skipped entry, so a newer kind can carry fields this version has
never seen. The index is not refused. A skipped entry is reported in two places:

- the refresh result and the state of the library have `skipped` (a count) and
  `skippedKinds`. The Sources tab shows them;
- the refresh writes a warning to the log.

A kind that is not a plain lowercase name, or that is not text, is a malformed entry
and the index is refused. An unknown field on an entry of a known kind is still refused.
Search lists add-ons unless the caller asks for a kind: `catalog-search` takes an optional
`kind` argument (`addon`, `backend` or `all`).

### A backend entry

```yaml
- kind: backend
  id: cpu-local-store           # the backend name, also its directory name
  alias: local-store            # optional: the name a model uses
  name: cpu-local-store
  description: Local Store is a local-first vector database.
  license: MIT
  icon: https://example.com/local-store.png
  urls:
    - https://github.com/mudler/LocalAI
  tags: [vector-database, CPU]
  version: 0.0.0-latest.0a1056fd8633
  channel: latest
  schemaVersion: 1
  artifact: oci://quay.io/go-skynet/local-ai-backends:latest-cpu-local-store
  digest: sha256:0a1056fd863328adf9d1097808aa2b52f68c4ca2e5385100a754755b3ca6218f
  mirrors:
    - oci://localai/localai-backends:latest-cpu-local-store
  capabilities:
    default: cpu-local-store
    metal: metal-local-store
  os: linux
  arch: amd64
```

One entry is one image for one platform, so a backend built for several platforms has
several entries with the same `id`. The reader is strict: an unknown field on an entry of
kind `backend` is refused. These fields are for a backend only and an add-on entry that sets
one is refused:

| Field | What it holds |
|---|---|
| `schemaVersion` | The version of the shared entry header. Absent means 1. A host refuses a higher one. |
| `license`, `urls`, `tags` | Display text, as in the gallery. `urls` are `http` or `https`. |
| `icon` | The URL of the icon, `https` only. The add-on `ui.icon` stays a path in the package. LocalAI never fetches the icon: the browser loads it. |
| `mirrors` | Fallback images of the same backend, tried in order. Each is an `oci://` image. The install pins a mirror with the digest of the artifact. |
| `capabilities` | A map from a host capability (`default`, `nvidia`, `metal`, ...) to the `id` of the concrete backend that serves it. |
| `mutable` | The artifact is a moving tag, such as a development image. The entry has no `digest`. |

The `digest` is the digest of the image for the platform. The install uses the image by
digest and never by tag. A backend entry must use an `oci://` artifact. It has no main
page or `ui.icon`, and only a backend entry can set `alias`. The backend name is a directory
name: it has no `/` and no `..`.

**Meta backends.** An entry with `capabilities` and no `artifact` is a meta backend, such
as `llama-cpp`. It has no `digest`, `os`, `arch` or `mirrors`, and it is listed for every
platform. Installing it installs the backend its capability names for this host, in the
same library and the same channel, pinned and verified like any other. A meta backend that
names nothing for the host, or names a backend the library does not have, is refused.

**Development images.** A `master-` image is rebuilt all the time, so the library cannot
pin it. Its entry keeps the tag, has `channel: development` and `mutable: true`, and has no
`digest`. The install resolves the tag to a digest, verifies that image, and records the
digest it installed in `metadata.json`.

### Signer identity pattern

The signature policy belongs to the library, as for add-ons. A backend library can name
the signer with `identityRegex` instead of an exact `identity`, so images that the merge
workflow signed on master and on a tag both verify:

```json
{
  "requireSignature": true,
  "issuer": "https://token.actions.githubusercontent.com",
  "identityRegex": "^https://github\\.com/mudler/LocalAI/\\.github/workflows/backend_merge\\.yml@refs/(heads/master|tags/.+)$"
}
```

Set `identity` or `identityRegex`, not both. The pattern must be one expression that
starts with `^` and ends with `$`. A pattern with a top-level alternation, such as
`^a|b$`, is refused, because one branch would match a part of the identity only. An
add-on install refuses a library policy that has a pattern: add-on libraries name the
signer exactly.

### Install a backend from a library

An administrator installs a backend from the Find add-ons tab of the Add-ons page. The entry
shows its kind. The page sends the install to the backend route, not to the add-on
lifecycle. The page installs an add-on and turns it on in one action (Install and turn
on); the install itself leaves an add-on disabled. A backend has no on or off: it is
ready to use when the install ends, and its button says Install.

The route is `POST /backends/library/install`. It is for administrators and exists where
the libraries are: on the instance that runs the add-on owner, and on a frontend, which asks the
owner to resolve the entry and installs the backend on its own node:

```bash
curl -X POST http://localhost:8080/backends/library/install \
  -H 'Content-Type: application/json' \
  -d '{"sourceId":"official-backends","name":"cpu-local-store","version":"latest"}'
```

The request names the entry and nothing else. The library resolves the image, the digest
and the policy, so a request cannot change them. The install:

1. resolves the entry of kind `backend` for this platform;
2. refuses it when `--require-backend-integrity` is on and the library does not require a
   signature;
3. pins the image by digest, verifies the signature before it fetches any layer and refuses
   an image over the size limit (the layers are summed from the manifest);
4. installs with the same code as the gallery, so the backend lands in the same directory
   with the same `metadata.json`, plus `library`, the ID of the library it came from.

The request answers at once with a job, the same kind of job the backend gallery uses:
`{"id": "...", "status_url": ".../backends/jobs/<id>"}`. Read `GET /backends/jobs/<id>` to
follow it. The `phase` of the job is the step it is in, in this order: `resolving`,
`verifying` (only when the library requires a signature), `size`, `fetching` (with
`progress` and the sizes of the fetch) and `writing`. A job that ends has `processed` set.
It ends with the phase `done` and `message` `completed`, or with `error` set and the
`phase` of the step that failed. The Find add-ons tab shows these steps. Refusals that depend on
the entry arrive this way: an unknown entry, a name installed from another origin (a
gallery, another library or a manual install; nothing changes), an image over the size
limit, a library that strict mode refuses. The request itself answers `400` when it does
not name an entry and `409` when the same backend is already being installed. Installing
again from the same library is a reinstall.

### Library backends on the Backends page

The Backends page marks a backend that came from a library with the library ID, and the
detail view shows the digest that `metadata.json` pinned. A backend of a gallery has no
mark, and the gallery upgrade check and the review of updates are unchanged.

When the cached index of the library lists a different build than the one installed, the
backend shows "Update available" and an Update button. The check is
`GET /backends/library/updates`. It reads the index the last refresh cached, so it does
not use the network, and it installs nothing. The build to compare is the one in the
channel of the installed version. If the library no longer lists that version and has
more than one channel, the check says nothing instead of guessing. The Update button
starts the same install as above, with the same steps, for the version the check names.
There is no automatic upgrade.

The LocalAI Assistant and `local-ai mcp-server` expose this check as the read-only
`list_backend_library_updates` tool. Taking an update stays a confirmed
`install_library_backend` call.

A library install touches only the node that serves the request. In a distributed
deployment the backend is installed on that node only; the per-node routes of the backend
gallery are unchanged.

### Size limits

The limit is on the compressed size of the package or image, by kind:

| Kind | Default | Configure |
|---|---|---|
| `addon` | 512 MiB | No |
| `backend` | 8 GiB | `--addons-backend-max-bytes` |

### The backend library

`gallery/backends/index.yaml` is the backend library. It lists every backend of the
backend gallery: 1088 backends (934 concrete backends and 154 meta backends) in 1240
entries, because a backend built for several platforms has one entry for each. LocalAI
registers it on the first start as the source `official-backends`, next to the backend
gallery `localai-backends`, and a source you remove stays removed. To add it again later,
use the Sources tab with this URL and the policy above:

```
https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/backends/index.yaml
```

The library is the source of truth. To add or change a backend, change the library; see
[Backends]({{% relref "features/backends#the-backend-library-and-the-generated-gallery" %}}).
The digests are never edited by hand. Pin them again after the backend images are rebuilt;
this is the only command that reads the registry:

```bash
go run ./scripts/backends/cmd/backendcatalog \
  --library gallery/backends/index.yaml --repin
```

A release image that the registry has no image for when the library is rendered is marked
`mutable` and has no digest, and the command lists it. It installs by its tag until it is
pinned.

## Manifest

`ParseManifest` accepts a single JSON object, at most 64 KiB, and rejects unknown
fields, duplicate keys, trailing values, and nesting beyond 64 levels.
`ValidateManifest` additionally checks the supplied host compatibility tuple.
See `tests/addons/fixtures/contracts` for service-only, headless service/tool,
page/navigation, chat-action-only, and incompatible-host fixtures.

The required fields are `schemaVersion` (1), `id`, `version`, `executable`, `os`,
`arch`, `host`, `api`, `protocol`, `dataFormat`, and `capabilities`. Compatibility
ranges are inclusive positive integer `{min,max}` pairs, not semver expressions.
The package version uses immutable `major.minor.patch`, optionally a lowercase
prerelease starting with a letter; moving aliases and build metadata are not
package versions. Registry channels are separate from package identity.

Namespace IDs match `[a-z][a-z0-9-]{0,63}`. IDs are never paths. An add-on ID
must not be one of the first path segments the host serves under `/api/addons/`:
`apps`, `proposals`, `artifacts`, `resources`, `conversations`, `dispatch`, `relay`,
`catalog`, `icon`, `assets`, `installed` and `sources`. Such a manifest is refused at
parse, at install and when a catalog library is refreshed, because the asset URL
`/api/addons/<id>/assets/...` would be answered by the host route of the same
name. A test derives the registered segments from the real routes and fails when
one is missing from this list. Executable and UI
asset paths are normalized package-relative ASCII paths: no absolute paths,
parent traversal, backslashes, percent escapes or URL schemes. These checks do
not substitute for safe extraction, symlink checks, executable mode validation,
or checking declared files exist, which belong to package installation.

Capabilities are independent: `service` is a boolean, `tools` is a unique list
of namespace names, and optional `ui` contains required `entry` and `sdk` range,
optional `css`, `pages`, `navigation`, and `chatActions` lists. Navigation names
must refer to declared pages. UI assets must not duplicate each other. Absent UI
requires neither assets nor SDK compatibility. Declared UI must be consistent;
an empty object does not count as a valid declaration. A chat action is either
a bare identifier (inert: it has no placement and is never drawn) or a full
declaration, and `ui.slots` and `ui.renderers` place components in closed host
slots (see [Extension points and slots](#extension-points-and-slots)). They are
host-controlled entries, not DOM hooks or permission to read chat history. Platform tuple equality is compatibility only,
not evidence that any release platform has passed native runtime tests.

Four optional fields describe how an addon appears as an app (see [Apps](#apps)).
Every one is optional, so earlier manifests stay valid and a headless manifest
needs none of them.

| Field | Meaning and bounds |
|---|---|
| `launchMode` (top level) | Absent or `always`: the owner runs the addon while it is enabled. `on-demand` is reserved and, like any other value, is rejected at install. |
| `ui.mainPage` | The page the Open action navigates to. It must be one of `ui.pages`. Without it the addon has no Open action. `ui.entry` keeps its meaning: the packaged ES module. |
| `ui.icon` | A packaged image: `.png`, `.webp`, `.jpg` or `.jpeg`, a normalized package-relative path of at most 256 bytes, at most 256 KiB. A remote URL, a `data:` URL, an absolute path and a traversal are rejected. It cannot also be the module or a stylesheet. |
| `ui.title`, `ui.description` | Plain text: one string, or a map of BCP 47 language tag to string (at most 16 tags). At most 80 and 280 characters. Markup characters (`<`, `>`), control characters and direction overrides are rejected. |

### Skills

An add-on can ship skills, the procedures a chat model reads before a task
(see [Skills]({{%relref "features/chats#skills" %}})). The manifest declares
them in `capabilities.skillDefinitions` and the package holds the files:

```json
"skillDefinitions": [
  { "name": "office" },
  { "name": "office-quickstart" }
]
```

```
skills/<name>/SKILL.md
skills/<name>/references/<file>      (up to 3 folders below the skill folder)
```

The description is not in the manifest: the `SKILL.md` frontmatter is its only
source. The manifest is checked at parse, at install and when a library is
refreshed:

| Rule | Limit |
|---|---|
| Skills per add-on | 16 |
| Name | `[a-z][a-z0-9-]{0,63}`, unique in the manifest |
| A plain name | the add-on ID, or the ID and `-` and more (`office-quickstart`), so two add-ons never ship one name |
| A built-in skill name | refused for a plain skill (`writing-skills`, `localai-workspace`, `localai-apps` and the office skills) |
| `shadows` | optional; must repeat `name` and be a replaceable built-in skill: `xlsx`, `docx`, `pptx` or `pdf` (`contract.ReplaceableSkills`) |

Staging checks the files against the manifest. The folders under `skills/`
must be the declared names, and a package that declares no skills must have
no `skills/` folder. Every entry is a plain file or a folder: links, special
files and executable files are refused. A path segment uses lowercase ASCII
letters, digits, `.`, `_` and `-`, does not start with a dot, and a path is at
most 128 bytes. A file is a `.md`, `.txt`, `.json`, `.csv`, `.yaml` or `.yml`
file of UTF-8 text with no NUL byte. **There are no scripts**: `.py`, `.sh`,
`.js` and other files are refused. A skill that needs to run code is a
built-in skill with `requires: [exec]`. `SKILL.md` is at most 64 KiB and must
parse with a `name` equal to its folder and a `description`; a reference file
is at most 256 KiB; a skill has at most 64 files and 1 MiB, and a package at
most 4 MiB of skill files. The receipt records each file with its size and
SHA-256 digest.

The add-on owner copies the skills into the chats' add-on skill layer, in the
workspace at `/addon-skills/<add-on>/`, when it installs or upgrades the
add-on and every time it starts, and deletes them when the add-on is
uninstalled. Each copied file is checked against the digest in the receipt,
so a package folder changed after install never reaches a chat. A copy that
fails is logged; the add-on itself still installs and runs. The files stay
while the add-on is stopped. A chat offers them only while the add-on is
installed, running, usable by the user and on for that chat, and for a chat
with a saved agent only when the agent grants the add-on.

#### Skills that replace a built-in skill

An add-on skill can replace a built-in skill of the same name, as a tool
replaces a built-in tool (see [Tools that replace a built-in
tool](#tools-that-replace-a-built-in-tool)). Only the office skills `xlsx`,
`docx`, `pptx` and `pdf` can be replaced; `writing-skills`,
`localai-workspace` and `localai-apps` cannot. The manifest says so with
`shadows`, which repeats the name:

```json
"skillDefinitions": [
  { "name": "xlsx", "shadows": "xlsx" },
  { "name": "docx", "shadows": "docx" }
]
```

- **In a chat.** While the add-on can serve (installed, running, usable by the
  user and on for the chat), the model sees one `xlsx` skill, the add-on's,
  and the built-in one is hidden. The index grows by nothing: one line
  replaces another. A skill a person writes (folder, personal or shared) still
  wins over the add-on's.
- **The built-in comes back.** When the add-on is stopped, off for the chat or
  removed, or the chat switches the add-on's skill off, the built-in skill
  serves again. The turn tells the model in one line, and the Skills menu
  says so on the built-in skill.
- **Checked twice.** The install refuses a `shadows` that is not on the list,
  and the chats refuse it again at each turn: an add-on skill with the name of
  a built-in skill that cannot be replaced is skipped as invalid.
- **Several add-ons.** Only one add-on serves a name. An administrator picks it
  in Settings, **Tools**, in the same runtime setting as the tools
  (`chats_tool_providers`, for example `{"xlsx": "office"}`; tool and skill
  names never overlap). With no choice, the add-on installed first serves, by
  the same rule as for the tools (see [Install
  time](#tools-that-replace-a-built-in-tool)). A choice
  of an add-on that is not running leaves the name to the built-in skill. The
  Skills page marks the skill of an add-on that is not chosen "Not active:
  <add-on> provides it", and the built-in skill "Replaced by <add-on>".
  `GET /api/chats/admin/tools` lists the replaced skills in `skills`, with who
  serves each one, the candidates oldest install first, `default` (the one that
  serves with no choice) and `installed_at` (the install time of each).
- **Before install.** The library entry and the installed view list the
  skills an add-on replaces in `skillShadows`, and the install dialog names
  each one: "This add-on replaces the built-in skill xlsx." When another
  installed add-on already replaces it, the dialog says that only one can be
  active and that the choice is in Settings > Tools. The install is still
  allowed.
- **On the Add-ons page.** The details of an add-on list its chat skills and
  the built-in skills it replaces. For an administrator, a skill that several
  add-ons replace has a switch, **Use for xlsx in the chats**: on, this add-on
  serves it; off, the next add-on that replaces it does. The switch writes the
  same choice as Settings > Tools.

In distributed mode the state is **shared**: the files are in the workspace
store that every frontend reads, and only the owner writes them, because only
the owner holds the verified package. Which add-ons are usable is read from
the broker at each turn, as for the tools. With the owner down the add-ons
are down, their skills are not offered and the built-in skills serve.

### SDK versions

This host provides SDK version 2. An add-on that has a UI declares the SDK
versions it works with in `ui.sdk` as a range, for example
`"sdk": {"min": 1, "max": 2}`. The host installs and loads the add-on only when
its own SDK version is inside that range. If it is not, the install is refused
with a message that names the range the add-on needs and the SDK version the
host provides:

```text
the add-on needs SDK 3 to 3 and this host provides SDK 2
```

The dispatch response for that refusal has status 403 and error type
`sdk_incompatible`, with the message above. A package that does not match its
library entry, or is not a valid add-on package, is 403 with error type
`package_unverified` and a message that says nothing was installed. A failed
install or upgrade can also name its step in the error type: `library_unavailable`
(the library could not be read), `library_conflict` (two enabled libraries list the
add-on), `release_not_found` (the library has no such add-on or version for this
platform), `package_fetch_failed` (the package could not be downloaded) and
`install_storage_failed` (the package could not be written to the add-on storage).
The owner writes the underlying error to its log as `Addon operation failed`, with
the action, the add-on ID and the operation ID. Every other failed install keeps the
generic `Addon request denied` message. The owner reads the library once on demand
when its startup refresh left nothing in the cache, so an install does not depend on
that refresh having finished. If the administrator sends the same
operation ID again, the owner returns the stored failure, so the repeat gets the
generic message and not the refusal text. Use a new operation ID to see the
refusal again.

### The SDK 2 surface

An add-on page gets these from the SDK object. The page component also receives the
query string of its address as `search`, next to `page` and `path`. Props pass through unchanged, and
the components are the ones the core pages use, so an add-on page and a core page
draw the same thing.

- `sdk.ui`: `Button`, `Text`, `Stack`, `Badge`, `Spinner`, `EmptyState`, and the
  shared `PageHeader`, `ConfirmDialog`, `ErrorWithTraceLink`, `ModelSelector`,
  `MediaInput`, `WaveformPlayer`, `DistributionBars`, `EmbeddingInspector`,
  `TabSwitch`, `LoadingSpinner`, `Modal`, `Lightbox` and `HomeModelPicker`. A `ref`
  reaches the host component, so `HomeModelPicker` keeps its handle.
- `sdk.ui` also has the settings kit, for a settings section or any form: see
  [The settings kit](#the-settings-kit).
- `sdk.theme.tokens`: the host's design tokens as CSS values (`var(--dk-...)`), for the
  add-on's own scoped style sheet: `text`, `muted`, `surface`, `inset`, `edge`, `hover`,
  `accent`, `accentText`, `accentWash`, `onAccent`, `okText`, `okWash`, `warnText`,
  `warnWash`, `errorText`, `errorWash`, `space1` to `space6`, `radius`, `radiusSmall`,
  `radiusMedium`, `textMeta`, `textSmall`, `textBody`, `fontBody` and `fontMono`. Every
  value is a custom property the host theme defines, in both themes. The same custom
  properties can be written in the style sheet directly.
- `sdk.hooks`: `useMediaCapture`, `useObjectUrl`, `useDelayedAction`,
  `audioBufferToWavBlob`, `useModels`, `useResources`, `useVoiceProfiles`,
  `useOperations` and `useMCPClient`. They run in the host React, so call them like
  any hook. A host without them gives an empty object.
- `sdk.navigation.openApp(address, { replace, state })`: opens a page of the web app,
  a core page or a page of another add-on. Only addresses under `/app` pass; any
  other address, scheme or API route is refused. The only `state` a page may pass is
  the way back, `{ from, fromLabel }`, which the Model Editor reads.
- `sdk.portal(children, node)`: draws children into a node outside the add-on's own
  tree, for a full-screen sheet.
- `sdk.viewer`: `isAdmin()` and `hasFeature(name)`. They answer for the signed-in
  user and are read-only. The server still decides every call.
- `sdk.iconNames`: the icon names `sdk.Icon` can draw. Any other name draws a
  neutral glyph. SDK 2 adds `arrow-left`, `arrow-right`, `chart-bar`, `circle-dot`,
  `clipboard`, `code`, `cube`, `filter-off`, `gender`, `globe`, `headphones`,
  `help-circle`, `mic`, `play`, `plug-off`, `save`, `shield`, `smile`, `waveform`,
  `camera`, `stop`, `circle`, `image`, `spinner`, `user-shield` and `mask`. The Studio
  and Talk add-ons add `boxes`, `compass`, `gauge`, `ghost`, `git-branch`, `images`,
  `key`, `layers`, `maximize`, `memory`, `music`, `pencil`, `repeat`, `server`,
  `sliders`, `square-dashed`, `store`, `undo`, `video`, `volume`, `palette`, `phone`,
  `columns` and `chevron-left`. `sdk.Icon` passes a `style` object.

SDK 2 is a superset of SDK 1, but a range is checked as written. A manifest
that declares only `"max": 1` is refused by a version 2 host. Before you raise
`max` to 2, check the add-on against the SDK 2 surface. A range that is
malformed (for example `min` above `max`) keeps the generic `invalid or
incompatible UI` error.

## Runtime wire version 1

JSON field names and ordering follow the exported DTO declarations and
`encoding/json`; `envelope.json` fixes the canonical envelope representation.
Opaque `arguments`, `payload`, and preview `summary` are JSON, not executable
SQL or authority. This is not a cross-language cryptographic canonicalization
scheme; forwarding must bind the actual transmitted bytes. Unknown/duplicate
fields are rejected at manifest and envelope/result/preview/receipt boundaries.

An envelope consists of `request` and host-issued `authority`. Request carries
`addonId`, optional `resourceId` (empty for addon-level actions), `operationId`,
nonzero lifecycle `generation`, `action`, and object `arguments`. Authority
carries `actor` (`userId`, `credentialId`, `mode`), `scope`, and RFC3339
`expiresAt`. Modes are `named-user` and explicit `local-owner`; scopes are
`read`, `preview`, `apply`, `output` and `act`. The scope `act` and the action
`act` go together: an envelope has one only with the other. Principal IDs are opaque nonempty
strings up to 256 bytes. Syntactic validity is **not authentication**: the owner
must create authority, verify current identity/credential/ACL and mode, and bind
scope to the actual method. Never accept browser/model-provided approval.
Call `Validate(now, limits)` at admission; unmarshalling checks structure and
nonzero expiry but intentionally does not use the wall clock.

`Runtime` provides Read, Preview, Apply, Receipt and OpenOutput. A runtime that
serves action tools also implements `ActRuntime` (`Act`), described below. Preview is
`{operationId,revision:{schema,data},expiresAt,summary}`. Schema revision is
nonzero; data revision can start at zero. Apply receives that exact revision
separately and must reject stale schema or data. Receipt is
`{operationId,state,revision:{schema,data},result}`. States are `pending`,
`committed`, `rejected`, `unknown`; unknown is not success or rejection and must
be reconciled before another mutation. Committed receipts require a nonzero
schema revision. Result has optional `payload` and `outputs`; each output has
opaque `handle`, safe basename `name`, MIME `mediaType`, and nonnegative `size`.
Handles are not URLs or filesystem paths. OpenOutput returns an authenticated
stream; callers close it. DTOs do not create artifacts or authorize downloads.


### The settings kit

`sdk.ui` carries the host components a settings section, or any form of an add-on, is
made of. They use only the host's classes and tokens, so an add-on draws its settings the
way the host draws its own, in the light and the dark theme and at phone width, without a
style sheet of its own. Use them instead of plain `<input>`, `<select>` and class names of
your own: a class the host does not define has no style.

| Component | Props | What it draws |
|---|---|---|
| `Section` | `title`, `description`, `actions`, `testId` | A titled block with a line under the title. Sections that follow each other are separated by a rule. |
| `Field` | `label`, `help`, `unit`, `error`, `testId` | A label, the control (the children), its unit after it and a help line under it. The label names the control. |
| `Select` | `value`, `onChange(value)`, `options: [{value, label, disabled}]`, `label`, `disabled`, `testId` | The host select. |
| `TextInput` | `value`, `onChange(value)`, `type` (`text`, `password`, `number`, `url`, `search`), `placeholder`, `min`, `max`, `mono`, `label`, `disabled`, `testId` | The host text input. |
| `Switch` | `checked`, `onChange(next)`, `label`, `disabled`, `testId` | The host switch. |
| `Choice` | `legend`, `hideLegend`, `value`, `onChange(value)`, `options: [{value, label, description, chip, disabled}]`, `disabled`, `testId` | One choice of a few, each option with a line that says what it does. |
| `StatusChip` | `tone` (`ok`, `warn`, `error`, `idle`, `accent`), `title`, `testId` | A state in colour, icon and words. |
| `Callout` | `tone` (`info`, `ok`, `warn`, `error`), `title`, `actions`, `role` (`alert`), `testId` | A notice inside the page. |
| `List`, `ListRow` | `List`: `label`, `testId`. `ListRow`: `title`, `meta`, `status`, `details`, `detailsLabel`, `testId` | Rows in one bordered list: a name, a line under it, a chip on the right, and details collapsed under a toggle. |
| `Code` | `testId` | A monospace block for a raw error or a command, wrapped and scrolled. |
| `Actions` | none | A row of buttons that wraps. |
| `Table` | `caption`, `columns: [{key, label}]`, `rows`, `testId` | The host table. |
| `ExternalLink` | `href` (https only), `testId` | A link out, in a new tab. |

The Sandbox and Web sections are made of these components only (`addons/sandbox/ui/src/Settings.jsx`,
`addons/web/ui/src/Settings.jsx`).

### Operation IDs and errors

Generate an operation ID once, before submission. Bind it to actor, addon,
resource, generation, action, arguments, and expected revision. Repeated IDs
with identical binding retrieve the same durable outcome; changed binding is a
conflict. Never retry a mutation under a new ID after timeout, disconnect or
unknown receipt. Cancellation does not prove rollback. Runtime implementations
must preserve these invariants transactionally; these contracts contain no
storage or deduplication implementation.

HTTP adapters should map malformed input to 400, missing authentication to 401,
current permission denial to 403, absent permitted resources to 404, stale
revision/generation or conflicting operation binding to 409, expired proposals
or artifacts to 410, oversized requests to 413, admission/quota limits to 429,
and unavailable owner/runtime to 503. A timeout (504) is an uncertain mutation
outcome requiring Receipt lookup, not implicit retry. Errors must be redacted
and must not leak paths, credentials or unauthorized resource existence. No HTTP
error DTO or endpoint is implemented by this package.

## Finite implementation defaults

Start with `DefaultLimits()`, change explicit host configuration fields, then
call `Validate()`. All values must remain positive; zero never means unlimited.
Durations are `time.Duration` (nanoseconds if serialized as JSON). These are
implementation defaults, not immutable product requirements.

| Limit | Default |
|---|---:|
| Manifest / envelope / payload bytes | 64 KiB / 2 MiB / 1 MiB |
| Read / preview / apply / shutdown timeout | 30 s / 30 s / 60 s / 10 s |
| Initial / maximum backoff; attempts | 250 ms / 30 s; 5 |
| Batch size / query depth / query rows | 100 / 8 / 1000 |
| Query result bytes | 1 MiB |
| Proposal expiry / artifact expiry | 5 min / 1 hour |
| Single artifact / aggregate artifact quota | 100 MiB / 1 GiB (a stored artifact is at most 64 MiB, the relay bound) |
| Artifact count / concurrency | 100 / 4 |

Initial backoff cannot exceed maximum; single artifact cannot exceed quota;
query bytes cannot exceed payload bytes, which cannot exceed envelope bytes;
query depth cannot exceed the parser's 64-level ceiling. Runtime/owner consumers
must enforce timeouts, bounded backoff (without new mutation IDs), query/batch
limits, proposal lifetime, artifact expiry/quotas and concurrency. DTO validation
alone does not run timers or jobs. Default JSON unmarshalling and ParseManifest
use fixed default ingress bounds; a transport needing different bounds must use
a bounded, strict wire decoder and validate the resulting DTO with its configured
limits (not rely on default unmarshalling to enlarge ingress limits).

## Verification

Run `sh scripts/addons/verify.sh contracts`. The inventory requires every named
Ginkgo case to pass in its JSON report: no empty selections, skips, stale reports,
or successful commands without reports. `sh scripts/addons/verify.sh prerequisites`
regression-tests those gates and checks real native toolchain/browser readiness.

## Private catalog service

`core/services/addons/catalog` provides owner-side source configuration, refresh,
search and release resolution. It does not expose routes, UI, installation or
runtime execution yet. Model and backend managers remain separate.

Construct `Sources` with a protected owner configuration path, an authoritative
`AdminCheck`, and a `CredentialResolver`. `Save` creates or replaces a source;
`Source{ID: id, Removed: true}` explicitly removes one. Source records contain
ID, display name, HTTP(S) URL, enabled state, authentication reference and
verification policy. Files are replaced atomically with mode 0600. The operator
must provide a private parent directory and only one writer. Loading rejects
symlinks and group/world-readable files. Source edits invalidate cached results.
Removing a source never opens or changes installed packages, receipts or data.
Installed receipts must retain their pinned source; changing that association
requires a separate authorized lifecycle operation, not a catalog refresh.

### Authority and credentials

The existing `core/http/auth.RequireAdmin` checks an authenticated Echo user's
role. `contract.Actor` has no role and is not an authenticated Echo context.
This service therefore injects `AdminCheck(context.Context, contract.Actor)`;
a missing checker denies all edits and configuration reads. The future owner adapter must revalidate
current user, credential and administrator status. It must not infer privileges
from user IDs, mode strings, model arguments or browser values. Refresh is an
internal owner operation; a future public refresh/test route must enforce admin
authority before calling it. No new secret endpoint is provided.

Configure reference bindings only in trusted owner code. Each binding pairs an
exact scheme/host/port origin with a `pkg/credentials.Store`. That store supports
bearer/basic secrets, including existing environment/file references. Secrets
resolve at request time. Source JSON contains only the reference; resolver JSON
omits bindings. Catalog credentials are not installed into the OCI downloader's
store and releases do not carry authentication references. Configure OCI pull
credentials separately. Plain HTTP credentials require the store's explicit
`allow_insecure` setting; use HTTPS in production. URLs with embedded credentials,
query parameters or fragments are rejected.

All catalog requests use `pkg/httpclient`, a 30-second timeout, and no redirects.
Redirect, credential, HTTP-status and parse failures return redacted errors, not
remote bodies or URLs. A failed refresh records its check time and last successful
refresh time and prevents stale cached releases from resolving successfully.

### Catalog document and limits

A catalog (the library index) is one YAML document: a block list of releases with
`id` (an index written before this name may still use `addonId`, which is read for one release), `name`, optional `description`, `version`, `artifact`
(`oci://host/repository`), `digest` (`sha256:` plus 64 lowercase hex characters),
`os` and `arch`. It uses the same format as the model and backend gallery indexes:

```yaml
- id: tables
  name: Tables
  description: Tables for LocalAI
  version: 1.0.0
  channel: latest
  artifact: oci://quay.io/go-skynet/local-ai-addons:1.0.0
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
  os: linux
  arch: amd64
  ui:
    title: Tables
    mainPage: databases
```

An entry may carry a `kind` (see [Backends in the library](#backends-in-the-library));
without it the entry is an add-on. Unknown fields, a second YAML document and duplicate keys are rejected. A JSON
array is refused too, even though YAML can read it: write the index as a block
list. An empty library is `[]`. Package manifests (`manifest.json`) stay JSON; only
the library index is YAML. Source identity and verification policy always
come from protected source configuration, not from the remote release.
The policy describes requirements for later package verification; refresh does
not claim to verify executable signatures.

A release may carry an optional `ui` object of display hints: `title`, `description`
(default-language plain text), `mainPage`, `icon` (a path inside the package, never a
URL) and `launchMode`. They follow the manifest rules for bounds, markup and
launch mode, and a catalog that breaks them fails refresh as a whole. They only
help browsing before install: the manifest inside the verified package is what an
installed addon shows, and the host serves an icon only after install.

A release may also carry `toolCount` and `tools`, the same names the installed view
uses: the chat tools the package declares in `capabilities.tools` (admin tools are
not chat tools and are not counted). `tools` holds at most 32 names and `toolCount`
is the full number. The add-on cards, the Find add-ons tab and the details panel show
"Adds N tools" from them before install. `addonpkg catalog` writes both fields, and
`addonpkg catalog-tools` adds them to an existing index from the manifests in
`addons/<id>/manifest.json`. They are a hint like `ui`, and only an add-on entry may carry them.

Refresh accepts at most 1 MiB and 1,000 releases. Duplicate release identities
within a source fail. Duplicate names across sources remain distinct. Search
matches names, IDs and descriptions, sorts by source/release identity and uses
a default page size of 50 (maximum 100). Disabled sources are neither searched
nor fetched. Resolve requires an explicit source, addon ID and exact version;
it selects only the exact owner target OS/architecture. Configure `Catalog.TargetOS`
and `Catalog.TargetArch` in trusted owner code before use; each empty field defaults
to this process's `runtime.GOOS`/`runtime.GOARCH`. Forwarded installs must use the
owner's target, never the forwarding frontend's platform. An absent target returns
`ErrNotFound`; ambiguous exact-target entries fail closed (duplicate identities are
also rejected during refresh). Search still shows all available platforms. Optional `channel` labels (for example `latest`, `development`, or `branch/name`)
resolve to a single exact-platform release and its pinned digest. `version` remains
the immutable manifest version; `selection` is stamped by Resolve, never trusted
from remote metadata. This service does not silently choose a source or upgrade.

`Sources.List(ctx, actor)` and `Sources.Get(ctx, actor, sourceID)` recheck the same
authoritative `AdminCheck` used for edits. List returns all configured sources in ID
order, including disabled libraries and libraries with no releases or no successful
refresh. Get returns `ErrNotFound` for an unknown source after authorization. Both
return independent value copies with authentication references and verification
policy, never resolved credential values. Reads are serialized with source edits
and refresh; configuration remains readable after reload without refreshing.

State uses the documented **per-instance designated-owner** exception: only the
configured owner constructs this service. Configuration persists; the bounded
catalog cache is rebuilt by explicit refresh after restart. Replicas must forward
to that owner, not construct independent caches or fall back on owner failure.

Run `sh scripts/addons/verify.sh catalog` for private bearer/basic HTTP fixtures,
redirect rejection, isolated credential references, disabled sources, refresh
failure, duplicate names, owner-target resolution, source persistence/removal,
denied reads/edits, defensive copies and concurrent configuration reads.


## Verified immutable package staging

The designated owner uses `packages.Installer.Stage` followed by `Commit`. Stage
re-resolves the exact catalog selection and rejects caller-substituted policy,
source, platform, artifact or digest. A stale channel selection must be resolved
again explicitly. Package metadata cannot override the source administrator's
verification policy. Installable code is trusted code, **not a sandbox**.

OCI images are distribution archives, not running containers: no Docker daemon
is needed. The installer pulls the catalog-pinned per-platform manifest digest,
checks image platform and compressed layer hashes, and uses the existing
Sigstore verifier when the configured source requires signatures. The original
reference and selected channel are retained alongside the actual digest. OCI
credentials remain in the registry credential store; catalog HTTP credentials
are never passed to a registry or package URL. A registry may redirect a
digest-pinned blob download to a storage host, as quay.io does. The installer
follows that redirect only, at most three times, without the registry
credential, and checks the blob digest on the bytes it receives. Every other
registry redirect is refused.

When the manifest declares `ui.icon`, staging opens it inside the package without
following links and requires a regular file of 1 byte to 256 KiB whose first bytes
are the signature of the declared type (PNG, WebP or JPEG). A missing, empty,
oversize, linked or mistyped icon fails staging. The receipt records the icon's path,
SHA-256 digest, size and media type; the host serves the icon only if the bytes still
match that digest.

HTTPS package URLs distribute uncompressed tar archives with a required catalog
SHA256. HTTP is rejected unless an administrator explicitly sets the source's
`allowLoopbackHTTP` for a literal loopback IP (development/testing only); public
insecure HTTP and credential-bearing URLs are rejected. HTTP package downloads
are anonymous and do not reuse catalog credentials; redirects are refused.
Archive signature verification is not implemented: a source requiring signatures
must distribute OCI, and HTTP packages from that source fail closed.

Packages contain `manifest.json`, the declared executable, and any declared UI
entry/CSS files. A service-only manifest needs no fake UI files. Host/API/protocol,
data format, SDK (only with UI), identity/version and platform must match. All
entrypoints/assets must be real regular files inside the immutable package;
executables must have the owner execute bit. Links, special files, traversal,
duplicate paths, file/directory collisions, and a supplied `receipt.json` are
rejected. OCI layers cannot overwrite earlier paths: packages are flattened
application bundles, not general-purpose container root filesystems.

Defaults cap compressed downloads and extracted content at 512 MiB (a backend image has its own
[limit](#size-limits)) and archive
entries at 10,000; owner configuration may set smaller positive limits. Staging
is private, cancellation-aware, and removed on failure. Existing packages are
never replaced or deleted. Commit publishes a unique read-only package directory
with `receipt.json` containing source, selection/channel/reference, verified digest,
verification result and manifest. The opaque stage handle is installer-bound and
single-use. Root must be owner-controlled, separate from mutable data, backups,
and addon-writable directories. Publication **does not activate** or start code;
lifecycle journaling and abandoned-stage recovery belong to the owner manager.

Run `sh scripts/addons/verify.sh packages` for local HTTP and OCI registry fixtures,
malicious archives, compatibility checks, cancellation, storage failures and
immutable publication. No external container images are published or downloaded.

Package transport applies the source's `allowLoopbackHTTP` development opt-in
at the OCI transport boundary, including registry authentication and signature
requests. Without it, implicit loopback HTTP fallback is rejected before any
plaintext request. Non-loopback HTTP is always denied, and a blob redirect
target must use HTTPS or, with the opt-in, a literal loopback IP. Other registry
redirects are denied.
The expanded package byte limit counts the entire decompressed stream before
TAR parsing: PAX/GNU metadata, file headers, payload, padding, and trailing bytes,
cumulatively across OCI layers. Downloaded compressed bytes have a separate
budget of the same configured size. Failed staging leaves installed packages intact.

The focused package tests use local OCI registries and block external trust
requests. Hermetic signed fixtures include trusted Fulcio, SCT and Rekor material
and exercise successful cryptographic verification, staging and commit. They also
verify rejection of signed identity, digest and trust mismatches. These offline
checks do not demonstrate live Sigstore verification; production signature
requirements are unchanged.

Extraction bounds both the decompressed TAR stream (including metadata, padding,
and trailing bytes) and cumulative file output across all OCI layers. Sparse
files cannot bypass the output limit by representing large holes with small TAR
entries. Failed staging removes temporary output without changing old packages.

Embedded hosts may explicitly provision `Installer.SignatureTrust` for a private
Sigstore deployment. This is host configuration, never catalog or package data;
it replaces only the trust material. Certificate identity, SCT, Rekor timestamp,
and artifact digest checks remain enabled. Leaving it unset retains public
Sigstore TUF trust. Offline acceptance fixtures generate local CA, signing, CT,
and Rekor keys and exercise the same signature verification and staging path;
they do not disable cryptographic requirements or depend on live services.

## Durable lifecycle and designated ownership

The lifecycle library uses a **designated-owner per-instance state exception**.
It does not use a rotating leader loop, election, or automatic failover.
Embedded hosts construct `addons.NewManager` with an explicit `OwnerConfig`:

- `InstanceID` identifies this process's deployment role.
- `OwnerID` identifies the only role permitted to own addon storage.
- `OwnerAddress` is the configured HTTPS address of that owner.
- `StorageID` identifies the durable local volume.
- `Root` is an absolute, private, owner-controlled directory.
- `SharedStorage` must be false. Shared-storage deployments are unsupported.

The host must assign unique instance identities and a local filesystem with
working advisory locks, atomic rename, and file and directory synchronization.
A process holds the storage lock until `Manager.Close`. The stored owner identity,
address, and storage identity must match on restart. A conflicting configuration
fails instead of adopting existing storage. Do not mount this root on replicas.
A frontend manager never creates or opens the root, even when the owner is absent.
Its lifecycle calls return `ErrNotOwner`; forwarding transport is a separate layer.
Application bootstrap accepts `config.WithAddons(config.AddonConfig{...})`.
`Enabled` defaults to false; no owner is inferred from missing configuration.
The owner loads protected sources and reconciles its journal before exposing
`Application.AddonManager()`. Application shutdown releases the storage lock.
Forwarding instances construct no catalog or installer and never reconcile storage.
`AdminCheck` is mandatory on the owner and excluded from serialized settings.
`Host` optionally sets compatibility; its zero value uses the current host and
version-one contracts. `OwnershipMode` is `named-user` (the default when empty) or
`local-owner`; see "Explicit local-owner mode" below. `local-ai run` sets it from
`--addons-ownership-mode`. Like the rest of `AddonConfig` it is startup-only
configuration with no runtime setting, and every frontend of one
deployment must use the same value.
Application bootstrap starts enabled addon processes after durable reconciliation.

The host supplies an authoritative `catalog.AdminCheck`. It must check current
account, credential, and administrator status, including for duplicate requests.
Actor fields do not grant administration rights. Installation rights do not grant
database access.

`Submit` accepts `install`, `upgrade`, `enable`, `disable`, and `uninstall`.
Install and upgrade consume the actual catalog and immutable package installer.
Installation starts disabled. Enable records desired state; it does not report
that a process is running. Status separates installed state, desired state, and
observed health. `StartEligible` requires ownership, installation, enabled intent,
and no unfinished operation. Runtime supervision is a separate layer.

Operation IDs are global to the owner journal. A retry must carry the same actor
and complete command. Conflicting reuse returns `ErrConflict`. Failed operations
remain failed; an explicit retry needs a new operation ID. Commands serialize
through the manager lock, including commands for the same addon.

The journal synchronizes complete snapshots at intent, prepared, applied, and
completed boundaries. Published packages are synchronized before prepared state.
Call `Reconcile` after restart before admitting runtime work. Recovery fails
unfinished intent rather than repeating a download or publication. Prepared and
applied operations finish from their recorded generation without resolving a new
catalog release. Persistence errors block further commands until reconciliation.

Uninstall clears installed and enabled intent. It retains immutable generations,
addon identity, data paths, and grant metadata. Mutable data lives under `data/`;
owner metadata lives under `metadata/`; immutable packages live under `packages/`.
Reconciliation removes abandoned `stage-*` directories but retains published
packages, including packages published just before a crash. Operators must inspect
unreferenced published packages before removing them. No automatic purge runs.

Drain, backup, and migration hooks are interfaces only. The lifecycle library does
not claim to execute those operations. Later runtime integration must implement
these hooks before changing packages for running processes or migrating data.

Run `sh scripts/addons/verify.sh lifecycle` for journal interruption, ownership,
idempotency, retention, and real catalog/package integration tests.

Owner bootstrap reconciles the journal and starts the enabled add-ons, and refreshes
all persisted enabled sources in the background. The server does not wait for the
refresh: until it ends, the release cache is empty, as after any failed refresh. The
release cache is not durable. The whole startup refresh has a 30-second deadline (or the
earlier application-context deadline); an offline or invalid enabled source costs a log
line, not the start. The enabled add-ons start four at a time. Disabled sources are never fetched. Forwarders neither read source metadata nor
contact catalog/package servers. Embedding hosts can use `Application.AddonSources()`
(owner only; nil on forwarders) for authoritative source administration and explicit
live `Refresh(ctx, sourceID)` calls; no background update loop is started.

Recovery rejects symlinked package roots and ancestors before enumerating staging.
Staging cleanup uses directory-rooted operations and descriptor-based permission
changes so links cannot redirect cleanup into external storage.

## Managed runtime

Application startup attaches the process supervisor only on the configured owner,
after journal reconciliation. Enabled generations start at attachment. Enable,
disable, upgrade, uninstall, and application shutdown synchronize process state;
disable and uninstall retain data. Forwarders do not attach a supervisor or execute
packages. No automatic owner failover is provided.

An upgrade of a running add-on proves the new generation before the journal
moves: the owner stops the running process, starts the new generation and waits
for it to become ready. When it does not become ready, the previous generation
is started again, the journal keeps it as the installed generation and the
operation fails (a new operation ID retries). The published package of the failed
release stays on disk; retained data is untouched. Killing the owner during that
start has the same result after a restart, because the journal never named the
new generation.

An enabled add-on whose process cannot start does not stop the owner. Startup
records the failure for that add-on only: it stays enabled in the journal and
stopped in the observed state, the other add-ons start, and commands on other
add-ons are not delayed or failed by it. The command that enables or upgrades the
broken add-on reports the failure, and a restart tries it again.

The owner executes the receipt's package-relative executable directly with the
single argument `--localai-addon-v1`. There is no shell. The environment allowlist
is `LANG=C` and `TZ=UTC`; host credentials and proxy variables are not inherited.
Descriptor 3 contains an ephemeral listener bound to `127.0.0.1`. Descriptor 4
contains one newline-terminated JSON `runtime.Startup` object: registration,
private random bearer credential, and finite host limits. The child must monitor
that descriptor after decoding and exit on EOF. EOF is cooperative liveness, not
an ownership fence: a paused child may not observe it before its owner restarts.

The supervisor requires a private durable `StateRoot` (application default:
`<addon root>/runtime`). A separately locked, fsynced process ledger records Linux
boot identity, PID, kernel start time, and incarnation before startup credentials
are sent. A pending record is persisted **before exec**. A restarted owner recovers
all recorded children using pidfds, checks exact identity, sends termination via
the retained handle, and waits for confirmed exit before any replacement launch.
Reused PIDs are not signaled. Corrupt, incomplete, unreadable, or uncertain pending
records fail closed; operators must establish that prior children are gone before
repairing the ledger. Do not delete runtime metadata to bypass recovery. Linux
with readable `/proc` and pidfd support is currently required; unsupported systems
refuse runtime startup rather than weaken identity checks. This is not a sandbox
or descendant containment: installed code is trusted, and must not detach workers
outside the supervised process lifetime.

Every local HTTP request requires the bearer credential and
`X-LocalAI-Incarnation`. The authenticated `/health` response must exactly match
the supplied registration, including protocol 1, endpoint, generation, incarnation,
and manifest capabilities. Capabilities are visible only after readiness and are
withdrawn on stop, exit, or failed health. Health is checked periodically. A restart
uses a fresh credential and incarnation, bounded exponential backoff, and the
configured attempt budget; sustained authenticated health resets that budget.

The protocol uses POST with JSON on `/read`, `/preview`, `/apply`, `/receipt`, and
`/output`. Read and preview accept the existing contract envelope. Apply wraps it
with a revision; output wraps it with a handle; receipt accepts an operation ID.
These transport wrappers are defined in the runtime package because the original
contract specifies Go methods but not their HTTP request DTOs. No contract method
or authority semantics changed. Receipt lookup is an internal recovery operation,
not a public authority-free endpoint. The host must authorize access before calling
the runtime client; model arguments cannot supply trusted authority.

The client rejects redirects, nonliteral/nonloopback endpoints, stale generations,
wrong method scopes, expired authority, oversized responses, and excess concurrent
requests. Streams have byte and time limits and propagate cancellation. Stop rejects
new admission and withdraws registration before bounded draining. Termination uses
the retained OS process handle, never a reconstructed numeric PID. An old stopped
instance cannot terminate a newer generation. The addon slot stays reserved during
drain and restart startup, including while the next process is not yet ready.
Stop success means all slot launch workers and processes have finished; cancellation
or cleanup timeout returns an error, retains the reservation until confirmed
cleanup, and never authorizes an overlapping generation. A failed durable cleanup
fences further launches. Stop callers wait at most two shutdown intervals; startup
callers wait at most readiness plus two shutdown intervals.

`Supervisor.Current(ctx, addonID, generation)` and `Manager.Runtime(ctx, addonID)`
explicitly obtain the current ready incarnation under lifecycle locks. A client
previously returned by Start or lookup stays bound to its original incarnation and
rejects calls after withdrawal. No stale mutation is transparently replayed on a
replacement; callers must use the existing operation/receipt recovery protocol.

Default shutdown is 10 seconds;
readiness is 30 seconds, restart delays range from 250 milliseconds to 30 seconds,
and the restart budget is five attempts. Runtime startup failure leaves durable
enabled intent intact for explicit reconciliation; it does not claim healthy service.

Shutdown withdraws admission and starts cleanup for **all** managed addons before
waiting for any child. Cleanup errors are combined; a timeout does not release
the process ledger or owner fence. Retry shutdown to join outstanding cleanup.

A completed lifecycle journal receipt records durable desired state, not proof
that runtime synchronization succeeded. Replaying the same authorized operation
ID retries synchronization against current desired state and returns cleanup or
startup errors until synchronization succeeds. It does not repeat package
publication or restore an older operation's desired state.

If lifecycle publication fails and the manager closes its runtime, that manager
requires recovery. Status, lifecycle submission, and reconciliation cannot report
success using retained instance handles. Repair the storage fault, successfully
close the manager, and create a new owner manager and supervisor to reconcile the
durable journal. The existing Linux proc/pidfd identity checks still apply;
ambiguous pending launches require operator recovery rather than an unsafe kill.

## Action tools

A tool of kind `action` does what a person asked a chat to do, at once, as that
person, and in that person's own space. It is not confirmed in the browser and
it is not a proposal. The sandbox add-on, which runs a command, and the office
add-on, which edits a file the user owns, use it. An `action` tool is not for
data of a shared resource: a change that others see is a `write`, `schema` or
`create` tool, which the OpenAI-compatible route leaves as a proposal for a
person to confirm.

- **Declaration.** The tool is in `capabilities.tools` with a definition of kind
  `action`, and the manifest sets `capabilities.actions` to `true`. The host
  refuses a manifest that declares an action tool without it. An action tool
  names no resource: if its schema has a `resourceId`, the host ignores the
  value the model gives and sets it to the ID of the conversation.
- **Who may call it.** Only the chats consumer (`studio-chats-addons` in the
  consumer table of [Tables](../tables/)). Every other consumer neither lists the
  tool nor runs it: the old chat route, the direct API, agents and scripts.
- **The call.** `POST /act` with an envelope whose action and scope are `act`.
  The actor is the user of the chat. The answer is a `Result`. The call may run
  up to `ActTimeout` (30 minutes), is never retried by the host, and an add-on
  that dies during it gives the caller `outcome_unknown`. The request carries the
  chat's conversation ID in `resourceId`. The host sets it from the chat, the
  model cannot name it, and an add-on uses it only to keep state for that chat,
  never as authority.
- **Undo.** What a tool does to the user's files is a change in the history of
  their workspace, and it can be undone there.

## Host operations

A host operation is how an add-on process asks the host to do something the
add-on cannot do itself, such as starting a job of the engine. It is contract
surface that third-party add-ons cannot use unless the host setting names them.

The rules:

- The host lists every operation in its code. No add-on gets an operation that
  is not listed, and an operation that is not registered is refused. The
  default is refuse.
- The host setting `--addon-host-ops` (`LOCALAI_ADDON_HOST_OPS`) names the
  add-ons that get the channel and the operation prefixes each may call, as a
  comma list of `addon-id=op-prefix`. The default is
  `fine-tuning=finetune.,quantization=quantize.,sandbox=workspace.,sandbox=egress.,office=workspace.,web=workspace.,telegram=connector.`. An empty value names nobody.
  This is a host setting, not a manifest field and not a model argument.
- An add-on that the setting names finds two optional fields in its startup
  object: `hostEndpoint`, a loopback address of the form
  `http://127.0.0.1:<port>`, and `hostCredential`, a secret of its own that is
  never the credential of the host-to-add-on channel. Both belong to one
  incarnation and end with it. An add-on that is not named gets neither field.
  The protocol version stays 1, and an add-on that ignores the fields works as
  before.
- A call is `POST` with the header `X-Addon-Host-Credential` and a JSON object
  `{"operationId", "op", "arguments"}`. `operationId` is the ID of a request
  the host is serving for that add-on at that moment. The host looks up the
  envelope it issued for that request. A call with no live request, with an
  expired authority or with a scope below the scope of the operation is
  refused. An `apply` operation needs an `apply` request; a `read` operation
  runs under a `read`, `preview` or `apply` request.
- The operation runs as the actor of that envelope, never as a user named in
  the arguments. The host refuses an argument with a name of identity or
  authority (for example `userId`, `scope` or `approved`).
- The host checks the feature of the operation for that actor, as the route
  middleware does for the same request. In local-owner mode the actor is the
  local owner and holds every feature.
- A body larger than the payload limit is refused. An operation that is marked
  idempotent runs once for one operation ID; a repeat gets the first result.
- A process that does not run the add-on manager offers no endpoint, so the
  operation fails closed.

The answer is `{"result": ...}` or `{"error": {"code", "message"}}`. The error
codes are `no_live_operation`, `scope`, `feature`, `unknown_op`, `bad_request`,
`too_large`, `not_found`, `conflict` and `unavailable`.

The channel itself ships with no operation. Each operation is added by its own
change and documented with it.

### Inbound operations

One class of operation has no user behind it: a message that comes from outside,
such as a Telegram message pushed by an add-on. Its scope is `inbound`. The rules
above change in these ways, and only for operations the host registers with that
scope (today `connector.inbound`):

- No live request is needed. The authority is the credential of the add-on's live
  incarnation, which only that process holds and which ends with it. A missing,
  wrong or old credential is refused with `no_live_operation` (401), and so is the
  credential of another add-on, because each add-on has its own listener.
- The call has no actor and no feature check. The operation must find its
  authority in the host's own records. For `connector.inbound` that is the stored
  binding: it must name the calling add-on and be switched on, or the call is
  refused with `scope` (403), also when the binding does not exist. The owner and
  the agent come from the binding. An argument that names a user, an actor or an
  owner is refused with `bad_request` before the operation runs.
- The add-on ID is the one the host built the listener for, never a value of the
  call. The host setting must still name the operation prefix for the add-on
  (`telegram=connector.` by default).

Every other operation keeps all its checks. An operation cannot become inbound by
a field of the request: the scope is part of its registration.

## Connector add-ons

A connector add-on carries the messages of a messaging platform between the platform
and the [chats](/features/chats-connectors/). The Telegram and Slack add-ons are
first-party connector add-ons. The host keeps what is the same for every platform (the
bindings, the sealed bot token, the allowlist, the sender log, one chat for each sender
or thread, the turn and its notifications), so an add-on holds only the transport and
never stores a token. The first-party add-ons share their Go route
(`addons/internal/connector`) and their binding page (`addons/internal/connector-ui`).

The manifest declares the capability. It needs no tool and no `service` flag:

```json
"capabilities": {
  "connector": {
    "kinds": [{
      "id": "telegram",
      "title": "Telegram",
      "description": "Talk to one of your saved agents from a Telegram bot.",
      "senderHint": "A chat ID, a user ID or an @username",
      "delivery": "pull",
      "page": "bots",
      "drafts": true,
      "previewMs": 400
    }]
  },
  "ui": { "...": "the page that adds and edits the bindings" }
}
```

| Field | Meaning and bounds |
|---|---|
| `kinds` | 1 to 4 kinds. A kind is what a binding stores in `kind`, so it names the platform. |
| `kinds[].id` | A lower-case identifier, unique in the manifest. A binding names the add-on too, so another add-on cannot take over the bindings of a kind. |
| `kinds[].title`, `description`, `senderHint` | Plain text for people, with the bounds of the UI texts. |
| `kinds[].delivery` | `pull`: the host asks for updates (long polling). `push`: the add-on calls the host operation `connector.inbound`. |
| `kinds[].page` | Optional. The add-on page that adds and edits bindings; it must be a declared page. The Connectors page links to it. |
| `kinds[].drafts` | Optional. `true` when the add-on serves the op `draft`. The host then shows the live answer of a direct conversation as a draft instead of a working message. |
| `kinds[].previewMs` | Optional. The shortest time between two previews (edits or drafts) of one binding, 250 to 10000 ms. Without it the host waits one second. |

**Outbound: the connector route.** The host calls `POST /connector` on the add-on
with the same bearer credential and incarnation header as every other route and a body
`{"op", "kind", "token", ...}`. The ops are `poll` (`offset`, `timeoutSeconds` up to
25), `send` (`chat`, `thread`, `text`, and optionally `messageId`), `edit` (`chat`,
`messageId`, `text`), `typing` (`chat`, `thread`, and optionally `messageId`),
`send_file` (`chat`, `thread`, `file`: `{"name", "mime", "data"}`), `me` (who the bot
is, for the token check on save and the Test button), `media` (`media`: one
reference of a message's `media`; the answer is `{"file": {"name", "mime", "data"}}`
with at most 1 MiB of data) and, for a kind that declares `drafts`, `draft`
(`chat`, `thread`, `draftId`, `text`).

- A `send` with a `messageId` replaces that message of the bot (the host's working
  message) with the first part of the text and sends the rest as new messages. When
  the message cannot be edited (it was deleted, or is too old), the first part goes
  as a new message too. The shared route (`addons/internal/connector`) does this for
  every platform.
- The `text` of a `send` is the answer, in Markdown. The add-on converts it to the
  markup of its platform and cuts it; the shared route lets a platform send each part
  in its markup and fall back to plain text by itself (Telegram does). The `text` of
  an `edit` or a `draft` is a preview: it goes as plain text.
- A `draft` shows `text` as a draft: a message that the bot is still writing, which
  the platform replaces when the answer is sent. `draftId` is not zero, and the host
  keeps one for the drafts of one answer. When the platform refuses it, the host
  sends a working message instead.
- An `edit` and a `draft` are not retried after a wait: a `rate_limited` answer comes
  back at once, and the host skips that preview. A `send` and a `send_file` wait
  once, as before.
- A `typing` with a `messageId` says that the host already shows a working message
  with that ID. A platform without a typing indicator (Slack) then posts nothing.
- `send_file` carries one file of 1 byte to 1 MiB whose `name` is
  `contract.SafeFileName` of itself (no path, no control characters, at most 128
  bytes). The route refuses any other file, and a platform that cannot send files.
  The answer names the message (Telegram) or the file (Slack) in `messageId`. The token is the
unsealed bot token of one binding. It is passed on each call and the add-on must not
write it anywhere. The answer is `{"updates", "nextOffset", "messageId", "botId", "botUsername", "file"}` or
`{"error": {"code", "retryAfter"}}` with a code of `unauthorized`, `conflict`,
`rate_limited`, `too_large` or `failed`. The host maps `unauthorized` to switching the binding off,
repeated `conflict` to the same, and `rate_limited` to a wait. `too_large` is the
answer of a `media` whose file is larger than 1 MiB or than the platform lets a bot
download; the host tells the sender that the file is too large. A runtime that
implements `contract.ConnectorRuntime` has the route; others get a 404. The add-on
cuts a long `text` to the limit of its platform.

**Inbound.** A pull add-on answers `poll` with normalized messages (`cursor`, `chat`,
`thread`, `direct`, `mention`, `messageId`, `replyTo`, `senderId`, `senderName`,
`senderUsername`, `text`, `unsupported`, `media`, `skip`) and the next offset.
`messageId` is the platform's ID of the message and `replyTo` the message it answers;
the thread conversation mode uses them. Core keeps the offset with the binding, passes it
to the next `poll` and stores the offset the answer names, so a new leader continues
where the old one stopped. A push add-on calls the host operation `connector.inbound`
with `{"bindingId", ...message}`; it is an [inbound operation](#inbound-operations).
Both paths end in one function of the host, so the allowlist, the sender log and the
rate limit are the same.

A message is answered when it is `direct` (a private chat) or a `mention` (it
addresses the bot in a group, such as a Slack mention or a reply in a thread the bot
follows); any other message is logged and ignored. Which chat a message belongs to
depends on the conversation mode of the binding (see
[How the bot answers](/features/chats-connectors/#how-the-bot-answers)); by default a
message with a `thread` belongs to a chat of that thread, shared by the people who
write in it, and any other message belongs to a chat of its sender. When a message has `media` (at most 8
references), the host reads each file with the op `media` before it makes the chat,
and attaches it to the chat, as an upload of the chat page would. The host makes the
name of each file safe again (`contract.SafeFileName`), and the chat saves it in its
own folder. An audio file (by its extension: `.ogg`, `.oga`, `.opus`, `.mp3`, `.m4a`,
`.wav`, `.flac`, ...) is a voice message: the host transcribes it with the
transcription model of the instance and the transcript, under
`[Voice message, transcribed]`, is the text of the turn; the recording is not kept.
With no transcription model, or when the transcription fails, the sender gets one
sentence and no turn starts. `unsupported` marks a message with no text and no file
that the add-on hands over.
The turn runs as the owner of the binding, with the tools of the binding's saved agent.

**Where it runs.** Core calls the route and registers the host operation on the process
that owns the add-ons. The binding loops run there, under the leader lock in a cluster.
A frontend that does not own the add-ons runs no loop.

**Existing bindings.** A binding of kind `telegram` that names no add-on (made before
connectors became add-ons) belongs to the add-on `telegram`. While that add-on does not
run, the binding is listed as not running with the reason "install the Telegram add-on".

## Owner forwarding and HTTP relays

The embedding host can configure `AddonConfig.Transport` at startup. The configuration
contains the owner HTTPS address, instance identities, CA roots, leaf certificate
SHA-256 pins, and Ed25519 signing keys. The owner also needs `ListenAddress` for its
separate mTLS listener. These settings are not runtime settings or browser inputs.
Without transport configuration, the public addon gateway remains unavailable.

All participating frontends must use the authoritative named-user database. The owner
queries current account, credential, and feature state before dispatch. Missing storage,
paused keys, revoked keys, expired sessions, and legacy synthetic identities deny access.
The embedding host's permissive installation callback cannot override these checks when
transport is enabled in the default named-user mode. Local-owner HTTP access exists only
when the deployment sets `OwnershipMode: "local-owner"`. A request never selects it; only the deployment does, and `local-ai run` does so by default when authentication is off.

The carrier requires TLS 1.3, CA verification, and explicit certificate pins. It obtains
a fresh random audience from the pinned owner before signing each request. Each owner
handler generates its own audience at construction. Restart therefore invalidates all
previous envelopes, including envelopes whose expiry has not passed. A bounded nonce
cache prevents replay within that handler. This cache is a documented per-owner exception;
it does not require shared storage because no other process can use that audience.

The signed payload binds the peer, actor, credential reference, operation ID, request
body, audience, nonce, and expiry. The carrier never forwards browser authorization,
cookies, or actor headers. It has no proxy discovery, redirects, affinity, fan-out,
owner election, or failover. An absent owner returns unavailable.

Two more optional capability fields describe the UI-only resource management (see
[UI-only resource management](#ui-only-resource-management)).

| Field | Meaning and bounds |
|---|---|
| `capabilities.undo` | Boolean. The add-on keeps a change set for the writes a chat makes at once and answers the host's undo calls (see [Undoing a change a chat made](#undoing-a-change-a-chat-made)). It changes no tool and offers nothing to a model. |
| `capabilities.adminTools` | At most 4 tool names that only the host's own UI-only endpoints may run. An admin tool is never in `tools`, never has a tool definition, is never offered to a model and is refused by every model, proposal and typed-read path: the declared-tool rule of those paths reads `tools` only. |
| `capabilities.connector` | Optional. 1 to 4 `kinds` of messaging binding the add-on serves, each with an `id`, a `title`, a `delivery` (`pull` or `push`) and optional texts and a `page`. See [Connector add-ons](#connector-add-ons). |
| `capabilities.resources` | Maps the management operations to tools: `create` (a declared tool of kind `create`), `rename` (a declared tool of kind `schema`), `inspect` (a declared tool of kind `read` whose result is an object with a `name` and a `revision`), `delete` (the one admin tool) and a plain-text singular `noun`. Every field is optional. An admin tool must be the `delete` of the mapping, and a mapping must name tools that exist with the kind it needs. A mapping with `create` or `delete` must also declare the read tool `resource-exists` in `tools`, because the host settles interrupted creations and deletions only on its explicit answer. |

A tool definition names the resource it works on with a required `resourceId`
argument, with two exceptions. A `create` tool has no `resourceId`: the host
reserves the resource. A `read` tool may leave `resourceId` out when it answers
about the person who calls it and not about a resource, for example a list of
that person's own items. The host runs such a read without a resource, binds the
envelope to the calling user, and the add-on must scope its answer to that user.
The host operation the add-on calls must scope by the actor of the envelope; the
add-on never takes a user from the arguments.
A `write` or `schema` tool, and a `read` tool that offers `resourceId`, must
require it.

A tool definition with `"uiOnly": true` is for the add-on's own page. It must be a
`read` tool without `resourceId`. The host runs it for the page like any such read,
but never offers it to a model, a chat, an agent or a trigger, and a model that
names it gets the unknown-tool answer. The Sandbox uses it for `set-backend` and
`confirm-host`: they change a setting, and the add-on asks the host whether the
caller is an administrator before it acts.

### Tools that replace a built-in tool

A chat has a few built-in tools that an add-on may replace: `bash`,
`web_search` and `web_fetch` (`contract.ReplaceableTools`). The file tools are
not on the list: they are the one file toolset of every chat. A tool
definition replaces one with `"shadows": "<name>"`:

```json
{ "name": "bash", "kind": "action", "shadows": "bash", "description": "...", "inputSchema": { "...": "..." } }
```

- **Checked at install.** The name must be on the list, a page-only tool
  cannot shadow, and one manifest shadows a name once. The tool must also look
  like the built-in it replaces, so the model calls it the same way and the
  user keeps the consent split of the built-in name:

  | Name | Kind | Must declare (and may require only) |
  |------|------|-------------------------------------|
  | `bash` | `action` | `command` (string) |
  | `web_search` | `read` | `query` (string) |
  | `web_fetch` | `read` | `url` (string) |

  Extra optional properties are allowed, and a shadowing tool names no
  `resourceId`. The tool adapter checks all of this again when it lists the
  tools: a tool that fails keeps its add-on name and replaces nothing.
- **In a chat.** When the add-on is installed, running and on for the chat, the
  model sees one tool under the built-in's name, served by the add-on, and the
  built-in one is hidden. The call still goes to the add-on under its own name
  (`addon__<id>__<tool>`), with the same checks and audit. An add-on that is
  off for the chat, stopped, not granted by the chat's agent or unavailable
  offers nothing, so the built-in tool serves again. The chat's **Tools** menu
  says so in one line ("Sandbox replaces bash but cannot serve this chat now
  ...: the built-in bash serves", or that the chat has no such tool when the
  built-in is off too), from `fallbacks` of `GET /api/chats/{id}/tools`. A
  tool without `shadows` keeps its add-on name.
- **Who served it.** A call the add-on served carries the card `tool_provider`
  with the add-on's ID, so the chat shows the provider on the tool line.
- **Several add-ons.** Only one add-on serves a name. An administrator picks it
  in Settings, **Tools** (runtime setting `chats_tool_providers`, a map from the
  tool to the add-on ID; the same map holds the choices for the built-in skills
  an add-on replaces, see [Skills](#skills)); with no choice, the add-on
  installed first serves (ties go by add-on ID, and an add-on installed before
  the host kept the time sorts last). A chosen add-on that is installed but
  cannot serve the chat (stopped, off for the chat, not granted by its agent)
  gives the built-in tool, not another add-on; a choice of an add-on that is
  not installed is ignored. With no choice, the oldest add-on that is running
  serves. Settings > Tools lists every installed add-on that replaces a name,
  running or not, oldest install first, with the install time of each, marks
  a stopped one **(stopped)** and the default as **Default: installed first**,
  and says "The add-on that replaces this tool is not running: the built-in
  tool serves" when none serves (`stopped` and `note` in
  `GET /api/chats/admin/tools`). The
  library entry and the installed view list the names an add-on replaces in
  `shadows`, and the install dialog warns when an installed add-on already
  replaces one: "This add-on replaces the built-in bash. Another add-on
  (Sandbox) already does. Only one can be active; you choose which in
  Settings > Tools. Until you choose, the one installed first serves." The
  install is still allowed.
- **Install time.** The owner records when an add-on was first installed
  (`installedAt`, RFC 3339 in UTC) in its lifecycle journal, next to the other
  install facts. An upgrade keeps it; removing the add-on and installing it
  again sets a new one. `GET /api/addons/apps` and the installed view of an
  administrator carry it, and **More details** shows it. An add-on installed
  before the host kept the time has none. **Distributed state: shared.** The
  time lives in the owner's journal, the one durable record of the install, and
  the frontends read it from the owner, so every frontend picks the same
  default. A choice in `chats_tool_providers` always wins over it.
- **Other consumers.** The OpenAI-compatible API, MCP and agents keep the
  add-on name; only chats act on `shadows`.

### Public API

- `POST /api/addons/dispatch` accepts a contract request. Supported lifecycle actions
  are `install`, `enable`, `disable`, and `uninstall`. `operation-status` is read-only.
- `POST /api/addons/relay` accepts `{request, kind, handle}`. Kinds are `status`,
  `progress`, `json`, `file`, and `asset`. Handles are not filesystem paths. For `file`
  the handle is the identifier of a private file result (see
  [Private file results](#private-file-results)), never an addon output handle.
- Both routes require named authentication and the `addons` feature. Lifecycle
  dispatch additionally requires current administration rights. Cookie requests require
  same-origin fetch metadata; header-authenticated clients use their validated keys.
- `POST /api/addons/proposals`, `GET /api/addons/proposals/{id}`,
  `POST /api/addons/proposals/{id}/confirm`, `.../cancel`, `.../reconcile` and
  `.../resolve` store, read, apply, withdraw and resolve data or schema proposals. See
  [Proposals and confirmation](#proposals-and-confirmation).
- `GET /api/addons/resources/{resourceId}/proposals?state=unresolved` lists, for the owner
  role of the resource only, the proposals on it whose outcome is unresolved, whoever
  proposed them. See [Unknown outcomes and recovery](#unknown-outcomes-and-recovery).
- `GET /api/addons/artifacts/{id}` and `GET /api/addons/artifacts/{id}/download` read
  the state of, and download, a private file result of the caller. See
  [Private file results](#private-file-results).
- `GET /api/addons/apps`, `GET /api/addons/apps/{id}/icon?generation=N`,
  `POST /api/addons/apps/{id}/start` and `.../stop` list and control the addons as
  apps. See [Apps](#apps).
- `GET`/`POST /api/addons/resources`, `.../resources/{resourceId}/grants`,
  `.../grants/{userId}`, `.../users`, `.../rename`, `.../delete`, `.../changes/{changeId}/undo`, `.../operations/{operationId}`
  and `/api/addons/conversations/{conversationId}/selection/{addonId}` list, create,
  share, rename and delete a person's resources and keep the resource a conversation
  works on. They are UI-only. See [UI-only resource management](#ui-only-resource-management).

Every failure response has the body `{"error":{"type","message"}}`. A denial or
unavailability has `type` `addon_error` and a `code`. An `outcome_unknown` response is
status `503` and also carries the original `operationId`. Keep that `operationId` after
an `outcome_unknown` response. Query
`operation-status`; do not create another mutation. The carrier never retries mutations.
`Manager.OperationStatus` binds the lookup to the original actor and current administrator.
It does not submit, repair, start processes, or write the journal. `completed` describes
durable lifecycle publication, not runtime health. `runtimeState: not-observed` makes
that distinction explicit. Ambiguous legacy actor bindings deny lookup.

Progress is coarse lifecycle-state SSE, not byte counts or percentage completion.
The stream sends an initial snapshot, changed snapshots, and a heartbeat snapshot.
It polls no faster than 250 milliseconds and rechecks authority on each poll.
It closes on a completed or failed operation. A lifecycle writer can delay observation;
disconnecting still cancels the waiting lookup. Install-byte progress requires later integration.

`RelayLimits` has finite startup defaults: 60 seconds, 16 concurrent streams, 64 MiB
for files, 8 MiB for assets, and 1 MiB for progress. Heartbeats default to five seconds.
Overrides cannot remove these bounds. Streams cancel on disconnect and reject excess bytes.
The receiving frontend applies the budget of the relay kind: `file` uses the file
limit, `asset` the asset limit, `progress` and `status` the progress limit, and `json`
at most 2 MiB. An unknown kind is denied on the frontend before the owner is contacted.
Only content type, attachment disposition, cache policy, and nosniff headers cross the carrier.
Browsers never receive an addon process address.

A denial that the owner makes before it writes the first response byte (an invalid kind,
missing admission or resolver, a refused resource, or a revoked credential) is returned
as a typed owner status: `403` for a denial and `503` for unavailability. The receiving
frontend decodes it, so every frontend reports the same error for the same decision.
Only an authorization, policy or request rejection is `403`. A runtime that is not
installed, enabled or serving, an owner that is busy or in recovery, an output that ends
before its first byte, and any unclassified owner error are `503`, never a denial. A
`503` does not prove that nothing ran: the owner can fail after it recorded or applied a
mutation. A frontend therefore reports a mutation that gets a `503`, or a transport
failure, as `outcome_unknown` with the original `operationId`, and never as plain
unavailability. The owner's own frontend reports it the same way as every other
frontend. When an error carries both a denial and an unavailable cause, `503` wins.
After the first byte the status cannot change; the owner then aborts the stream and the
frontend reports the stream as failed, never as a clean end.

### Resource integration remains closed by default

`Admission.Admit` encloses the complete private-output stream lifetime. A host-owned
resolver maps resource references and opaque output handles to trusted records. A missing
admission provider or resolver denies access, including access by installation administrators.
The owner installs the policy described below as both providers at bootstrap; a frontend
that forwards to the owner, or a host without the policy, keeps denying. File results are
held by the private artifact store described in [Private file results](#private-file-results);
without it a read that returns a file output is denied whole and a `file` relay is denied.

### Principals, roles and grants

`core/services/addons/policy` is the owner-local authority for resource access. Its store
is a private directory (`policy/` below the addon root, file mode 0600, one holder at a
time) that is separate from replaceable packages, so it can be backed up and restored on
its own. A resource is an opaque host-issued ID bound to the trusted addon identity and
an immutable owner user ID; the addon, the model and a request never supply either.

| Role | May |
|---|---|
| Viewer | read, and download private outputs |
| Editor | everything a Viewer may, and mutate rows |
| Owner | everything an Editor may, and change schema, share and delete |

Grants cover whole resources, never tables or rows. Installation administration confers
no access to user data: the policy has no administrator role. Only the owner grants or
revokes, the owner role cannot be granted, and an action that is not classified is a
mutation that needs the owner. A missing resource and a resource the caller may not touch
return the same denial.

Named-user mode uses the immutable user ID and rejects anonymous and legacy synthetic
principals (such as the legacy shared API key). Explicit local-owner mode disables
sharing and warns that every unauthenticated caller shares one authority: any local-owner
caller owns every resource. A populated store opened in the other mode is refused.
`policy.MigrateMode` changes it only while the host is stopped (the store lock must be
free), after it wrote a backup, and shared data can never return to local-owner mode.

Account and credential status are checked again at each admission. A storage outage
of the account database is `503`, while a missing, disabled or unauthorized account is
`403`, from every frontend. Only a definite denial (an error that wraps the shared
forbidden sentinel) is `403`. Any other error from the account or creation check, and a
cancelled check, is unclassified and fails closed as `503`; it is never reported as a
refusal of a valid user. The policy keeps no
current-user state: the actor is an argument of every call, so shared MCP sessions carry
none. Writes to one resource run one at a time; reads run concurrently. A revocation (or
lowering a role) is durable first, then invalidates the user's pending proposals through
the `ProposalInvalidator` seam, cancels the user's admitted work and returns only when
that work ended. If it does not end within the drain bound the call reports `503`
(`ErrDrainPending`): the revocation is already effective for new work but is not
acknowledged. The owed drain is stored with the change itself, together with the
resource's authority epoch at the lowering. Every admission carries the epoch it was
admitted under. Repeating the same `SetGrant` or `Revoke` after a failed attempt (or after
a restart) invalidates proposals again and waits only for the user's work admitted before
that lowering; it is acknowledged only after both finished, and the mark is then cleared.
Work admitted after the grant became durable again is neither cancelled nor waited on by
the retry. A later lowering starts a new epoch and covers it. Disabling or uninstalling an addon keeps its resources and grants; purge is
a separate, explicit operation that is not available to tools.

A new resource needs explicit creation authority. At bootstrap that authority requires a
current account check and an addon that is installed, enabled and not changing on the
owner; an absent addon is denied and a manager that cannot answer is `503`. `Register`
records the resource pending and inaccessible before the addon creates anything; `Commit`
activates it, and `Reconcile` resolves registrations left by a crash. The owner runs
`Reconcile` at startup before it serves. It asks the add-on whether the backing exists:
`yes` activates the record, `no` drops it. Reconcile is fail-soft per record. When the
answer cannot be established (the add-on is disabled, failed to start or does not
answer), that record stays pending and hidden, is never dropped or committed, keeps its
pending quota, and the pass goes on with the other records. Startup never aborts for this,
and the owner logs a warning with the settled and kept counts. A background loop re-runs
Reconcile every 30 seconds while any record is pending, so a record is settled once its
add-on is enabled and ready. The loop stops when the owner closes and waits for a running
pass. Pending reservations of a disabled add-on therefore stay pending until the add-on is
enabled again. A database with no record is never reachable.

Registrations are bounded. Per owner user at most 8 pending and 256 total resources, and
at most 4096 in the store (`Options.MaxPendingPerUser`, `MaxResourcesPerUser`,
`MaxResources`; zero selects these finite defaults). A request over a limit is denied
with `403` (`ErrQuota`); abandoning or reconciling away a registration frees its slot.

The store file is validated on open: an unknown role, a grant to the owner, a second JSON
document after the first, or an unknown field refuses the store, which is never reset.
Leftover `.policy-*` temporary files from a crash are removed on open. If a change was
renamed into place but the directory synchronization failed, the in-memory state follows
the file (a revocation is not undone) and the store accepts no further writes, reporting
`503`, until the host restarts.

### Explicit local-owner mode

Set `--addons-ownership-mode=local-owner` (or `AddonConfig.OwnershipMode` in an embedding
host) to select this mode. `local-ai run` also selects it by default when authentication
is off and you set no mode. Nothing else selects it: a missing credential, an absent
account database or an unknown value never does, and an unknown value fails startup.
If you turn authentication on later, set the mode to `named-user` and move the data
with `policy.MigrateMode` while stopped; the store refuses the other mode. The owner logs a warning at startup that unauthenticated
callers share one authority and that any caller that can reach the port can confirm
addon proposals.

- Every caller that passes the addon route chain acts as one synthetic principal,
  `local-owner`, with a fixed credential `local-owner`. Named-user mode rejects that ID,
  so it cannot collide with a real user.
- That principal owns every resource and sharing is disabled: `SetGrant` and `Revoke`
  are denied.
- With the authentication feature disabled (no account database) there is no caller
  identity at all: any client that can reach the HTTP port shares the authority, including
  lifecycle administration. Use this only on a trusted loopback or private network.
- With an account database, the route chain still authenticates callers and applies the
  addons feature gate, and lifecycle changes still need an administrator; only data
  ownership collapses to the single local owner.
- The store records the mode. Opening a populated store in the other mode is refused;
  use `policy.MigrateMode` while stopped. Shared data cannot return to this mode.

#### Moving between the modes

The mode is a property of the stored data, so it is changed with the host stopped and
never by changing the configuration alone:

1. Stop the owner. The migration needs the store lock, which a running owner holds.
2. Make a backup of the policy store. The migration writes one itself and refuses to
   continue when it cannot.
3. Name the mapping of identities. From `local-owner` to named users, every resource
   needs the account that takes it (`{"local-owner": "<account id>"}`; there is no
   wildcard and no default, and the target must be a current account). From named
   users to `local-owner`, a store that holds named-user data is refused, because
   that data would lose its separation.
4. Start the owner in the new mode. A store opened in the wrong mode is refused at
   startup, so a configuration mistake never silently changes who owns data.

A backup made in one mode restores into the other mode only with the same explicit
mapping (see [Data recovery](#data-recovery)).

Resource reads obtain the current ready runtime incarnation and check the exact generation.
Private output copying rechecks credentials and stays inside admission until completion
or cancellation. Immutable assets use installed package receipts, declared entry/CSS paths,
and a contained filesystem root; an account that holds the addons feature may read the
declared files of an enabled add-on it may see (see [The asset route](#the-asset-route)).

Mutation MCP tools remain deliberately unavailable. The later MCP integration defines
safe listing/status tools for proposals; prompt text cannot authorize mutation and no tool
confirms a proposal. For that reason `/api/addons/dispatch`, `/api/addons/relay` and the
proposal routes are deliberately not mapped in the LocalAI Assistant tool catalog
(`pkg/mcp/localaitools`) yet. The route map test only
requires an entry for each registered tool, so no test is weakened. The routes are described
in the generated Swagger document under the `Addons` tag.

Run `sh scripts/addons/verify.sh policy` for the policy suite.

## Proposals and confirmation

Every data or schema mutation that comes from an API client, an MCP session or the
OpenAI-compatible chat route is a stored proposal first. A proposal is applied only
by an explicit confirmation of that stored operation from the person's own browser
session. The chats on the server, their agents and their triggers are the exception
described under [Lifecycle](#lifecycle): there the add-on switch is the consent.
Sharing and database deletion are never proposals and never tools.

### Declared tools

The owner checks every request that names a tool against the manifest of the installed
current generation before anything reaches the add-on. The tool name is the top-level
`tool` key of the request `arguments`. The rule is the same for a `read`, a proposal
(`write` or `schema`), the confirmation of a stored proposal and the opening of an
output, on the owner's own frontend and on every forwarding frontend:

- No `tool` key: the request names no tool, and the rule has nothing to check.
- An exact `tool` key: the value must be a string that equals one entry of
  `capabilities.tools` byte for byte. A name of another add-on, a case, Unicode or
  white-space variant, an empty value and a value that is not a string are denied.
- A key that equals `tool` only after case folding or trimming white space (`Tool`,
  `TOOL`, ` tool`) is denied, because an add-on decoder may read it as the tool.
- A repeated key is denied at any depth of `arguments`. Keys are compared after JSON
  unescaping, white-space trimming and Unicode case folding, so `"tool"` and
  `"\u0074ool"` collide, and so do `"tool"` and `"TOOL"`. A runtime that keeps the
  first value would otherwise run another tool than the one that was checked.
- `arguments` must be exactly one JSON object. An array, a scalar, `null`, trailing
  data after the object, invalid JSON and nesting deeper than 64 levels are denied.
  This rule runs in the declared-tool check itself, so it holds for reads,
  proposals, confirmations and output opening whatever validation ran earlier.
- A denial is the standard 403, and the add-on is not contacted. A proposal is denied
  before the preview and nothing is stored. A stored proposal is checked again at
  confirmation, so a tool that an update withdrew is never applied.
- Only a real answer is a denial: the tool is not declared, or the generation is stale
  or not installed. When the declarations cannot be read, the request is unavailable
  (503) and nothing runs: the manager is closed or not the owner, the journal is
  uncertain or runtime recovery is required, the request context is done, or the
  source fails for any other reason. An unclassified failure is unavailable, never
  success and never a policy denial. The owner and every forwarding frontend give the
  same status. The owner reads declarations from its in-memory journal, cached for
  each generation.

### Lifecycle

Tools reach a model through the same proposals: a chat that sets
`metadata.addon_tools` can call the tools of enabled addons, and a change a
tool proposes waits as a pending proposal. The chats on the server (`/api/chats`),
their agents and their triggers are the one exception: turning an add-on on for
a chat or an agent is the consent, so a change tool is applied at once as the
chat's user. The chats adapter stores the proposal and confirms it in the same
call with the credential of a chat turn, so the same admission, revision
and receipt rules hold, a failed call leaves no pending proposal, and no API
key, MCP session or browser request can use that credential. The result says
what was applied and whether it can be undone: Tables, Fine-tuning and
Quantization keep a change set for the changes they can roll back, and the result
carries `undoable`, the `changeId` and a one-line `undo`. See
[Undoing a change a chat made](#undoing-a-change-a-chat-made). `GET /api/addons/proposals?state=pending`
lists the caller's own pending proposals (newest first, at most 32). See
[Tables](../tables/#model-tools-and-approval-cards) for the tool names, the
approval card and the inventory of tool consumers.

`POST /api/addons/proposals` takes a typed `write` or `schema` request. When the
arguments name a tool, the owner derives the action from the kind the manifest gives
that tool (`write` tools are data writes, `schema` tools are schema changes) and
refuses a request whose `action` disagrees, a `read` or `create` tool, and a tool the
manifest does not define. The owner
resolves the resource, requires write authority (`schema` needs the owner role), asks the
addon for a preview and stores a proposal with:

- the canonical arguments, the addon generation, the resource and the proposing user;
- the schema and data revisions the preview saw;
- the exact number of effects and a bounded sample of them (the addon's preview summary,
  using `count` and `sample` when it has them);
- a `digest` of everything above (it also covers the operation ID, the proposing user and
  the action; the owner recomputes it from the stored record before it applies, so a record
  that was changed at rest is refused and never runs), and an expiry (the shorter of the addon's preview expiry
  and the proposal expiry limit, five minutes by default).

Nothing is applied by this call. The request's `operationId` becomes the proposal ID, so
every later step, including a lost reply, is resolved by the same ID. Repeating the same
request returns the same proposal. Reusing an ID with other arguments, another actor or
another resource is a denial.

A proposal is in exactly one state:

| State | Meaning |
|---|---|
| `pending` | Waiting for a human decision. |
| `cancelled` | Withdrawn by its actor, or invalidated when the user's authority on the resource was lowered or revoked. |
| `expired` | Not confirmed in time. The owner records it when the clock runs out, on the next proposal, on start and on recovery, whether or not anyone read it. |
| `executing` | Confirmed and handed to the addon. Durable before the addon sees the call. |
| `applied` | Committed. The receipt is stored with the proposal. |
| `rejected` | Refused by the addon (for example, the revision moved) or found stale (the addon generation changed). |
| `outcome-unknown` | The apply may or may not have run. A receipt lookup, or the resource owner's explicit acknowledgement, resolves it. |
| `abandoned-unknown` | The resource owner stopped waiting for an unresolved outcome. It no longer blocks writes and is never applied again; a later receipt lookup is still recorded. |

Bounds. A user holds at most 32 live proposals (pending ones that have not expired, and
unresolved ones) and at most 16 MiB of stored canonical arguments across them; the store
holds at most 1024 proposals and 64 MiB of arguments. The user's own bound is checked
first, so one user can exhaust only their own share. A limit reached is a typed quota
denial (HTTP 403) that names which bound it was. To make room for a new proposal the owner
drops settled proposals only (cancelled, expired, applied, rejected), the proposer's own
first and then the oldest; a live proposal of another user is never dropped. Settled
proposals are also dropped after a retention window of 24 hours by default
(`BrokerOptions.Retention`), so a repeated request inside the window still gets its stored
answer. Retention cannot be switched off.

Every transition is written atomically and fsynced by the owner before it is reported. The
store lives beside the policy store and follows the same rules: one process holds it, a
corrupt file is refused and never reset, and a restart recovers it.

### Creating a resource

A request with `action: "create"` and no `resourceId` proposes the creation of a new
resource, such as a Tables database. Nothing exists yet, so the owner works in this order:

1. It checks the creation authority of the caller and that the named tool is declared.
2. It reserves a pending resource for the proposer. A reservation is invisible to everyone,
   the proposer included, counts against the pending limit of the user and is bound to the
   proposer. The proposal records its ID as `resourceId`.
3. It asks the addon for a preview with the reserved ID. The preview only validates the
   request and creates nothing. What the human sees (`count` 1 and the requested `name`) is
   built by the owner and not taken from the addon.

Confirming runs the creation under the creation authority of the proposer, which is checked
again, and not under a role on a resource. The addon operation ID is `create-` and the
reserved ID, so a repeat is one operation. A committed receipt makes the proposer the
private owner of the resource. Cancelling, expiry and a rejected receipt drop the
reservation; the resource was never visible.

An uncertain apply keeps the reservation pending and the proposal `outcome-unknown`. A
receipt lookup settles both. The proposer can also stop waiting (`resolve`): the
reservation is dropped only when the addon's `resource-exists` tool confirms that the
database does not exist, and it stays pending when the addon says it exists or cannot
answer. At start the owner settles reservations of unknown or executing proposals by asking
the addon, and keeps those of proposals that are still pending. A reconciliation never drops
the reservation of a creation that is running in this process.

### Confirm

`POST /api/addons/proposals/{id}/confirm` with `{"digest": "..."}` submits exactly the
stored parameters under resource admission, once. The runtime validates the revision in
the same transaction as the write, so a data change after the preview is a `rejected`
proposal and not a silent overwrite. Confirming again returns the stored receipt and
applies nothing.

The route is reachable only from a host confirmation surface:

1. The credential must be a browser session cookie of the user who proposed. A named API
   key, a bearer token, a legacy key or an MCP session can propose and read, and can never
   confirm, whatever its role.
2. The request must be a same-origin fetch (`Sec-Fetch-Site: same-origin`,
   `Sec-Fetch-Dest: empty`, `Sec-Fetch-Mode` `cors` or `same-origin`).
3. The `X-LocalAI-Confirm` header must equal the proposal ID, and the body must carry the
   digest of what was displayed.
4. A request that carries a tool, agent or MCP execution marker (`Mcp-Session-Id`,
   `X-LocalAI-Tool-Call`, `X-LocalAI-Agent`, `X-LocalAI-Execution-Context`) is refused.

In local-owner mode there is no session; rules 2 to 4 apply, and any API credential on the
request is refused. The owner repeats the credential check, so a frontend cannot confirm
for a key credential either.

Limit of the confirmation boundary in local-owner mode. Without accounts there is no
identity to bind a confirmation to. The only boundary is the set of browser headers
above, and a script that sets them can confirm: any caller that can reach the HTTP port
shares the authority to propose and to confirm, and that includes the proposals
it made itself. This is inherent to local-owner mode, where unauthenticated callers
share one authority, and it is accepted and not changed. The owner logs a warning at
startup that says so. Use local-owner mode only on a trusted loopback or private
network, and use named-user mode wherever a confirmation must come from a particular
person. Plain text, a boolean in arguments, or a model saying "yes"
are only data and never approve. Clients without a confirmation UI can propose and read;
they never auto-apply. No MCP tool maps to confirm: the MCP exposure of proposals (list
and status only) is a later task and must keep that rule.

### Unknown outcomes and recovery

Every proposal call is idempotent by the proposal ID: a proposal is a single-use state
machine, and repeating a call with the same ID returns the stored state and never starts
a second operation. The owner therefore answers an outage by what it knows about the
proposal, and every frontend reports the same answer for the same owner state:

| Response | Meaning | What to do |
|---|---|---|
| `503` `proposal_unavailable` with `operationId` | The owner could not serve this call, and this call sent nothing to the addon. The proposal is in the state the owner recorded, which can be any state: pending, or already settled by an earlier call. | Read the proposal with `GET`, then repeat the same call with the same ID. |
| `503` `outcome_unknown` with `operationId` | The request to the addon may have reached it (`executing` or `outcome-unknown`). A request that never left the owner (no free slot, a withdrawn add-on, an invalid envelope, a connection that was never written to) returns the proposal to `pending` and is `proposal_unavailable`. An add-on's 409 conflict ends the proposal as `rejected`. | Do not confirm again as a new operation. Reconcile, wait for the automatic lookups, or resolve. |

Which type a route can emit does not depend on who proposed the operation. A proposal,
a confirmation and a cancellation by the proposer can be either type. A resolution is
always `proposal_unavailable`, because resolving never applies anything and the resolver
is the resource owner, who is not always the proposer. A confirmation that the addon
committed answers `200` with the applied proposal; the owner does not read the record
back through a second account check that could fail after the apply.

A reply lost between a forwarding frontend and the owner for a confirmation is reported
as `outcome_unknown`, because the frontend cannot know whether the owner started it.
The lifecycle dispatch rule is unchanged: any uncertain lifecycle mutation is an unknown
outcome with its original operation ID.

If the reply to a confirmation is lost, or the owner restarts while a proposal is
`executing`, the proposal is `outcome-unknown` and the response is `outcome_unknown` with
the original `operationId`. The mutation is never submitted again under any ID.

`POST /api/addons/proposals/{id}/reconcile` looks the operation's receipt up on the addon
and records a committed or rejected result. It is read-only toward the addon and can be
repeated. An addon that has no receipt for the operation stays `outcome-unknown`: a
missing receipt does not prove the call never ran. While an outcome is unresolved, every
other write or schema proposal on the same resource is denied; a pending proposal
shows `blocked: true`; reads continue. A write denied for this reason is `403`. For
the owner role of the resource only, the body is type `outcome_blocked` and carries
`blockedBy`, the operation ID to resolve. Any other user gets the plain denial and never
learns another user's operation ID, also not in a proposal view. On start the owner settles proposals left
`executing` by a crash the same way and never applies them.

Recovery has three steps and none of them submits the operation again:

1. **Automatic lookups.** The owner repeats the receipt lookup for each unresolved
   proposal with exponential backoff (from the `BackoffInitial` limit up to
   `BackoffMax`). They run for a finite window: the apply timeout plus five minutes,
   counted from the moment the apply was handed to the addon. A final receipt settles the
   proposal and lifts the block. After the window, only an explicit lookup remains.
2. **Explicit lookup.** `reconcile`, by the proposer, at any time.
3. **Owner resolution.** The owner role first finds what to resolve:
   `GET /api/addons/resources/{resourceId}/proposals?state=unresolved` lists every
   unresolved or abandoned proposal on the resource, whoever proposed it, with its ID,
   state, proposing user, action, exact effect count and bounded effect sample, expiry,
   digest and age in seconds, oldest first. It never returns the proposed arguments or a
   credential. Only the owner role of that resource may call it, from the same
   confirmation surface as resolve with `X-LocalAI-Confirm` equal to the resource ID;
   editors, installation administrators and API keys are denied, and no MCP tool maps
   to it. Then `POST /api/addons/proposals/{id}/resolve` with
   `{"digest": "...", "reason": "..."}` (the digest from the list, required and checked
   by the owner itself, not only by the HTTP route) lets the owner role of the resource
   stop waiting. It is accepted only from the same confirmation surface as confirm (the
   owner's own browser session, a same-origin fetch, `X-LocalAI-Confirm` equal to the
   proposal ID, no tool or agent marker). The proposer's role is not enough, an
   installation administrator without an owner role on the resource has no access, and
   no API key or MCP tool can call it. A final receipt found at that moment settles the
   proposal instead. Otherwise the proposal becomes `abandoned-unknown` and records who
   acknowledged it (`resolution.actorId`), when and the reason (up to 512 characters).
   Writes on the resource are accepted again.

An abandoned operation keeps its original operation ID. The owner keeps looking its
receipt up in the background for a day and on every `reconcile`; if the addon reports
that it ran, the proposal becomes `applied` with that receipt (or `rejected`), and the
`resolution` stays on the record, so a late success is recorded and never hidden. An
abandoned record is kept for 30 days and is not dropped to make room.

Data consistency. Resolving is the owner asserting that the outcome is not worth waiting
for; it does not prove the change did not run. If it did run, it stays in the data. Later
proposals preview the current revision, and the runtime refuses an apply whose revision
moved, so a change prepared against the state before the unknown one is rejected and not
applied on a stale view. Check the data before you resolve when that matters.

### Revocation races

Lowering or removing a user's grant cancels that user's pending proposals on the resource
before the change is acknowledged, and stops their admitted confirmations. A confirmation
that already reached the addon becomes `outcome-unknown` and is resolved by receipt.
Other frontends reach the proposal state only through the owner's gateway, signed and
actor-bound like every other forwarded call.

Run `sh scripts/addons/verify.sh proposals` for the proposal suites.

## UI-only resource management

A person manages their own resources, for example Tables databases, from the host's own
pages. The routes below are the whole surface. They are **never tools**: no model tool, no
MCP tool, no proposal and no typed dispatch reaches any of them, and sharing and deletion
have no tool definition anywhere. The routes live in the `addons` feature group with the
usual browser protections (CSRF, same-origin cookie requests), and they are registered in
the route-feature registry.

| Route | Who | What |
|---|---|---|
| `GET /api/addons/resources?addonId=` | any caller with the feature (keys too) | The resources of the add-on the caller owns or that were shared with the caller, with the caller's own role, the display name, the owner's display name for a shared one and, for the owner role only, how many people it is shared with and the revision. At most 100. The names are read at most half of the add-on's concurrency budget at a time, so a page that reads while it lists is not refused. |
| `POST /api/addons/resources` | the person's own session | Create a resource through the add-on's create tool. Caller becomes the owner; the resource is private. |
| `GET /api/addons/resources/{resourceId}/grants` | owner role, own session | Who it is shared with: immutable account ID, display name, role. |
| `PUT /api/addons/resources/{resourceId}/grants/{userId}` | owner role, own session | Give a named user `viewer` or `editor` on the whole resource. |
| `DELETE /api/addons/resources/{resourceId}/grants/{userId}` | owner role, own session | Remove the grant. |
| `GET /api/addons/resources/{resourceId}/users?q=&limit=` | owner role, own session | The user picker. |
| `POST /api/addons/resources/{resourceId}/rename` | owner role, own session | Rename through the add-on. |
| `POST /api/addons/resources/{resourceId}/delete` | owner role, own session | Delete the resource and its data. |
| `POST /api/addons/resources/{resourceId}/apply` | role of the tool, own session | One data or schema change through a declared write or schema tool, with no card. |
| `POST /api/addons/resources/{resourceId}/changes/{changeId}/undo` | the role of the change, own session | Undo one change a chat made at once, by the change set the add-on kept. |
| `GET /api/addons/resources/{resourceId}/operations/{operationId}?kind=` | owner role (rename, delete) or a role that may write (apply, undo), own session | Read-only receipt lookup of a rename, a deletion, a change or an undo. |
| `GET`/`PUT`/`DELETE /api/addons/conversations/{conversationId}/selection/{addonId}` | any caller with the feature | The resource a conversation works on. |

"Own session" means the same rules as a confirmation: a browser session cookie, a
same-origin fetch, `X-LocalAI-Confirm` equal to the resource ID (the operation ID for a
creation) and no tool, agent or MCP marker. An API key, a bearer token and a legacy key are
refused, whatever their role. In local-owner mode the rules are the same without a session.
The grants, the picker and the receipt lookup are owner-role reads of other people's data and
use the same surface.

Every call goes to the owner through the carrier, so a frontend that holds no addon state
gives the owner's own answer. The state is the owner's alone and is single-runner by design:
the policy store with its `deleting` records, the proposals and the selection store
(`selections/` below the addon root) are never read or written by a frontend, and a
frontend that cannot reach the owner reports a mutation as `outcome_unknown` with the
original operation ID. The owner checks the caller's role itself, with the same denial
for an unknown resource and for one that is not the caller's. Installation administration is
not data access: an administrator lists and changes what they own and nothing else.

### Creating, renaming and deleting

These are the person's own explicit actions, so they need no approval card, but they follow
every other rule of a mutation.

- **Create.** The owner reserves a pending resource, runs the add-on's create tool through
  the same preview, apply and receipt steps as a chat creation, and activates it. The
  resource ID is derived from the person and the `operationId`, so repeating the call
  returns the same resource and a repeat with other arguments is refused. The arguments are
  validated against the tool definition of the manifest exactly as a model's call is, and a
  crash is settled at the next start like any pending reservation. The per-person limits
  apply (`quota_reached`).
- **Rename.** The call runs under the resource's admission, behind an unresolved outcome
  (`outcome_blocked` for the owner role), against the generation and the revision the owner
  listed (`stale`, nothing changed). The add-on's receipt for the operation is the record:
  repeating an `operationId` replays it, and two people that chose the same ID never share one.
- **Delete.** The order is durable at every step:
  1. the resource becomes `deleting`: inaccessible to everyone, no new admission and no grant
     change, and admitted work is cancelled and waited for (bounded);
  2. with no work left, the owner checks the revision it listed and the confirmation: the
     request must carry `confirm`, equal to the display name exactly. A failure returns the
     resource to use;
  3. the deletion is marked confirmed, pending proposals of every user on the resource are
     cancelled, and the add-on is asked to delete under one operation identifier;
  4. only after the add-on confirmed, the record and every grant are removed.

  A crash leaves a `deleting` record. At the next start an unconfirmed one is returned to
  use, and a confirmed one is finished; a reconcile loop retries one that cannot be finished
  while the add-on is down. The resource stays inaccessible meanwhile. Deleting never
  uninstalls the add-on and never touches other resources. An outcome that cannot be
  established is `outcome_unknown` with the original `operationId`: repeat the same call, or
  look the receipt up; nothing is submitted under a new operation.

  Before it calls the delete tool, the owner asks the add-on's `resource-exists` tool whether
  the backing is still there. Only that explicit answer of the declared tool can say "gone",
  and then the record is removed without the delete tool. An add-on that is not installed,
  does not declare the tool or cannot answer never counts as "gone": the deletion stays
  `deleting`, is reported as `outcome_unknown` and is retried by the reconcile loop, so the
  data is never left behind a removed record. This applies to the request, to a repeat of it
  and to the recovery at start.

The delete tool is declared in `capabilities.adminTools`. The owner is its only caller, from
the delete endpoint and its own crash recovery, under the host action `delete`, which no
model tool, proposal or typed request can carry. The add-on refuses the tool under any other
action.

### Changing data and schema from the UI

`POST /api/addons/resources/{resourceId}/apply` is how a person's own page changes rows or
the schema, for example the Tables page. The body is `{operationId, generation, revision,
tool, arguments}`. It is not a proposal: nothing is stored for later and no card is shown,
because the request in the person's own tab is the intent. It keeps every rule of a
mutation:

- **Tool.** `tool` must be declared in `capabilities.tools` with a definition of kind `write`
  or `schema`. Reads, creations, the add-on's mapped rename, create and delete tools, admin
  tools and undeclared names are `400`. The arguments are the tool's own, without the
  resource (the path names it) and without `tool`; they are validated against the
  definition exactly as a model's call is, so an identity, role or approval field or an
  unknown field never reaches the add-on.
- **Role.** A `write` tool needs at least the editor role; a `schema` tool needs the owner
  role. A viewer, an editor changing the schema, a person with no grant and an installation
  administrator get the answer of an unknown resource.
- **Admission and revision.** The change is admitted per resource, waits behind an unresolved
  outcome, and is checked against the generation and the revision the caller read (`409
  stale`, nothing changed). The add-on's receipt is the record: repeating an `operationId`
  with the same request returns the same result and never applies twice; the same ID with
  another request is refused as `409 operation_conflict` before the add-on is called, with
  nothing applied (never `outcome_unknown`). The host binds each operation ID to a request
  hash (`requestHash`: SHA-256 of the resource, the tool and the canonical arguments, per
  user), returns it with the result, and keeps it for a bounded number of recent
  operations. An add-on that refuses the reuse with `409` on `/apply` gets the same
  answer.
- **Result.** The answer carries the new `revision` and the receipt's `result` (for Tables,
  the `count` and the changed row `ids`). An outcome that cannot be established is `503
  outcome_unknown` with the original `operationId`; look it up with `kind=apply` and never
  submit a new operation. The lookup answers `state` plus the `tool` and `requestHash` the
  ID was used for. Pass `requestHash=<hash of your request>` to ask about your own request:
  a hash that differs from the recorded one is `409 operation_conflict`, and a hash the
  host does not hold (for example after a restart) is `state: unknown`, which is safe to
  answer by repeating the same request. A client that compares `requestHash` before it
  believes `committed` never mistakes another change for its own.

### Undoing a change a chat made

A write, schema or create tool called from a chat, an agent or a trigger is applied at once
(there is no card), so undo is the safety net. An add-on that declares `capabilities.undo`
keeps a change set for the changes it can roll back, in its own storage, and says so in the
receipt of the change: the receipt payload has an `undo` object, `{"id", "available",
"summary", "reason"}`. The host copies it into the tool result as `undoable`, `changeId`,
`undo` (what an undo does, one line) and `reason` (why nothing was kept), and the chat shows an
Undo button on the entry. The model is told that the person can undo it and that it cannot.

`POST /api/addons/resources/{resourceId}/changes/{changeId}/undo` takes `{operationId,
generation}`. The change ID is the one the tool result named. It runs under these rules:

- **Humans only.** It accepts only the person's own browser session, with the rules of
  every UI-only mutation: a same-origin fetch, `X-LocalAI-Confirm` equal to the resource ID,
  and no API key, bearer token, tool, agent or MCP marker. This is deliberate. It is never a
  tool and no MCP tool maps to it, so a model cannot undo its own work or anyone else's
  without the person.
- **Role.** The role of the undo is the role of the change, which the add-on reports:
  `write` changes need the editor role, `schema` changes and creations the owner role. Other
  callers get the answer of an unknown resource.
- **Stale.** The undo is applied against the revision the add-on has just reported. If the
  resource moved on after the change, the answer is `409` with type `stale` and a message that
  says what changed; nothing is overwritten.
- **Typed refusals.** `not_found` (`404`: never kept, too large to keep, or dropped),
  `expired` (`410`: past the 7 days of retention), `already_undone` (`409`), `cannot_undo`
  (`409`: for example the job it started has finished) and `stale` (`409`). The type of the
  error is the code. `outcome_unknown` and `outcome_blocked` behave as for every mutation.
- **Idempotent.** The same `operationId` with the same change is one undo and returns the same
  result; after `outcome_unknown` look the receipt up with `kind=undo`.
- **Result.** `{resourceId, changeId, operationId, revision, summary, deleted}`. `deleted` is
  true when the change was the creation of a resource: the host deletes it with its own delete,
  and only while the add-on says nothing touched it since.

For an add-on author: declare `"undo": true` in `capabilities`, add the key `undo` to the
receipt payload of every change, and answer two calls that are never in `capabilities.tools`,
so no model can name them. The read tool `changes-get` takes `{change}` and answers
`{id, class, state, summary, detail, current, deletes}` with `class` `write` or `schema` and
`state` `available`, `stale`, `expired`, `undone`, `cannot` or `not_found`. The apply with the
action `undo` and the tool `changes-undo` takes `{change}` and the revision `current`, and
answers a committed receipt, or a rejected receipt whose payload is `{"error": code,
"message"}`. The add-on checks the action and the scope itself. Tables, Fine-tuning and
Quantization implement it; Fine-tuning and Quantization undo only the start of a job, by
stopping it while it runs.

In a cluster the undo travels the same carrier as every resource call: a frontend forwards it
to the owner, which calls the add-on, and the typed refusal crosses the carrier as the same
typed error. The change sets are state of the add-on, in its own storage on the owner (for
Tables inside each database file), so they are covered by the add-on's backups, restores and
purges and need no extra shared state on the frontends; the carrier and the owner keep none.

An SDK-based page does not call the route itself. `sdk.resources.apply` (see
[the SDK](#the-resource-calls-of-the-sdk)) sends it for the add-on and generation the SDK is
bound to.

### Sharing

Grants are whole-resource and `viewer` or `editor`; the owner role cannot be granted.
Named-user mode only: local-owner mode answers `409` with type `sharing_disabled`, and
anonymous and legacy principals are never grantees. The grantee is named by the immutable
account ID that the picker returned, and must be an active named account that holds the
`addons` feature. The picker finds such accounts by a prefix of the display name or of the
address (2 to 64 characters, at most 20 results) and returns only an ID and a display name,
never an address; the caller and the owner are left out, and the wildcard characters of a
query mean themselves. Lowering or removing a grant cancels the user's pending proposals and
stops their admitted work before the call is acknowledged; a drain that does not finish in
the bound is `503` and the change is already effective for new work.

### The resource of a conversation

The selection is a convenience for chat, not an authority. The owner stores an opaque
resource ID keyed by the authenticated user, the conversation ID and the add-on, in its own
bounded store: at most 256 entries per user (the least recently used is replaced), 8192 in
total, expiring after 90 days without use. It is not process-global, not shared between
people or conversations, not part of the chat history and not sent to the model.

- `PUT` requires at least the viewer role on the resource now.
- A tool call of a chat request that names no `resourceId` uses the conversation's selection.
  The conversation ID comes from the authenticated request (the `X-LocalAI-Conversation-Id`
  header the chat sends), never from the model's arguments, and the owner authorizes the
  reference for the caller on every call. A reference that was revoked, whose resource was
  deleted, that was edited in the store or that belongs to another person gets the denial of
  an unknown resource: the result of kind `error` with code `resource_unreachable`, from the
  owner and from every frontend. The reference stays and works again if the grant does.
- With no selection the tool call returns a result of kind `error` and code
  `select_resource`: no operation runs and the model is told to ask the person to choose.
- A refusal the model can act on is a result of kind `error`, never a raw error string.
  The codes are `invalid_request` (the add-on refused the call; a `message` carries its
  short reason when it gave one, such as a missing training engine), `not_permitted` (the
  person lacks a permission), `resource_unreachable` (no such resource, or no access; a read
  and a change are denied the same way, and the result does not tell a missing resource from
  one the person may not use) and
  `not_running` (the add-on is starting or stopped; nothing was sent). Each note says that
  nothing changed and not to repeat the call. A preview that finds a conflict, such as a
  table that exists, is an `invalid_request`, not an outage. The tools of the fine-tuning and
  quantization add-ons are not offered at all to a person without those permissions.
- An explicit `resourceId` in the arguments is the person's own word and is used as before.

### Errors

| Status | `error.type` | Meaning |
|---|---|---|
| 400 | `invalid_request` | A missing or wrong confirmation, an operation the add-on does not support, arguments or a name it refuses, a malformed identifier. |
| 403 | `addon_error` | Not the person's own session, or the feature or role is denied: one answer for all of them. |
| 403 | `not_permitted` | The add-on checked a host permission of the person, such as fine-tuning, and it is not granted. Nothing changed. |
| 403 | `resource_unreachable` | The resource does not exist or the person has no access to it: one answer for both, for a read and for a change. |
| 403 | `package_unverified` | An install failed because the package does not match its library entry or is not a valid add-on package. Nothing was installed. |
| 403 | `library_unavailable`, `library_conflict`, `release_not_found`, `package_fetch_failed`, `install_storage_failed` | An install or upgrade failed at the step the type names. Nothing was installed. The server log has the detail. |
| 403 | `quota_reached` | The per-person resource limits. |
| 403 | `outcome_blocked` | Another operation on the resource has an unresolved outcome (`blockedBy` is shown to the owner role only). |
| 404 | `not_selected` | No resource is selected, or the reference is no longer usable. |
| 409 | `stale` | The generation or the revision the caller saw is out of date; nothing changed. |
| 409 | `sharing_disabled` | Local-owner mode has no named users to share with. |
| 503 | `outcome_unknown` | A mutation may have run. `operationId` is the original; repeat the same call. |
| 503 | `addon_error` | The owner or the add-on cannot answer. |

A request that the add-on itself refuses as invalid (an unknown table, an unknown field, a
value its schema rejects) is `400` with `error.type` `invalid_request` on every route that
reaches the add-on: the typed apply, a read through dispatch or relay, a proposal and its
confirmation, an artifact open, and the chat tool result (kind `error`, code
`invalid_request`). The owner's own frontend and every forwarding frontend give the same
answer. The add-on answers HTTP 400 or 422 and the runtime client types that answer as a
refusal; any other non-success stays an outage (`503`). A refusal is deterministic and
nothing was applied: it is never `outcome_unknown`, the same call is refused again, and the
caller corrects the request instead of repeating it. An operation ID whose preview or
apply was refused is not consumed: the host forgets its binding and the add-on removes the
pending line it wrote for it, so the corrected request may reuse the same ID. A refused
proposal is not stored. A confirmation the add-on refused ends the proposal as `rejected`.

A selection call has no operation: a lost answer is plain unavailability and the call is
safe to repeat.

## Private file results

An addon read can return file outputs, such as a CSV export. A file result is a private,
temporary artifact of the user who asked for it. It is not a shared file: the model,
media, upload and agent file routes and the frontend code artifacts never see these
bytes, and the existing file APIs are unchanged.

### What the host does with an output

1. The owner never returns the addon's output handle to a client. When a read result has
   file outputs, the owner reserves one artifact for each (all of them, or none) and
   replaces the handle in the result with the **artifact ID**: an opaque, random,
   host-generated identifier. An addon handle, URL or path is never an identifier and
   never names a file. The handle must be a valid ID of the contract; a URL or path in its
   place is refused and nothing is read.
2. The artifact records the requesting principal, the source resources (the request's
   resource, as the policy recorded it), the addon and its generation, the operation, the
   sanitized name, the media type, the declared size and the expiry. The addon handle is
   kept only in memory while the copy runs; only a digest of it, with the principal and
   the operation, is stored so that a repeated request finds the same artifact.
3. A bounded background worker copies the bytes from the registered runtime endpoint of
   the addon (and from nowhere else) into a private staged area under `artifacts/` in the
   addon root: directories `0700`, files `0600`. The worker streams with one 32 KiB buffer
   and never holds the whole file. It stops at the declared size: a stream that is
   short, long by one byte, cancelled or failing is never published, its partial bytes are
   removed and the artifact is recorded as `failed` with a fixed reason. The bytes are
   published only by an atomic rename after the stream ended cleanly.
4. The copy runs under an output admission of **every** source resource, so a revocation
   cancels it and waits for it, like any other admitted work. At completion the owner
   checks again that the account is current and that the principal can read every source
   resource, that the addon is installed, enabled and at the same generation, and that the
   artifact is not expired. Only then is it recorded as `complete` (metadata is written
   atomically with fsync, validated on load and never reset).

Another user's artifact, an unknown ID, an expired one and a malformed one are the same
`403`, so identifiers cannot be probed. Read access to the source resource does not give
access to another user's artifact, and sharing a database never shares its exports.

### Routes

- `GET /api/addons/artifacts/{id}` returns the artifact view: state (`transferring`,
  `complete`, `failed`, `cancelled`), sanitized name, media type, size, sources, addon,
  generation and expiry. It is the status lookup of the background transfer. It is
  read-only, actor-bound and never shows a path or an addon handle.
- `GET /api/addons/artifacts/{id}/download` streams a complete artifact. The receiving
  frontend relays it from the owner through the `file` relay, so a download works from
  every frontend and no frontend holds the bytes. It needs the session or key of the
  requesting principal and the `addons` feature. Before the first byte and while streaming
  the owner checks the account and read access to all source resources again; a revocation
  cancels the stream. A failure after the first byte aborts the connection, so a truncated
  file never looks complete (the response carries the exact `Content-Length`).

No MCP tool maps to these routes in this release. A tool that returns a file result is
added with the Tables export; it will return the artifact reference, not a URL.

### Response safety

Every download is an attachment (RFC 6266, with an ASCII `filename` and a UTF-8
`filename*`) with a sanitized name, `X-Content-Type-Options: nosniff`,
`Cache-Control: private, no-store`, a `Content-Security-Policy` that sandboxes the
response and `Content-Length`. The name loses path elements, control characters,
bidirectional overrides and invisible characters, shell and file system metacharacters and
leading or trailing dots, is prefixed when it is a reserved device name and is at most 100
characters. The media type is one of `text/csv`, `text/plain`, `application/json`,
`application/x-ndjson`, `application/zip`, `application/pdf`, `image/png`, `image/jpeg` or
`application/octet-stream`; everything else (HTML, SVG, XML, JavaScript) is served as
`application/octet-stream`, and parameters are dropped. A browser request with a session
cookie must be a same-origin fetch or navigation like the other addon routes.
Spreadsheet formula handling of a CSV is the addon's concern; the host only delivers the
bytes as a download.

### Limits

All limits are finite and validated at startup; a partial or unbounded override is
refused.

| Limit | Default |
| --- | --- |
| One artifact | 64 MiB (at most the relay file bound; a file result larger than the configured relay file bound is refused whole at ingest, so nothing is staged that cannot be downloaded) |
| Declared bytes of one user / of all users | 1 GiB / 4 GiB |
| Artifact records of one user / of all users | 100 / 1000 |
| Concurrent transfers (all / one user) | 4 / 2 |
| Concurrent downloads | 16 |
| Lifetime from creation | 1 hour |
| One transfer | 5 minutes |

Quotas are enforced under one lock when the artifact is reserved, so concurrent requests
cannot exceed them; a refusal reserves nothing. A quota refusal is `403`, a busy store is
`503`. A repeated request (same principal, operation and output) returns the same
artifact and uses no more quota.

### Lifecycle

- **Expiry.** A sweep (every minute) removes artifacts at their expiry, transfers that no
  worker owns, artifacts of an uninstalled addon and files that no record names; opening
  the store also settles interrupted transfers and removes unreferenced files. An
  expired artifact is never served, even if the sweep did not run yet or the expiry falls
  while a download is being admitted or streamed. A download that meets a sweep which
  removes the artifact at that moment gets the same `403` as any expired or unknown
  identifier, never a corruption error. The sweep removes the bytes only of records it
  really changed: a transfer that completes while the sweep runs keeps its bytes.
- **Storage errors.** Only bytes that are definitely missing, not a regular file or of
  another size than recorded mark the artifact `failed` and remove the bytes (`503`). A
  transient storage error (for example too many open files or an I/O error) is `503`
  and leaves the bytes and the record as they are, so a later download can succeed.
- **Disable.** A disabled addon denies downloads and accepts no new file result. The
  complete bytes are kept until their normal expiry. Enabling it again serves an artifact
  only while it is unexpired and only to a principal that can still read every source
  resource.
- **Uninstall.** An uninstalled addon denies downloads at once and its artifacts are
  removed by the next sweep (they are not kept until expiry, because nothing can answer for
  them). An addon update to another generation does not serve the older build's files.
- **Restart.** Complete artifacts survive a restart. A transfer that was running is
  recorded as failed and its partial bytes are removed.
- **Close.** Closing the owner cancels running transfers and streams, waits for them and
  writes nothing afterwards.

Run `sh scripts/addons/verify.sh artifacts` for the artifact suites.

## Apps

Enabled addons behave like apps: a list of what is available, with an icon, a title,
a state and, when the addon has a page to open, an Open action.

### Who sees what

`GET /api/addons/apps` needs named authentication and the `addons` feature. The
owner builds the answer from its own journal, so every frontend gives the same
answer:

- A caller sees every addon that is installed **and** desired-enabled. Version one has
  no per-addon access list; the data an addon holds stays under its own per-resource
  ACLs, which every call checks again. The apps a caller sees are the addons the same
  caller could call. Hiding an entry is never the control.
- An administrator also sees installed addons that are disabled (state `stopped`,
  `enabled: false`), so that a stopped addon can be started. The owner decides who is
  an administrator from its own current account check, never from the request. If that
  check cannot answer, the caller gets the reader's view.
- A headless addon is listed with its state and tools and has no `mainPage`, `icon`
  or `title` beyond its identifier.

Each entry has `id`, `name` (the same as `id`), `title`, `description` (the
default-language text; `titles` and `descriptions` carry every language when the
manifest localizes them), `icon`, `version`, `generation`, `enabled`, `state`,
`mainPage`, `hasUI`, `launchMode`, `tools` (at most 32 names; the manifest has no tool
descriptions) and `toolCount`. Administrators also get `admin` with `canStart`,
`canStop` and `managementUrl`. Nothing of the package location, its source or the
addon's own endpoint is returned.

`state` is an observation of the process, separate from `enabled`:

| State | Meaning |
|---|---|
| `running` | The process is alive and admitting calls. |
| `starting` | A launch is between exec and readiness, at first start or restart. |
| `failed` | The process is not serving and no launch is under way. The supervisor restarts it after its backoff while its budget lasts, so `failed` can be followed by `starting`; when the budget is spent it stays `failed` until an administrator stops and starts the addon. |
| `stopped` | The addon is disabled, or no process of this generation is held. |

An app can also carry `update: "update-pending"`. It is a separate observation from
`state` and is present only when the owner's cached update check says an update is
available for exactly the installed package. The list answers from that cache: it never
evaluates a check and never reaches a library, so an add-on nobody checked, one whose
check failed and one that is up to date have no `update` field. See
[Update detection and upgrade](#update-detection-and-upgrade).

`mainPage` is `/app/addons/{id}/{page}` and is present only for an enabled addon whose
manifest declares a `ui.mainPage` that is one of its pages. `icon` is
`/api/addons/apps/{id}/icon?generation=N`. The route is served by the host from the
verified package, relayed from the owner, with `X-Content-Type-Options: nosniff` and
`Cache-Control: private, no-store`. It is never an addon process port and never a URL
the manifest names. A stale generation, an unknown or hidden app and an app without a
verified icon give the same 403.

### Start and stop

`POST /api/addons/apps/{id}/start` and `.../stop` take
`{"operationId": "...", "generation": N}`. They are thin routes over the typed `enable`
and `disable` lifecycle actions: the same administrator check, the same owner
forwarding, the same operation identity and the same outcome-unknown rule. Stop drains
the process and keeps data.

- Only administrators can start or stop. Everyone else gets 403 whatever the body says,
  before the body is read.
- `generation` is the generation the caller saw in the list. If the installed
  generation differs, the request is denied (403) before any operation is recorded.
- A repeated `operationId` replays; it never runs twice. After an `outcome_unknown`
  response, keep the `operationId` and poll `operation-status` with it.
- Health stays a separate observation: a successful start records intent and starts the
  process; the list's `state` says whether it is running.

The typed `enable` and `disable` actions of `POST /api/addons/dispatch` accept the same
generation precondition through the request's `generation` field when it is not zero.

No MCP tool maps to the Apps routes in this release. The admin mutation tools for addons
are a later task and will have their own review.

### Built-in add-ons

`GET /api/addons/builtin` lists the page add-ons that ship inside the web bundle, so the
My add-ons page can show them next to the installed add-ons. The answer is
`{"builtin": [...]}`, and each entry has an `id`, a `kind` (always `page`), a `titleKey`,
an `icon` and a `mainPath`, plus a `feature` or `adminOnly` when the entry has a gate.
Today the list is empty: Studio and Talk, which used to be built in, are packaged
add-ons now (see [The Studio add-on](#the-studio-add-on) and
[The Talk add-on](#the-talk-add-on)), so a person installs and removes them like the
others. The route is kept for the day a page is built in again. It needs an
authenticated caller and does not need the `addons` feature. When authentication is off,
the caller is the local owner and sees every entry. The answer depends on the caller and is
sent with `Cache-Control: private, no-store`.

### Pins

Pinned add-ons are listed in the sidebar under Add-ons, in pin order. The menu of a pinned card in My add-ons has **Move earlier** and **Move later**.

`GET /api/addons/pins` returns the caller's pinned add-on IDs as a JSON array, in order.
`PUT /api/addons/pins` takes a JSON array of add-on IDs, replaces the caller's pins with it
and returns the saved list. Both routes need an authenticated caller. They need no
administrator role and no `addons` feature, like `GET /api/addons/builtin`. A `PUT` from a
browser must be a same-origin request or carry a credential header. The answer depends on
the caller and is sent with `Cache-Control: private, no-store`.

- **Filtering.** A pin is filtered like a tile. The list holds only IDs the caller may see
  now: the built-in add-ons that pass their gates (none today), plus the installed apps the owner lists
  for the caller. An ID the caller cannot see is left out of a read and dropped from a
  save, with no hint whether it exists. Duplicates are dropped.
- **Hidden pins survive.** A pin that is only hidden for now (an access change, or an
  add-on removed inside its retention window) is kept after the visible pins when the
  caller saves.
- **Limit.** A save of more than 32 IDs, or a body that is not a list of strings, is
  refused with `400`. If the add-on owner does not answer, a save is refused with `503`, so
  an outage cannot drop pins.
- **Retention.** When an add-on is removed, its pins are hidden at once and kept for 30
  days. If the add-on is installed again inside that window, the pins come back. After 30
  days the pins are deleted. A background sweep runs every six hours; with several
  frontends, one of them runs it at a time. A disabled add-on is not removed, so its pins
  never expire. A sweep that cannot read the add-on owner changes nothing.
- **Upgrade seed.** A read or a save never pins anything. Once, at the upgrade that adds
  this feature, the accounts that exist then get Studio and Talk pinned. Both are
  add-ons, so a pin is drawn once the add-on is installed. A seeded pin does not
  expire while its add-on was never installed, however long that takes; once the
  add-on was installed, its pin follows the retention rule like any other (the sweep
  records the first install in the table of seed runs, `addon_pin_seed_runs`, or in
  the pins file). An account created later starts with no pins. An unpin stays after a restart.
- **Startup.** If the pins file cannot be read (it is corrupt), the server does not start
  and logs the error. It does not fall back to memory, because that would lose every unpin
  at the next restart.
- **Distributed state: shared.** With an account database, the table `addon_pins` is
  shared by every frontend. With authentication off there is one local owner, and the pins
  are kept in `addon-pins.json` in the data path, per instance.

`builtin` and `pins` are reserved add-on IDs: an add-on cannot use them, because the host
serves `/api/addons/builtin` and `/api/addons/pins`.

## Add-ons page and management

The web UI serves one page for add-ons at `/app/addons`, called **Add-ons**. It is
shown to a caller who holds the `addons` feature, and to anyone who can open a built-in
tile (none today). Studio and Talk are add-ons: they are listed only while they
are installed. The first tab is **My add-ons**. Administrators also
reach **Find add-ons** and **Sources**. The tabs are a
convenience: the owner checks administration again on every call, so a request that skips
the page is denied the same way (403).

The sidebar has the items **Chat**, **Models** and **Add-ons**. **Add-ons** opens the page at
`/app/addons`. The add-ons a user shows in the sidebar are listed under it, in order, as indented links with the add-on icon and title; nothing is listed when none is
shown, and the list is hidden when the sidebar is collapsed. The user adds an add-on to the sidebar with **Show in sidebar** in the menu of its card, and
changes the order with **Move earlier** and **Move later** in the same menu. Pins belong to the user, follow the user across browsers, and are
capped at eight. A pin whose add-on is stopped opens the detail view; a pin whose add-on
was removed or is hidden is not drawn. Operators who
need the previous sidebar can set `LOCALAI_LEGACY_NAVIGATION=true` (flag
`--legacy-navigation`); it also brings back the Add-ons hub, a one-tab bar over `/app/addons` that the legacy sidebar lists. The switch is a rollback aid and is removed one release after the
new navigation ships; the UI reads it from the `addon_navigation` key of `/api/features`.

A tab is addressed as `/app/addons?tab=browse|libraries`; My add-ons has no `tab`. What the
former Installed tab showed is the **More details** section of an add-on in My add-ons, so
`?tab=installed` opens My add-ons. The management link of an app
(`/app/addons?manage=ID`) opens My add-ons with the details of that add-on open.

### Old links

The generators that had their own page now open inside the Studio add-on, and the
Studio and Talk pages open their add-ons. The old links still work and keep the model, the
query string and the hash:

| Old link | Opens |
|---|---|
| `/app/studio` | `/app/addons/studio/studio` |
| `/app/studio/<tab>` | `/app/addons/studio/studio/<tab>` |
| `/app/talk` | `/app/addons/talk/talk` |
| `/app/image` | `/app/addons/studio/studio/images` |
| `/app/video` | `/app/addons/studio/studio/video` |
| `/app/3d` | `/app/addons/studio/studio/threed` |
| `/app/tts` | `/app/addons/studio/studio/tts` |
| `/app/sound` | `/app/addons/studio/studio/sound` |
| `/app/diarization` | `/app/addons/studio/studio/diarization` |
| `/app/transform` | `/app/addons/studio/studio/transform` |
| `/app/face` | `/app/addons/face/recognition` |
| `/app/face/<model>` | `/app/addons/face/recognition?model=<model>` |
| `/app/voice` | `/app/addons/voice/voice/recognition` |
| `/app/voice/<model>` | `/app/addons/voice/voice/recognition?model=<model>` |
| `/app/voice-library` | `/app/addons/voice/voice/library` |
| `/app/voice-library/new` | `/app/addons/voice/voice/library/new` |

None of these links has a core page behind it: the face, voice, Studio and Talk pages exist
only in their add-ons. Without the add-on, a link opens the add-on page, which says the
add-on is not available.
A model id can hold `/` or `:`, which an add-on path segment refuses, so the model travels
as `?model=` (for example `/app/voice/org%2Fname` opens `/app/addons/voice/voice/recognition?model=org%2Fname`; the face links and the
Studio links do the same).

A link with a model (`/app/image/<model>`) opens `/app/addons/studio/studio/images?model=<model>`. The
`/app/studio?tab=` links redirect to `/app/addons/studio/studio/<tab>`. A viewer without the `3d`,
`audio_diarization` or `audio_transform` feature still lands on the app home, as before.

The legacy switch (`LOCALAI_LEGACY_NAVIGATION`) restores the old sidebar and the Add-ons
hub only. These redirects do not depend on it: with the switch on, the
old generator links also open the Studio add-on. The legacy sidebar lists Home, Models, All chats, Agents, MCP connectors, Memory and
the hubs; Studio and Talk are in My add-ons, as add-ons.

### The Studio add-on

The Studio add-on (`studio`) is a packaged add-on. It is not shipped with LocalAI: install
it from **Find add-ons** with **Install and turn on**. Once it is usable, it appears in
My add-ons and can be shown in the sidebar. Until then the sidebar, My add-ons and the Home `/studio`
command do not list it, and the old Studio links open the add-on page, which says the
add-on is not available.

| Page | Path | Who sees it |
|---|---|---|
| Studio (main page) | `/app/addons/studio/studio` | users with the `addons` feature |

The tab is the one path segment after the page: `/app/addons/studio/studio/images`,
`.../video`, `.../threed`, `.../tts`, `.../sound`, `.../transform` and `.../diarization`.
The overview is the page without a tab. A model travels as `?model=<id>` and a result as
`?work=<id>`. A tab whose feature is off for the user (`3d`, `audio_diarization`,
`audio_transform`) is not listed.

The pages call core routes with the signed-in user's own session (see "Core routes from
add-ons"): `/v1/images/generations`, `/v1/images/upscale`, `/video`, `/3d/*`, `/tts`,
`/v1/audio/speech`, `/v1/sound-generation`, `/audio/transformations`,
`/v1/audio/diarization`, `/api/models/*`, `/api/resources` and `/api/operations`. The
core keeps these routes and checks every call again, so the page never widens what a
user may do. The history stays in the browser, under the same keys as before the move,
so what a person made earlier is still there when the add-on is installed. The words are
the `media` locale files of the web UI, in every language the web UI has.

### The Talk add-on

The Talk add-on (`talk`) is a packaged add-on, separate from Studio. Install it from
**Find add-ons** with **Install and turn on**. It adds the page `/app/addons/talk/talk` (main page) for
users with the `addons` feature. The page opens a WebRTC session to a realtime pipeline
through `/v1/realtime/calls` and reads the pipeline list from `/api/pipeline-models`, with
the signed-in user's own session; the core keeps both routes. The page uses the MCP servers
that the person added in the browser, the same list as Chat. The words are the English `talk` locale
file of the web UI; other languages read the English text.

### The Voice add-on

The Voice add-on (`voice`) is a packaged add-on. It is not shipped with LocalAI: install it
from **Find add-ons** with **Install and turn on**. Once it is usable, it appears in
My add-ons and can be shown in the sidebar.

The add-on has one page, `/app/addons/voice/voice`, and one entry in My add-ons. Its
sections are tabs of that page, and the tab is the next segment of the address:

| Tab | Path | Who sees it |
|---|---|---|
| Recognition (default) | `/app/addons/voice/voice/recognition` | users with the `voice_recognition` feature; the tab shows an off notice for anyone else |
| Library | `/app/addons/voice/voice/library` (the form to add a voice is `.../library/new`) | administrators |
| Text to speech | `/app/addons/voice/voice/speech` | users with the `addons` feature; the server refuses the speech call without `audio_speech` |
| From a recording | `/app/addons/voice/voice/recording` | users with the `addons` feature |

The earlier page addresses (`/app/addons/voice/recognition`, `.../library`, `.../create`
and `.../speech`) still open: each one redirects to its tab and keeps the query string
(`?model=`, `?selected=`, `?voice=`).

All of these also need the `addons` feature to see the tile. The pages call core routes
with the signed-in user's own session (see "Core routes from add-ons"): `/v1/voice/*` for
recognition (verify, analyze, embed, register, identify, forget), `/api/voice-profiles`
for the library and `/v1/audio/speech` for the speech page. The core keeps the voice
registry and these routes; the server checks every call again, so an add-on page never
widens what a user may do.

The **Recognition** tab is the workbench: who is this, same person, the people this
browser enrolled, and the analyze and embedding tools under **More tools**. **Library**
manages the reference recordings that text-to-speech can use and is shown to
administrators only. **Text to speech** speaks text with a voice model. **From a
recording** links to the diarization workspace of Studio. The Face add-on shares the same
views, built from one source directory into each add-on.

### The Face add-on

The Face add-on (`face`) is a packaged add-on, separate from the Voice add-on. It is not
shipped with LocalAI: install it from **Find add-ons** with **Install and turn on**. Once it
is usable, it appears in My add-ons and can be shown in the sidebar.

| Page | Path | Who sees it |
|---|---|---|
| Recognition (main page) | `/app/addons/face/recognition` | users with the `face_recognition` feature; the page shows an off notice for anyone else |

The tile needs the `addons` feature. The page calls core routes with the signed-in user's
own session (see "Core routes from add-ons"): `/v1/face/*` (verify, analyze, embed,
register, identify, forget) and the model list. The core keeps these routes and the face
registry; the server checks every call again, so the page never widens what a user may do.

The page holds the face recognition workbench and the management of the known faces: the
people this browser enrolled, the enrol form and the removal behind Undo. It has no
administrator-only part, so every user who holds `face_recognition` can use all of it. The
detect-and-analyze and embedding tools are under **More tools**. The Voice add-on shares the
same views, built from one source directory into each add-on.

### The Fine-tuning and Quantization add-ons

The Fine-tuning add-on (`fine-tuning`) and the Quantization add-on (`quantization`) are
packaged add-ons. They are not shipped with LocalAI: install them from **Find add-ons**
with **Install and turn on**. Once usable, they appear in My add-ons and can be shown in the sidebar.

| Add-on | Page | Path | Who sees it |
|---|---|---|---|
| `fine-tuning` | Fine-Tune | `/app/addons/fine-tuning/jobs` | users with the `fine_tuning` feature; the page shows an off notice for anyone else |
| `quantization` | Quantize | `/app/addons/quantization/jobs` | users with the `quantization` feature; the page shows an off notice for anyone else |

The old links `/app/fine-tune` and `/app/quantize` open the add-on page. Without the add-on,
they show the add-on page that says the add-on is not available. The pages call
`/api/fine-tuning/*` and `/api/quantization/*` with the signed-in user's own session (see
"Core routes from add-ons"); the core keeps these routes and checks every call again.
Both add-ons offer chat tools that start a job. In the chats a tool call starts it at once; on other surfaces it is a proposal that waits for a person to confirm. They
reach the engine through [Host operations](#host-operations), and a tool can control only
a job that a tool started. See [Fine-tuning](../fine-tuning/#fine-tuning-is-an-add-on) and
[Quantization](../quantization/#quantization-is-an-add-on).

### My add-ons

My add-ons is a grid of cards with a search box. The built-in cards (none today) come
first, then the installed add-ons by identifier. A card shows the icon, the name, the state
(**On**, **Off**, or the state the host observed, such as **Starting** or **Failed**), the
description, **Adds a page** when the add-on has a page and the number of **chat tools** when
it has tools. A running add-on with a page also has an **Open** link. Each card has one
overflow menu with **Show in sidebar** (or **Remove from sidebar**), **Details**, and, for an
administrator, **Remove**. A pinned add-on also has **Move earlier** and **Move later** in the
menu. Showing a ninth add-on in the sidebar opens a dialog that asks the user to remove one
first. Selecting a card, or **Details**, opens the preview pane beside the list
(`/app/addons?detail=ID`) and never the main page, because the main page needs a live
process. The pane shows the name and state, the description, screenshots only when the host
lists images for the add-on (it lists none today, so no slot is drawn), what the add-on adds
(its page and its chat tools), **Turn on** or **Turn off**, **Remove** and the version and
source. For an administrator it also has **More details**: health (with the install time), trust, compatibility,
storage, data recovery and updates. Administrators also see **Find add-ons**.
A caller without the `addons` feature sees only the built-in cards (none today), and the page does not
call `GET /api/addons/apps` for that caller.

With nothing installed, an administrator sees three add-ons from the sources, each with
**Install and turn on**, and a link to **Find add-ons**. A search that matches nothing says
that no add-on matches it.

The pane also lists the pages the add-on names in
`ui.navigation`. The page draws `GET /api/addons/apps`: the icon from the host's icon route, the
title in the interface language (falling back to the default text and then to the
identifier), the description, a state chip (`running`, `starting`, `failed`, `stopped`),
an **update pending** chip when the owner's cached check reports one, an **Open** link only when the entry
has a `mainPage`, and a tool summary. Icons always
come from the same origin; the page never loads an image from a manifest or a library.

The list is read again every 5 seconds (30 seconds in a hidden tab). A failed read
doubles the wait up to 60 seconds, keeps the last list on screen and says it may be
out of date. Leaving the page cancels the pending read. Administrators see **Turn on** and
**Turn off** on an entry that carries the `admin` object. They send
a fresh operation ID with the generation that was listed and keep that ID for
retries of that one action: once the operation is final (completed, failed or refused)
the ID is dropped, so the next click is a new operation and is never answered from an
old one. After `outcome_unknown` the page reads the original
operation with `operation-status` and never submits a second one. If the reads end
without a final answer, the add-on shows **Check status** (one more read of the same
operation) and **Dismiss**, and no other action on that add-on is sent until one of them
is used. After a `completed` status the page reads the list again and says so when the
add-on did not change, because the owner answers a replayed operation with its old status
and applies nothing. A 403 (a stale
generation or no permission) and a 503 are shown with the same words everywhere on the
page, and the list is read again.

The Open link points at `/app/addons/{id}/{page}`, the page of the add-on described in
[Add-on pages](#add-on-pages) below.

### Management actions

All of these go through `POST /api/addons/dispatch`, are administrator-only on the owner,
and take strict arguments: an unknown field is a denial. The reads change nothing. The
writes keep the operation identity and outcome-unknown rule of the lifecycle actions.

| Action | Kind | Arguments | Payload |
|---|---|---|---|
| `catalog-search` | read | `text`, `kind` (`addon` by default, `backend` or `all`), `sourceId`, `offset`, `limit` (at most 100) | `{releases, total}`; only releases for the owner's configured target platform are listed, and `total` counts that set |
| `catalog-details` | read | request `addonId`; `sourceId`, `version` | `{release, installed}`; `installed` is `null` or `{version, generation, enabled}` |
| `catalog-sources` | read | none | `{sources}`; each source has `refresh` once the owner fetched it |
| `addons-installed` | read | none | `{addons}`, the installed view below |
| `catalog-source-save` | write | one source: `id`, `name`, `url`, `enabled`, `policy`, `authReference`; `removed: true` deletes it | the stored source |
| `catalog-source-refresh` | write | `sourceId` | `{sourceId, count, checkedAt, lastSuccess}` |
| `upgrade` | write | `sourceId`, `version` | the operation status |

A library carries a **reference** to a credential, never a secret. `authReference` must be
the name of a credential the owner configured; any other value is denied and is not
stored or shown back. The URL of a library cannot carry credentials or a query. A
browser session cannot turn on the plaintext loopback opt-in for a library that does not
already have it. Removing a library never touches installed addons: they stay installed,
keep their data and keep running.

A refresh is a write because it makes the owner fetch a remote document. A library that is
disabled, unreachable or invalid answers 503 with `outcome_unknown` and the original
operation ID, like any other write whose result cannot be proved; the `refresh` object of
`catalog-sources` says what the owner last learned (`checkedAt`, `lastSuccess`, `count`,
`failed`).

### Installed view

Each entry of `addons-installed` has `id`, `title`, `description`, `icon`, `version`,
`generation`, `enabled`, `state` (the process observation, separate from `enabled`),
`sourceId`, `channel`, `selection`, `verification` (`sha256` or `sha256+sigstore`),
`digest`, `platform` (`os`, `arch`), `compatibility` (the `host`, `api`, `protocol` and
`dataFormat` ranges of the manifest), `tools`, `toolCount`, `hasUI`, `mainPage`,
`launchMode`, `generations` (retained installed generations), `restarts` (supervisor restart
attempts), `storage` (`bytes` and `truncated` for the retained data) and `update`.

The size of the retained data is read in small batches, never follows symbolic links, and
stops after 20000 entries or about 0.75 seconds, whichever comes first; `truncated` is then
true and `bytes` is a lower bound. The owner keeps the last result for 30 seconds, so a
page that polls does not walk the tree again.

`update` is the owner's update check of the add-on: `state`, `available`, the candidate
`version` and `digest`, `checkedAt`, `lastSuccess`, `stale`, `reasons` and a redacted
`error` class. It is absent when the owner cannot answer for the add-on, and then the page
says that the update status is unknown. The page never compares version labels itself and
never reads a failed check as current. See
[Update detection and upgrade](#update-detection-and-upgrade). Process output is not
collected in this release, so the page has no log view; the `state` and `restarts` fields
are the health evidence it shows.

Uninstalling stops the addon and removes its package from use. Its data, identity and
grants are kept, so a later install of the same addon picks them up. The page says so
before it submits the uninstall.

## Data recovery

An add-on's data is the data the owner keeps for it under `<root>/data/<id>`, the owner's
records of its resources and shares, and the proposals made against it. Recovery keeps
that recoverable. It is administrator-only, it is never a tool, and the owner decides
every step. Replacing a package alone never restores data: the previous package is
kept, but it does not make the data it could not read readable again.

### Backups

`POST /api/addons/{id}/recovery/backup` makes one backup. The owner blocks new admission
for the add-on's resources, makes its pending proposals invalid, drains the work that was
admitted (bounded), stops the add-on and copies its data: every SQLite file is written
with `VACUUM INTO`, so the write-ahead log is part of the copy and the copy holds no log
of its own. Scratch directories named `exports` and `tmp` are not copied, and symbolic
links are never followed. The owner's access state (resources and shares), the proposals
of the add-on and the conversation selections are copied beside it.

A backup is a directory `<root>/backups/<id>/<backupId>` of mode `0700` with files of mode
`0600`. Its `manifest.json` records the creation time, the reason (`manual`, `upgrade` or
`restore`), the ownership mode, the package digest, trusted source, channel,
verification level and data format, and the size, `sha256` and, for a SQLite file, the
schema version of every file. The backup is verified while it is still private: every
digest is read again and every SQLite file passes `PRAGMA integrity_check`. Only then is
it renamed into place and flushed. A backup that fails, is cancelled or does not fit
leaves nothing behind, and the add-on is started again if it was meant to run.

The free space is checked before anything is copied: it must be at least the size of
the data plus a margin of 16 MiB or a tenth of the data. A backup that does not fit
answers `503` with the type `insufficient_storage` and changes nothing.

Retention is explicit: the owner keeps `BackupKeep` backups per add-on (default three)
and removes older ones, and with `BackupMaxAge` also those older than that. The newest
verified backup and the recovery copy of an upgrade are never removed by retention, and
`POST .../recovery/delete-backup` refuses them with `sole_recovery_copy`: the only
verified recovery copy is never removed silently. A purge with an explicit scope is the
way to remove them. `POST .../recovery/prune` applies the policy now.

### Upgrade of an add-on that has data

An `upgrade` of an add-on that has a data directory runs this order, and every step is
recorded before it runs:

1. The trusted identity of the staged package is compared with the installed one: the
   same add-on from the same source and channel with the same verification level. A
   different one is refused before anything stops. The same rule holds for a reinstall of
   retained data. A package that declares an older data format than the data is refused.
2. Admission is blocked, proposals are invalidated, admitted work drains and the add-on
   stops.
3. A verified backup is made.
4. The package is published and the candidate generation is started while nothing else
   touches the data. That start is the migration under exclusive ownership: an add-on
   migrates its own data when it starts and writes its progress to `migration.json` in
   its data directory (`state` is `running`, `complete` or `failed`, with `from`, `to`
   and `error`). The start is the runtime's health check; a `migration.json` that does
   not say `complete` fails the upgrade. Tables does this with `Store.Migrate(ctx,
   fromVersion, toVersion)`: each database changes in one transaction, the version of
   the store is recorded last, a run that stopped is resumed, and data in a newer format
   than the executable reads is refused and left as it is.
5. Only when the candidate is healthy is the generation published, with the journal
   ordering of a plain upgrade.

A failure at any step before the publication is undone. When the data was not touched,
the old generation runs again. When the candidate began to migrate and the format is the
same, the verified backup is put back before the old generation starts, and what the
candidate left is kept as a recovery copy. When the migration changes the data format and
fails, the add-on stays unavailable in the state `needs-recovery`: the previous package
is retained and so is the verified backup, but the previous package cannot read data the
new one began to change, so nothing starts it. The administrator restores the backup or
purges the data. While the state lasts, the backup that was made for it is always kept:
retention, a prune and a delete do not remove it, and a manual backup is refused, because
a copy of half migrated data must not look like the newest good one. A restart finds the operation at its recorded phase and applies one
rule: a generation that was not published is undone, and a published one is finished.

Add-ons without data keep the plain upgrade and start the candidate as before.

### Restore

`POST /api/addons/{id}/recovery/restore` restores one backup. The add-on must not be
running, and the request carries the add-on ID typed as `confirm`. The backup must
belong to the add-on named in the route: a backup of another add-on is refused with
`invalid_request` before anything is touched. The backup is verified before anything changes. Its files are staged beside the data and swapped in
atomically; the data they replace stays as a recovery copy, named `previous-<id>`, until
an administrator purges it. A crash at any point is settled by the owner on restart:
before the swap the restore is undone, from the swap on it is finished.

The answer lists warnings: the time the data goes back to, how many changes the owner
applied after the backup (their effects are gone), and the shares that were not
restored. Restoring never gives back access:

- Every owner and every grant is checked again against the current accounts. An account
  that is gone, paused or pending is not restored.
- The owner keeps a durable log of lowered access (a removal or a lowering of a role),
  compacted per person and resource: a later, stricter lowering replaces the ones it
  covers, so frequent changes of other people never push a removal out. A grant that was
  removed after the backup is not restored, and one that was lowered is restored at the
  lower role. The cutoff is the moment the access state was read for the backup, taken
  under the same lock, and the add-on's admission hold also stops changes of its grants.
- The log has a hard size limit. When it must drop entries, it records the time up to
  which it may be incomplete. A restore from a backup older than that time restores the
  data and no access at all: no owner has to be trusted to be still valid, so the
  resources come back with their owner only and the answer says so
  (`historyTruncated`). The owner shares them again.
- Proposals that were pending are cancelled. Operations that were executing, or whose
  outcome was unknown, become `outcome_unknown` and are looked up by receipt of the
  restored add-on; they are never applied again.

A backup made in local-owner mode needs an explicit ownership mapping,
`{"local-owner": "<account id>"}`, to be restored in named-user mode; there is no
wildcard and no default, and the target must be a current account. Data of named users
is refused in local-owner mode. The mode of a stopped installation can be changed with
`policy.MigrateOwnership`: it writes a backup of the policy store first, takes the same
mapping, and refuses to move populated named-user data to the local owner.

Repeating the `operationId` of a restore returns its receipt and does not restore again.

### Purge of retained data

`POST /api/addons/{id}/purge` removes retained data. It is not the deletion of a
database and not an uninstall, which never remove retained data. The add-on must be
uninstalled or disabled and not running, the request carries an explicit scope
(`data`, `backups`, `recoveryCopies`) and the add-on ID typed as `confirm`. The purge is
recorded before it starts and finished by the owner after a crash. It touches no other
add-on and removes the owner's records of the add-on's resources only when `data` is in
the scope. Purging the data clears `needs-recovery`.

### Routes and rules

| Route | What it does |
|---|---|
| `GET /api/addons/{id}/recovery` | state, backups with their verification state, retention |
| `POST /api/addons/{id}/recovery/backup` | make a backup |
| `POST /api/addons/{id}/recovery/verify` | read a backup completely and record the result |
| `POST /api/addons/{id}/recovery/restore` | restore a backup |
| `POST /api/addons/{id}/recovery/delete-backup` | delete a backup that is not the sole recovery copy |
| `POST /api/addons/{id}/recovery/prune` | apply the retention policy now |
| `POST /api/addons/{id}/purge` | purge retained data |

All of them need a named administrator. Every mutation is accepted only from the
administrator's own browser session: a same-origin fetch whose `X-LocalAI-Confirm`
header equals the `operationId`; an API key, a tool call or an MCP session is refused.
Each is decided by the owner, so a forwarding frontend gives the same typed answer, and
a lost reply is `outcome_unknown` with the original `operationId`. Typed refusals are
`needs_recovery`, `addon_running`, `busy`, `backup_not_found`, `backup_corrupt`,
`incompatible_format`, `identity_changed`, `mapping_required`, `mapping_invalid`,
`downgrade_unsupported`, `sole_recovery_copy`, `confirmation_mismatch`,
`operation_conflict`, `invalid_request` and `insufficient_storage`. None of these is a
tool: no MCP tool, model tool, proposal or typed dispatch of a model maps to them, and
the manifest of an add-on cannot declare them.

The Add-ons page shows the recovery state of an installed add-on: the backups with
their size and verification state, a banner for `needs-recovery` with the actions
restore and purge, the phase of an upgrade in progress and the warnings of the last
restore.

## Update detection and upgrade

The owner knows what is installed and which library and channel it came from, so the
owner decides whether an update exists. A browser never compares versions and never
chooses the library. Detection and upgrade are two different things: a check only
reads, and an upgrade is one explicit administrator action that names the exact
candidate.

### States

`GET /api/addons/{id}/update` returns one of these states:

| State | Meaning |
|---|---|
| `unknown` | No check has run for the installed package. |
| `up-to-date` | The library offers nothing newer for the selection the add-on was installed from. |
| `update-available` | A compatible candidate exists and differs from the installed package. |
| `incompatible` | A candidate exists that this host cannot run. `reasons` names `platform`, `host-version`, `api-version`, `protocol-version`, `sdk-version` or `data-format`. |
| `check-failed` | The owner could not tell. `error` is a class: `source-unavailable`, `refresh-failed`, `credential-unavailable`, `not-listed`, `ambiguous-release`, `canceled` or `internal`, and `lastSuccess` is when the library was last read. A failed check is never shown as up to date. |
| `integrity-error` | The library lists the installed immutable version with another digest. It is never an update. |

The status also carries `installedVersion`, `installedDigest`, `candidateVersion`,
`candidateDigest`, the `sourceId`, `channel` and `selection` the add-on came from,
`checkedAt`, `stale` (the answer rests on data older than the time to live) and
`throttled` (an explicit refresh was not run because one ran moments ago).

### How the candidate is chosen

- A moving channel (`development`, a `branch-*` channel, `latest`) compares resolved
  digests. A different digest is an update even when the version label is the same.
- A development build installed from a mutable entry (a moving tag such as
  `0.0.0-development`, with no digest in the library) is compared by the digest that its tag
  names. Its label is not compared as a version. An explicit check (**Check for updates**,
  **Check again** or the automatic upgrade) resolves the tag in the registry and keeps the
  digest, so a later status read compares against it and does not use the network. Before
  the first explicit check the state is `unknown`. When the tag names another build, the
  state is `update-available` with that digest as `candidateDigest` and the label with the
  first 12 hex digits of the digest as `candidateVersion`. A tag that cannot be resolved is
  `check-failed` with `refresh-failed`. The upgrade resolves the tag again and refuses with
  `candidate_changed` when the tag moved after the check.
- An add-on installed from a version label is immutable. The owner offers the highest
  greater version of the same major version, and never a pre-release to a stable
  install. The catalog entry of the installed version with another digest is an
  `integrity-error`: an immutable version was rewritten.
- The library and the channel never change by themselves. An entry of another channel or
  another library is never offered, whatever its version.
- A catalog entry may carry an optional `requires` object with `host`, `api`,
  `protocol`, `dataFormat` and `sdk` ranges (`{"min": 1, "max": 2}`). It is a hint for the
  check only: the manifest of the verified package is checked again when the candidate is
  staged. A candidate whose data format ends below the installed one is incompatible. The
  `sdk` range is only checked for a package that declares one, so a package without a UI
  is not refused for it.

### Cache, refresh and credentials

The check and its cache live on the owner only, like the journal they read: a forwarding
frontend asks the owner and never keeps an answer of its own. The cache is bounded for each
add-on (default 256 entries, 15 minutes) and ends with the owner. A
status read answers from it or evaluates the library data the owner already holds; it never
uses the network. `POST /api/addons/{id}/update/check` fetches the library again through
the credential reference the library is configured with, at most once per interval for an
add-on and for a library (default 15 seconds; `UpdateCacheTTL` and `UpdateRefreshInterval` of the
add-on configuration change the two defaults), and then returns the new status. A secret,
an address or the text of a failure never appears in a status. Checks never apply an
update.

### Who sees what

Anyone who holds the addons feature and may see the add-on reads the status: an
administrator any installed add-on, everyone else an enabled one. A caller who is not an
administrator gets the state, the versions, the times and the reasons, without the
library, channel, selection or digests. Checking and upgrading are administrator-only and
accepted only from the administrator's own browser session (same-origin fetch,
`X-LocalAI-Confirm` equal to the `operationId`); an API key is refused, whatever its
role. The owner decides again on every call, from every frontend.

### Routes

| Route | Who | What |
|---|---|---|
| `GET /api/addons/{id}/update` | Feature holders | The owner's status, redacted for non-administrators. |
| `POST /api/addons/{id}/update/check` | Administrator, browser session | Fetch the library again and return the status. Applies nothing. |
| `POST /api/addons/{id}/upgrade` | Administrator, browser session | Upgrade to the candidate that was checked. |
| `POST /api/addons/dispatch` | Administrator | The typed actions `update-status`, `update-check` and `update-upgrade` (arguments `expectedDigest` and `candidateDigest`) give the same answers; the plain `upgrade` action of earlier releases still works and is not pinned. |

No MCP tool maps to these routes. A read-only status tool may be exposed later.

### The upgrade

`POST /api/addons/{id}/upgrade` takes `operationId`, `expectedDigest` (the digest of the
installed package the caller saw), `candidateDigest` (the digest of the candidate the
check resolved) and optionally `generation`. The owner then:

1. refuses, before it records anything, an installed package that is not the expected one
   (`409 stale_installed`), a candidate that is not the one the library resolves now
   (`409 candidate_changed`), an incompatible candidate (`409 incompatible`), a rewritten
   immutable version (`409 integrity_error`), and the same transition under another
   operation ID that has not failed (`409 upgrade_conflict`). A library that cannot be
   read is `503 check_failed`;
2. stages and verifies the candidate, never changing the library, the channel or the
   trusted identity of the package;
3. for an add-on with data, backs it up, drains it and migrates it under the data recovery
   described above, and publishes the new generation only when it starts.

Repeating an `operationId` returns the recorded result and does not run again, from
any frontend. Two requests with the same pins and different operation IDs run once: the
second is `upgrade_conflict`, in flight or finished. Two upgrades to different candidates
from the same installed package are serialized by the installed digest, and the loser is
`stale_installed`. A candidate that fails to stage, fails its compatibility check at
execution, does not start or is canceled keeps the last good generation running, or leaves
the add-on in the explicit recovery state described under data recovery. The previous
installation is never deleted. `409 upgrade_failed` says that the attempt failed; a new
attempt needs a new operation ID. After `outcome_unknown`, read `operation-status` with
the same ID and do not submit a new upgrade.

### Automatic upgrade

By default add-ons upgrade only when an administrator starts the upgrade. The
setting **Upgrade add-ons automatically** (`auto_upgrade_addons` in the runtime
settings, flag `--auto-upgrade-addons` or `LOCALAI_AUTO_UPGRADE_ADDONS`) is off by
default, like the matching setting for backends. When you turn it on, the owner checks
the library of every installed add-on shortly after start and then every six hours,
the same schedule as the backend update check. When a library lists a newer version
for the channel the add-on was installed from, the owner runs the same pinned upgrade
that the **Upgrade** action runs. The digest and signature checks, the compatibility
checks, the data backup and the recovery behavior above all apply, and an upgrade that
fails any of them changes nothing and is written to the log. An automatic upgrade never
changes the library, the channel or the trusted signer. A candidate that failed is not
tried again until the library lists a different one. With the setting off, nothing
upgrades by itself.

### In the page

The Installed list and the management sheet show a badge with the candidate version and
the short digest, and the compatibility reasons of an incompatible candidate. An
administrator gets **Check for updates** with the time of the last check, and **Upgrade**
when the state is `update-available`. **Upgrade** opens a confirmation that names the
library, the channel, the installed and the new version and digest, and says that the data
of the add-on is backed up first. It then shows progress and, on failure, the error and the
kept version. Anyone else sees the status without actions.

The **Upgrade** action is on the card of the add-on in the list and on its entry in **Find
add-ons**, next to the update notice, and in its details. All of them open the same
confirmation.

When the notice is shown but the upgrade cannot start, the card says why instead of hiding the
action:

| What the card says | Cause | What to do |
|---|---|---|
| Only an administrator can upgrade it | Your account is not an administrator. | Ask an administrator. |
| The update is not confirmed yet | The check did not resolve a candidate to pin (no digest). | Select **Check again**. |
| The library names no build to install | A check ran and the owner still reports an update without a digest to pin. | Check again later. |
| The last check failed | The library could not be read. | Select **Check again**. |
| The update cannot run here, with the reasons | The state is `incompatible`. | None until the host or the platform fits. |
| The library lists this version with different contents | The state is `integrity-error`. It is never an update. | None. |
| The data needs recovery first | The add-on is in the recovery state. | Restore a backup in the details, then upgrade. |
| The latest check found no update | The list showed a cached notice that a newer evaluation no longer supports. | Select **Check again**. |

After **Check again**, the card shows the answer of the check: "Checked just now" (or the
time of the check) and what the check found, for example that the add-on is up to date. When
the check found an update with a digest, **Upgrade** replaces the reason. A check that fails
shows why, and you can run it again.

**Upgrade all (N)** shows in the bar of the list for administrators when at least one add-on
has an update. It opens a dialog that lists every add-on with an update: the name, the
installed and the new version, and, for one that cannot start, the reason above. Only add-ons
that can start can be selected; **Select all** and **Select none** change the selection.
**Upgrade selected** then runs the single-add-on upgrade for each selected add-on, one after
another, never in parallel. Each is its own operation with its own operation ID and its own
pinned digests, so the owner decides every one again. The dialog shows each add-on as queued,
upgrading, done, failed (with the reason the owner gave) or skipped. A failure does not stop
the others. You can close the dialog while it runs: the upgrades continue and the bar shows
the progress and then a summary. **Retry failed** runs the failed add-ons again as new
operations.

**Check for updates** in the bar of the list reads the library of every installed add-on
again; it applies nothing, and an add-on that cannot be checked does not stop the others.

An upgrade started from the list, the details, **Find add-ons** or **Upgrade all** keeps
running when you leave the page. It appears in the Activity page under the **Add-ons**
filter, with progress, **Cancel** and **Retry**. Cancel stops the request, and the page then
reads the operation to report what the owner did. The list of upgrades is kept by the
browser that started them, so it is lost on a page reload.

While the add-on service restarts (an upgrade restarts it), the page cannot read the
operation and the server answers 503. The page then waits longer before each new read, 2,
4, 8 and up to 30 seconds, and the upgrade says *Waiting for the add-on service to start*
with the time of the next check; **Check now** in the details reads it at once. The Tools
menu of a chat does the same for the state of the Sandbox: it says that the Sandbox is
starting and reads its state again by itself, with the same back-off.

## Add-on pages

An add-on with a `ui` object contributes pages to the web UI. The code of those pages is
the packaged ES module the manifest names, served by the owner, and the host loads it
after the host itself was built: nothing about an add-on is known to the UI build. The
host never resolves imports for it, never hands it a router, a store or a request
function, and never lets it replace a route.

### The UI entry of an app

An enabled app whose installed package has a sha256 digest carries a `ui` object in
`GET /api/addons/apps`. A disabled app, a headless app and an app without a digest do not.

| Field | Meaning |
|---|---|
| `entry` | The same-origin URL of the module: `/api/addons/{id}/assets/{digest}/{path}`. |
| `css` | The URLs of the declared style sheets, under the same digest. |
| `sdk` | `{min, max}`, the host SDK versions the module accepts. |
| `pages`, `navigation`, `chatActions` | The identifiers the manifest declares. The host decides where each is shown. |

`digest` is the package sha256 in lower-case hex. A new package is a new URL, which is
why the files are immutable.

### The asset route

`GET /api/addons/{id}/assets/{digest}/{path}` streams one declared file (the entry module
or a style sheet) from the verified package on the owner, relayed by the frontend that
received the request. It is a read for every caller who holds the `addons` feature.

- The file is served only while the add-on is installed and enabled and the digest is
  the current one. An older package, another add-on's digest, a disabled or uninstalled
  add-on, a file the manifest does not declare, a traversal, a directory and a missing
  feature all give the same denial without any file content, and a denial is never
  cached. (A path that ends in a slash is stripped by the app's router first, so it gives
  404 instead of 403.)
- The owner serves the bytes, never the add-on process, and a file that is not a regular
  file of the package is refused.
- The response is `text/javascript` or `text/css` with `X-Content-Type-Options: nosniff`
  and `Cache-Control: private, max-age=31536000, immutable`.
- The browser authenticates with its same-origin session cookie. An API key goes in the
  `Authorization` header. A credential in the URL is never accepted, and a cookie
  request without same-origin fetch metadata is refused.
- A replica relays the response from the owner: the same bytes, headers and denials.

There is no MCP tool for this route, as for the other Apps routes.

### The host SDK

The module exports `createAddon(sdk)` and nothing it needs comes from anywhere else.
`createAddon` returns `{ Component, chatActions, dispose }`; `Component` and
`chatActions` are optional and `dispose()` is required. The SDK is a frozen object:

| Member | What it is |
|---|---|
| `React` | The host's own React. Use `sdk.React.createElement` and `sdk.React.useState`: hooks run on the host's React instance. A bare `react` import fails to load, and a second bundled React would break hooks. |
| `ui` | Wrappers over host components: `Button`, `Spinner`, `EmptyState`, `Badge`, `Stack` and `Text`. A wrapper has a fixed type, so React never remounts it. |
| `Icon`, `iconNames` | A semantic icon. A name outside `iconNames` draws a neutral icon. |
| `theme` | `tokens` (CSS custom property references with stable names), `mode()` and `subscribe`. |
| `locale` | `language()`, `subscribe`, `define(language, bundle)` and `t(key, fallback, vars)`. Bundles belong to this add-on only. |
| `navigation` | `navigate(path)`, `href(path)`, `current()`. Only the add-on's own declared pages resolve; anything else throws. |
| `api` | `read(request)` and `propose(request)`, both bound to this add-on and its installed generation by the SDK. A read is always the typed `read`, with the `resourceId` of the resource the person named; a write is only ever a stored proposal that the host confirms. There is no raw request. Operation identifiers are `op-` and a UUID, which is a namespace name the owner accepts. |
| `resources` | The person's own resources of this add-on: `list`, `create`, `rename`, `remove`, `apply`, `operation`, `grants`, `setGrant`, `revokeGrant`, `searchUsers` and `selection.get/set/clear`. See [the resource calls of the SDK](#the-resource-calls-of-the-sdk). |
| `notify` | `info`, `success`, `warning`, `error`. |
| `markDirty(flag)`, `isDirty()` | Unsaved edits. The shell asks before it leaves or replaces the page. |
| `layers` | `openDialog(spec)` and `openPanel(spec)`: the host draws the dialog or side panel and owns its focus, Escape and unmount. |
| `signal`, `onDispose(fn)` | The signal aborts when the add-on is withdrawn; disposers run once. |

A chat action is `{id, label, icon, open(context)}`, where `id` must be one of
`ui.chatActions`. `context` is exactly `{conversationId, resourceRef?}`. There is no
history, message or send hook, and an add-on cannot intercept submission or override a
route. An action that is undeclared, duplicated, without a label or without `open` is
ignored; an error inside `open` never reaches the host.

Compile an add-on with classic JSX transformed to `React.createElement` over
`sdk.React`, inside the `createAddon` factory. Do not import a JSX runtime. The fixture
packages under `tests/addons/fixture/ui` are written in that form.

### The resource calls of the SDK

`sdk.resources` is the page of an add-on talking to the
[UI-only resource management](#ui-only-resource-management) of the host. It is a
fixed list of typed calls: the add-on never supplies an address or a header, so it
cannot reach another route or another add-on. The host shapes each call, adds the
own-session confirmation header that the route needs and sends the add-on's
installed generation, so a stale tab is refused by the server.

| Call | Route |
|---|---|
| `list()` | `GET /api/addons/resources?addonId=` |
| `create({arguments})` | `POST /api/addons/resources` |
| `rename(id, {name, revision})` | `POST .../{id}/rename` |
| `remove(id, {confirm, revision})` | `POST .../{id}/delete` |
| `apply(id, {tool, arguments, revision})` | `POST .../{id}/apply` |
| `operation(id, kind, operationId)` | `GET .../{id}/operations/{operationId}?kind=` |
| `grants(id)`, `setGrant(id, userId, role)`, `revokeGrant(id, userId)`, `searchUsers(id, query)` | the grant and picker routes |
| `selection.get/set/clear(conversationId, ...)` | `/api/addons/conversations/{conversationId}/selection/{addonId}` |

A call that changes something takes an operation ID. The SDK makes a new one for each
call; pass `{operationId}` in the options to repeat the same operation after an
`unknown_outcome` answer. A malformed identifier, role or revision is refused in the
SDK before any request. After the signal aborts every call throws and nothing is sent.
Errors are `AddonsError` with a `kind` (`stale`, `denied`, `invalid`,
`unknown_outcome`, `sharing_disabled`, `not_selected`, `unavailable`, `network`), the
HTTP `status`, the server's `type` and, for an unknown outcome, the original
`operationId`.

A chat request that has add-on tools enabled carries the conversation in the
`X-LocalAI-Conversation-Id` header (when it has the shape the server accepts), which is
how a tool call that names no resource finds the person's selection.

### Core routes from add-ons

An add-on page can call the core API of LocalAI, for example `/v1/face/verify`, with
`sdk.core.fetch(path, init)`. The call runs with the signed-in user's own session,
through the same authentication and authorization as any request the browser makes.
The add-on gets no extra credential and no elevated role: a route that needs the
administrator role stays closed to a user without it, and a route behind a feature
(for example `face_recognition`) stays closed to a user whose feature is off. Those
calls answer `403`, and `401` when the session is missing. The add-on cannot widen
what the user may do.

`sdk.core.fetch` is `fetch` with a fixed policy. It sends the cookies of the page
(`credentials: 'same-origin'`), refuses other origins (`mode: 'same-origin'`),
refuses redirects, and does not use the cache. It removes any `Authorization` and
`Cookie` header that the add-on sets, and it stops when the add-on is withdrawn.

The path must be a same-origin path that starts with one `/`. These are refused with
an `invalid_path` error before any request is made:

- an absolute URL, a path that starts with `//`, a backslash, or a control character;
- a `.` or `..` segment, also when it is written with percent-encoding;
- the add-on gateway (`/api/addons`) and the auth routes (`/api/auth`). The add-on
  gateway has its own typed calls, and the auth routes hold account credentials. The
  check uses the decoded path, so `/api/%61ddons/apps` is refused too;
- an encoded `/` or `\` in the first two segments, because it can hide a segment
  boundary. In a later segment it is a normal identifier (`/api/voice-profiles/a%2Fb`).

### How the host loads it

1. The SDK range must contain the host SDK version (1). Otherwise nothing is imported and
   the page says the add-on needs another interface version.
2. Every URL must be a same-origin asset of that add-on's own package, all under one
   digest. Under a path prefix the host puts its base path in front, as for every API
   call.
3. Style sheets are read as text and scoped: every selector becomes
   `:where([data-addon="{id}"]) …`, `:root`, `html` and `body` map to the add-on's root,
   `@import`, `@font-face` and every other at-rule except `@media`, `@supports`,
   `@container`, `@layer` and `@keyframes` are dropped, and keyframe names are
   renamed. The sheet reaches only the add-on's own root.
   The same rule is applied to every declaration, including those inside
   keyframes, nested rules and `@media`, `@supports`, `@container` and `@layer`
   blocks, and to at-rule conditions: a declaration is dropped when its text,
   after comments and CSS escapes are resolved, contains `url(`, `src(`,
   `image-set(` (also with a vendor prefix), `cross-fade(`, `element(`,
   `paint(`, `image(`, `expression(`, `behavior` or `-moz-binding`, or when it
   has a backslash escape outside a quoted string. `data:` images are dropped
   as well; an add-on that needs an image uses an asset of its own package
   from its code.
4. The module is imported by URL with a timeout and an abort, `createAddon` is called
   with the SDK, and the result is checked.
5. The page component is drawn inside an error boundary in the add-on's root. A throw
   draws an error card and nothing else.

### Revalidation, updates and unsaved edits

The shell reads the apps list while an add-on page, the Add-ons navigation or a chat
action needs it (every 5 seconds, 30 in a hidden tab). An add-on that is no longer
listed, was disabled, failed, was replaced by another generation or package, or has no
UI any more is withdrawn at once: its SDK signal aborts, its component unmounts, its
slots (navigation entries and chat actions) disappear, `dispose()` runs, its style
sheets are removed, and the page says why with a **Reload** action. The owner already
denies its code and calls, so an old copy that stays in memory cannot do anything.

A reader no longer sees a stopped add-on at all, so for a reader a disabled add-on reads
as "not installed, or you can no longer use it".

If the add-on reported unsaved edits with `markDirty`, it is not unmounted: the page
stays on screen, frozen and with its signal aborted, with a banner saying the edits
cannot be saved. **Reload** then asks before it discards them. Leaving the page asks
as well, and so does closing the tab.

### Navigation entries

A page named in `ui.navigation` is linked in the detail view of the add-on and under its
pinned entry while the add-on is enabled, not failed and compatible with the host SDK. The same registry keeps
the validated chat actions per add-on and generation and removes them with the add-on.
Declared chat actions are drawn by the host in the chat page (see
[Extension points and slots](#extension-points-and-slots)); a bare identifier has no
placement and is not drawn.

### Verification

`sh scripts/addons/verify.sh esm-ui` runs the node unit tests of the SDK, loader, style
scoping and registry, the Go specs of the asset route through real stored keys and
sessions, and a browser spec against the UI test server running with authentication and
a real owner. That spec installs the fixture packages after the build, from a catalog on
loopback, with the protocol fixture as their process.

## Extension points and slots

An add-on extends the existing LocalAI pages through declarations in its manifest. The
host validates every declaration at install and draws the contributions itself. Nothing
is registered while the add-on runs: the module supplies only the components and the
handlers its manifest names, and the host ignores the others (and logs them in a
development build).

### Declarations

All three lists are inside `ui` and are optional. A manifest without them is valid.

| Field | Entry | Rules |
|---|---|---|
| `ui.chatActions` | A bare identifier, or `{id, label, icon, placement, target}` | `id` is a namespace name. `label` is plain text (60 characters at most) or a language map; markup characters are rejected. `icon` is a host icon name from the SDK list or the packaged `ui.icon` path; a remote or absolute address is rejected. `placement` is `composer` or `message`. `target` has exactly one of `{tool}` (a tool in `capabilities.tools`) and `{component}` (an export of the module). |
| `ui.slots` | `{id, slot, component}` | `slot` is one of the host slot identifiers below. `id` is a namespace name that no chat action uses. |
| `ui.renderers` | `{kind, tool, component}` | `kind` is `approval-card` or `tool-result`. `tool` is a tool of the same add-on. One renderer for each kind and tool. |

A component name is a PascalCase identifier of at most 64 characters. Each list has at
most 16 entries. Identifiers are unique across chat actions and slots. Any unknown field
in an entry is rejected.

### Host slots

The slot list is closed. A new slot is a host change with its own review: it is never
added by a manifest. A slot passes frozen props and nothing else. No slot passes message
text, history, the model or any host state.

| Slot | Where the host draws it | Props |
|---|---|---|
| `chat.composer.actions` | In an overflow menu next to the attach buttons of the composer. Actions of placement `composer` are menu items. Components are drawn in the menu. | `conversationId`, `disabled` |
| `chat.message.actions` | In the action row of each message. Actions of placement `message` are icon buttons. | `conversationId`, `messageId`, `role` |
| `app.nav` | On the Add-ons page, below the tile grid. Under the legacy switch: at the end of the second navigation row of the Add-ons hub, after the links of `ui.navigation`. | none |
| `settings.sections` | On the add-on's own page: the **Settings** tab of its details on the Add-ons page (`/app/addons?detail=<id>&view=settings`), one section for each component. Only the add-on's own sections are drawn there, and only for an administrator. | none |
| `files.preview` | On the Files page, in the preview of the selected file, between its size and its download link. The first component that handles the file's extension is drawn; the host's own view stays. | `path`, `name`, `extension` |

A `settings.sections` component is drawn on the Settings tab of its add-on's details, not
on the Settings page. The Settings page has an **Add-ons** group, shown when at least one
add-on declares a section, that lists those add-ons, each with a link to its Settings tab.
An add-on that has a page of its own gets a **Settings** link from the host above that
page. The tab and the link are for administrators; a section is never drawn for anyone
else, so a section does not need to hide itself from other users. The old address of a
section, `/app/settings#ad-section-<add-on>-<contribution>`, opens the add-on's Settings
tab. Some settings of an add-on's feature are LocalAI's own runtime settings (the network
of the Sandbox commands, which add-on serves a built-in tool): they stay on the Settings
page, and the add-on's Settings tab shows them under **Server settings for this add-on**
with a link to each (for example `/app/settings?group=tools#settings-egress`). Draw the
section with [the settings kit](#the-settings-kit).

`messageId` is the position of the message in the conversation. `role` is `user`,
`assistant`, `system` or `tool`.

A `files.preview` contribution declares the extensions it handles, lower case and without a
dot (`"extensions": ["docx", "xlsx"]`, one to 16, no repeats). No other slot has the field.
`path` is the path of the file inside the user's files, `name` its name and `extension` its
extension. The component reads the file through a read tool of its own add-on, as the user;
the host never hands it file content.

### What the host guarantees

- A contribution is drawn only while the apps list the server returned says the add-on is
  enabled, not failed and compatible with the host SDK, and only for the generation and
  package the loaded module belongs to. A disable, a crash, an update or a revocation
  removes every contribution at the next list. A re-enabled add-on is loaded again on the
  next page load.
- The page reads the apps list once for the slots. It keeps reading it only while a
  usable add-on contributes.
- Each contribution is in its own error boundary. A component that throws leaves a small
  "Unavailable" marker, and the page keeps working.
- A contribution is drawn in a container of its own. It cannot replace or hide a host
  node. The style sheet of the add-on is scoped to its root.
- A tool action is one typed `read` call of the same add-on, for the tool the manifest
  named, with `{tool, context}` as arguments. The context has `conversationId` and, for a
  message action, `messageId` and `role`. The server authorizes the call as for every other
  call: hiding a button is never the control. The result is drawn by the declared
  `tool-result` component or, without one, as bounded text. A result that names a stored
  proposal (`{"proposalId": "..."}`) opens the approval frame.
- A component action opens the exported component in a host dialog, with the same
  context and a `close` function.
- The server, not the page, limits a tool action to the tools the add-on declared: a
  request for an undeclared tool is a 403 (see [Declared tools](#declared-tools)).

#### Add-on UI code is trusted code

The UI module of an add-on is native code that runs in the origin of the host page. It is
not sandboxed. The host cannot technically prevent it from reading the page, calling the
same routes as the person or driving host buttons, including the buttons of the approval
frame. The controls are the trust decision when the person installs the add-on, the
server-side authorization of every request (roles, grants, declared tools and the
revocation of credentials) and the approval frame that the host owns: a confirmation
needs the proposer's own browser session and the digest of the stored proposal. Install
only add-ons that you trust.

### Approval cards

A proposal of an add-on is shown in a frame the host owns. The frame loads the stored
proposal, shows who asks, how many changes it has, when it expires and its state, and draws
the `approval-card` component declared for the tool inside it. The component receives
plain data (`proposalId`, `tool`, `effects`, `arguments`). It gets no callback and no
digest. Confirm and Cancel are the frame's buttons. A confirmation sends the
`X-LocalAI-Confirm` header and the digest that was displayed, and the server accepts it only
from the proposer's own browser session, as in [Proposals and confirmation](#proposals-and-confirmation).
An add-on asks for the frame with `sdk.approvals.present(proposalId)`. The proposal must
belong to the same add-on.

### Private files in a page and in chat

A read tool may answer with file results. LocalAI replaces each output of the
add-on by a private artifact identifier of its own before anything reaches a
browser or a model. `sdk.files.read(request, options)` is `sdk.api.read` for
such a tool: it resolves to `{ files, payload }`, where each file is `{ id,
name, mediaType, size }` as the host recorded it. The add-on's handle, a path
and a URL are never part of it. The add-on draws the file with
`sdk.ui.ArtifactCard`, which takes only `artifactId`: the card loads name, size,
state and expiry from `GET /api/addons/artifacts/{id}` and offers the
authenticated download of the host route. A chat tool result of kind
`artifact` is drawn with the same card. The identifier is the only thing the
chat stores. An add-on cannot supply a link, a name or a path that the card
shows.

### Verification

`sh scripts/addons/verify.sh host-slots` runs the node unit tests of the slot registry,
the component validation of the loader and the approval rules; the Go specs of the
manifest declarations and the apps list; and a browser spec against the UI test server
with authentication and a real owner. The spec installs the `slots` fixture package after
the build. It contributes to every slot, with a contribution that throws and a style sheet
that collides with host class names.

## First-party package and release channels

The first-party add-ons (Tables, Voice, Face, Fine-tuning, Quantization, Studio and Talk) are each distributed as an OCI image of one platform for each
supported operating system and architecture, joined by an image index. The host
installs it as any other OCI package: it extracts layers into a private directory and
starts the executable as a managed process. No container runtime runs the add-on.
Docker or ORAS only construct and move the distribution.

### Package format

`sh scripts/addons/package.sh --layout <dir> --version <semver> --platform <os/arch>`
writes a local OCI layout with one image and a `receipt.json` of its digests. The tool
never pushes, tags, signs or logs in.

Add `--config addons/<id>/package/platforms.yaml` to package another add-on. The script
reads the add-on name, the executable and the UI directory from that file and, without
`--executable`, builds `./addons/<id>/cmd/<executable>` for the host platform. Tables is
built with `CGO_ENABLED=1`. The other add-ons are pure Go and are built with
`CGO_ENABLED=0`, so their executables need no C toolchain on any platform. The default
configuration is the one of Tables.

| Part | Value |
|---|---|
| Image manifest | OCI image manifest, `artifactType` `application/vnd.localai.addon.package.v1`. |
| Annotations | `io.localai.addon.id`, `io.localai.addon.version`, `io.localai.addon.platform`, `org.opencontainers.image.version`, and `org.opencontainers.image.revision` when the commit is known. |
| Config | OCI image config with the `os` and `architecture` of the platform. No history, no timestamp. |
| Layer `executable` | The entrypoint (mode 0755) and the runtime files the platform needs (mode 0644), as `application/vnd.oci.image.layer.v1.tar+gzip`. |
| Layer `ui` | Only when the manifest declares a UI: every file under the directory of `ui.entry`. The declared entry, style sheets and icon are required. The layer is absent otherwise. |
| Layer `manifest` | `manifest.json` with `version`, `os` and `arch` set to the release version and platform. |
| Image index | One entry for each enabled platform, `artifactType` `application/vnd.localai.addon.package.index.v1`, with the version and commit as annotations. |

Every part is deterministic: the same inputs give the same digest. A package is rejected,
before it can be published, when:

- the entrypoint, a declared UI asset or a required runtime library is missing;
- the manifest, the image config, the annotations or the receipt name another platform
  or version, or the artifact type differs;
- a layer has an unexpected media type, role, order or file, or a blob does not hash to
  its digest;
- a file has set-id, sticky or group/world-write bits, a non-entrypoint file is
  executable, the entrypoint is not, or a path is absolute, escapes the root, repeats or
  is not a regular file;
- the UI bundles its own React runtime (the host provides React through the UI SDK);
- the icon is not an image of its declared type.

`addonpkg extract` restores a verified layout to a directory with the validated
executable mode. The installer applies the same path rules and ignores the modes in the
archive other than the executable bit.

### Release platform matrix

Each first-party add-on has its own `addons/<id>/package/platforms.yaml`. It lists the
platforms of that add-on. A platform is shipped only
while `enabled: true`, and it needs a native runner and the `executable-start` native
check. Until a platform's native check passes on a runner of that platform, it stays
listed with `enabled: false`. The workflow fails closed on an empty matrix and on a
missing repository input (`ADDON_OCI_REPOSITORY`). There is no default registry.
The add-on repository is `quay.io/go-skynet/local-ai-addons`; every other
backend image repository is refused.

Every platform matrix lists four platforms: `linux/amd64` on `ubuntu-24.04`, `linux/arm64`
on `ubuntu-24.04-arm`, `darwin/arm64` on `macos-14` and `darwin/amd64` on `macos-15-intel`.
Voice, Face, fine-tuning, quantization, Studio and Talk enable all four. A test fails when a
platform is enabled without a recorded check.

A package that ships skills names their folder with `skills:` (for example
`skills: addons/office/skills`). The build lays it at the package root as `skills/`,
in a layer of its own, on every platform, and checks it with the rules the owner
applies at install: the folders must be the skills the manifest declares, and every
file must pass the checks of [Skills](#skills). A package that the owner would refuse
is never built.

Each package also has a catalog template in `gallery/addons/<id>.yaml.tmpl`, so it can be
listed in Find add-ons once a maintainer renders and publishes its entries. See
`gallery/addons/README.md` for the commands.

All add-ons publish to one OCI repository, the value of `ADDON_OCI_REPOSITORY`. Each
add-on is a set of tags of that repository, and every tag starts with the add-on id:
`<id>-sha-<short sha>` for a commit, `<id>-<version>` for a release, and
`<id>-development` or `<id>-latest` for a channel alias. For example, the Fine-tuning
build of one commit is
`oci://quay.io/go-skynet/local-ai-addons:fine-tuning-sha-99d1dca2c869`.

A release entry of the official library is pinned by digest. A development entry is
mutable, as a development backend is: it names the moving tag
(`oci://quay.io/go-skynet/local-ai-addons:fine-tuning-development`), sets `mutable: true`
and has no digest. At install LocalAI resolves the tag to a digest, verifies the signature
against that digest, installs by it and records it in the receipt. A mutable entry must be
an OCI artifact. The update check compares a mutable entry by the digest its tag names (see
[How the candidate is chosen](#how-the-candidate-is-chosen)).

Set `ADDON_OCI_REPOSITORY` to `quay.io/go-skynet/local-ai-addons`. The value has no tag
and no digest. A value that is a backend image repository makes the add-on tools refuse
it. `addonpkg target --repository <repository> --addon <id> --tag <tag>` prints the
reference one add-on publishes to, without a network request.

The index entries pin each platform image by digest, so the tag only names the
artifact. The host accepts a tag reference (`oci://<repository>:<tag>`) and reads the
repository with any number of path elements, so an index that still names the old
repository-per-add-on form (`oci://<repository>/<id>:<tag>`) keeps working.

Publishing events (a push to `master`, a version tag and a published release) fail closed
for an add-on until at least one of its platforms is enabled in its `platforms.yaml`.
Pull requests and other pushes publish nothing, so an add-on without an enabled platform
only gets no package legs there. A missing or malformed `platforms.yaml` fails the plan on
every event.

### Channels and tags

`scripts/addons/channel.sh --event <name> --ref <full ref> --sha <commit>` is the one
place that decides what an event may publish. Its output is what the workflow uses.

| Event | Immutable tag | Moving tag |
|---|---|---|
| Pull request | none | none: nothing is pushed, no secret or OIDC token is available |
| Push to `master` | `sha-<12 hex of the commit>` | `development` |
| Push to another branch | none | none: a branch push never publishes, whatever its name |
| Push of a `v<semver>` tag | `<semver>` | none: a tag alone never advances `latest` |
| Published, stable, non-draft release | `<semver>` | `latest`, after complete publication |
| Published pre-release | `<semver>` | none |

The names in this table are the part after the add-on id. The registry tag is `<id>-<name>`,
for example `tables-sha-4edf30cda051` or `voice-development`.

Names are in disjoint namespaces: `latest` and `development` are reserved, `<semver>`
and `sha-<12 hex>` are immutable, and every branch channel starts with `branch-`. The
branch channel is `branch-` + a label + `-` + the first 12 hex characters of the SHA-256
of the full original ref (`refs/heads/...`). The label is the branch name in lower case,
every run of characters outside `a-z0-9` replaced by one dash, cut to 40 characters
(`x` when nothing is left). The hash makes `Feature/X`, `feature-x` and `feature/x`
three channels. Only the exact ref `refs/heads/master` is `development`. Nothing built
from another branch is published, and no new branch channel is created. Channels that
an earlier run published stay where they are.

Immutable tags are never overwritten. A second publication of a version with other
content is refused. The one exception is a rebuild of the same version and commit after
an earlier run published it: the published content is kept (`--adopt-existing`), and the
workflow verifies its signature before it promotes anything.

### Publication and signing

The workflow `.github/workflows/addons-package.yml` has these jobs.

| Job | What it does | Credentials |
|---|---|---|
| `validate` | One leg for each add-on. Builds the UI of the add-on, and runs the packaging checks (which cover every add-on) on every event, including pushes and releases. A red `validate` stops `package`, `publish` and `promote`. | none |
| `plan` | Runs `channel.sh` and, for each add-on, checks its platform matrix. It joins the matrices into one list of legs and names the add-ons that have legs. It checks the registry input. | none |
| `package` | Needs `plan` and `validate`. One native runner for each enabled platform of each add-on: builds the executable and UI, runs `package.sh` and the native runtime check, uploads the layout. | none |
| `publish` | One leg for each add-on. Needs `plan`, `validate` and every package leg. Joins the legs of the add-on, pushes platform digests and the index by digest to the repository of the add-on, tags the immutable version, signs by digest (keyless, OCI 1.1 referrers, new bundle format) and verifies with the exact identity of this workflow. | registry login and `id-token: write`, in a protected environment |
| `promote` | One leg for each add-on. Needs `publish`, and through it `validate`. Moves aliases, serialized by add-on and channel. | registry login, in a protected environment |

An add-on with no enabled platform gets no legs. On a pull request or a branch push this
fails nothing. On an event that publishes, the plan fails for that add-on and nothing is
published, so a release never leaves one add-on behind without notice.

A pull request, including one from a fork, never reaches `publish` or `promote`, and
`pull_request_target` is not used. A missing or failed leg, or a failed `validate`,
leaves every alias unchanged: nothing is published from a partial set of legs, `latest`
moves only after complete validation, and aliases move only after signing. The workflow
policy (`addonpkg policy`) requires this chain in the job graph. It rejects a `publish`
that does not need `validate`, and a `validate` job with `continue-on-error` or an
`always()`, `failure()` or `cancelled()` condition.

Promotion is serialized by a concurrency group for each channel and is checked against
the registry: `development` and branch channels do not move to a commit that is an
ancestor of the one they name, and `latest` does not move to a lower version or to a
pre-release. A slow older run therefore ends as a no-op.

A force-pushed branch is the new truth of that branch. When the commit a branch
channel names is no longer in the checkout (git answers "not a valid commit name"), the
promotion succeeds only if the run is a `push` to the exact ref that owns the channel
(`--ref` and `--event` of `addonpkg promote`; the channel name is derived from the ref).
For any other event or ref the alias stays where it is and the command fails with
`E_UNREACHABLE`. `development` follows the strict rule: an unreachable commit is always
refused with `E_UNREACHABLE`, and it needs a manual decision. Any other git failure
fails the promotion. A slow run of a pre-rewrite commit that finds the rewritten tip in
its checkout cannot be told apart from a new diverged commit, so a promotion of the
old commit after a force-push can briefly move the channel back until the next push.
The registry has no atomic
compare-and-swap for tags, so the group, not the check, is what closes the window
between two runs.

The signer identity is the workflow file of this package pipeline, not the backend
image workflows: `https://github.com/<owner>/<repo>/.github/workflows/addons-package.yml@<ref>`
with the issuer `https://token.actions.githubusercontent.com`. The identity contains the
ref of the run, which differs for each release tag.

### Catalog entries

`gallery/addons/tables.yaml.tmpl` and `addonpkg catalog` render one entry for each
platform from a verified merged layout. An entry pins the digest of the platform image,
names the repository and the immutable tag, carries the channel it is current for and
holds the expected signature policy. The catalog resolves a selection by version or by
channel, so a channel is metadata that names the entry and its digest, never a tag that
is read at install time. See [gallery/addons/README.md](https://github.com/mudler/LocalAI/blob/master/gallery/addons/README.md).

An entry may also carry a `requires` object, the compatibility hint of the update check
(see [Update detection and upgrade](#update-detection-and-upgrade)).

The host applies the signature policy of the library (source), not the one of an entry:
a refresh replaces an entry's policy with the policy the administrator configured on the
source. Executable verification (the signature of the package digest against the
workflow identity) and the trust of the catalog document itself (how the library is
fetched) are two separate things, and the host tests them separately.

#### Workspace operations

An add-on that holds `sandbox=workspace.`, `office=workspace.` or `web=workspace.` in the host operation setting can ask for the files of the user it is working for:

- `workspace.materialize` builds a read-only tree of some folders of the user in a directory of the add-on's own data directory.
- `workspace.commit` takes the `upper.tar` layer the add-on left in such a directory and commits it as one change set. It needs an `act` request.
- `workspace.changes` lists the change sets after a revision.
- `workspace.admin` answers only when the user of the request is an administrator, and is refused for anyone else. An add-on uses it to guard a setting; it never decides who is an administrator itself.
- `workspace.export` copies one file of the user into the data directory, and `workspace.import` (an `act` request) writes one file of the data directory into the files of the user, with the version check of the files API.

Every call runs as the user of the live request, never as a user named in the arguments. Folders are named relative to the home of that user, and `dir` is a relative folder inside the add-on's data directory; an absolute path, a parent reference or a symbolic link is refused.

#### Network operations

An add-on that holds `sandbox=egress.` in the host operation setting (the default) can ask about the network connections of the commands it runs (see [Network access](#network-access)):

- `egress.check` (`act`, feature `chats`) takes `{host, port, kind, tls}`, with `kind` `connect` or `http`, and answers `{action, reason, allowPrivate}`, with `action` `allow` or `block`. When a card asks the person (see the cards under [Network access](#network-access)), the call waits for the answer, at most 60 seconds, before it answers. The chat is the resource of the live envelope and the user is its actor: arguments named `chat`, `user`, `preset` or `allow`, or any other unknown name, are refused. The host writes the audit line of the verdict itself. The operation may wait up to 60 seconds for a card and has a 75-second timeout.
- `egress.audit` (`act`, feature `chats`) takes `{events: [...]}`, at most 500 lines of `{host, port, kind, verdict, reason, addr, bytesUp, bytesDown, durationMs, at}`. `verdict` is `done` (an allowed connection ended; the bytes it moved) or `block` (the add-on refused it itself: an address that is private, the server's own or a metadata address, a TLS name that is not the host, a limit, or a connection while no call ran).

## Office

The Office add-on reads and edits documents in the user's files, from a chat. It is a package like the others (`office` in the add-on library) and uses the `workspace.*` host operations to take a file in and give the result back.

| Tool | Kind | What it does |
|---|---|---|
| `read-docx`, `read-xlsx`, `read-pptx`, `read-pdf` | read | Return the text of a file: paragraphs with their index, cells row by row (a formula that has no stored result shows as the formula, such as `=SUM(A1:A2)`), slides with their shapes, pages. The answer is cut to about 48 KiB and says so. |
| `edit-docx`, `edit-xlsx`, `edit-pptx` | action | Apply a list of changes (at most 200) to a file. All of them are saved, or none. |
| `create-docx`, `create-xlsx`, `create-pptx`, `create-pdf` | action | Make a new file. `create-pdf` takes headings, paragraphs, bullets and tables, and writes Latin-1 text with the built-in font; text in other scripts is refused with a reason. It needs no sandbox. |

The action tools run at once, as the user of the chat, with no confirmation, and only in a chat. Without accounts, the files are the same ones the chat and the Files page use. With accounts and `local-owner` mode, the `workspace.*` operations are refused, because the local owner is no account and has no files; use `named-user` mode for the Office and Sandbox add-ons. A file that someone else changed after the tool read it is never overwritten: the tool's version is saved as a second file next to it, and the answer says so. A file that is not of the type its name says, or a path outside the user's files, is refused with a reason the model can read.

The add-on also ships four skills, `xlsx`, `docx`, `pptx` and `pdf` (see [Skills](#skills)). Each one replaces the built-in skill of the same name while the add-on can serve the chat. They are short procedures written for the tools above: read first, change only what must change, create a file only when none exists, and say what the tools cannot do (charts, tracked changes, form fields and the like) instead of describing a manual method. They need no command runner, so they are never "unavailable here". When the add-on is stopped, off for the chat or removed, the built-in skills come back. A test checks that every tool a skill names is a tool of the manifest.

## Web

The Web add-on serves the chat tools `web_search` and `web_fetch` in place of the built-in
ones. Its tools `search` and `fetch` declare `"shadows": "web_search"` and
`"shadows": "web_fetch"`, so a chat with the add-on on and running sees one `web_search` and
one `web_fetch`, served by the add-on. It adds two things the built-in tools do not have:
search providers that need a key (Brave Search, Tavily, Google Programmable Search), tried in
an order the administrator chooses, and the text of PDF files. Both tools run the code of the
built-in tools for the rest: the same SSRF guard, limits and untrusted-content label. See
[Web search and web pages]({{%relref "features/web#the-web-add-on" %}}) for what it does in a
chat and how to set it up.

| Tool | Kind | What it does |
|---|---|---|
| `search` | read | Replaces `web_search`. Arguments `query`, `maxResults`, `freshness`, `site`. |
| `fetch` | read | Replaces `web_fetch`. Arguments `url`, `maxChars`, `format`. Reads PDF too. |
| `status`, `set-settings`, `test-search` | read, page only (`uiOnly`) | The settings of the add-on, on the Settings tab of its details on the Add-ons page: the providers, their order and keys, and a test of each provider. A change and a test need an administrator, which the add-on asks the host with `workspace.admin`. |

The contract allows no underscore in an argument name, so the add-on's tools name
`max_results` and `max_chars` of the built-in tools `maxResults` and `maxChars`. The model
reads the add-on's schema.

The keys are kept in `settings.json` in the add-on's data folder (mode 0600). They are never
returned by a tool, written to a log or put in a model's prompt or a tool result: the
Settings tab shows only whether a key is set. A key reaches its provider in a request header
(`X-Subscription-Token`, `Authorization` or `X-Goog-Api-Key`), never in a URL, and the clients
of the keyed providers follow no redirect, so a key is never sent to another host.

The add-on runs on the add-on owner, like every add-on. Its settings, its search cache and
its rate limits live in that one process, so they are the same for every frontend of a
cluster.

## Sandbox

The Sandbox add-on lets a chat run commands and code in isolation. It has one tool for the model, the action tool `bash`, which replaces the chat's built-in `bash` (`"shadows": "bash"`), so a chat with the Sandbox sees one `bash` and it runs isolated. The model reads and changes files with the chat's own file tools (`read_file`, `write_file`, `edit_file`, `list_files`, `search_files` and the others; see [Chats]({{%relref "features/chats#running-commands" %}})): they work on the same files, so a script written with `write_file` runs with `bash` at once, and what `bash` writes is in the user's files after the call. `bash` runs inside an isolated sandbox of the user and the chat, with the user's files mounted under `/workspace/home`. The skill folders are not mounted in the sandbox: to run a skill's script there, the model copies it into the home with the file tools first. The status tool keeps its add-on name (`addon__sandbox__status`). Without the Sandbox, or with it off or unable to run, a chat may still run commands on the server itself, without isolation, when an administrator allows it; see [Commands on this machine]({{%relref "features/chats#commands-on-this-machine" %}}).

An older package of the add-on also served `read`, `write`, `edit`, `glob` and `grep`. They are gone: reinstall the package to drop them. A chat's switch for the add-on is kept by add-on ID, so nothing a chat stored changes.

- **One sandbox per user and chat.** The key is the pair of the user and the conversation. The user comes from the host. Nobody else reaches the sandbox, and the model cannot name another user or chat.
- **Changes are one change set.** After every call the add-on commits what the code changed, as one change set in the history of the user's files, so it can be undone there. If someone else changed the same file meanwhile, the sandbox's version is kept as a copy and the answer says so. A file change by someone else makes the sandbox rebuild its view of the files before the next call.
- **Limits.** At most 4 sandboxes for a user (the oldest idle one gives way to a new one) and 16 in all. When the 16 are used, the oldest suspended sandbox of any user gives way to a new one, so sleeping sandboxes never keep a user out; a call is refused with a busy answer only when every sandbox is running. A sandbox that is idle for 10 minutes is suspended and wakes on the next call. A sandbox is removed 24 hours after it was made. An add-on restart removes every sandbox it left, suspended ones too. When the add-on stops (it is disabled, or the server shuts down) it first stops all its sandboxes, for at most 8 seconds. The supervisor waits for that before it kills the add-on process; a server that is killed with SIGKILL cannot do this, and the next start removes what was left. When a suspended sandbox is removed, the host backend also stops the processes it left running.
- **Output.** A tool answer is cut at 64 KiB, and a `bash` call stops after its timeout (120 seconds by default, 28 minutes at most, so the answer is back before the 30-minute limit of an action call).
- **Without a backend.** When the host has no way to run sandboxes, nothing runs. A chat is not offered the Sandbox tools then: it gets one stand-in tool that tells the model why, so the model answers that it cannot run code instead of trying. Without the add-on, or with it switched off for a chat, the chat's model is told that it cannot run commands, and the chat's Tools menu says so to the user; see [Running commands]({{%relref "features/chats#running-commands" %}}). A tool that is called anyway answers "This host cannot run commands." The same happens when the Files workspace is off, because a sandbox needs the user's files.
- **Where you see the state.** The Sandbox page tells every user which backend runs the code, or that no backend works, or, while the host backend is in use, that commands run directly on this machine with no isolation. An administrator also sees, on that page and in Settings, the reason each backend does not work and what to do. An administrator can choose the host backend there: the page shows the warning that commands then run on this machine without isolation, and asks for the typed confirmation before it saves. A user who is not an administrator is told that only an administrator can change this.
- **Undo.** Each `bash` call that changes files is one change set in the history of the user's files (Files, History), and it is undone there, one call at a time. A whole turn is not one change set: the sandbox runs in the add-on process and the Files service commits each write of a tool on its own. The file tools of the chat itself work the same way, with an Undo note under the call.
- **Shared code.** The Host backend, the manager that keeps one sandbox per user and chat, and `bash` live in `core/services/studio/sandbox` (`host`, `runner`). The add-on runs them over its host operations; the chats' built-in `bash` runs the same code over the workspace store directly.

### Sandbox backends and settings

The add-on runs commands in one of these backends, chosen by an administrator on the Settings tab of the Sandbox details on the Add-ons page (the Sandbox page links to it):

| Choice | What it is |
|---|---|
| Automatic | KVM (Cloud Hypervisor) when the host has it, otherwise gVisor, otherwise nothing. It never picks the host backend, not even as a last resort. |
| gVisor | `runsc` with user namespaces. Needs `runsc` (the package carries it), `taskset`, and on Ubuntu 24.04 and later an AppArmor profile (the package carries `gvisor-profile`, which prints it). |
| KVM | Cloud Hypervisor with `virtiofsd`, `mke2fs`, `debugfs` and `/dev/kvm`. The package carries Cloud Hypervisor and, on amd64, `virtiofsd`. `bwrap` is used when present and not in a container. |
| Off | No sandbox. Every tool answers that the host cannot run commands. |
| Host | Runs commands on the host as the user LocalAI runs as, with no isolation. |

The Settings tab lists the backends of the host, each with its state (in use, ready or unavailable); the reason a backend does not work is under **Details**. When gVisor fails with `exit status 128` and `cannot read client sync file`, the row also says that gVisor needs an AppArmor profile that allows user namespaces and a container that may create them, and links to [The Sandbox in the LocalAI container](#the-sandbox-in-the-localai-container). The tab also shows, read-only, the network of the Sandbox commands (its limit, the preset of a new chat, the ports and how long the audit keeps a line) with a link to the server setting. An explicit choice is never replaced by another when it does not work: the tools say they cannot run.

The **Host** backend is for a machine that can run neither of the others. Choosing it takes two steps. The page shows a plain warning and asks the administrator to type the sentence *I understand that commands run on this host without isolation*. The add-on then saves the choice. The confirmation is forgotten when another backend is chosen. The backend refuses to start when LocalAI runs as root. Every command is logged with the name of the chat user, in the LocalAI log with the prefix `[sandbox]` (LocalAI writes everything an add-on prints to its output or error into its own log, one line at a time, cut at 4096 bytes), and the Settings tab of the Sandbox details (for administrators) shows a notice for as long as the host backend is the one in use. The model sees it in the answer of the status tool. Treat the machine as one that code can harm.

The choice is kept in the add-on's data directory. Changing it removes the running sandboxes; what they did is already in the users' files.

The guest image, `runsc` and the KVM programs are not part of the add-on's source. A release sets `main.guestImage` (the image, by digest) at build time. The checksums of the `runsc` and of the Cloud Hypervisor and `virtiofsd` it was tested with are compiled into the add-on (`runscPins` and `kvmPins` in `addons/sandbox/cmd/sandbox`), and tests keep them equal to `package/runsc.json` and `package/kvm.json`. A build without a guest image reports that on the page, and only the host backend can run.

### Network access

The guest of a sandbox has no network device: no NIC, no TAP device and no user-mode network stack. A command reaches the network only through one proxy, on guest port 3128, which the guest image sets in `HTTP_PROXY`, `HTTPS_PROXY`, pip, apt, git, npm and `GOPROXY`. The guest agent forwards each connection to that port to the add-on, over vsock (KVM) or a Unix socket (gVisor), and the add-on's proxy asks LocalAI about each connection with the host operation `egress.check`. LocalAI decides; the add-on dials. The proxy never decrypts TLS and holds no certificate the guest trusts.

The guest image has `curl`, `git`, `pip` and the CA certificates, and all of them use the proxy. The model is told so at each turn, with the chat's preset. To read or search web pages it is told to prefer `web_fetch` and `web_search` when the chat has them (the built-in ones or the Web add-on's), and to use `curl` for APIs, downloads and installs. With the network off, the note says that nothing is reachable.

Commands that run on this machine (the host backend, or the chats' built-in `bash`) have the network of the server: none of this applies to them, and the settings say so.

**Presets.** A chat has one of three presets, and an administrator sets the ceiling, the widest preset any chat may use:

| Preset | What a command reaches |
|---|---|
| Off (default) | Nothing. |
| Package registries only | The HTTPS hosts of the package registries below, on port 443. |
| Allow all | Every public host on an allowed port. |

The preset of a chat is the lower of its own choice and the ceiling (`chats_egress_ceiling`, default `off`). A new chat starts with `chats_egress_default`, capped by the ceiling. The preset lives in the chat's record on the server, never in a file of the workspace: the sandbox can write the home, so a file there could be changed by the code it is meant to confine. A preset never reaches a private, loopback or link-local address, nor the server's own addresses, nor a cloud metadata address. With `chats_egress_allow_private` on, a host that an administrator allowed by a rule or a card answer may reach private addresses; the metadata addresses stay refused. See [Runtime settings]({{%relref "features/runtime-settings#tools" %}}) for the six `chats_egress_*` settings.

**Package registries.** The list ships with LocalAI (`core/services/studio/egress/registries.json`) and changes only by a reviewed pull request that also changes this table and is named in the release notes. Every entry is an exact host name, HTTPS on port 443, with no pattern. Upload hosts (such as `upload.pypi.org`) are not on it, and neither is `raw.githubusercontent.com`, which serves any user's content. An administrator adds exact hosts with `chats_egress_extra_registries`, for example an internal mirror (which is private and so also needs the private switch and an administrator's rule). Extra registries are registries: they follow the Package registries rules (HTTPS only, and only with that preset or a card). To allow one more host for any request, use the list below.

**Always allowed hosts.** An administrator can list exact hosts in `chats_egress_allowed_hosts` (at most 64; `host` or `host:port` to pin the port; no pattern). A host on the list is allowed, with no card, in every chat whose effective preset is Package registries only or Allow all, on any request kind (plain HTTP as well as HTTPS) and on the allowed ports (`chats_egress_ports`), or only on the pinned port. With the preset off in a chat nothing on the list is allowed there, and with the ceiling off the list does nothing at all. The list does not widen the guard: a private, loopback, link-local, metadata or server address is not allowed by it, only by the private switch together with an administrator's rule or card answer, and the TLS server name must still match. An administrator's entry also wins over a deny the person stored in one chat before the host was listed. The audit line of a request this way has the reason `allowed_host`. The chat's Network panel shows the list to the person, read only.

| Registry | Hosts |
|---|---|
| Python (PyPI) | `pypi.org`, `files.pythonhosted.org` |
| JavaScript (npm) | `registry.npmjs.org`, `registry.yarnpkg.com` |
| Go modules | `proxy.golang.org`, `sum.golang.org` |
| Rust (crates.io) | `index.crates.io`, `static.crates.io`, `crates.io` |
| Ruby (RubyGems) | `rubygems.org`, `index.rubygems.org` |
| Java (Maven Central) | `repo.maven.apache.org`, `repo1.maven.org` |
| GitHub releases and archives | `github.com`, `codeload.github.com`, `objects.githubusercontent.com`, `release-assets.githubusercontent.com` |

**What the proxy checks.** The host of a request goes through the same guard as `web_fetch` ([Addresses inside your network]({{%relref "features/web#addresses-inside-your-network" %}})): a canonical name or literal, no address zone, no numeric tricks, no `localhost`. A host the guard refuses is never resolved, so it does not leak through DNS. A name is resolved once; when any answer is not allowed the connection is refused, and the proxy dials the address it checked. The addresses of the node's own interfaces and the whole loopback range are refused even when public. Only ports 80 and 443 by default (`chats_egress_ports`; 22 and 25 are always refused). HTTPS goes as `CONNECT host:443`: after the tunnel opens, the first TLS record must be a ClientHello whose server name is the CONNECT host, or the tunnel is closed (this stops a tunnel to one tenant of a CDN that talks to another). Plain HTTP goes as an absolute `http://` URL: each request of a kept-alive connection is decided on its own, the `Proxy-*` and hop-by-hop headers are dropped, a `Host` header that names another host is refused, and every request gets a new upstream connection. `CONNECT` to port 80, the origin form, `https://` in absolute form and `Upgrade` are refused with 400. The proxy follows no redirect: the guest's client follows it with a new request, which is checked again.

**Cards.** When a command asks for a host that the chat's preset does not admit but the ceiling would, and a person watches the chat, LocalAI stores a card in the chat and the person sees it in the chat: *A command in this chat wants to reach* `host:port` (an international host name is checked and shown in its ASCII form, so a look-alike name such as `pаypal.com` with a Cyrillic `а` reads `xn--pypal-4ve.com`; a name with no valid ASCII form is refused), with **Allow once** (the rest of that command), **Allow for this chat** (a rule) and **Deny** (a deny rule, so the host does not ask again; it can be removed in the network panel). The connection waits for the answer for up to 60 seconds, and never past the call's own timeout, so `pip` goes on by itself after the click; without an answer it gets the 403 line with *the user did not answer*. A late answer still counts: *Allow for this chat* stores the rule, and *Allow once* lets the next call of the chat reach the host within ten minutes. A card nobody answered expires when the chat's turn ends; it stays visible as *Not answered* and can still be answered, with the same buttons, under the call that asked and in the network panel of the **Tools** menu. Ceiling `off` never shows a card; ceiling `registries` shows one only for a registry host (so a chat on Off can approve `pypi.org`); ceiling `all` for any public host. A host inside your network shows a card only with `chats_egress_allow_private` on, and only an administrator can allow it. A chat shows at most three cards at a time and one per host and port; a second connection to the same host waits on the same card. Unattended runs (triggers, connectors) and sub-agents get no card: the block is in their output. The card is written to the chat record before the answer is sent on the chat bus, and the waiting check reads it again every two seconds, so an answer given through any frontend reaches it within two seconds even when the bus message is lost. When the ceiling goes down while a card waits, the card is refused, and every answer to it gets `400 egress_above_ceiling` (the same answer twice to an answered card is `204`; another answer is `409 already_answered`). A card the ceiling cannot admit shows no buttons: it says that network access is off (or limited) on this server, names who can change it, and gives an administrator a link to **Settings > Tools > Sandbox network**. The Network row of the chat's Tools menu says the same (*Off: the administrator has not allowed network access.*) when the ceiling is `off` or below `all`.

**What the model reads.** The Sandbox `bash` says that the network is HTTP and HTTPS through the proxy only. When the Sandbox serves `bash`, each turn tells the model the preset in one line, such as *Network for commands: package registries only (Python (PyPI), ...). A blocked host asks the user; do not try to get around the proxy.* When no card can appear (a ceiling no wider than the preset, an unattended run, a sub-agent), the line says instead that a blocked host is refused and nothing asks the user, so the model names the host it needs in its answer. A blocked request gets `403` with one line of text in the tool output, for example *Blocked by LocalAI: example.com:443 is not allowed for this chat (off). Ask the user to allow it.* With ceiling `off`, or a chat preset above the ceiling, the note of the turn says that the server's setting keeps the network narrower, that no approval changes it, and that the model should tell the user instead of waiting or retrying. A request blocked for that reason gets the line *blocked by the server's setting for the Sandbox network, not by a missing approval*; a block that waits on a person keeps the *the user did not answer* line.

**Only while a command runs.** The proxy knows the call in progress of each sandbox. A connection while no call runs (a background process left by an earlier command) is blocked with the reason `idle`, nothing is asked, and it is audited at the next call. When a call ends, every tunnel it opened is closed. An allow verdict is cached for the call in progress for at most 30 seconds; a block is never cached.

**Limits.** These are fixed in this release:

| Limit | Value |
|---|---|
| Concurrent connections of one sandbox | 32 (the guest agent caps all its ports at 64) |
| New connections of one sandbox | 20 per second, burst 40 |
| Concurrent connections of the add-on process | 256 |
| Dial timeout | 10 s |
| Idle tunnel (no bytes either way) | 120 s |
| Bytes from the guest, per call | 64 MiB |
| Bytes to the guest, per call | 4 GiB |

**Audit.** Every verdict is a line of the audit (who, which chat, host, port, kind, verdict, reason), and every allowed connection adds a `done` line with the address it reached, the bytes each way and how long it lasted. The reason names the rule that decided: `preset`, `allowed_host` (the administrator's always-allowed list), `rule`, `once`, `denied`, `ceiling`, `port`, `private` and the others the proxy adds, such as `idle` and `sni`. Lines older than `chats_egress_audit_days` (default 30) are removed daily.

**What fails.** Tools that ignore the proxy variables, SSH remotes (`git@...`), raw TCP and UDP, ICMP and DNS from the guest fail, as before.

**Residual risks.** The presets bound what a command reaches; they do not make every reachable host safe:

- A registry that takes uploads (`npm publish` with a token the command holds) can carry data out under "Package registries only", and so can any host under "Allow all". The preset is the person's choice of risk; the per-call upload cap bounds the volume, not the risk.
- Inside a TLS tunnel the `Host` header is encrypted and cannot be checked; a CDN that routes by it can still be reached through an allowed name.
- A command that sees the proxy can try many hosts; each one is checked and audited.

**Distributed mode.** Each piece of state has one mode:

| State | Mode |
|---|---|
| Ceiling, private switch, ports, extra registries, default | shared: runtime settings, like the other `chats_*` settings |
| A chat's preset, rules and cards | shared and durable: the chat record in the chat store (the database in a cluster) |
| A card that waits | stateless per request: the host operation on the owner waits; it wakes on the answer's bus message or on a re-read of the card every two seconds |
| The decision of one connection | stateless per request: the host operation reads the settings and the chat record at each check |
| The map from a call's resource to its chat | per instance: a cache of a fixed mapping (a hash of the chat ID); each frontend fills its own, and a miss only costs a search of the user's chats |
| Audit | shared: the `studio_egress_events` table in a cluster, a JSON file under the data path, or memory without one |
| Audit pruning | single-runner: one frontend at each daily tick, under an advisory lock |
| Verdict cache, rate limits, connection counts | per instance: the add-on process on the owner; an allow lives at most 30 seconds and only within one call, so a settings change applies within 30 seconds |

With `--addons-role frontend` a frontend runs no proxy; the owner does, and the host operations run on the owner's host.

### Packaging and setting up the Sandbox add-on

The Sandbox package is for Linux on amd64 and arm64. The macOS platforms are listed in its platform file and stay disabled.

- **`runsc`.** `addons/sandbox/package/runsc.json` pins one gVisor release (20261012.0) with the URL, the checksum the release publishes for each tarball, and the checksum of the `runsc` inside it. `addons/sandbox/package/fetch-runtime.sh <os/arch> <dir>` downloads it, checks both checksums, and the release workflow puts `runsc` and `gvisor-bin/` in the package. At run time the add-on checks `runsc` against the pinned checksum, copies it with its helpers to `runsc/` in its data directory as executable files, and uses that copy. A `runsc` that does not match is never used. Without one in the package, a `runsc` in a system folder (`/usr/local/sbin`, `/usr/local/bin`, `/usr/sbin`, `/usr/bin`, `/sbin`, `/bin`) is used as it is. The add-on does not read `PATH`: LocalAI starts it with none, so it looks for `runsc`, `taskset` and the KVM tools in those folders only.
- **AppArmor.** On Ubuntu 24.04 and later, a program needs an AppArmor profile to make user namespaces. The package carries `gvisor-profile`, a small program that prints the profile of one `runsc`. The add-on copies it next to `runsc`, and when gVisor fails for this reason the page tells an administrator the exact step, with the real paths. Run it once, as root, on the machine that runs LocalAI (not inside a container):

  ```bash
  /path/to/data/addons/sandbox/runsc/gvisor-profile /path/to/data/addons/sandbox/runsc/runsc \
    | sudo tee /etc/apparmor.d/localai-runsc
  sudo apparmor_parser -r /etc/apparmor.d/localai-runsc
  ```

  The profile names the binary by path, so a new `runsc` at a new path needs a new profile. Inside a container, AppArmor is that of the host: use a profile that allows user namespaces for the container (or `--security-opt apparmor=unconfined`), together with a seccomp profile that allows `unshare` and `clone` with `CLONE_NEWUSER`.
- **The guest image.** The guest runs a Debian userland, the guest kernel and the guest agent. The `sandbox guest image` workflow (`.github/workflows/sandbox-images.yml`) builds `addons/sandbox/images/guest/Dockerfile` for amd64 and arm64, pushes it to the project registry (`quay.io/go-skynet/sandbox-guest`, which must allow anonymous pulls) and outputs the reference by digest. The package build passes that reference in `SANDBOX_GUEST_IMAGE=<repository>@sha256:<digest>`, and `scripts/addons/package.sh` builds the add-on with `-ldflags "-X main.guestImage=..."`. `package.sh` refuses a Sandbox build without the variable; `--local-check` builds with a stand-in reference no registry serves, for a check that does not publish. To build the image by hand: `docker buildx build -f addons/sandbox/images/guest/Dockerfile .` from the repository root.
- **KVM.** `addons/sandbox/package/kvm.json` pins Cloud Hypervisor v54.0 (amd64 and arm64) and virtiofsd v1.14.0 (amd64) with checksums, and `fetch-runtime.sh` puts them in the package. The add-on checks each against the checksum compiled into it and copies it, as an executable file, to `kvm-bin/` in its data directory. Upstream publishes no static virtiofsd for arm64, so on arm64 install virtiofsd 1.13 or later in a system folder. The KVM backend also needs `mke2fs` and `debugfs` from e2fsprogs (the LocalAI image installs them) and access to `/dev/kvm` for the user LocalAI runs as. Bubblewrap is optional in a container: the add-on does not use it when it finds `/.dockerenv` or `/run/.containerenv`, or when `SANDBOX_NO_BWRAP=1` is set in its environment (`SANDBOX_NO_BWRAP=0` forces it). `addons/sandbox/images/host/Dockerfile` still builds an image with the same programs for a product that wants them in one place.
- **Tests in CI.** The `sandbox tests` workflow (`.github/workflows/sandbox-tests.yml`) runs the Go tests and the end-to-end suite, the gVisor integration suite on amd64 and arm64 (with the pinned `runsc` and its profile loaded), the KVM integration suite on a runner that has `/dev/kvm` (it skips, and says so, on one without), and the release smoke test.
- **Release smoke test.** `scripts/addons/sandbox_smoke.sh` takes the package that `package.sh` made, starts the add-on from the extracted files under the real supervisor with the default settings, and runs a command through the tool path of a chat. It passes when the command ran in gVisor or KVM, or, on a host that cannot, when every tool answers "This host cannot run commands." and nothing ran. `--require isolated` makes the second case a failure.

### The Sandbox in the LocalAI container

gVisor, KVM and the host backend refuse to run as root, so the sandbox needs the add-on process to run as an unprivileged user. The LocalAI container image starts as root only long enough to run `entrypoint.sh`, which hands the data folders (`/models`, `/backends`, `/configuration`, `/data`, `/tmp/generated`) to the user `localai` (uid and gid 10001) and then runs LocalAI as that user, with the groups that own `/dev/kvm`, `/dev/kfd` and `/dev/dri/*` added. The add-on runs as the same user.

- **Existing volumes.** A volume created by an older image is owned by root. On the first start with the new image the entrypoint changes its owner to uid 10001 (`chown -R`), which takes a while on a large models folder, and logs it. Nothing needs to be done by hand. If the volume cannot be changed (a read-only or network mount that refuses `chown`), set its owner to 10001:10001 on the storage side, or run the container with `--user` set to the owner of the data.
- **Another user.** Set `LOCALAI_UID` and `LOCALAI_GID` to use another id. A container that already starts as a non-root user (`docker run --user`, Kubernetes `runAsUser`) is left as it is; the data folders must then be writable by that user.
- **Keep root.** Set `LOCALAI_RUN_AS_ROOT=true` to run as root as before. The sandbox is then unavailable, and the page says why.
- **Devices.** Pass `/dev/kvm` to the container (`--device /dev/kvm`); the entrypoint adds the group that owns it. In Kubernetes, request the device through your device plugin and give the pod a `fsGroup` or `supplementalGroups` entry for it.
- **User namespaces.** gVisor needs the container to be allowed to create user namespaces, and on an Ubuntu 24.04 or later host an AppArmor profile that allows them (see **AppArmor** in [Packaging and setting up the Sandbox add-on](#packaging-and-setting-up-the-sandbox-add-on)). Without them `runsc` stops at once, and the backend's reason ends with `exit status 128` and `cannot read client sync file ... EOF`. Docker's default seccomp profile blocks this: use a profile that allows it, or `--security-opt seccomp=unconfined` on a host you trust. KVM needs `/dev/kvm` and no extra privileges.
- **Cluster.** The add-on runs on the add-on owner node only. In Kubernetes, give that pod the settings above; a node pool dedicated to it is the way to keep the isolation requirements off the other nodes.

What the source tree does not prove: the images and the `runsc` download are not built in the test suite, and the gVisor and KVM backends need a host that allows them. Their tests skip with the reason on a host that does not. The host backend and the whole path from a chat to a file in the user's files run in the tests.
