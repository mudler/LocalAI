---
title: Addon contracts
---

## Status and trust

The version-one contracts in `pkg/addons/contract` define managed-process addons.
The owner can install and run addons. Public HTTP routes, distributed request
forwarding, and Tables storage are not yet available. Installed executables are trusted code, not sandboxed code. Every addon
requires an executable; a UI bundle alone is not an addon.

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

Namespace IDs match `[a-z][a-z0-9-]{0,63}`. IDs are never paths. Executable and UI
asset paths are normalized package-relative ASCII paths: no absolute paths,
parent traversal, backslashes, percent escapes or URL schemes. These checks do
not substitute for safe extraction, symlink checks, executable mode validation,
or checking declared files exist, which belong to package installation.

Capabilities are independent: `service` is a boolean, `tools` is a unique list
of namespace names, and optional `ui` contains required `entry` and `sdk` range,
optional `css`, `pages`, `navigation`, and `chatActions` lists. Navigation names
must refer to declared pages. UI assets must not duplicate each other. Absent UI
requires neither assets nor SDK compatibility. Declared UI must be consistent;
an empty object does not count as a valid declaration. Chat actions identify
host-controlled overflow-menu entries opening addon panels, not DOM hooks or
permission to read chat history. Platform tuple equality is compatibility only,
not evidence that any release platform has passed native runtime tests.

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
`read`, `preview`, `apply`, and `output`. Principal IDs are opaque nonempty
strings up to 256 bytes. Syntactic validity is **not authentication**: the owner
must create authority, verify current identity/credential/ACL and mode, and bind
scope to the actual method. Never accept browser/model-provided approval.
Call `Validate(now, limits)` at admission; unmarshalling checks structure and
nonzero expiry but intentionally does not use the wall clock.

`Runtime` provides Read, Preview, Apply, Receipt and OpenOutput. Preview is
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
| Single artifact / aggregate artifact quota | 100 MiB / 1 GiB |
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

A catalog is a JSON array of releases with `addonId`, `name`, optional
`description`, `version`, `artifact` (`oci://host/repository`), `digest`
(`sha256:` plus 64 lowercase hex characters), `os` and `arch`. Unknown fields
and trailing JSON are rejected. Source identity and verification policy always
come from protected source configuration, not from the remote release.
The policy describes requirements for later package verification; refresh does
not claim to verify executable signatures.

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
are never passed to a registry or package URL. Registry redirects are refused.

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

Defaults cap compressed downloads and extracted content at 512 MiB and archive
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
plaintext request. Non-loopback HTTP and registry redirects are always denied.
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
`local-owner`; see "Explicit local-owner mode" below. There are no CLI flags or
runtime settings for addons: like the rest of `AddonConfig` it is startup-only
configuration that the embedding host supplies, and every frontend of one
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

Owner bootstrap reconciles the journal, then refreshes all persisted enabled
sources before exposing the manager. The release cache is not durable. The whole
startup refresh has a 30-second deadline (or the earlier application-context
deadline); an offline/invalid enabled source explicitly fails startup and releases
the owner lock. Disable that source in owner configuration to start offline.
Disabled sources are never fetched. Forwarders neither read source metadata nor
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
when the deployment sets `OwnershipMode: "local-owner"`; it is never inferred.

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

### Public API

- `POST /api/addons/dispatch` accepts a contract request. Supported lifecycle actions
  are `install`, `enable`, `disable`, and `uninstall`. `operation-status` is read-only.
- `POST /api/addons/relay` accepts `{request, kind, handle}`. Kinds are `status`,
  `progress`, `json`, `file`, and `asset`. Handles are not filesystem paths.
- Both routes require named authentication and the `addons` feature. Lifecycle
  dispatch additionally requires current administration rights. Cookie requests require
  same-origin fetch metadata; header-authenticated clients use their validated keys.
- `POST /api/addons/proposals`, `GET /api/addons/proposals/{id}`,
  `POST /api/addons/proposals/{id}/confirm`, `.../cancel`, `.../reconcile` and
  `.../resolve` store, read, apply, withdraw and resolve data or schema proposals. See
  [Proposals and confirmation](#proposals-and-confirmation).

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
that forwards to the owner, or a host without the policy, keeps denying. Private output
descriptions still need the artifact store, so `Output` denies until it exists.

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
`Reconcile` at startup before it serves. Until the code that creates resources supplies
a backing-resource existence check, the default drops pending registrations and keeps
committed ones. A database with no record is never reachable.

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

Set `AddonConfig.OwnershipMode` to `local-owner` to select this mode. It is the only way:
a missing credential, an absent account database or an unknown value never selects it, and
an unknown value fails startup. The owner logs a warning at startup that unauthenticated
callers share one authority.

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

Resource reads obtain the current ready runtime incarnation and check the exact generation.
Private output copying rechecks credentials and stays inside admission until completion
or cancellation. Immutable assets use installed package receipts, declared entry/CSS paths,
and a contained filesystem root. Asset access currently requires an actor-bound lifecycle
operation and administration; registry-driven user asset access is a later integration.

Mutation MCP tools remain deliberately unavailable. The later MCP integration defines
safe listing/status tools for proposals; prompt text cannot authorize mutation and no tool
confirms a proposal. For that reason `/api/addons/dispatch`, `/api/addons/relay` and the
proposal routes are deliberately not mapped in the LocalAI Assistant tool catalog
(`pkg/mcp/localaitools`) yet. The route map test only
requires an entry for each registered tool, so no test is weakened. The routes are described
in the generated Swagger document under the `Addons` tag.

Run `sh scripts/addons/verify.sh policy` for the policy suite.

## Proposals and confirmation

Every data or schema mutation that comes from chat, an agent or an API client is a
stored proposal first. A proposal is applied only by an explicit confirmation of that
stored operation from the person's own browser session. Sharing and database deletion
are never proposals and never tools.

### Lifecycle

`POST /api/addons/proposals` takes a typed `write` or `schema` request. The owner
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
for a key credential either. Plain text, a boolean in arguments, or a model saying "yes"
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
| `503` `proposal_unavailable` with `operationId` | The owner could not serve the call and the proposal did not reach the addon (or the call was a proposal, a cancellation or a resolution, which never run it). | Read the proposal with `GET`, then repeat the same call with the same ID. |
| `503` `outcome_unknown` with `operationId` | The proposal reached the addon on the owner (`executing` or `outcome-unknown`). | Do not confirm again as a new operation. Reconcile, wait for the automatic lookups, or resolve. |

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
other write or schema proposal on the same resource is denied and names the blocking
operation (`blockedBy`); reads continue. On start the owner settles proposals left
`executing` by a crash the same way and never applies them.

Recovery has three steps and none of them submits the operation again:

1. **Automatic lookups.** The owner repeats the receipt lookup for each unresolved
   proposal with exponential backoff (from the `BackoffInitial` limit up to
   `BackoffMax`). They run for a finite window: the apply timeout plus five minutes,
   counted from the moment the apply was handed to the addon. A final receipt settles the
   proposal and lifts the block. After the window, only an explicit lookup remains.
2. **Explicit lookup.** `reconcile`, by the proposer, at any time.
3. **Owner resolution.** `POST /api/addons/proposals/{id}/resolve` with
   `{"digest": "...", "reason": "..."}` lets the owner role of the resource stop
   waiting. It is accepted only from the same confirmation surface as confirm (the
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
