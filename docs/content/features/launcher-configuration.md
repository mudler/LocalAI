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
