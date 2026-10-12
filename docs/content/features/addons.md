---
title: Addon contracts
---

## Status and trust

The version-one contracts in `pkg/addons/contract` define managed-process addons.
They do not yet install or run addons, expose HTTP routes, or implement Tables
storage. Installed executables are trusted code, not sandboxed code. Every addon
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
a missing checker denies all edits. The future owner adapter must revalidate
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
it fails closed if that tuple contains multiple platforms rather than choosing
an arbitrary executable. Platform-aware selection and channel updates require
later integration; this service does not silently choose a source or upgrade.

State uses the documented **per-instance designated-owner** exception: only the
configured owner constructs this service. Configuration persists; the bounded
catalog cache is rebuilt by explicit refresh after restart. Replicas must forward
to that owner, not construct independent caches or fall back on owner failure.

Run `sh scripts/addons/verify.sh catalog` for private bearer/basic HTTP fixtures,
redirect rejection, isolated credential references, disabled sources, refresh
failure, duplicate names, source persistence/removal and denied edits.
