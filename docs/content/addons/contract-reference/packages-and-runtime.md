+++
title = "Catalog, packages and the managed runtime"
description = "The private catalog service, package staging, the durable lifecycle and the managed runtime."
weight = 50
+++

Look up the private catalog service, how a package is staged and verified, the durable lifecycle and the managed runtime. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

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
`id`, `name`, optional `description`, `version`, `artifact`
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

An entry may carry a `kind` (see [Backends in the library]({{% relref "/addons/install-and-update#backends-in-the-library" %}}));
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

Every release that the host lists also carries `official`. The host sets it when it reads
the index, from the ID and the address of the library. It is `true` only for the official
library at an address that LocalAI ships, so a library that an administrator named `official`
at another address does not qualify. An index cannot set it: the host overwrites whatever
the index says. The install dialog uses it to decide whether to warn about the source.

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
[limit]({{% relref "/addons/install-and-update#size-limits" %}})) and archive
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

Windows cannot pass numbered descriptors, and a child cannot turn an inherited
socket into a listener. On Windows the owner passes two inherited pipe handles
instead and names them in two more environment variables:

| Variable | Handle |
|---|---|
| `LOCALAI_ADDON_STARTUP_HANDLE` | Read end of the startup pipe. It carries the same `runtime.Startup` object as descriptor 4, and EOF has the same meaning. |
| `LOCALAI_ADDON_ENDPOINT_HANDLE` | Write end of the endpoint pipe. |

The child binds its own listener to `127.0.0.1` on an ephemeral port, writes the
address as one line (for example `127.0.0.1:49152`, at most 64 bytes with the
newline) on the endpoint pipe and closes it. The owner then writes the startup
object, whose registration names that endpoint. The owner stops a child that
reports any other address, or that does not report within the ready timeout.
The environment of the child also has `SYSTEMROOT` (the network stack of
Windows needs it) and `TMP` and `TEMP` (the temporary folder of LocalAI). The
executable must end in `.exe`, because Windows has no execute bit.

A package with `"osSettings": {"windows": {"execution": "wsl2"}}` (see [Manifest](manifest/)) runs in a WSL 2
distribution on a Windows host. No handle of the owner exists there. The owner starts
the executable through `wsl.exe` with `LOCALAI_ADDON_CHANNEL=stdio`, and the standard
input and output of the process carry one multiplexed session (yamux). The owner is the
client and the add-on is the server:

| Stream | Opened by | Carries |
|---|---|---|
| First stream | owner | The add-on writes its process ID as one decimal line (at most 24 bytes with the newline). The owner then writes the `runtime.Startup` object. The end of this stream, or of the session, has the meaning of EOF on descriptor 4. |
| Every later stream | owner | One connection to the protocol endpoint of the add-on. |
| Every stream | add-on | One connection to the host operation endpoint. |

The owner serves the protocol endpoint on a `127.0.0.1` port of Windows, so the
registration names a Windows address. The SDK serves the host operation endpoint on a
`127.0.0.1` port in WSL and writes that address in the startup object, so the add-on
reaches the host without a network path from WSL to Windows. Standard error stays the
log stream. The SDK points standard output at standard error, so a print never goes
into the session. When the session ends, the SDK ends the process after the shutdown
budget and two more seconds.

The Go SDK does this for you: `protocol.Run` uses the descriptors, the handles or the
standard streams, whichever the owner passed. An add-on that does not use `protocol.Run` calls
`protocol.StartupChannel` and `protocol.Listener` in place of opening
descriptors 4 and 3 itself.

The supervisor requires a private durable `StateRoot` (application default:
`<addon root>/runtime`). The path must be absolute and must not contain a symbolic
link. A separately locked, fsynced process ledger records the boot identity, PID,
kernel start time, and incarnation of each child before startup credentials
are sent. A pending record is persisted **before exec**. A restarted owner recovers
all recorded children, checks exact identity, sends termination, and waits for
confirmed exit before any replacement launch. Reused PIDs are not signaled.
Corrupt, incomplete, unreadable, or uncertain pending
records fail closed; operators must establish that prior children are gone before
repairing the ledger. Do not delete runtime metadata to bypass recovery.

How identity is read and how a child is stopped depends on the platform:

| Platform | Identity | Recovery |
|---|---|---|
| Linux | `/proc` boot ID and process start time | A pidfd is opened before the identity is read and the signal goes through that handle, so a recycled PID cannot be hit. Readable `/proc` and pidfd support are required. |
| macOS | `kern.boottime` and the process start time from `kern.proc.pid` (microsecond resolution), read through `sysctl` | The identity is read, then the PID is signaled. macOS has no pidfd, so the check and the signal are two steps. PIDs on macOS rise in sequence and wrap at 99999, so a recycle inside that gap is not realistic, but the gap is not zero. |
| Windows | The process creation time, an absolute time with 100 ns resolution, read through a process handle | A process handle is opened before the creation time is read, and the process is terminated through that handle, so a recycled PID cannot be hit. |

Other systems refuse runtime startup rather than weaken identity checks. This is not a sandbox
or descendant containment: installed code is trusted, and must not detach workers
outside the supervised process lifetime. On Windows the owner also puts each
add-on process in a job object that stops every process in it when the add-on
stops, and when LocalAI itself exits. On Windows the state root must not be a
link, but it has no permission check: see the security limits in
[Install on Windows]({{% relref "/start/install/windows" %}}).

Every local HTTP request requires the bearer credential and
`X-LocalAI-Incarnation`. The authenticated `/health` response must exactly match
the supplied registration, including protocol 1, endpoint, generation, incarnation,
and manifest capabilities. Capabilities are visible only after readiness and are
withdrawn on stop, exit, or failed health. Health is checked periodically. A restart
uses a fresh credential and incarnation, bounded exponential backoff, and the
configured attempt budget; sustained authenticated health resets that budget.

The protocol uses POST with JSON on `/read`, `/preview`, `/apply`, `/receipt`, and
`/output`, and on `/act`, `/connector`, `/client` and `/client/stream` for the add-ons that
declare actions, a connector or client routes (see
[Paired clients: inference and add-ons]({{% relref "/addons/addon-clients" %}})). Read and preview accept the existing contract envelope. Apply wraps it
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
durable journal. The same process identity checks as above still apply;
ambiguous pending launches require operator recovery rather than an unsafe kill.
