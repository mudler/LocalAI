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
