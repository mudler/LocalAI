+++
title = "Manage add-ons, data and updates"
description = "Turn add-ons on and off, back up and restore add-on data, and check for and apply updates."
weight = 14
+++

Turn add-ons on and off, back up and restore their data, and check for and apply updates. An administrator does all of this on the **Add-ons** page. For the short version, see the [Add-ons user guide]({{% relref "/addons/user-guide" %}}).

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

### Add-on page addresses and model actions

Open add-on pages through My add-ons or their declared navigation entries.
The canonical address is `/app/addons/<id>/<page>`, with an optional relative
suffix beneath the declared page. Historical standalone generator and add-on
addresses are not registered and show Not Found. There is no compatibility
support window for these unreleased routes.

Installed-model inspectors, gallery Open actions and model details resolve
`capabilities.ui.modelActions` from the authenticated installed Apps list.
A declaration names a model capability, a declared page and an optional safe
relative path. Core does not map provider IDs to private page paths.

One usable provider opens with one click. Multiple providers appear as named
choices in the action menu, sorted by provider ID. When none is usable, the
capability label remains without a dead link or an install promotion.
Chat and completion keep their native destinations.

The host sends the selected model as a URLSearchParams value under `model`.
Slashes, colons, Unicode and query delimiters remain part of the model name.
Each click refreshes the actor-filtered installed list and checks the captured
provider, page, path, generation, immutable entry and authentication authority.
A changed installation or session refuses the stale action instead of choosing
another provider. Projection does not load add-on code or query the catalog.
Talk still lands on its existing page; this does not change its pipeline.

The legacy navigation switch changes the sidebar and hubs only. It does not
restore removed addon-specific route registrations.

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
that the person added in the browser, the same list as Chat. The words are the `talk` locale
files of the add-on, in every language of the web UI.

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

Canonical links open the add-on shell, which reports unavailable installations. The pages call
`/api/fine-tuning/*` and `/api/quantization/*` with the signed-in user's own session (see
"Core routes from add-ons"); the core keeps these routes and checks every call again.
Both add-ons offer chat tools that start a job. In the chats a tool call starts it at once; on other surfaces it is a proposal that waits for a person to confirm. They
reach the engine through [Host operations]({{% relref "/addons/contract-reference/host-operations#host-operations" %}}), and a tool can control only
a job that a tool started. See [Fine-tuning]({{% relref "/models/training/fine-tuning" %}}#fine-tuning-is-an-add-on) and
[Quantization]({{% relref "/models/training/quantization" %}}#quantization-is-an-add-on).

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
[Add-on pages]({{% relref "/addons/contract-reference/apps-and-pages#add-on-pages" %}}) below.

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
the scope. Purging the data also removes the OAuth client an administrator approved for
the add-on. An uninstall keeps that client, so a reinstall needs no new approval.
Purging the data clears `needs-recovery`.

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
  names. Its label is not compared as a version. An explicit check (**Check now** on the
  Updates page, **Check again** or the automatic update) resolves the tag in the registry and keeps the
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
setting **Update add-ons automatically** (`auto_upgrade_addons` in the runtime
settings, flag `--auto-upgrade-addons` or `LOCALAI_AUTO_UPGRADE_ADDONS`) is off by
default, like the matching setting for backends. When you turn it on, the owner checks
the library of every installed add-on shortly after start and then every six hours,
the same schedule as the backend update check. When a library lists a newer version
for the channel the add-on was installed from, the owner runs the same pinned upgrade
that the **Update** action runs. The digest and signature checks, the compatibility
checks, the data backup and the recovery behavior above all apply, and an upgrade that
fails any of them changes nothing and is written to the log. An automatic upgrade never
changes the library, the channel or the trusted signer. A candidate that failed is not
tried again until the library lists a different one. With the setting off, nothing
upgrades by itself. Each scheduled pass reads the current owner-host policy, so
changes take effect without a restart. If the owner policy is unavailable, the
pass skips upgrades and logs an error; it does not use frontend-local settings.

At the end of a pass the owner writes one item to the inbox of the administrators when it did something: "Add-ons: 1 updated automatically, 1 waiting for approval". It names the add-ons that were upgraded, the ones that failed, and the ones that wait for an administrator because the new version needs host access that nobody approved (an automatic upgrade never approves). A failure or a wait links to the updates that need you. A pass that did nothing writes nothing, and the same add-on failing or waiting for the same candidate again is not written again, also after a restart. The owner is the only writer, so the item exists once in a cluster. With the setting off nothing is written, and add-on updates are not in the backend digest of the inbox; see [Updates in the inbox]({{% relref "/models/backends#updates-in-the-inbox" %}}).

### On the Updates page

**Admin → Updates** lists the add-ons that have an update, next to the backends, with
the same states: ready, updating, failed, held for approval, and cannot update. It shows
the time of the last add-on check and **Check now** reads the library of every installed
add-on again. A held upgrade opens the same approval dialog as in Apps. An upgrade or a
check needs the administrator's own browser session, as everywhere else. See [Backends]({{% relref "/models/backends#the-updates-page" %}}).

### In the page

The Installed list and the management sheet show a badge with the candidate version and
the short digest, and the compatibility reasons of an incompatible candidate. An
administrator gets **Check again** on one add-on, with the time of the last check, and
**Update** when the state is `update-available`. **Update** opens a confirmation that names the
library, the channel, the installed and the new version and digest, and says that the data
of the add-on is backed up first. It then shows progress and, on failure, the error and the
kept version. Anyone else sees the status without actions.

The **Update** action is on the card of the add-on in the list and on its entry in **Find
add-ons**, next to the update notice, and in its details. All of them open the same
confirmation.

When the notice is shown but the update cannot start, the card says why instead of hiding the
action:

| What the card says | Cause | What to do |
|---|---|---|
| Only an administrator can update it | Your account is not an administrator. | Ask an administrator. |
| The update is not confirmed yet | The check did not resolve a candidate to pin (no digest). | Select **Check again**. |
| The library names no build to install | A check ran and the owner still reports an update without a digest to pin. | Check again later. |
| The last check failed | The library could not be read. | Select **Check again**. |
| The update cannot run here, with the reasons | The state is `incompatible`. | None until the host or the platform fits. |
| The library lists this version with different contents | The state is `integrity-error`. It is never an update. | None. |
| The data needs recovery first | The add-on is in the recovery state. | Restore a backup in the details, then update. |
| The latest check found no update | The list showed a cached notice that a newer evaluation no longer supports. | Select **Check again**. |

After **Check again**, the card shows the answer of the check: "Checked just now" (or the
time of the check) and what the check found, for example that the add-on is up to date. When
the check found an update with a digest, **Update** replaces the reason. A check that fails
shows why, and you can run it again.

The Apps page has no bulk update and no check of all add-ons. **Open Updates** in its
bar goes to the Updates page, where **Update N ready** runs the add-on updates one after
another (a failure does not stop the others, and a held one waits for approval), and
**Check now** reads the library of every installed add-on again. A check applies
nothing, and an add-on that cannot be checked does not stop the others. Add-ons that
share a library are all checked, whether they are turned on or off, and every one with a
newer build is listed together. An installed version that this server can no longer run
cannot be turned on: the server says so and you update it first.

An update started from the list, the details, **Find add-ons** or the Updates page keeps
running when you leave the page. It appears in the Activity page under the **Add-ons**
filter, with progress, **Cancel** and **Retry**. Cancel stops the request, and the page then
reads the operation to report what the owner did. The list of updates is kept by the
browser that started them, so it is lost on a page reload. Background tracking continues
across navigation only within the same authentication session. An authentication
refresh, account or permission change, logout initiation, or removal of the
authentication provider expires pending observations. Late replies then cannot
verify an update or publish its result into the new session. This does not undo
an operation already accepted by the server.

While the add-on service restarts (an upgrade restarts it), the page cannot read the
operation and the server answers 503. The page then waits longer before each new read, 2,
4, 8 and up to 30 seconds, and the upgrade says *Waiting for the add-on service to start*
with the time of the next check; **Check now** in the details reads it at once. The Tools
menu of a chat does the same for the state of the Sandbox: it says that the Sandbox is
starting and reads its state again by itself, with the same back-off.
