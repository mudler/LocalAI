# Add-on skills and skill shadowing

Status: design note, 2026-10-30. Owner decisions are final; open questions are
marked **Open** with a recommendation. No product code in this change.

## Problem

The chat's skill index has four layers (folder, personal, shared, built-in).
An add-on can bring tools but not the procedure that tells a model how to use
them. The Office add-on reads, edits and creates Word, Excel and PowerPoint
files and reads PDF files. The built-in `xlsx`, `docx`, `pptx` and `pdf` skills
describe another way: a sandbox with Python libraries (`requires: [exec]`).
Without a command runner they show as "unavailable here". With the Office
add-on installed, the model has good tools and a skill that points at a
different method.

## Decisions (owner, final)

1. An add-on ships skills inside its package: `skills/<name>/SKILL.md` plus
   optional reference files. They are in the chat's skill index while the
   add-on is installed, running and on for that chat. Otherwise they are not
   offered. There is no stale copy.
2. The contract is the **name**, as for tools. An add-on skill with the name of
   a built-in skill replaces it; the add-on has priority. The built-in skill
   stays as the fallback when the add-on is absent.
3. A skill can be replaced only if its name is on a replaceable list in core.
   The list starts with `xlsx`, `docx`, `pptx`, `pdf`. It does not hold
   `localai-workspace`, `localai-apps` or `writing-skills`. The install checks
   it, like the other manifest fields.
4. Add-ons that replace the same skill are mutually exclusive. The install
   warns. The administrator chooses the active one in Settings > Tools. Until
   then the oldest install is active, with a notice.
5. The Skills menu shows "provided by <add-on>" for an add-on skill and for a
   built-in skill that an add-on replaces. Skills of a stopped add-on are not
   offered.

## Design

### 1. Manifest

`capabilities` gets one optional field. It is additive; host, API and protocol
versions stay 1, as for `actions`, `uiOnly` and `shadows`. An older host refuses
the manifest at parse (unknown field), so first-party packages that use it ship
with the host release that knows it.

```go
// pkg/addons/contract/skills.go
// SkillDefinition declares one skill the package ships under skills/<Name>/.
type SkillDefinition struct {
    Name string `json:"name"`            // skills.ValidName; also the folder name
    // Shadows names a built-in skill this skill replaces. It must equal Name
    // and be on ReplaceableSkills. Empty: the skill is a plain add-on skill.
    Shadows string `json:"shadows,omitempty"`
}
// Capabilities.SkillDefinitions []SkillDefinition `json:"skillDefinitions,omitempty"`
```

```json
"skillDefinitions": [
  {"name": "xlsx", "shadows": "xlsx"},
  {"name": "docx", "shadows": "docx"}
]
```

`shadows` repeats the name on purpose. It is the explicit intent the install
warning and the replaceable check read from the 64 KiB manifest without opening
the package, and it makes a replacement an act, not an accident of naming. The
description is not in the manifest: `SKILL.md` frontmatter is the one source
(one canonical home per fact).

```go
// pkg/addons/contract/shadows.go (next to ReplaceableTools)
var ReplaceableSkills = map[string]struct{}{"xlsx": {}, "docx": {}, "pptx": {}, "pdf": {}}
func ReplaceableSkill(name string) bool
```

`ValidateManifest` refuses, naming the skill:

- a name that fails `skills.ValidName`, or a duplicate name;
- more than 16 skills;
- `shadows` that is not equal to `name`, or not in `ReplaceableSkills`;
- a plain (non-shadowing) skill whose name is a built-in skill name (a test
  derives the built-in names from the embedded tree and fails when a built-in
  is neither replaceable nor protected by this rule);
- a plain skill whose name does not start with `<addon id>-` or equal the
  add-on id (see section 7).

The check runs at parse, at install and at library refresh. The package
declares the list; the files are checked in section 2.

### 2. Package files and verification

Layout, relative to the package root:

```
skills/<name>/SKILL.md
skills/<name>/references/<file>      (any depth below the skill folder, max 3 levels)
```

Staging (`packages.Installer.Stage`), for a manifest with `skillDefinitions`:

- The set of folders under `skills/` equals the declared names. A missing or an
  undeclared folder fails staging. A manifest without skills must have no
  `skills/` folder.
- Every entry is a regular file or a directory, opened without following links.
  Symlinks, hard links, special files, duplicate paths, file and directory
  collisions: refused, as for the icon and the other assets.
- No executable bit on any skill file (mode `& 0111 == 0`). The only executable
  of a package is the declared one.
- Paths: normalized, ASCII `[a-z0-9._-]` segments, no leading dot, max 128
  bytes, no traversal, no backslash, no percent escape.
- Allowed file types: `SKILL.md` and files ending `.md`, `.txt`, `.json`,
  `.csv`, `.yaml`, `.yml`. Content is valid UTF-8 with no NUL. **No scripts**:
  `.py`, `.sh`, `.js` and the like are refused. A skill that needs a script is
  a built-in skill with `requires: [exec]`, not an add-on skill.
- Limits: `SKILL.md` 64 KiB (the existing `skills.MaxFileSize`); a reference
  file 256 KiB; 64 files and 1 MiB per skill; 4 MiB of skill files per package.
- Frontmatter: the staging code calls the same `skills.Parse` as the runtime.
  `name` must equal the folder and the manifest name; `description` is not
  empty and at most 1,024 bytes after `Clean`; `requires` is read by
  `skills.Requires`. A skill that breaks a rule fails the install; the runtime
  `Invalid` path stays as a second guard (a skill file that changed on disk).

Digest and signature need no new mechanism. The files are part of the package
that the catalog digest pins and that the Sigstore check covers when the source
requires it. Staging records, like the icon, a per-skill list in the receipt:

```go
type PackageSkill struct {
    Name  string
    Files []PackageSkillFile // path, size, sha256 (hex)
}
// PackageReceipt.Skills []PackageSkill
```

The sync (section 3) re-hashes each file it copies against the receipt and
refuses a mismatch, so a package directory edited after commit never reaches a
chat. The packaging build gets one more optional line in `platforms.yaml`,
`skills: addons/office/skills`, laid at the package root like `ui:`; the
release workflow fails if the folder and the manifest disagree (the same check
as staging, run at build time). Skills are platform independent and are copied
into every platform leg.

### 3. Exposing the layer to the chat

**Decision: a fifth layer, `LayerAddon`, held in the workspace store under
`/addon-skills/<addon>/<skill>/...`, shown to the model at
`/workspace/shared-addon-skills/<addon>/<skill>/SKILL.md`.** It mirrors the
built-in layer: `SyncBuiltin` copies the embedded tree into `/builtin-skills`,
and a new `SyncAddonSkills` copies a package's `skills/` into
`/addon-skills/<id>/`. `SharedGuestPath` and the sandbox mount learn the new
guest prefix; the layer is read-only for everyone, written only by the server.

Reasons, against reading the package directory directly:

- `skills.Resolve` reads a `workspace.Store`. The sandbox of a chat mounts
  workspace content. Reading the package directory would need a second reader,
  a second mount and a path to a directory that exists on the owner node only.
- Reference files and the sandbox mount work with no new code once the folder
  is a workspace folder, because they already do for the built-in layer.
- The add-on id is in the guest path. It is the namespace for references and
  shows the provider to the model without extra text.

Why the add-on id and not a flat `/workspace/shared-addon-skills/<skill>`: two
add-ons may ship the same replaceable name (decision 4). The inactive one must
still exist so the Skills page can show it; a flat folder would make one
overwrite the other.

Sync rules (idempotent, like `SyncBuiltin`):

- Runs on the node that holds the owner, in this order: at install commit, at
  upgrade (after the new package is staged and before it is activated), at
  owner start (reconcile), and at uninstall (delete `/addon-skills/<id>`).
- A file that already has the same bytes is left alone. A file or folder that
  the package no longer has is deleted, in one change set. One add-on's sync
  never touches another's folder.
- The folder stays while the add-on is stopped or disabled. Whether it is
  offered is decided per turn (section 4), not by deleting files. This keeps a
  stop cheap and the sandbox mount stable. "No stale copies" is met by the
  index rule plus delete-at-uninstall, not by file churn.

**Open Q1: copy at install, or at first use?** Recommendation: at install and
at owner start. The data is at most 4 MiB per add-on and the copy runs once.

### 4. Index assembly

`skills.Resolve` gets one root per **usable** add-on:

```go
skills.Root{Layer: skills.LayerAddon, Addon: "office",
    Path: "/addon-skills/office", Guest: "/workspace/shared-addon-skills/office"}
```

Layer order, nearest first: folder, personal, shared, **addon**, built-in.
The add-on layer is above the built-in layer and below every layer a person
writes. A user's own `xlsx` skill still beats the add-on's: the person's
explicit choice wins over a shipped default, as for the built-in layer today.

Studio builds the add-on roots per turn from the broker, the same way it lists
tools (`Shadowers`-style lister, one call, no cache beyond the broker's own):

1. List add-ons with skills. An add-on contributes a root when the skill
   layer is usable: installed, running, enabled, allowed for this user
   (`AddonGate`), not in the chat's `addons_off`, and granted by the chat's
   agent if it has one. Anything else contributes no root.
2. Pick the provider of each replaceable name (decision 4):
   - an administrator pin `chats_skill_providers[name] = <add-on>` if that
     add-on is usable; if it is installed but not usable, no add-on serves the
     name and the built-in does, with a note;
   - else the usable add-on with the lowest `FirstInstalledSeq`, ties by id.
3. Pass the choice to `Resolve` as `Options.AddonOrder func(addon, name string)
   int` (0 for the chosen provider, 1 for others). `less` orders add-on skills
   of one name by it before depth and path, so the others get
   `Shadowed = "addon"` and fall out of the index through the existing rule.

`Resolve` also enforces the guard, as a second line behind the install check:
an add-on-layer skill whose name matches a skill of the built-in layer in the
same call and is not on `ReplaceableSkills` is `Invalid` ("the name is a
built-in skill and cannot be replaced"). It is never offered. A built-in skill
that loses to an add-on skill gets `Shadowed = "addon"` and a new field
`Skill.Provider` (add-on id) on the winner and `ShadowedBy` on the loser, for
the UI.

`Skill` gets: `Addon string` (add-on id, add-on layer only), `AddonTitle`.
`SkillID` stays `layer:name`; for the add-on layer it is `addon:<name>`, which
is unambiguous because only one add-on provides a replaceable name at a time
and plain names are unique by section 7.

The path shown to the model is `GuestPath`:
`(read /workspace/shared-addon-skills/office/xlsx/SKILL.md)`. Reference files
are read from the same folder with the file tools, like the built-in
references.

The refresh key already covers `name`, `GuestPath` and `Hash` of every valid,
non-shadowed skill, so an add-on that starts, stops, upgrades (new hash) or
changes the active provider changes the key and the sandbox refreshes. A
switch alone does not.

### 5. Per-chat enable and disable

Two levels, both already in the model:

- **By add-on.** `addons_off` (the chat's list of add-on ids) is the switch the
  Tools menu uses. An add-on that is off for the chat contributes no skill root.
  So switching the Office add-on off for one chat brings back the built-in
  `xlsx`. No new field.
- **By skill.** The Skills menu switches (`skills_off`) work as today with the
  id `addon:<name>`. Switching off the winner of a name uncovers the skill
  below it (the built-in one), as the Skills documentation already says for a
  plain name. For an add-on-replaced skill the menu shows both rows: the
  add-on's (on) and the built-in one marked "replaced by <add-on>" with no
  switch of its own while the add-on serves it. `validSwitch` accepts the layer
  `addon`.

**Open Q2: should switching off the add-on's `xlsx` skill (not the add-on)
re-expose the built-in `xlsx`?** It would, by the existing precedence rule, and
the built-in one then says "unavailable here" when there is no runner.
Recommendation: accept this. It is the documented behaviour of a plain-name
switch, and it is the exact lever someone wants who prefers the sandbox method.

### 6. Distributed mode

Per `.agents/distributed-state.md`, mode **shared**: the skill files live in the
workspace store, which every frontend already reads (the same store as the
built-in and shared layers). The add-on package directory is read only by the
owner node, at sync time; no frontend reads a package directory.

- The runtime state (running, enabled, gate) is read per turn from the broker,
  as tools are. It is derived state, not a new cache; the owner is the source
  of truth. A frontend never decides on its own that an add-on is usable.
- `FirstInstalledSeq` is already needed for tool shadowing; the pin
  `chats_skill_providers` is a runtime setting, stored like
  `chats_tool_providers` (shared through the settings store).
- With `--addons-role frontend`, the sync does not run on the frontend. If the
  owner is down, the add-ons are down, no root is contributed, and the
  built-in skills serve: the same degradation as the tools. The files left in
  the store are not offered.
- Two owner restarts at once are not possible (the owner is a singleton). The
  sync writes through the workspace change set, so a concurrent reader sees
  the old or the new folder state per file, never a partial `SKILL.md`; the
  refresh key (hash) covers the gap.

**Open Q3: should the sync run on the owner or on the frontend that serves the
install request?** Recommendation: on the owner only. It holds the verified
package and the receipt; a frontend would need the bytes over the wire.

### 7. Name rules between add-on skills

The model sees plain names. Rules:

- A **replaceable** name (`xlsx`...) may be shipped by several add-ons; only one
  is active (section 4).
- A **plain** name must be `<addon-id>` or start with `<addon-id>-`
  (`office-quickstart`). The install enforces it, so two add-ons cannot
  collide on a plain name by construction, and a plain name never equals a
  built-in name. The cost is a less pretty name; the benefit is that there is
  no runtime conflict case, no precedence among add-ons, and the model can tell
  where a skill comes from.
- A user's own skill of any name beats the add-on layer (section 4).
- A name that still collides at runtime (an old package, an edited store): the
  skill of the lowest `FirstInstalledSeq` wins, the other is `Shadowed` and the
  Skills page says so. This is a guard, not a feature.

**Open Q4: prefix rule or free names?** Recommendation: the prefix rule. Free
names would need a second chooser UI for something no add-on needs today.

### 8. Token budget

- Replacing a built-in skill swaps one index line for one line; the index grows
  by nothing for the four Office skills (the four built-in lines are replaced,
  not added).
- A plain add-on skill adds one line: the name, the description (at most 1,024
  bytes, the guideline is 300) and the path (about 70 bytes). With the 16-skill
  cap per add-on, one add-on costs at most about 6 KiB; the index limits stay
  100 entries and 16 KiB, and nearer layers come first. The add-on layer sits
  above the built-in layer, so when the cap cuts the index, built-in lines go
  first, not add-on lines.
- Nothing from the skill body or references is read until the model opens it.
- Only usable add-ons contribute lines, so a stopped add-on costs nothing.

**Open Q5: should the add-on cap be lower than 16?** Recommendation: keep 16;
no add-on is near it, and a lower number is a breaking change later.

### 9. Worked example: the Office add-on

The add-on ships `skills/xlsx`, `docx`, `pptx`, `pdf`, each `shadows` itself.
All four are written for the tools the model sees as
`addon__office__<tool>` (read, edit, create) and need no command runner, so
they have no `requires`. Each is a short procedure (target 1 to 2 KiB, no
scripts, no code to run):

| Skill | Tools | Procedure outline |
|---|---|---|
| `xlsx` | `read-xlsx`, `edit-xlsx`, `create-xlsx` | Read first (sheet names, then one sheet). Edit cells by sheet and address; do not rewrite the file to change a cell. Create a new workbook only when no file exists. Formulas are strings starting with `=`; the file is not recalculated, say so. Report the sheet and range changed. Fall back: the built-in skill does not apply (no runner); say that a feature the tools lack (charts, pivots) cannot be done. |
| `docx` | `read-docx`, `edit-docx`, `create-docx` | Read to get the paragraph indices, then edit by index. Edit the smallest paragraph range. Do not recreate a document to fix a typo. Tables are read-only in edit if the tool says so; state the limit. |
| `pptx` | `read-pptx`, `edit-pptx`, `create-pptx` | Read to get slide and shape ids, edit text by shape id, create a new deck from an outline (title and bullets per slide). Layout and images are outside the tools; say so before promising them. |
| `pdf` | `read-pdf`, `create-pdf` | Read page ranges (up to 50 pages per call, continue with `firstPage`). A scanned page has no text: say that and stop, do not guess. Create from text sections. There is no edit; to change a PDF, create a new one. |

Each skill ends with a short "when the tools cannot do it" section: tell the
user what is outside the tools (macros, charts, tracked changes, form fields)
instead of describing a manual method. Files are paths in the user's files
(`/reports/plan.xlsx`); results come back to the same files, and the tools
save a second file next to one that changed meanwhile. The text is plain
Simplified Technical English; the exact wording is written in phase 4 with the
tool descriptions open next to it, so the two cannot drift. A test (below)
checks that every tool name a skill mentions exists in the manifest.

When the Office add-on is stopped, off for the chat or removed, the built-in
four return, as unavailable in a chat with no runner, as today.

### 10. Migration of the built-in text

Nothing moves. The embedded tree, `SyncBuiltin`, the NOTICE file and the
`requires: [exec]` markers stay. The built-in skills are the fallback in
decision 2; they are not copied, forked or edited. The docs line "For most
edits the Office add-on's tools are simpler" becomes "When the Office add-on
runs, its own skills replace these four". There is no data migration: the new
layer is empty until an add-on with skills is installed, the new manifest field
is optional, `chats_skill_providers` defaults to empty, and the chat records
need no column (the switch id `addon:<name>` fits the existing list).

### 11. UI

- **Skills menu** (over the message box) and **Skills page**: each add-on skill
  line has the label "provided by <add-on title>". A built-in skill that an
  add-on replaces stays in the list, dimmed, with "replaced by <add-on title>";
  it has no switch while replaced. A skill of an inactive add-on (same
  replaceable name, not chosen) shows "not active: <chosen add-on> provides
  it". Skills of a stopped add-on are absent from the menu; the Skills page
  lists them in a collapsed "Add-ons that are not running" group with the
  reason, so an administrator can see why. Keys under `skills.provider.*` in
  the 9 locales.
- **Settings > Tools** (built on `refactor/builtin-file-tools`; this note does
  not edit it): below the tool rows, a "Skills" group with one row per
  replaceable name that at least one installed add-on ships. The row shows the
  provider (`Built-in` or the add-on), a select of the candidates, and the
  notice "N add-ons replace <skill>: choose which one to use" while no pin
  exists (the oldest install is active). Same component and API shape as the
  tool provider rows; the setting is `chats_skill_providers`.
- **Install dialog and catalog details**: the warning of decision 4, with one
  line per replaced skill: "This add-on replaces the built-in skill <skill>."
  and, when another add-on already does, the sentence of the decision. The
  install is allowed. The warning data comes from the manifest and the list of
  installed manifests; no package read.

### 12. Tests

Ginkgo, as in the rest of the repo.

- `pkg/addons/contract`: `skillDefinitions` parse; refuse bad name, duplicate,
  more than 16, `shadows` not equal to name, `shadows` not on the list, a plain
  name equal to a built-in name, a plain name without the prefix. A test that
  every `ReplaceableSkills` name exists in the embedded built-in tree and that
  `localai-workspace`, `localai-apps`, `writing-skills` are absent from the list.
- `core/services/addons/packages`: staging accepts a good `skills/` tree;
  refuses missing and undeclared folders, symlink, hard link, exec bit, script
  extension, non-UTF-8, NUL, oversize file, too many files, bad frontmatter,
  folder name different from `name`, a `skills/` folder with no declaration;
  the receipt holds the digests.
- `core/services/studio/skills`: `LayerAddon` precedence; add-on over built-in;
  user layers over add-on; `AddonOrder`; the guard (`Invalid`) for a
  non-replaceable collision; `Provider` and the shadowed marks; the index line
  and the budget with and without the add-on.
- `core/services/studio` (`skillkey_internal_test.go` style): the refresh key
  moves when an add-on starts, stops, upgrades or when the pin changes, and not
  on a switch; `addons_off` removes the roots; a stopped add-on gives the
  built-in back; an agent scope keeps working with add-on names;
  `validSwitch` accepts `addon:<name>`.
- Sync: copy, idempotence, removal of a dropped file, uninstall, digest
  mismatch refused, another add-on's folder untouched.
- Distributed: with two studio services on one store, both see the same index
  after the owner syncs; with the owner role down, the built-in serves.
- Pin and default: no pin gives the oldest install; a pin to a stopped add-on
  gives the built-in with a note; two add-ons shipping `xlsx` give one active.
- Office: a test reads the four `SKILL.md` files and checks that each parses
  with `skills.Parse`, has `shadows` itself, has no `requires`, and mentions
  only tools that exist in `manifest.json`.
- UI (Vitest/Playwright, as the Skills menu has today): labels, the dimmed
  replaced row, the Settings > Tools row, the install warning.

### 13. Docs

- `docs/content/features/addons.md`: manifest section (`skillDefinitions`,
  limits table, `ReplaceableSkills`), package staging rules, the Office
  section (its four skills), distributed mode (mode: shared, owner syncs).
- `docs/content/features/chats.md`, Skills: five layers, the add-on layer,
  precedence, `provided by`, the Settings > Tools chooser, the reworded Office
  sentence.
- A short "write an add-on skill" paragraph for add-on authors: layout, no
  scripts, name rule, how to replace a built-in one.

## Phased plan

Each phase is one PR-sized step that leaves `master` working.

1. **Loader and layer.** `SkillDefinitions` in the contract (parse and name
   rules only, no `shadows` yet); staging checks and receipt digests;
   `SyncAddonSkills` on install, upgrade, start and uninstall; `LayerAddon`,
   guest path, `SharedGuestPath`, `validSwitch`; per-turn roots from the
   broker (usable and `addons_off`); index and refresh key. An add-on skill
   with a plain name already works end to end. Tests for all of it.
2. **Shadowing, replaceable list, install warning.** `shadows`,
   `ReplaceableSkills`, the runtime guard in `Resolve`, `Skill.Provider`,
   `AddonOrder`, `FirstInstalledSeq` use, `chats_skill_providers` and its
   settings validation, install and catalog warning data in the API.
3. **UI.** Skills menu and page labels, Settings > Tools chooser (after
   `refactor/builtin-file-tools` lands), install dialog warning, locale keys.
4. **Office skills.** The four `SKILL.md` files in `addons/office/skills`,
   `platforms.yaml` line, release check, manifest entries, docs for the Office
   section, the consistency test with the tool manifest.

Docs for each phase ship with the phase.

## Open questions (summary)

| # | Question | Recommendation |
|---|---|---|
| Q1 | Copy skill files at install, or lazily? | At install and owner start. |
| Q2 | Does switching off the add-on's skill re-expose the built-in one? | Yes, by the existing plain-name rule. |
| Q3 | Which process runs the sync? | The owner only. |
| Q4 | Prefix rule for plain add-on skill names? | Yes, `<id>` or `<id>-...`. |
| Q5 | Cap of skills per add-on? | 16. |
| Q6 | Allow reference scripts in add-on skills? | No in phase 1; a later change can allow `.py` files with `requires: [exec]` once there is a use. |
| Q7 | Pin per skill name, or one pin per add-on? | Per skill name, like the tool pins; an add-on that replaces four skills needs four pins, but the Settings row can offer "use <add-on> for all it replaces". |
| Q8 | Show a notice in the chat when the built-in serves because the chosen add-on is stopped? | Yes, one line in the turn note, as for tools. |
