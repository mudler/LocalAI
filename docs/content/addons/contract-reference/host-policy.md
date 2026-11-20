+++
title = "Host policy"
description = "The internal host policy store, host access in the UI and the administrator host operation policy."
weight = 10
+++

Look up the internal host policy store and the rules for administrators. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

## Internal host policy store

`core/services/addons/hostsettings` provides internal, host-owned durable policy
infrastructure. The gateway now provides typed read/update actions over the existing
owner carrier. The owner revalidates the authenticated actor and checks current host
administrator authority before updates; caller-supplied role flags are rejected.
Reads are explicitly read-only. Lost mutation responses retain the carrier's
unknown-outcome behavior, with operation IDs and durable receipts for reconciliation.
Trusted host consumers can also read snapshots over a pinned mutual-TLS internal
route, without impersonating an end user. Frontends never fall back to local policy
when the owner or its store is unavailable.
All read, update and replay results expose only schema version, revision and policy, excluding private migration provenance retained in owner storage.

Application initialization opens `host-settings.json` under the add-on owner root
after acquiring the owner storage guard and attaches it to the gateway. Frontends
only forward requests; they never open or import this store. Startup failure,
context cancellation and application shutdown close the store once. A restart
retains the saved policy and revision rather than importing configuration again.
Studio now reads the owner's network policy for new-chat defaults, administrator
network views, chat network settings, approval-card decisions and connection checks.
These reads carry the request context and return errors. A frontend uses the pinned
mutual-TLS internal route; an owner reads its durable store directly. Each host check
reads current policy, so tightening applies to the next request on an existing
frontend without restarting it. A waiting approval card rechecks policy when it
wakes or polls, including after an answer. The Sandbox proxy's existing short-lived
verdict cache still bounds reuse of an earlier connection verdict as described below.

An unavailable owner or store does not substitute frontend settings or a cached allow.
Defaults, views and card decisions return an error; connection checks and waiting
cards return a blocking verdict with a policy-unavailable error. When add-ons are
disabled, Studio retains its existing local configuration behavior, including the
last valid parsed lists. An enabled add-on deployment with no owner gateway instead
fails closed.

Studio also reads the owner's `chats_tool_providers` map for replaceable tools
and skills. Provider selection, command availability, skill indexes and
administrator tool/skill views read current pins with the request context. An
owner update takes effect on the next read without restarting frontends or chat
workers. When the owner or store cannot supply policy, these operations return
an error rather than use frontend pins, a cached choice or a built-in fallback.
A mandatory provider-policy failure during chat-tool priming prevents the
harness from starting. On a running chat, that failure aborts the turn with a
fatal error before the model receives the message or any tool executes.
This differs from a stopped or unusable add-on: with policy available, the
existing built-in fallback rules still apply. Optional connector and skill-listing
failures retain their existing behavior; an optional connector failure does not
abort the turn. With add-ons disabled, Studio continues to use its local
`chats_tool_providers` configuration.

The legacy `/api/settings` route does not read or update owner policy. With
add-ons enabled, `GET /api/settings` omits the eight migrated fields listed below
and returns `addon_host_settings_authority: "owner_required"`, even if the owner
is unavailable. `POST /api/settings` returns `409 Conflict` if any migrated field
is present, including `null`, case-insensitive names or duplicate keys. A mixed
patch is rejected atomically before any local setting is applied or persisted.
Unrelated local settings remain readable and writable; `chats_egress_audit_days`
is local audit retention, not migrated owner policy. With add-ons disabled, the
legacy GET and POST behavior is unchanged. The settings UI is not yet connected
to the owner store.

The schema is limited to six network fields (`chats_egress_ceiling`,
`chats_egress_default`, `chats_egress_allow_private`, `chats_egress_ports`,
`chats_egress_extra_registries`, `chats_egress_allowed_hosts`), provider pins
(`chats_tool_providers`) and the owner's upgrade switch (`auto_upgrade_addons`).
Audit retention is excluded. The intended consumers are the Sandbox's host network
checks, chat tool and replaceable-skill selection, and the owner's upgrade loop.
Provider pins name replaceable tools (`bash`, `web_search`, `web_fetch`) or skills
(`xlsx`, `docx`, `pptx`, `pdf`), not protected or plain add-on skills.

The store uses one private file on trusted owner storage, with an exclusive lock,
atomic replacement and file/directory sync. It is owner-only state, not a replicated
frontend cache; the host must authenticate and authorize callers before updates.
On first start, the default standalone `--addons-role=combined` instance imports
its sole effective host configuration automatically, including legacy runtime
settings. A fresh dedicated `--addons-role=owner` with no legacy host policy
starts with restrictive defaults. If that owner has non-default legacy policy,
startup requires explicit selection with `--addons-migrate-host-settings`
(or `LOCALAI_ADDONS_MIGRATE_HOST_SETTINGS=true`). This owner-only option selects
only that process's effective configuration; it never selects, merges or imports
frontend copies. Review the owner configuration before enabling it. The option
is unnecessary after migration: an existing valid store remains authoritative.

Migration records private source/version provenance. Missing or ambiguous migration
input fails closed, and a corrupt existing file never triggers re-import or
defaults. Keep this state on trusted owner storage with working exclusive file
locks, atomic rename and file/directory sync; do not share it as a frontend cache
or run multiple owners against it. Existing singleton-owner deployment constraints
still apply.

Updates use an expected revision (compare-and-swap), presence-aware patches and an
operation ID. Omitted fields stay unchanged; explicit empty strings and false values
clear their fields, and a null provider entry deletes that pin. Stale revisions
conflict rather than overwrite another edit. A matching operation ID and body replay
the saved result; reusing a retained ID with a different body fails. The store keeps
at most 1,024 durable receipts and prunes the oldest revisions. A retry after its
receipt is pruned conflicts on its stale revision rather than applying again.
Acknowledgement follows durable persistence; uncertain durability requires reopening
and reconciling the operation before further use.

## Host access in the UI

An administrator gives an add-on its host operations in the browser, at install and at upgrade. No file edit and no restart are needed. The policy file and flags still work. They are a floor that the UI shows and cannot change, and the deny rules of the file always win. Only the administrator's own browser session can change host access. An API key, a tool call or an agent cannot grant, revoke, edit a deny rule or change the blanket switch. Each refusal of such a caller is written to the audit file with the kind of credential.

**With sign-in off, UI grants are locked.** Without sign-in every caller that reaches the port is the same local owner, and the checks that tell a person from a script are headers that the caller sets. So in that mode the UI cannot change host access by default: the policy file and flags still grant, every write route answers `403 locked_by_deployment`, and the page and the install dialog show the operations read-only with the reason. To allow UI changes anyway, start with `--addon-host-ui-grants-without-auth`. The server then logs a warning, and anyone who can reach the port can grant host operations, set deny rules and turn the blanket switch on. Turn sign-in on instead when other people or programs can reach the server.

The owner never grants what the verified package does not declare. A grant is always the operations you approved, that the package declares, and that this LocalAI supports.

### The install step

When the manifest of an add-on declares host operations, the install dialog shows a **Host access** step before **Install and turn on**. It lists each operation with a short title, one sentence and a risk word (reads, changes data, network, runs jobs), grouped by family.

- **Required** operations are on and cannot be turned off. The add-on does not run without them.
- **Optional** operations are off. The add-on runs without them, with fewer features. Turn on the ones you want.
- An operation that your policy blocks is greyed out and says "Blocked by the administrator policy". If a required operation is blocked, the install button is disabled and the dialog says why. The owner refuses the same install.
- An operation that the deployment already allows says so and needs no choice.
- An operation that this LocalAI does not support says "Not supported by this LocalAI".

For add-ons of the official library the required operations are already on and you still click **Install and turn on**. For an add-on of another library the warning below names the library it comes from. The list is read from the package that the owner verifies, not from the library listing, and the owner checks it again when it installs.

### Add-ons from another library

An add-on runs as trusted code on this server, whatever library it comes from. The first step of the install dialog therefore shows a warning for every add-on that is not from the official library, whether or not it asks for host operations:

> This add-on comes from *library*, not the official library. It runs as trusted code on this server and can use the credentials it is given. Install it only if you trust its author.

The same warning shows above the approval form of a held upgrade when the package is not from the official library. The dialog warns unless the library entry is marked `official` and the owner's preview agrees (the preview also checks the signature). A host that sends no answer gets the warning. The Host access step does not repeat it.

An agent that installs an add-on through the dispatch route carries no approval. If the add-on needs a host operation that is not held, the owner answers `approval_required`.

### Upgrades that ask for more

If a new version asks for a **required** operation that the add-on does not hold, the upgrade is held and the current version keeps running. The Installed tab shows a banner and a "Waiting for approval" label, and the **Host access** tab shows how many approvals wait. Select **Review and approve** to see what the new version adds, then **Approve and upgrade**. New optional operations never block an upgrade. They stay off, and the add-on runs without them. Automatic upgrades never approve anything. A version that asks for fewer operations does not remove any grant. The page lists the extra ones as "no longer requested" and you can turn them off.

### The Host access page

The **Host access** tab of the Add-ons page (administrators only) shows, for each add-on, every operation it asks for and what it holds.

- Each operation shows where its grant comes from: **Deployment** (policy file or flags, read-only), **You** (install, upgrade or edit) and **Blanket** (the switch below), with the date.
- Turn an operation off to revoke it. The change applies to the next call at once. Calls that already run finish. If you turn off an operation that the add-on requires, the page says that the add-on stops, and asks first.
- **Revoke all** removes everything that you or the blanket switch granted. **Keep as my own** turns blanket grants into explicit ones, so revoking the blanket grants later does not remove them.
- A grant that was made for a different package under the same name has no effect. The page marks it and offers to remove it.
- **Never allow** lists deny rules. Rules of the policy file or flags are locked. Rules made here can be removed. Before a new rule is saved, the page shows which add-ons lose which operations and which will stop. A rule can name one add-on or all add-ons, an operation, a family that ends with a dot, or `*`.

If the deployment sets `--addon-host-ui-grants=false`, or sign-in is off and `--addon-host-ui-grants-without-auth` is not set, the page is read-only and says why.

A grant is tied to the add-on ID and to the library and signer the package came from. An install or upgrade that resolves to another library or signer is held until you approve it in the browser, like an upgrade that asks for more. The old version keeps running. When an uninstall cannot delete the grants of the add-on, it fails and the add-on stays installed.

Each change is written to the audit file before it is applied. If the audit file cannot be written, the change is refused with `503` and nothing changes.

### Blanket approval

The switch **Approve everything an add-on asks for** is off by default. When it is on, an add-on that you install or upgrade gets the host operations that its manifest declares as required, without the install step. The scope is **Official library** or **Any library**. You can also include optional operations, and you can apply the switch to add-ons that are already installed. It never grants an operation that the manifest does not declare, and it never overrides a deny rule.

To turn it on you type a sentence that matches the scope. The dialog states the risk first. For **Any library**, every entry of every library that you configured receives the operations its manifest declares, so a malicious or compromised entry gets them at once. A deployment can set the switch with `--addon-host-blanket`. The page then shows it read-only.

When you turn the switch off, the grants that it made stay and stay marked **Blanket**, so nothing stops by surprise. The page offers **Revoke all blanket grants**, and the dialog can revoke them as it turns the switch off.

## Administrator host operation policy

`--addon-host-policy` (environment: `LOCALAI_ADDON_HOST_POLICY`) selects an
administrator-controlled JSON file. Use an absolute path to a readable regular
file, at most 64 KiB. LocalAI reads it once at startup; changes require a restart.
There is no implicit search, remote download, or manifest-selected policy path.
Keep the file outside addon-writable directories. The deployment administrator
must protect its ownership and mount permissions. A symlink to a regular file is
accepted; path validation does not prove that the file is administrator-owned.

```json
{
  "schemaVersion": 1,
  "grants": {
    "sandbox": ["workspace.", "egress."],
    "office": ["workspace."]
  }
}
```

```sh
local-ai run --addon-host-policy=/etc/localai/addon-host-policy.json
# Equivalent selection:
LOCALAI_ADDON_HOST_POLICY=/etc/localai/addon-host-policy.json local-ai run
# Explicitly grant nothing from the file (its deny rules, if any, still apply):
local-ai run --addon-host-ops=
# Disable policy selection inherited from the environment:
local-ai run --addon-host-policy=
```

The file requires `schemaVersion`. Version `1` also requires `grants` (an object)
and has no `deny`. Version `2` adds an optional `deny` object and makes `grants`
optional, so a file that only denies is valid. Keys are case-sensitive. Unknown
fields, duplicate keys, duplicate entries, malformed JSON and trailing data are
errors. An empty `grants` object is valid.
The limits are 128 addon IDs and 32 prefixes (or deny entries) per addon. IDs use
lowercase ASCII letters, digits and hyphens and start with a letter or digit.
Grant prefixes use the same grammar as `--addon-host-ops`: lowercase ASCII letter
segments, each ending in a dot, such as `workspace.` or `workspace.files.`. They
are not regular expressions or exact operation names. The broker must still
register an operation before any prefix can authorize it.

| Field | Version | Meaning |
| --- | --- | --- |
| `schemaVersion` | 1, 2 | Integer `1` or `2`. A larger number stops startup with `policy requires a newer LocalAI`. |
| `grants` | 1 (required), 2 (optional) | Addon ID to list of operation prefixes. |
| `deny` | 2 | Addon ID, or `"*"` for every addon, to a list of deny entries. |

Keep the file at `schemaVersion` `1` until you need a deny rule. A LocalAI binary
that predates version 2 refuses a version 2 file and does not start. That is
intended: an older binary must never start and silently drop a deny rule.
Remove the `deny` object and set the version back to `1` before you run an older
binary.

### Deny rules

A deny rule forbids host operations for good, whatever grants them: the policy
file, `--addon-host-ops`, a grant that an administrator makes in the UI, or the
blanket approval. Deny always wins. A deny entry is one of:

- a prefix such as `egress.`, which names every operation that starts with it;
- an exact operation name such as `workspace.admin`;
- the single token `"*"`, which names every operation. It must be the only entry
  of its list.

The key `"*"` applies a rule to every addon. It cannot be an addon ID.

```json
{
  "schemaVersion": 2,
  "grants": {"sandbox": ["workspace.", "egress."]},
  "deny": {
    "*": ["connector."],
    "web": ["workspace.admin"],
    "telegram": ["*"]
  }
}
```

`--addon-host-deny` (environment: `LOCALAI_ADDON_HOST_DENY`) gives the same rules
as a comma list of `addon-id=entry` pairs, and `*=entry` for every addon:

```sh
local-ai run --addon-host-deny='*=connector.,web=workspace.admin,telegram=*'
```

The deny rules of the file and of the flag are added together. They are read once
at startup, so a change needs a restart. A deny rule that an administrator adds
in the UI applies at once and can be removed in the UI. A rule from the file or
the flag cannot be removed from a browser. An install or upgrade that needs a
denied operation is refused.

Selection uses presence, not string length:

1. An explicit `--addon-host-ops` wins, including an empty value.
2. Otherwise, an explicitly set `LOCALAI_ADDON_HOST_OPS` wins, including empty.
3. Otherwise, the selected policy supplies the complete grant map.
4. The generic configuration resolver grants nothing when unconfigured.

Overrides replace the grants of the file; they never merge with them. The
selected file is always opened, even when a flat list replaces its grants,
because it may hold deny rules and a deny must not disappear when someone adds a
flag. LocalAI then logs `grants ignored, deny applied`. A file at `schemaVersion`
`1` has no deny rules, so a deployment that sets both a v1 file and a flat list
sees no change except that the file must now be valid. An invalid selected file
stops startup. An explicitly empty `--addon-host-policy` disables an environment
policy and supplies no grants. Startup logs report the source and counts, not
file contents. Manifests, addon requests and libraries cannot grant host
operations. In split deployments, the owner enforces authority; frontend settings
cannot add grants. Programmatic `WithAddonHostOps`, including an empty map, is an
exact startup decision and takes a copy of the supplied map and prefix slices.

Grants that an administrator makes in the UI add to the file and flag grants.
The effective set of an addon is the grants of the file and flags, plus the grants
of the UI, plus blanket grants, minus every deny rule. The file and flags stay a
read-only floor in the UI.

| Setting | Environment | Effect |
| --- | --- | --- |
| `--addon-host-deny` | `LOCALAI_ADDON_HOST_DENY` | Deny rules, added to those of the file. |
| `--addon-host-ui-grants=false` | `LOCALAI_ADDON_HOST_UI_GRANTS` | Makes the UI grant store read-only, so the file and flags are the only source of grants. On by default. |
| `--addon-host-ui-grants-without-auth` | `LOCALAI_ADDON_HOST_UI_GRANTS_WITHOUT_AUTH` | Lets the UI change host grants although sign-in is off. Off by default: with sign-in off the UI grants are locked, because anyone who can reach the port could change them. Logs a warning when set. |
| `--addon-host-blanket=off\|official\|any` | `LOCALAI_ADDON_HOST_BLANKET` | Fixes the blanket approval mode. The UI then shows it as set by the deployment. Unset leaves the choice to the administrator. The deployment value is applied when it is read and is never copied into `host-grants.json`: remove the flag and the switch returns to what an administrator set in the UI. |

### Breaking upgrade change: standalone and custom deployments

**The generic binary no longer grants host operations by default.** Upgrading
an unconfigured standalone or custom deployment removes the former implicit
grants, even when official addons are already installed. Those addons can lose
host-backed functionality. Startup warns about unconfigured grants even before
any addon is installed. LocalAI does not infer consent from installed addons,
write migration grants into addon storage, or automatically restore old grants.

Existing explicit flag or environment settings keep their effective grants,
including explicitly empty settings. An override replaces the grants of the
file; it never unions with them. A missing or invalid selected file fails startup.

To retain the former grants, review `deploy/addons/host-policy.json` from your
trusted LocalAI release, then install and explicitly select it:

```sh
sudo install -d -m 0755 /etc/localai/addons
sudo install -o root -g root -m 0644 deploy/addons/host-policy.json /etc/localai/addons/host-policy.json
local-ai run --addon-host-policy=/etc/localai/addons/host-policy.json
# Or configure this environment variable in your service, then restart:
# LOCALAI_ADDON_HOST_POLICY=/etc/localai/addons/host-policy.json
```

The exact former explicit setting remains valid:

```sh
local-ai run --addon-host-ops='fine-tuning=finetune.,quantization=quantize.,sandbox=workspace.,sandbox=egress.,office=workspace.,web=workspace.,telegram=connector.'
```

The reviewed deployment file contains exactly:

| Addon ID | Prefix grants |
| --- | --- |
| `fine-tuning` | `finetune.` |
| `quantization` | `quantize.` |
| `sandbox` | `workspace.`, `egress.` |
| `office` | `workspace.` |
| `web` | `workspace.` |
| `telegram` | `connector.` |

This file is host-owned deployment policy, not authority supplied by a manifest
or catalog. Grants trust addon identity, not a verified signature or provenance.
Installing different code under a granted ID gives that code the selected grants.
Protect the file against writes by addons and untrusted users. Review and reduce
the grants if you do not intend to trust every listed ID.

### Maintained launcher selection

The stock Docker runtime copies the reviewed file to
`/etc/localai/addons/host-policy.json` and explicitly selects that path through
`LOCALAI_ADDON_HOST_POLICY`. No executable-relative or working-directory search
is involved. `--addon-host-ops=` overrides this selection with zero grants.
`--addon-host-policy=` disables the image's policy selection and gives zero
grants unless an explicit host-operations override is present.

| Launcher or deployment | Host policy decision |
| --- | --- |
| Stock runtime `Dockerfile` | Ships and explicitly selects the reviewed file. |
| `docker-compose.yaml`, `docker-compose.distributed.yaml` | Inherit the stock runtime image selection; custom images must select their own policy. Only the addon owner enforces grants in split mode. |
| `website/static/install/kubernetes.yaml` | Inherits the stock image selection. A custom image must ship/select policy or mount an administrator file and select its absolute path. |
| External Helm chart referenced by the Kubernetes guide | Not shipped here. Check the selected image and chart environment; custom images are unconfigured unless the operator selects policy. |
| `.devcontainer/docker-compose-devcontainer.yml` | Uses the development target, not the stock runtime stage. A manually launched binary is unconfigured. |
| Release binaries, `website/static/install.sh`, the desktop launcher packages (`launcher/`) | Unconfigured standalone behavior. Operators must explicitly select policy or host operations; packaging does not migrate consent. |
| Custom systemd services | No maintained unit ships in this tree. Set `LOCALAI_ADDON_HOST_POLICY` or explicit host operations in the unit, protect the file, and restart. |
| `docs/docker-compose.yaml` | Documentation-site server only; it does not launch LocalAI. |

These non-stock launchers do not promise the former implicit grants. Automatic
policy installation or selection for them is deferred; use the explicit upgrade
procedure above. Merely copying a file without selecting it does not grant
anything. Do not fetch host policy from an addon library.

### Owner-resolved execution observations

The owner resolves execution-status bindings from an authenticated actor and a
conversation ID. Callers cannot choose the provider, tools, runtime incarnation,
agent grants, or consent. The resolver uses the chat's current add-on switches,
agent grants, provider pins, and installed-provider ordering. An installed but
idle pinned provider does not fall through to another add-on.
Execution observations read the same current owner policy as normal execution,
not frontend-local provider pins. Owner policy outages fail closed; a CAS pin
change invalidates a binding whose current selection no longer matches.

The binding path uses existing principal-scoped chat and agent reads on the
owner, supporting JSON and SQL/GORM stores. Named actors use their account ID;
the local owner uses the anonymous chat principal. Current account status,
feature permissions and credentials are checked through normal authorization.
The owner must have these stores; frontend tool lists are never authority.

The status gateway re-resolves current authorization and selection before
dispatch and before publishing positive facts, and compares the exact generation,
runtime incarnation and tools. A changed current selection withdraws old facts;
a fresh request can succeed after access is restored. This is current-read
consistency, not a historical revoke-and-restore (ABA) guarantee or a database
transaction spanning the read. The host compares
current selection identity only; it is not a durable revision or owner nonce.
A binding does not authorize command execution, grant access or replace consent.
Request/session integration shares observations only within a bounded request.

### Execution safety notices

The Sandbox document skills (`docx`, `xlsx`, `pdf`, `pptx`) are offered only
while the Sandbox reports an isolated backend. When it runs the chat's commands
and is not known to be isolated, the skill list and `load_skill` say so, with the
reason the status is unknown, instead of saying the chat has no skills.

The chat Tools menu and command chip describe the actual selected execution
provider, not an installed app's name. Any provider that reports host execution
gets a no-isolation warning. Unknown readiness or isolation gets an explicit
warning, not an isolated or safe badge. A status read does not grant execution
permission or replace the built-in host command consent flow.

Provider explanations are bounded plain text. Dismissing a notice applies only
to that mounted chat context and exact provider binding, including generation,
incarnation, readiness and isolation. Loading, switching providers,
changing isolation, changing actors or logging out resets the dismissal.
Actor or authentication-session changes also invalidate pending status reads
before the next effect runs; logging in as the same user starts a new epoch.
Built-in command execution, including fallback from an unavailable provider,
shows the generic host warning without Sandbox installation or repair advice.
This warning does not change command consent or administrator settings.

The network panel retains the configured policy when the backend cannot verify
its enforcement. It explicitly says that the policy does not apply to commands.
Provider-reported isolation alone does not establish network enforcement.
Missing current authorization, selection or runtime context yields unknown;
installing an app does not confer authority or prove network enforcement.

The chat `/about` card reports isolation from the selected command provider.
When the provider status is missing or unknown, the card does not claim isolation.
After an authentication refresh or account change, pending Apps reads and action
results cannot populate the new session. The server still authorizes every action.

### Model destinations (contract and backend metadata)

An installed addon can declare optional `capabilities.ui.modelActions`:

```json
{"modelActions":[{"capability":"FLAG_IMAGE","page":"studio","path":"images"}]}
```

Each entry requires an existing non-native model capability and a declared UI
page. The optional relative suffix must stay beneath that page. One entry per
capability is allowed within an addon, with at most 27 entries. Native chat and
completion cannot be shadowed; aggregate `FLAG_LLM` and `FLAG_ANY` are excluded.
The remaining concrete host model flags form the vocabulary, guarded by parity
tests. No labels, priorities, permissions, URLs or query templates are accepted.

The combined page/suffix follows SDK routing bounds: at most 256 ASCII characters
and eight segments, each 1–64 characters from `[A-Za-z0-9._~-]`; `.` and `..`
segments are forbidden. Absolute paths, empty suffixes, percent escapes, queries,
fragments, unknown keys, duplicate keys and noncanonical keys are rejected.
Installed Apps metadata preserves these typed declarations and existing
unavailable-UI omission; it does not discover or promote catalog entries.

Studio declares its existing image, video, speech and sound tabs. Talk declares
its transcription landing page, Face face recognition and Voice speaker
recognition. Talk currently has no selected-model query semantics; its landing
behavior is unchanged. This backend milestone does not yet alter model Open
dispatch or remove historical frontend routes.
