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
  decides every role, and applies a mutation from a chat, an agent or a script only
  after the proposer's own browser session confirms it. Prompt text, a model and
  an addon cannot supply that approval.

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

## Turning the owner on or off

A normal `local-ai run` starts the addon owner, so Apps, Add-ons and Tables work
without extra setup. The node is its own owner: it keeps addon state under the
`addons` folder of the data path (`--data-path`) and reaches the owner over a
loopback carrier with keys that are made at each start and never written to disk.

| Flag | Environment variable | Default | What it does |
|---|---|---|---|
| `--addons-enabled` | `LOCALAI_ADDONS_ENABLED` | `true` | Runs the addon owner. `--addons-enabled=false` turns it off completely: no owner starts, no addon runs, and every addon request is refused as unavailable. |
| `--addons-ownership-mode` | `LOCALAI_ADDONS_OWNERSHIP_MODE` | `named-user` | `named-user` gives each account its own access. `local-owner` makes every caller share one owner. Any other value stops startup with an error. |

`named-user` needs authentication. If authentication is off, the owner stays off and
LocalAI logs a warning; it does not stop. To use addons without accounts, select
`--addons-ownership-mode=local-owner` explicitly (see
[Explicit local-owner mode](#explicit-local-owner-mode)). The mode is never chosen
for you.

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
`sdk_incompatible`, with the message above. Every other failed install keeps
the generic `Addon request denied` message. If the administrator sends the same
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
`addonId`, `name`, optional `description`, `version`, `artifact`
(`oci://host/repository`), `digest` (`sha256:` plus 64 lowercase hex characters),
`os` and `arch`. It uses the same format as the model and backend gallery indexes:

```yaml
- addonId: tables
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

Unknown fields, a second YAML document and duplicate keys are rejected. A JSON
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
  `fine-tuning=finetune.,quantization=quantize.`. An empty value names nobody.
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

Two more optional capability fields describe the UI-only resource management (see
[UI-only resource management](#ui-only-resource-management)).

| Field | Meaning and bounds |
|---|---|
| `capabilities.adminTools` | At most 4 tool names that only the host's own UI-only endpoints may run. An admin tool is never in `tools`, never has a tool definition, is never offered to a model and is refused by every model, proposal and typed-read path: the declared-tool rule of those paths reads `tools` only. |
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
  `.../grants/{userId}`, `.../users`, `.../rename`, `.../delete`, `.../operations/{operationId}`
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

Set `AddonConfig.OwnershipMode` to `local-owner` to select this mode. It is the only way:
a missing credential, an absent account database or an unknown value never selects it, and
an unknown value fails startup. The owner logs a warning at startup that unauthenticated
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

Every data or schema mutation that comes from chat, an agent or an API client is a
stored proposal first. A proposal is applied only by an explicit confirmation of that
stored operation from the person's own browser session. Sharing and database deletion
are never proposals and never tools.

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
tool proposes waits as a pending proposal. `GET /api/addons/proposals?state=pending`
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
| `GET /api/addons/resources/{resourceId}/operations/{operationId}?kind=` | owner role (rename, delete) or a role that may write (apply), own session | Read-only receipt lookup of a rename, a deletion or a change. |
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
  an unknown resource; the reference stays and works again if the grant does.
- With no selection the tool call returns a result of kind `error` and code
  `select_resource`: no operation runs and the model is told to ask the person to choose.
- An explicit `resourceId` in the arguments is the person's own word and is used as before.

### Errors

| Status | `error.type` | Meaning |
|---|---|---|
| 400 | `invalid_request` | A missing or wrong confirmation, an operation the add-on does not support, arguments or a name it refuses, a malformed identifier. |
| 403 | `addon_error` | Not the person's own session, feature or role denied, unknown resource: one answer for all of them. |
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
Add-ons drawer can show them next to the installed apps. The answer is
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
  add-ons, so a pin is drawn once the add-on is installed, and an add-on that is not
  installed within the retention window loses its seeded pin. An account created
  later starts with no pins. An unpin stays after a restart.
- **Startup.** If the pins file cannot be read (it is corrupt), the server does not start
  and logs the error. It does not fall back to memory, because that would lose every unpin
  at the next restart.
- **Distributed state: shared.** With an account database, the table `addon_pins` is
  shared by every frontend. With authentication off there is one local owner, and the pins
  are kept in `addon-pins.json` in the data path, per instance.

`builtin` and `pins` are reserved add-on IDs: an add-on cannot use them, because the host
serves `/api/addons/builtin` and `/api/addons/pins`.

## Add-ons page and management

The web UI serves one page for add-ons at `/app/addons`, the **Add-ons drawer**. It is
shown to a caller who holds the `addons` feature, and to anyone who can open a built-in
tile (none today). Studio and Talk are add-ons: they are in the drawer only while they
are installed. Administrators also
reach **Browse**, **Installed** and **Libraries** from the drawer. The tabs are a
convenience: the owner checks administration again on every call, so a request that skips
the page is denied the same way (403).

The sidebar has four items: **Chat**, **Models**, **Add-ons** and **Settings**. Below them
a **Pinned** section lists the add-ons the user pinned, in the order the user chose. Pins
belong to the user, follow the user across browsers, and are capped at eight in the
sidebar. A pin whose add-on is stopped is muted and opens the detail view; a pin whose
add-on was removed or is hidden is not drawn. An empty section is not drawn. Operators who
need the previous sidebar can set `LOCALAI_LEGACY_NAVIGATION=true` (flag
`--legacy-navigation`); it also brings back the Add-ons tab of the Build hub, which then opens `/app/addons`. The switch is a rollback aid and is removed one release after the
new navigation ships; the UI reads it from the `addon_navigation` key of `/api/features`.

A tab is addressed as `/app/addons?tab=browse|installed|libraries`. The management link of
an app (`/app/addons?manage=ID`) opens the Installed tab with that addon's details open.

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
| `/app/voice` | `/app/addons/voice/recognition` |
| `/app/voice/<model>` | `/app/addons/voice/recognition?model=<model>` |
| `/app/voice-library` | `/app/addons/voice/library` |
| `/app/voice-library/new` | `/app/addons/voice/create` |

The two face links and the four voice links open their add-on only while it is installed and usable. Without
it, they open the same pages in the core web UI, so nobody loses a page they can open today.
The Studio and Talk links have no core page to fall back to: without the add-on they open
the add-on page, which says the add-on is not available.
A model id can hold `/` or `:`, which an add-on path segment refuses, so the model travels
as `?model=` (for example `/app/voice/org%2Fname` opens `/app/addons/voice/recognition?model=org%2Fname`; the face links and the
Studio links do the same).

A link with a model (`/app/image/<model>`) opens `/app/addons/studio/studio/images?model=<model>`. The
`/app/studio?tab=` links redirect to `/app/addons/studio/studio/<tab>`. A viewer without the `3d`,
`audio_diarization` or `audio_transform` feature still lands on the app home, as before.

The legacy switch (`LOCALAI_LEGACY_NAVIGATION`) restores the old sidebar and the Add-ons
tab of the Build hub only. These redirects do not depend on it: with the switch on, the
old generator links also open the Studio add-on. The legacy sidebar lists Home, Models, Chat and
the hubs; Studio and Talk are in the drawer, as add-ons.

### The Studio add-on

The Studio add-on (`studio`) is a packaged add-on. It is not shipped with LocalAI: install
it from **Browse add-ons** in the drawer and start it. Once it is usable, it appears in
the drawer and can be pinned. Until then the sidebar, the drawer and the Home `/studio`
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
**Browse add-ons** and start it. It adds the page `/app/addons/talk/talk` (main page) for
users with the `addons` feature. The page opens a WebRTC session to a realtime pipeline
through `/v1/realtime/calls` and reads the pipeline list from `/api/pipeline-models`, with
the signed-in user's own session; the core keeps both routes. The page uses the MCP servers
that the person added in the browser, the same list as Chat. The words are the English `talk` locale
file of the web UI; other languages read the English text.

### The Voice add-on

The Voice add-on (`voice`) is a packaged add-on. It is not shipped with LocalAI: install it
from **Browse add-ons** in the drawer and start it. Once it is usable, it appears in the
drawer and can be pinned. The Build hub has no Voices tab.

| Page | Path | Who sees it |
|---|---|---|
| Recognition (main page) | `/app/addons/voice/recognition` | users with the `voice_recognition` feature; the page shows an off notice for anyone else |
| Speech (text to speech) | `/app/addons/voice/speech` | users with the `addons` feature; the server refuses the speech call without `audio_speech` |
| Library | `/app/addons/voice/library` | administrators |
| Create | `/app/addons/voice/create` | administrators |

All of these also need the `addons` feature to see the tile. The pages call core routes
with the signed-in user's own session (see "Core routes from add-ons"): `/v1/voice/*` for
recognition (verify, analyze, embed, register, identify, forget), `/api/voice-profiles`
for the library and `/v1/audio/speech` for the speech page. The core keeps the voice
registry and these routes; the server checks every call again, so an add-on page never
widens what a user may do.

The recognition page has three tabs. **Speakers** is the workbench: who is this, same
person, the people this browser enrolled, and the analyze and embedding tools under **More
tools**. **Speech voices** opens the library page and is shown to administrators only.
**From a recording** links to the diarization workspace of Studio. The Face add-on shares
the same views, built from one source directory into each add-on.

### The Face add-on

The Face add-on (`face`) is a packaged add-on, separate from the Voice add-on. It is not
shipped with LocalAI: install it from **Browse add-ons** in the drawer and start it. Once it
is usable, it appears in the drawer and can be pinned. The Build hub has no Faces tab.

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
packaged add-ons. They are not shipped with LocalAI: install them from **Browse add-ons**
in the drawer and start them. Once usable, they appear in the drawer and can be pinned.
The Build hub has no Fine-Tune or Quantize tab.

| Add-on | Page | Path | Who sees it |
|---|---|---|---|
| `fine-tuning` | Fine-Tune | `/app/addons/fine-tuning/jobs` | users with the `fine_tuning` feature; the page shows an off notice for anyone else |
| `quantization` | Quantize | `/app/addons/quantization/jobs` | users with the `quantization` feature; the page shows an off notice for anyone else |

The old links `/app/fine-tune` and `/app/quantize` open the add-on page while the add-on
is installed and usable, and the same page in the core web UI otherwise. The pages call
`/api/fine-tuning/*` and `/api/quantization/*` with the signed-in user's own session (see
"Core routes from add-ons"); the core keeps these routes and checks every call again.
Both add-ons offer chat tools that propose a job and wait for a person to confirm. They
reach the engine through [Host operations](#host-operations), and a tool can control only
a job that a tool started. See [Fine-tuning](../fine-tuning/#fine-tuning-is-an-add-on) and
[Quantization](../quantization/#quantization-is-an-add-on).

### The drawer

The drawer is a grid of tiles with a search box. The built-in tiles (none today) come
first, then the installed add-ons by identifier. Each tile has a pin control, a real
button with a name and a pressed state, so it works from the keyboard; a pinned tile also
has buttons to move it earlier or later. Pinning a ninth add-on opens a dialog that asks
the user to unpin one first. A running tile opens the add-on; every other tile opens a
detail view (`/app/addons?detail=ID`) and never the main page, because the main page
needs a live process. Administrators also see **Browse add-ons** and **Add a new add-on**.
A caller without the `addons` feature sees only the built-in tiles (none today), and the page does not
call `GET /api/addons/apps` for that caller.

The detail view shows the entry as described next, and the pages the add-on names in
`ui.navigation`. The drawer draws `GET /api/addons/apps`: the icon from the host's icon route, the
title in the interface language (falling back to the default text and then to the
identifier), the description, a state chip (`running`, `starting`, `failed`, `stopped`),
an **update pending** chip when the owner's cached check reports one, an **Open** link only when the entry
has a `mainPage`, and for an add-on without pages a short tool summary. Icons always
come from the same origin; the page never loads an image from a manifest or a library.

The list is read again every 5 seconds (30 seconds in a hidden tab). A failed read
doubles the wait up to 60 seconds, keeps the last list on screen and says it may be
out of date. Leaving the page cancels the pending read. Administrators see **Start**,
**Stop** and **Manage** on an entry that carries the `admin` object. A start or stop
sends a fresh operation ID with the generation that was listed and keeps that ID for
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
| `catalog-search` | read | `text`, `sourceId`, `offset`, `limit` (at most 100) | `{releases, total}`; only releases for the owner's configured target platform are listed, and `total` counts that set |
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

### In the page

The Installed list and the management sheet show a badge with the candidate version and
the short digest, and the compatibility reasons of an incompatible candidate. An
administrator gets **Check for updates** with the time of the last check, and **Upgrade**
when the state is `update-available`. **Upgrade** opens a confirmation that names the
library, the channel, the installed and the new version and digest, and says that the data
of the add-on is backed up first. It then shows progress and, on failure, the error and the
kept version. Anyone else sees the status without actions.

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
| `app.nav` | On the Add-ons page, below the tile grid. Under the legacy switch: at the end of the second navigation row of the Build hub's Add-ons tab, after the links of `ui.navigation`. | none |
| `settings.sections` | At the end of the Settings page, one section for each component, under a heading with the title and icon of the add-on. | none |

`messageId` is the position of the message in the conversation. `role` is `user`,
`assistant`, `system` or `tool`.

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
missing registry/namespace input (`ADDON_OCI_REPOSITORY`). There is no default registry.
The add-on repository namespace is `quay.io/go-skynet/local-ai-addons`; every other
backend image repository is refused.

Every platform matrix lists four platforms: `linux/amd64` on `ubuntu-24.04`, `linux/arm64`
on `ubuntu-24.04-arm`, `darwin/arm64` on `macos-14` and `darwin/amd64` on `macos-15-intel`.
Voice, Face, fine-tuning, quantization, Studio and Talk enable all four. A test fails when a
platform is enabled without a recorded check.

Each package also has a catalog template in `gallery/addons/<id>.yaml.tmpl`, so it can be
listed in Browse add-ons once a maintainer renders and publishes its entries. See
`gallery/addons/README.md` for the commands.

Every add-on has its own repository. Tables publishes to `ADDON_OCI_REPOSITORY`. Each
other add-on publishes to `<namespace>/<id>`, where the namespace is
`ADDON_OCI_REPOSITORY` without its last path element.

Set `ADDON_OCI_REPOSITORY` to the Tables repository inside the add-on namespace:
`quay.io/go-skynet/local-ai-addons/tables`. The other add-ons then publish to
`quay.io/go-skynet/local-ai-addons/voice`, `.../face`, `.../fine-tuning` and
`.../quantization`. The value has no tag and no digest. A value outside the add-on
namespace makes the add-on tools refuse the repository.

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
