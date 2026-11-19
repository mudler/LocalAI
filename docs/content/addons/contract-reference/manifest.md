+++
title = "The manifest and the runtime"
description = "The manifest, the runtime wire version, the default limits and verification."
weight = 40
+++

Look up the add-on manifest, the runtime wire version, the default limits and the verification of a package. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

## Manifest

`ParseManifest` accepts a single JSON object, at most 64 KiB, and rejects unknown
fields, duplicate keys, trailing values, and nesting beyond 64 levels.
`ValidateManifest` additionally checks the supplied host compatibility tuple.
See `tests/addons/fixtures/contracts` for service-only, headless service/tool,
page/navigation, chat-action-only, and incompatible-host fixtures.

The required fields are `schemaVersion` (1), `id`, `version`, `executable`, `os`,
`arch`, `host`, `api`, `protocol`, `dataFormat`, and `capabilities`. The optional
`osSettings` block holds OS-specific runtime settings (see [OS settings](#os-settings)). Compatibility
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

### OS settings

`osSettings` is optional. It holds runtime settings that apply on one OS only. Each key
is an OS, and the value is the settings object of that OS. When `osSettings` is absent,
the executable runs natively on `os` and `arch` with no OS-specific setting.

This version defines one key and one setting:

```json
"osSettings": {
  "windows": {
    "execution": "wsl2"
  }
}
```

`osSettings.windows.execution` is the execution environment of the executable. The only
value is `"wsl2"`, and only with `"os": "windows"` and `"arch": "amd64"`. Such a
package installs on a Windows host, but its executable is a linux/amd64 program (no
`.exe` name) that the owner runs in a WSL 2 distribution. The owner talks to it over
its standard streams (see [Packages and runtime](packages-and-runtime/)). Use it only
for an add-on that cannot run on Windows itself, such as the Sandbox add-on.

The host refuses:

- an `osSettings` object with no OS key,
- an OS key other than `windows`, and the `windows` key on a package whose `os` is not
  `windows`,
- an unknown field in an OS object,
- a missing `execution`, or any value other than `"wsl2"` on windows/amd64.

A manifest that sets `osSettings` must declare `featureLevel: 10`. An add-on author does
not write `osSettings` in the manifest template. The packager takes it from the
`execution` key of the platform row in `platforms.yaml`, and the package verifier
refuses a manifest whose `osSettings` differs from the row.

### Feature level

The four compatibility ranges above are integers that every published add-on states
(`host`, `api`, `protocol`, `dataFormat`). A new optional manifest field cannot use them
without making every published add-on incompatible, so the host also has a **feature
level**: one integer that counts the additive manifest features it understands.

- The host declares its level (`contract.HostFeatureLevel`). This version has level 10.
  A host that predates the levels is level 1.
- A manifest may set `featureLevel` (a positive integer, default 1). It states the level
  the add-on needs. A manifest that uses a field of a higher level must say so: the
  parser refuses a manifest that uses `capabilities.resources.agentScoped`,
  `capabilities.resources.nounPlural` or `i18n.<language>.resources` and does not declare
  `featureLevel: 2`. `capabilities.ownerData` is level 3.
  The setup text (`setup` on the manifest, `kinds[].setup` and the `setup` of a kind translation) is level 4.
  `kinds[].toolOnly` is level 5.
  `capabilities.executionProvider.jobs` is level 6.
  `capabilities.client` is level 7. A wildcard tail (`{name...}`) as the last segment of a client
  route is level 8; a host of level 7 refuses such a manifest with the reason below.
  `capabilities.modelSearch` (a source of the model-search slot) is level 9.
  `osSettings` (see above) is level 10.
- A host with a lower level refuses the add-on with the plain reason **needs a newer
  LocalAI**. The add-on is not installed and not upgraded. A failed install or upgrade has
  the cause `needs_newer_host`, and an update check shows the add-on as incompatible with
  the reason `feature-level`.
  The Add-ons page and the install and upgrade dialogs show it as "This add-on needs a newer
  LocalAI." in every language of the interface.
- The library entry has the optional `requires.featureLevel`. The host reads it before it
  downloads the package. If the entry says nothing, the manifest is checked when the
  package is staged, with the same reason.

An older host cannot read the new names. It refuses a manifest with an unknown field, and
it also refuses a **library index** that has an unknown field, because the index reader is
strict too. So one entry with `requires.featureLevel` makes the whole index unreadable
for a host that predates the field. For that reason the `catalog` command of the packager
writes the field only when it gets `--feature-level-in-index`, and the committed index
does not have it. Without it an older host still lists the add-on and refuses it when it
stages the package. Turn the flag on once no host without the field needs the library.

Capabilities are independent: `service` is a boolean, `tools` is a unique list
of namespace names, and optional `ui` contains required `entry` and `sdk` range,
optional `css`, `pages`, `navigation`, and `chatActions` lists. Navigation names
must refer to declared pages. UI assets must not duplicate each other. Absent UI
requires neither assets nor SDK compatibility. Declared UI must be consistent;
an empty object does not count as a valid declaration. A chat action is either
a bare identifier (inert: it has no placement and is never drawn) or a full
declaration, and `ui.slots` and `ui.renderers` place components in closed host
slots (see [Extension points and slots]({{% relref "/addons/contract-reference/apps-and-pages#extension-points-and-slots" %}})). They are
host-controlled entries, not DOM hooks or permission to read chat history. Platform tuple equality is compatibility only,
not evidence that any release platform has passed native runtime tests.

Four optional fields describe how an addon appears as an app (see [Apps]({{% relref "/addons/contract-reference/apps-and-pages#apps" %}})).
Every one is optional, so earlier manifests stay valid and a headless manifest
needs none of them.

| Field | Meaning and bounds |
|---|---|
| `launchMode` (top level) | Absent or `always`: the owner runs the addon while it is enabled. `on-demand` is reserved and, like any other value, is rejected at install. |
| `ui.mainPage` | The page the Open action navigates to. It must be one of `ui.pages`. Without it the addon has no Open action. `ui.entry` keeps its meaning: the packaged ES module. |
| `ui.icon` | A packaged image: `.png`, `.webp`, `.jpg` or `.jpeg`, a normalized package-relative path of at most 256 bytes, at most 256 KiB. A remote URL, a `data:` URL, an absolute path and a traversal are rejected. It cannot also be the module or a stylesheet. |
| `ui.title`, `ui.description` | Plain text: one string, or a map of BCP 47 language tag to string (at most 16 tags). At most 80 and 280 characters. Markup characters (`<`, `>`), control characters and direction overrides are rejected. |

### Translated text

The English text of a manifest is the source and the fallback. An add-on can
carry the text that LocalAI shows to people in other languages, so the library
list and the add-on cards read in the language of the user. Two fields do this.

**The title and the description of the add-on** use the language maps of
`ui.title` and `ui.description` (see the table above). Write the English text
under `en`, then one entry for each language:

```json
"title": { "en": "Tables", "it": "Tabelle", "de": "Tabellen", "pt-BR": "Tabelas" },
"description": { "en": "Databases of tables.", "it": "Database di tabelle." }
```

**The kinds of a connector add-on** use the optional top-level field `i18n`.
It is an optional addition to `api` 2. Its shape is closed on purpose:

```json
"i18n": {
  "it": { "kinds": { "telegram": { "title": "Telegram", "description": "Parla con uno dei tuoi agenti salvati da un bot Telegram." } } },
  "pt-BR": { "kinds": { "telegram": { "title": "Telegram", "description": "Converse com um dos seus agentes salvos por um bot do Telegram." } } }
}
```

| Rule | Value |
|---|---|
| Language tags | A BCP 47 subset: two or three letters and up to three subtags, such as `it`, `pt-BR`, `zh-CN`. At most 16 languages. A tag is unique whatever its case. `en` is refused: the English fields are the manifest. |
| Fields | `kinds.<id>.title`, `kinds.<id>.description` and `kinds.<id>.setup`. The kind must be declared in `capabilities.connector.kinds`, and a description or a setup can be translated only when the English kind has one. An unknown field fails the strict parser, as does a duplicate key. |
| Text | Plain text, as for `ui.title`: no `<` or `>`, no control characters, no direction overrides, no leading or trailing space. A title has at most 80 characters, a description at most 280 and a setup at most 120. All the translated text of one manifest has at most 12 000 characters. The whole manifest keeps its size limit. |
| Fallback | A field that a language leaves out, and every language that the manifest does not list, shows the English text. Each field falls back on its own. |
| Not translatable | Tool descriptions, input schemas and skills are written for the model and stay English. User setting keys and values are identifiers. The text of the add-on's own pages is part of the UI bundle and has its own strings. |

A host that does not know `i18n` refuses a manifest that has it, as it refuses
any unknown field, so a package with `i18n` needs a host that reads it. The
language maps of `ui.title` and `ui.description` are older and every host reads them.
The field is part of the manifest, so the package digest and its signature cover
it: a changed translation is a new package.

**What the host does with it.** The library index carries the translated title and
description of each entry (`ui.titles` and `ui.descriptions`, by language), so the library
lists them before the add-on is installed. The generator of the index
(`scripts/addons/catalog.sh`) copies them from the manifest, so they are never
edited in the index. A search matches the English and the translated text.

`GET /api/addons/apps` and `GET /api/chats/connectors/kinds` return the text of every
language in `titles` and `descriptions`. When the request names a language, each item that
translates it also has `language` and the resolved `localizedTitle` and
`localizedDescription` (`localized_title` and `localized_description` for connector kinds). The
language is the `lang` query parameter; without it, the `Accept-Language` header (quality values
are honored); without both, English and no extra field. An unknown or malformed `lang` is
English and the header is not read behind it. `title` and `description` never change, so an
existing client sees the same answer as before. The UI reads the maps and picks the language of
the user itself.

### Setup text

Some add-ons do nothing until the person gives them something: a bot token, an access
token, an account on another service. The setup text says so in one short phrase, so the
UI can show "Needs a bot token from @BotFather" before the person starts. It is plain
text that completes the sentence "Needs ...". Write it without a capital letter, a final
full stop or a verb of its own: `a bot token from @BotFather`, `an access token and the
repositories it may use`.

It has two places, and both are optional:

- A **connector kind** declares `kinds[].setup` in `capabilities.connector`.
- An **add-on that has no connector kind** (a tool add-on) declares the top-level
  `setup`. It is one string or a map of language tag to string, like `ui.title`.

```json
"featureLevel": 4,
"setup": { "en": "an access token", "it": "un token di accesso" }
```

| Rule | Value |
|---|---|
| Length | At most 120 characters, and not empty. |
| Text | Plain text, as for `ui.title`: no `<` or `>`, no control characters (so no line break and no tab), no direction overrides, no leading or trailing space. It is not Markdown. |
| Translation | For a kind, `i18n.<language>.kinds.<id>.setup`, only when the English kind has a setup. For the add-on, the language map of `setup`. A language that leaves it out, and every language that the manifest does not list, shows the English text. |
| Feature level | Level 4. A manifest that uses it must declare `featureLevel: 4`. A host with a lower level refuses the add-on with "needs a newer LocalAI"; a host that predates feature levels refuses the unknown field. Do not add it to a package that older hosts must still install. |

**What the host does with it.** `GET /api/chats/connectors/kinds` returns `setup` (English),
`setups` (every translation, by language tag) and, when the request names a language that
the kind translates, `localized_setup`. `GET /api/addons/apps` returns `setup`, `setups` and
`localizedSetup` for an add-on in the same way. The fields are left out when the manifest
declares no setup. The language is chosen as for the other texts: the `lang` query parameter,
else `Accept-Language`. Each localized field falls back to English on its own.

**Authoring.** Translate by hand, in the tone of the main locale of the language. Keep product
names (GitHub, Slack, Telegram, Discord, Jellyfin, Home Assistant, LocalAI) as they are. Keep the
meaning of the English text; do not add claims to it. The packaging tests require every first-party
add-on to have the title and the description in `en`, `it`, `de`, `es`, `fr`, `id`, `ko`, `pt-BR`
and `zh-CN`, and every connector kind to have the same fields as its English kind in each of them.

### Skills

An add-on can ship skills, the procedures a chat model reads before a task
(see [Skills]({{% relref "/features/chats-and-agents/chats-skills#skills" %}})). The model loads one by
its name with the chat's `load_skill` tool, which also names the files of the
skill's folder, such as `references/`, for `read_file`. The manifest declares
them in `capabilities.skillDefinitions` and the package holds the files:

```json
"skillDefinitions": [
  { "name": "office" },
  { "name": "office-quickstart" }
]
```

```
skills/<name>/SKILL.md
skills/<name>/references/<file>      (up to 3 folders below the skill folder)
```

The description is not in the manifest: the `SKILL.md` frontmatter is its only
source. The manifest is checked at parse, at install and when a library is
refreshed:

| Rule | Limit |
|---|---|
| Skills per add-on | 16 |
| Name | `[a-z][a-z0-9-]{0,63}`, unique in the manifest |
| A plain name | the add-on ID, or the ID and `-` and more (`office-quickstart`), so two add-ons never ship one name |
| A reserved name | refused for a plain skill: core names `writing-skills`, `localai-workspace`, `localai-apps`, `assemble-media`, `media-prompting`, and document names `docx`, `pptx`, `pdf`, `xlsx` |
| `shadows` | optional; must repeat `name` and name a slot: one the skill declares with `slot` (needs `api` 2), or a default slot (`xlsx`, `docx`, `pptx`, `pdf`) |
| `slot` | optional, needs `shadows` and `api` 2; see Document skill providers |

Staging checks the files against the manifest. The folders under `skills/`
must be the declared names, and a package that declares no skills must have
no `skills/` folder. Every entry is a plain file or a folder: links, special
files and executable files are refused. A path segment uses lowercase ASCII
letters, digits, `.`, `_` and `-`, does not start with a dot, and a path is at
most 128 bytes. A file is a `.md`, `.txt`, `.json`, `.csv`, `.yaml` or `.yml`
file of UTF-8 text with no NUL byte. **There are no scripts**: `.py`, `.sh`,
`.js` and other files are refused. A procedure can declare `requires: [exec]`, but its skill tree still contains no executable files. `SKILL.md` is at most 64 KiB and must
parse with a `name` equal to its folder and a `description`; a reference file
is at most 256 KiB; a skill has at most 64 files and 1 MiB, and a package at
most 4 MiB of skill files. The receipt records each file with its size and
SHA-256 digest.

The add-on owner copies the skills into the chats' add-on skill layer, in the
workspace at `/addon-skills/<add-on>/`, when it installs or upgrades the
add-on and every time it starts, and deletes them when the add-on is
uninstalled. Each copied file is checked against the digest in the receipt,
so a package folder changed after install never reaches a chat. A copy that
fails is logged; the add-on itself still installs and runs. The files stay
while the add-on is stopped. A chat offers them only while the add-on is
installed, running, usable by the user and on for that chat, and for a chat
with a saved agent only when the agent grants the add-on.

#### Document skill providers

A slot is a skill name that one add-on serves in place of another. The host
holds no list of formats: the add-on that provides a skill declares its slot.
For manifests that declare nothing, the host reads four default slots (`docx`,
`pptx`, `pdf`, `xlsx`) from a data file in the contract, so existing packages
keep working.
Core does not embed these document procedures. Its remaining built-in skills cannot be replaced.
The existing `shadows` declaration identifies a document provider:

```json
"skillDefinitions": [
  { "name": "xlsx", "shadows": "xlsx" },
  { "name": "docx", "shadows": "docx" }
]
```

A manifest of `api` 2 can declare the slot, which also adds a format the host
has never heard of. `extensions` and `words` tell the host which files the
skill covers and how to find the skill by name when a chat meets such a file,
and `library` is the hint it gives for a script:

```json
{ "name": "odt", "shadows": "odt",
  "slot": { "kind": "documents", "extensions": [".odt"], "words": ["odt"], "library": "odfpy" } }
```

`kind` is `documents`: the skill is offered only while the add-on's commands
provider reports isolated execution. At most 8 extensions (lower case, with the
dot) and 8 words. A slot may not be the name of a protected core skill.

Unknown unprefixed names remain invalid. Ordinary add-on skills keep the add-on namespace rule.

- **In a chat.** `load_skill xlsx` returns the eligible provider's instructions.
  Folder, personal, and shared overrides still take precedence.
- **Eligibility first.** A provider must be enabled, running, visible to the user, and on for the chat.
  A saved agent must grant the add-on explicitly.
  Sandbox document procedures also require usable isolated execution through the selected Sandbox command provider.
  A host command runner or an installed but unselected Sandbox does not qualify.
  Office procedures require no command execution.
- **Several providers.** An administrator chooses in **Settings > Tools**, using `chats_tool_providers`, for example `{"xlsx": "office"}`.
  Without a choice, the eligible provider installed first serves.
  An unavailable or ungranted pin stays unavailable; it does not select another provider silently.
  `GET /api/chats/admin/tools` lists skill providers and candidates in `skills`.
- **No core fallback.** A stopped, disabled, OFF, removed, or ungranted provider does not restore a built-in document procedure.
  If no provider qualifies, the document authoring skill is absent or explains its unavailable pin.
  Core document-text extraction, `read_file`, and `search_files` remain unchanged.
- **Before install.** The library and installed views retain `skillShadows` as the compatibility field.
  The install dialog describes the skill provided, not a nonexistent built-in replacement.
- **On the Add-ons page.** Details list the document skills an add-on provides.
  An administrator can use **Use for xlsx in the chats** to choose between providers.
  This writes the same setting as **Settings > Tools**; it does not grant access to an agent.

The workspace store shares skill files across frontends; only the owner writes verified package contents.
Each turn reads provider availability from the broker. When the owner is down, add-on document skills do not fall back to core.
Saved skill names and explicit pins remain intact.
Legacy built-in document OFF preferences migrate to add-on OFF preferences through the persisted settings path.
See [Document skills]({{% relref "/features/chats-and-agents/chats-skills#document-skills" %}}) for setup and preference behavior.

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
`sdk_incompatible`, with the message above. A package that does not match its
library entry, or is not a valid add-on package, is 403 with error type
`package_unverified` and a message that says nothing was installed. A failed
install or upgrade can also name its step in the error type: `library_unavailable`
(the library could not be read), `library_conflict` (two enabled libraries list the
add-on), `release_not_found` (the library has no such add-on or version for this
platform), `package_fetch_failed` (the package could not be downloaded) and
`install_storage_failed` (the package could not be written to the add-on storage).
The owner writes the underlying error to its log as `Addon operation failed`, with
the action, the add-on ID and the operation ID. Every other failed install keeps the
generic `Addon request denied` message. The owner reads the library once on demand
when its startup refresh left nothing in the cache, so an install does not depend on
that refresh having finished. If the administrator sends the same
operation ID again, the owner returns the stored failure, so the repeat gets the
generic message and not the refusal text. Use a new operation ID to see the
refusal again.

### The SDK 2 surface

An add-on page gets these from the SDK object. The page component also receives the
query string of its address as `search`, next to `page` and `path`. Props pass through unchanged, and
the components are the ones the core pages use, so an add-on page and a core page
draw the same thing.

- `sdk.ui`: `Button`, `Text`, `Stack`, `Badge`, `Spinner`, `EmptyState`, and the
  shared `PageHeader`, `ConfirmDialog`, `SideSheet`, `SidePane`, `PaneLayout`,
  `ActionMenu`, `ErrorWithTraceLink`, `ModelSelector`,
  `MediaInput`, `WaveformPlayer`, `DistributionBars`, `EmbeddingInspector`,
  `TabSwitch`, `LoadingSpinner`, `Modal`, `Lightbox` and `ModelPicker`. A `ref`
  reaches the host component, so `ModelPicker` keeps its handle (`open()` and
  `focus()`). `ModelPicker` is the one model picker of the core pages. It lists the
  installed models and shows an alias as `alias of <model>`; an alias takes its
  capabilities from the model it points to, so a `capability` filter finds it. Props:
  `value` and `onChange`; `capability`; `multiple` (an array value, shown as chips);
  `allowCustom` (a typed name is accepted); `clearable`; `searchable`; `variant`
  (`chip` or `field`); `models` and `loading` when the add-on holds the list itself.
  `HomeModelPicker` and `ModelSelector` stay as names for the same picker.
- `sdk.ui` also has the settings kit, for a settings section or any form: see
  [The settings kit](#the-settings-kit).
- `sdk.ui` also has the Share dialog, `ShareDialog`, and the role and sharing line
  of a list row, `RoleBadge` and `SharedByBadge`. `sdk.resources.shareClient(resourceId)`
  is the dialog's client for one resource of the add-on. See
  [The Share dialog]({{%relref "/features/sharing#the-share-dialog" %}}).
- `sdk.theme.tokens`: the host's design tokens as CSS values (`var(--dk-...)`), for the
  add-on's own scoped style sheet: `text`, `muted`, `surface`, `inset`, `edge`, `hover`,
  `accent`, `accentText`, `accentWash`, `onAccent`, `okText`, `okWash`, `warnText`,
  `warnWash`, `errorText`, `errorWash`, `space1` to `space6`, `radius`, `radiusSmall`,
  `radiusMedium`, `textMeta`, `textSmall`, `textBody`, `fontBody` and `fontMono`. Every
  value is a custom property the host theme defines, in both themes. The same custom
  properties can be written in the style sheet directly.
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
- `sdk.userSettings`: `get()` and `set(key, value)`, the settings that the add-on declared
  for the signed-in person (see [User settings]({{% relref "/addons/contract-reference/host-operations#user-settings" %}})). Both calls are bound to
  the add-on that makes them; a page cannot name another add-on. A write is the person's
  own action from their browser session.
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

### Panels: a sheet, a pane and a row menu

`sdk.ui` also carries the three parts of a list with a detail and a form, the way the host's
Add-ons and MCP connectors pages draw them. Use them instead of a panel of your own: Escape,
focus, the scrim and the phone layout are the host's.

| Component | Props | What it draws |
|---|---|---|
| `SideSheet` | `title`, `description`, `footer`, `headActions`, `onClose`, `wide`, `closeLabel`, `testId`, `labelId` | A create or edit form over the page: a head, one scrolling body and a footer for the buttons, with the primary action last. It is a dialog: focus moves in, Tab stays inside, Escape and a click on the scrim call `onClose`. Do not set a width on it; `wide` is the only other size. |
| `SidePane` | `open`, `title`, `meta`, `actions`, `footer`, `onClose`, `wide`, `testId` | The details of one item beside a list. Put it in a `PaneLayout` next to the list. From 1280 px it docks and pushes the list; below that it overlays the page, and on a phone it is a bottom sheet. Escape closes it and focus returns to the control that opened it. |
| `PaneLayout` | none | The row that holds the list column and a docked `SidePane`. |
| `ActionMenu` | `items: [{key, label, icon, onClick, danger, disabled, hidden}]` (a `{divider: true}` item separates), `ariaLabel`, `compact` | The three-dot menu of a row. |

The connector pages (`addons/internal/connector-ui`) are the reference: a row list, a docked
pane and a sheet.

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
`read`, `preview`, `apply`, `output` and `act`. The scope `act` and the action
`act` go together: an envelope has one only with the other. Principal IDs are opaque nonempty
strings up to 256 bytes. Syntactic validity is **not authentication**: the owner
must create authority, verify current identity/credential/ACL and mode, and bind
scope to the actual method. Never accept browser/model-provided approval.
Call `Validate(now, limits)` at admission; unmarshalling checks structure and
nonzero expiry but intentionally does not use the wall clock.

`Runtime` provides Read, Preview, Apply, Receipt and OpenOutput. A runtime that
serves action tools also implements `ActRuntime` (`Act`), described below. Preview is
`{operationId,revision:{schema,data},expiresAt,summary}`. Schema revision is
nonzero; data revision can start at zero. Apply receives that exact revision
separately and must reject stale schema or data. Receipt is
`{operationId,state,revision:{schema,data},result}`. States are `pending`,
`committed`, `rejected`, `unknown`; unknown is not success or rejection and must
be reconciled before another mutation. Committed receipts require a nonzero
schema revision. Result has optional `payload` and `outputs`; each output has
opaque `handle`, safe basename `name`, MIME `mediaType`, and nonnegative `size`.
Handles are not URLs or filesystem paths. A read tool may return `payload` next to
`outputs`: the host refuses a payload over 128 KiB and gives the model the payload as a
short excerpt next to the handles. OpenOutput returns an authenticated
stream; callers close it. DTOs do not create artifacts or authorize downloads.


### The settings kit

`sdk.ui` carries the host components a settings section, or any form of an add-on, is
made of. They use only the host's classes and tokens, so an add-on draws its settings the
way the host draws its own, in the light and the dark theme and at phone width, without a
style sheet of its own. Use them instead of plain `<input>`, `<select>` and class names of
your own: a class the host does not define has no style.

| Component | Props | What it draws |
|---|---|---|
| `Section` | `title`, `description`, `actions`, `testId` | A titled block with a line under the title. Sections that follow each other are separated by a rule. |
| `Field` | `label`, `help`, `unit`, `error`, `testId` | A label, the control (the children), its unit after it and a help line under it. The label names the control. |
| `Select` | `value`, `onChange(value)`, `options: [{value, label, disabled}]`, `label`, `disabled`, `testId` | The host select. |
| `TextInput` | `value`, `onChange(value)`, `type` (`text`, `password`, `number`, `url`, `search`), `placeholder`, `min`, `max`, `mono`, `label`, `disabled`, `testId` | The host text input. |
| `Switch` | `checked`, `onChange(next)`, `label`, `disabled`, `testId` | The host switch. |
| `Choice` | `legend`, `hideLegend`, `value`, `onChange(value)`, `options: [{value, label, description, chip, disabled}]`, `disabled`, `testId` | One choice of a few, each option with a line that says what it does. |
| `StatusChip` | `tone` (`ok`, `warn`, `error`, `idle`, `accent`), `title`, `testId` | A state in colour, icon and words. |
| `Callout` | `tone` (`info`, `ok`, `warn`, `error`), `title`, `actions`, `role` (`alert`), `testId` | A notice inside the page. |
| `List`, `ListRow` | `List`: `label`, `testId`. `ListRow`: `title`, `meta`, `status`, `details`, `detailsLabel`, `testId` | Rows in one bordered list: a name, a line under it, a chip on the right, and details collapsed under a toggle. |
| `Code` | `testId` | A monospace block for a raw error or a command, wrapped and scrolled. |
| `Actions` | none | A row of buttons that wraps. |
| `Table` | `caption`, `columns: [{key, label}]`, `rows`, `testId` | The host table. |
| `Notice` | `severity` (`info`, `warn`, `error`), `title`, `reason`, `fixLabel` with `onFix`, `testId` | What is off or not available, why, and one action. Hosts older than this release do not have it: fall back to `Callout`. |
| `ExternalLink` | `href` (https only), `testId` | A link out, in a new tab. |

The Sandbox and Web sections are made of these components only (`addons/sandbox/ui/src/Settings.jsx`,
`addons/web/ui/src/Settings.jsx`).

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
