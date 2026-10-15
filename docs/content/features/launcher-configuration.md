---
title: Launcher configuration
---

The desktop launcher stores settings in `~/.localai/launcher.json`. Existing
flat settings migrate automatically to schema version 2. The launcher continues
to run the standalone server with the same paths, address, and environment.

## Settings layout

Version 2 separates settings into these objects:

- `schema_version`: `2`.
- `mode`: `standalone` or `worker`. Files without a mode use `standalone`.
- `shared`: `models_path`, `backends_path`, `log_level`, `auto_start_server`,
  and `show_welcome`.
- `standalone`: `address` and `environment_vars`.
- `worker.connection`: `frontend_url`, `registration_token`,
  `advertised_address`, `node_name`, and optional integer `grpc_max_port`.
- `worker.nats`: `url`, `tls_ca`, `tls_cert`, `tls_key`, `jwt`, and `user_seed`.
- `worker.environment_vars`: separate advanced runtime overrides.
- `legacy`: preserves `start_on_boot` when present in an older file.

The worker objects currently prepare storage only: this migration does **not**
add worker controls or a worker launch command. Selecting `worker` in a manually
edited file will not start a standalone server as a fallback. Connection
validation and reserved worker environment handling belong to worker launch
support, not storage.

Legacy environment settings stay in the standalone profile and are not copied
to worker settings. Saving unrelated settings retains remembered worker values.
Empty worker settings may be stored. Unknown modes and unsupported schema
versions produce a settings error instead of launching a guessed role.

`auto_start_server: false` disables automatic startup; `true` enables it.
Absent or `null` retains the default of enabled. The obsolete `auto_start` key
is ignored. `legacy.start_on_boot` is inert, including when true: it does not
override an explicit opt-out or install an OS service, login item, or boot task.

## File security and recovery

The launcher requires an owned regular configuration file and tightens existing
permissions to `0600` **before** reading its contents. Saves write and flush an
owner-only temporary file in the same directory, then atomically replace the
configuration. Failed saves leave the active settings unchanged and block
automatic startup until settings can be saved securely again.

Symlink files, hard-linked files, foreign ownership, and symlink configuration
directories are rejected. `~/.localai` must be owned by your account and not
writable by other users. New configuration directories use `0700`; existing
model and backend directory permissions are not recursively modified.

If saving or loading fails, check ownership, free disk space and directory
permissions. Remove unsafe links only after checking their targets; restore a
regular `launcher.json` from a trusted backup if needed. Do not resolve errors
by granting world-write access. Configuration errors do not include saved
values. Configuration and environment dumps have been removed from launcher
logging.

{{% notice warning %}}
Remembered registration tokens and optional NATS JWT/seed values are **plaintext**
inside the protected file, not encrypted at rest. File permissions do not protect
against the same user, root, backups, or child-process inspection. Protect backups
accordingly. Masked editing controls are not part of this storage migration.
{{% /notice %}}

## Launch validation and logs

Worker startup is still disabled in the launcher. The internal launch builder
now validates the saved worker profile, but does not bypass the forthcoming
runtime compatibility, status and descendant-ownership gates. Editing JSON does
not enable worker startup, and worker errors never fall back to standalone.

The worker profile requires an explicit frontend URL (prefer
`https://controller.example/prefix`), one NATS endpoint (prefer
`tls://nats.example:4222`), a registration token, and an advertised address such
as `worker.example:50051` or `[2001:db8::10]:50051`. URL userinfo, queries and
fragments are forbidden; NATS paths are not supported. Frontend path prefixes
are retained. Plain HTTP/NATS requires explicit protected-network acknowledgement
at the builder boundary; no launcher control enables it yet.

The base port must be 2–65535. Reverse HTTP uses base minus one; gRPC allocates
from base through `grpc_max_port` (default 65535). Loopback addresses only work
for colocated peers. Advertised addresses do not restrict wildcard binding or
prove remote reachability. Reverse HTTP/gRPC requires a protected network; this
is not end-to-end encryption.

Authentication is always required. The registration token is passed only in the
child environment, never argv. Normally registration supplies and renews NATS
JWT/seed credentials after approval. Optional static overrides must be a matching
valid user JWT and seed pair; static mode has no automatic renewal. Possession
of credentials is not evidence of approval or readiness. NATS TLS file paths
must be readable regular files, and client certificate/key must be paired.
Relative CA, client certificate and key paths resolve against the launcher's
working directory when the launch description is built, not the child's data
directory. The builder validates and snapshots those absolute paths without
changing saved settings. Prefer absolute paths when configuring TLS files.
NATS TLS settings require `tls://`; they do not configure frontend HTTPS trust,
which uses the OS trust store.

Advanced worker environment settings cannot override role, authentication,
connection, address, path or launcher-observability settings, including supported
aliases. Inherited reserved values are removed. Non-reserved accelerator options
are retained. Standalone arguments and environment precedence remain unchanged.
The builder snapshots values without modifying the launcher's environment.

Child output is filtered before reaching the UI, in-memory buffer or log file.
Credential-bearing, control-character, oversized and incomplete lines are omitted
rather than retained raw. Configured credentials, issued JWT/seed forms,
authorization headers and credential-bearing URLs are covered. Lines are limited
to 16 KiB, UI/memory buffers to 100 KB, and each session log to 1 MiB (additional
file output is dropped). New log files are owner-only (`0600`) in an owned `0700`
logs directory; unsafe directories are rejected. Existing historical log files
are not rewritten or deleted. Review and remove old logs yourself if they may
contain credentials; filtering is not encryption or protection from your account
or root.

## Private worker status contract (runtime support)

Worker startup in the launcher remains disabled until ownership and lifecycle
controls are implemented. This runtime support does not enable the settings UI
or start a worker automatically.

Compatible runtimes implement `local-ai worker-status-capability`, which prints
`localai-worker-status-v1` and exits without starting a worker. The launcher
compatibility helper uses a five-second, bounded-output probe, not a guessed
release-number threshold. An incompatible runtime requires an upgrade; there is
no standalone fallback or log-based readiness inference.

Launcher-managed workers opt in through child-only environment variables:

- `LOCALAI_LAUNCHER_STATUS_PATH`: absolute `status.json` path inside a fresh,
  owner-only mode-0700 run directory.
- `LOCALAI_LAUNCHER_GENERATION`: unique per-launch identity, matched by the reader.

Both values are reserved against inherited/profile overrides. Without them,
workers do not create status files. The file is a local observation channel, not
an HTTP API, transport, process ledger, or command queue. It is atomically
replaced using an owner-only mode-0600 temporary file, every two seconds and on
observed transitions. Unsafe ownership, permissions, symlinks, hard links,
malformed data, unknown schema versions, and snapshots over 16 KiB are refused.
A publication failure invalidates old evidence and requests worker shutdown.

Schema 1 includes generation, increasing sequence, publication time,
registration outcome/time, credential availability (booleans, never secrets),
NATS connectivity and server-required authentication, confirmed lifecycle subscriptions, armed local checks,
last successful heartbeat acknowledgement, heartbeat interval, and categorical
failure. Static credentials do not imply approval. Registration pending and
approved-but-missing-credentials remain distinct. Non-2xx heartbeats are failures.

The reader measures sequence advancement with a monotonic clock; rereading a
file cannot refresh it. Ten seconds without advancement means status unavailable.
Initial registration has a 30-second progress deadline, except a fresh explicit
pending response. Ready requires approval, credentials, NATS, subscriptions,
armed passing checks and a heartbeat within three intervals (normally 30 seconds).
Early `/readyz` success and process existence are not proof.

**Ready — waiting for work** means the worker can accept work under these checks.
It does not certify reverse network reachability, model availability, VRAM, or
successful inference. Full authenticated frontend/inference and native macOS
launcher acceptance remain separate validation requirements.

#### Approval and process freshness

Status schema 1 also carries the worker PID. The future lifecycle controller must
supply the PID of its owned child to the reader, together with the fresh random
per-launch generation; it must never learn the expected PID from the snapshot.
This binding is not a replacement for portable descendant ownership or PID-reuse
checks during crash recovery. Worker startup in the launcher remains disabled.

Each new sequence must have a publication timestamp less than ten seconds old,
not in the future, and not older than the last accepted publication. Approval and
heartbeat timestamps cannot be later than publication. Once admitted, remaining
freshness is measured with the reader's monotonic clock, including the initial
publication age. Neither rereading nor a delayed unseen sequence grants a fresh
lease. Approval must be nonzero and less than 30 seconds old.

Both static and registration-issued credential paths query controller approval
at ten-second intervals, independently of heartbeat and JWT renewal, including
non-expiring JWTs. `GET /api/node/:id/status` uses the existing worker
registration-token authentication boundary and returns only `id` and `status`.
It does not mint credentials, update heartbeat, or restore offline nodes. An old
controller without this endpoint fails closed. Like existing self-service routes,
the shared registration token is not per-node authorization.

Managed credential renewal uses `POST /api/node/register` with `renew_node_id`
and `token`, rather than restarting registration. This observes the existing row
without restoring its status or updating telemetry; credentials are minted only
for a healthy node. Periodic approval never changes the credential manager, and
renewal responses cannot overwrite the independently observed approval evidence.
The optional `renewal_failure` field records `request_failed`, `pending`, or
`missing_credentials` independently of retained credentials. Any renewal failure
prevents ready status until a successful credential renewal clears it. Heartbeat
and approval success cannot clear a renewal failure.
Dynamic credential-provider NATS connections keep retrying authentication if the
JWT expires during a renewal outage, including repeated identical broker errors.
A later successful renewal can therefore reconnect to the still-running broker
without restarting it or the worker. This does not bypass authentication or make
the worker ready during the outage: all readiness evidence must become current
again. Static credentials retain NATS's default terminal authentication-error
behavior. The credential manager's existing bounded renewal retries are unchanged.
This worker-only surface is not an admin action or a UI capability.
The approval route is not registered when the controller has no registration
token, and renewal-only requests are refused in that configuration. Missing or
incorrect bearer tokens are rejected before registry access. These worker
self-service operations do not use admin feature permissions or MCP tools.
