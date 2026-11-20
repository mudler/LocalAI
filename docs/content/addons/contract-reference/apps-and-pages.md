+++
title = "Apps, pages and slots"
description = "Apps, add-on pages and the extension points of the UI."
weight = 100
+++

Look up the Apps, the pages of an add-on and the extension points of the UI. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

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
[Update detection and upgrade]({{% relref "/addons/manage-addons#update-detection-and-upgrade" %}}).

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
My add-ons page can show them next to the installed add-ons. The answer is
`{"builtin": [...]}`, and each entry has an `id`, a `kind` (always `page`), a `titleKey`,
an `icon` and a `mainPath`, plus a `feature` or `adminOnly` when the entry has a gate.
Today the list is empty: Studio and Talk, which used to be built in, are packaged
add-ons now (see [The Studio add-on]({{% relref "/addons/manage-addons#the-studio-add-on" %}}) and
[The Talk add-on]({{% relref "/addons/manage-addons#the-talk-add-on" %}})), so a person installs and removes them like the
others. The route is kept for the day a page is built in again. It needs an
authenticated caller and does not need the `addons` feature. When authentication is off,
the caller is the local owner and sees every entry. The answer depends on the caller and is
sent with `Cache-Control: private, no-store`.

### Pins

Pinned add-ons are listed in the sidebar under Add-ons, in pin order. The menu of a pinned card in My add-ons has **Move earlier** and **Move later**.

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
- **Explicit selection.** New and existing accounts, including the local owner, start
  without implicit pins. Startup, reads, and saves never add Studio, Talk, or any other
  ID on their own. Existing saved user pins remain unchanged. Explicit pins and unpins
  survive restarts and are shared across frontends with an account database.
  Every absent ID follows the same 30-day retention rule, even if it was never installed.
  The store no longer creates or uses seed metadata; it does not drop existing database tables.
- **Startup.** If the pins file cannot be read (it is corrupt), the server does not start
  and logs the error. It does not fall back to memory, because that would lose every unpin
  at the next restart.
- **Distributed state: shared.** With an account database, the table `addon_pins` is
  shared by every frontend. With authentication off there is one local owner, and the pins
  are kept in `addon-pins.json` in the data path, per instance.

`builtin` and `pins` are reserved add-on IDs: an add-on cannot use them, because the host
serves `/api/addons/builtin` and `/api/addons/pins`.

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

#### Translating an add-on page

An add-on page reads its words from JSON bundles named `<namespace>.<language>.json` in
`addons/<id>/ui/src/i18n/`, one for English and one for each language of the web UI
(`core/http/react-ui/public/locales`). The page follows the language of the web UI, and a key that a bundle
lacks reads in English. The connector pages (Telegram, Slack, Discord, GitHub, Jellyfin, Home Assistant)
also share the bundles in `addons/internal/connector-ui/src/i18n/`, which hold the readiness list, the
permissions, the approval, the pairing and the file limits. A bundle uses `{{name}}` placeholders and
`_one` / `_other` plural keys. Do not translate product names, identifiers, capability keys or code.

`node addons/internal/i18ncheck/check.mjs` (part of `make test-i18n` and of the UI unit tests in CI) fails
when a language lacks a bundle, a bundle lacks a key of English or has an extra one, a placeholder differs
from English, or a value is empty. There is no baseline: add a key to English and every language in the
same change.

The Voice, Face, Fine-tuning, Quantization and Memory pages read their words from the same kind of bundle
(`addons/<id>/ui/src/i18n/<id>.<language>.json`, flat keys). A list of notes is stored as numbered keys
(`voice.why.0`, `voice.stored.0.where`) and the page puts it back together, so each item is translated like any
other text. A string that has no translation yet repeats the English text in that language's bundle, which
satisfies the gate and shows English; replace it when a translation exists.

Compile an add-on with classic JSX transformed to `React.createElement` over
`sdk.React`, inside the `createAddon` factory. Do not import a JSX runtime. The fixture
packages under `tests/addons/fixture/ui` are written in that form.

### The resource calls of the SDK

`sdk.resources` is the page of an add-on talking to the
[UI-only resource management]({{% relref "/addons/contract-reference/proposals-and-resources#ui-only-resource-management" %}}) of the host. It is a
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
refuses redirects, and does not use the cache. It removes any `Authorization`,
`Cookie` and `X-LocalAI-Confirm` header that the add-on sets, and it stops when the
add-on is withdrawn. `X-LocalAI-Confirm` marks a person's own action in their own tab,
and add-on code is not the person.

The path must be a same-origin path that starts with one `/`. These are refused with
an `invalid_path` error before any request is made:

- an absolute URL, a path that starts with `//`, a backslash, or a control character;
- a `.` or `..` segment, also when it is written with percent-encoding;
- the add-on gateway (`/api/addons`) and the auth routes (`/api/auth`). The add-on
  gateway has its own typed calls, and the auth routes hold account credentials. The
  check uses the decoded path, so `/api/%61ddons/apps` is refused too;
- the sharing routes: `/api/shares`, the folder sharing routes under
  `/api/chats/files/shares`, and every API path whose last segment is `share`,
  such as `/api/chats/agents/{id}/share`, `/api/collections/{name}/share` and
  `/api/chats/connectors/{id}/share`. Only the person shares, in the host's Share
  dialog: an add-on page renders `sdk.ui.ShareDialog` with
  `sdk.resources.shareClient` for its own resources, or `sdk.ui.ConnectionShareDialog`
  for a connector binding (see [Share with your team]({{%relref "/features/sharing#in-an-add-on" %}}));
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

All four lists are inside `ui` and are optional. A manifest without them is valid.

| Field | Entry | Rules |
|---|---|---|
| `ui.chatActions` | A bare identifier, or `{id, label, icon, placement, target}` | `id` is a namespace name. `label` is plain text (60 characters at most) or a language map; markup characters are rejected. `icon` is a host icon name from the SDK list or the packaged `ui.icon` path; a remote or absolute address is rejected. `placement` is `composer` or `message`. `target` has exactly one of `{tool}` (a tool in `capabilities.tools`) and `{component}` (an export of the module). |
| `ui.slots` | `{id, slot, component}` | `slot` is one of the host slot identifiers below. `id` is a namespace name that no chat action uses. |
| `ui.renderers` | `{kind, tool, component}` | `kind` is `approval-card` or `tool-result`. `tool` is a tool of the same add-on. One renderer for each kind and tool. |
| `ui.controls` | A host control ID | One of the IDs in [Host controls](#host-controls). At most 8, no repeats. The add-on supplies no component for it. |

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
| `app.nav` | On the Add-ons page, below the tile grid. Under the legacy switch: at the end of the second navigation row of the Add-ons hub, after the links of `ui.navigation`. | none |
| `settings.sections` | On the add-on's own page: the **Settings** tab of its details on the Add-ons page (`/app/addons?detail=<id>&view=settings`), one section for each component. Only the add-on's own sections are drawn there, and only for an administrator. | none |
| `files.preview` | On the Files page, in the preview of the selected file, between its size and its download link. The first component that handles the file's extension is drawn; the host's own view stays. | `path`, `name`, `extension` |

A `settings.sections` component is drawn on the Settings tab of its add-on's details, not
on the Settings page. The Settings page has an **Add-ons** group, shown when at least one
add-on declares a section, that lists those add-ons, each with a link to its Settings tab.
An add-on that has a page of its own gets a **Settings** link from the host above that
page. The tab and the link are for administrators; a section is never drawn for anyone
else, so a section does not need to hide itself from other users. The old address of a
section, `/app/settings#ad-section-<add-on>-<contribution>`, opens the add-on's Settings
tab. Below the add-on's own sections the tab draws the [host controls](#host-controls) the
add-on has, such as the network of the commands it runs: LocalAI's own runtime settings,
drawn and saved by LocalAI. Draw the section with [the settings kit]({{% relref "/addons/contract-reference/manifest#the-settings-kit" %}}).

`messageId` is the position of the message in the conversation. `role` is `user`,
`assistant`, `system` or `tool`.

A `files.preview` contribution declares the extensions it handles, lower case and without a
dot (`"extensions": ["docx", "xlsx"]`, one to 16, no repeats). No other slot has the field.
`path` is the path of the file inside the user's files, `name` its name and `extension` its
extension. The component reads the file through a read tool of its own add-on, as the user;
the host never hands it file content.

### Host controls

A host control is a component that LocalAI ships and draws itself, bound to its own
settings. An add-on asks for one by ID in `ui.controls`; it supplies no code, value or
label for it, so it cannot change what the control does. The list of controls is closed,
like the slot list. Any add-on can use any control it is entitled to: the host never reads
the add-on's ID to decide.

| Control | Where the host draws it | The add-on must |
|---|---|---|
| `network-policy` | On the add-on's **Settings** tab: the network of the commands it runs (limit, preset of a new chat, ports, extra registries, always allowed hosts, private networks, and the audit; see [Network access]({{% relref "/addons/catalogue/work/sandbox-settings#network-access" %}})). In the chat's Tools menu: the **Network** row, while the add-on runs the chat's commands, with a link to the add-on's Settings tab for an administrator. On a network card: the same link. | Be granted a host operation that covers `egress.check` (for example `devbox=egress.` in `LOCALAI_ADDON_HOST_OPS`) and declare [`capabilities.network`]({{% relref "/addons/contract-reference/manifest-extensions#network-capability" %}}). A manifest written before that field existed and declares a tool that replaces `bash` keeps the control. |
| `tool-providers` | On the add-on's **Settings** tab: the rows of Settings > Tools for the built-in tools it replaces (who serves the tool, the choice when several add-ons replace it, and the switch of the built-in tool). | Declare at least one tool that replaces a built-in tool (`shadows`). |
| `sandbox-profiles` | On the add-on's **Settings** tab: the policy of its [sandbox profiles]({{% relref "/addons/catalogue/work/sandbox-settings#sandbox-profiles" %}}) (the default profile, who may use each one, the memory and disk quota and the pins to the previous image). In the chat's Tools menu: the **Profile** row. | Declare [`execution.profiles`]({{% relref "/addons/catalogue/work/sandbox-settings#sandbox-profiles" %}}) on the tool that replaces `bash`. It needs no host operation. |

The network controls read the durable policy from the add-on owner, not the
frontend's runtime settings. The form displays the owner's revision. **Apply** sends
only changed policy fields with that revision, then reads the owner again after a
successful write. Enabling private access still requires the typed confirmation;
the confirmation phrase stays in the browser, outside the policy patch. Audit
retention remains a runtime setting, not an editable owner-policy field here.

If another administrator changes the policy, the owner rejects the stale revision.
Choose **Reload and rebase edits**, review your retained edits against the current
owner policy, then apply them. If the owner is unavailable, the form keeps your
draft and blocks saving; it does not substitute local runtime settings. If a write's
outcome is uncertain, **Retry same operation** reuses its original identity and
payload. Reloading alone does not clear an uncertain write.

Always allowed hosts accept exact names, `*.example.com` for descendants, or `*`
for public destinations. Add `example.com` separately to include the apex. Patterns
can specify a port, such as `*.example.com:8443`. They still obey the network
ceiling, allowed ports, and private-address guard. Extra registries require exact
names. The owner validates every change; see [Network access]({{% relref "/addons/catalogue/work/sandbox-settings#network-access" %}}).
These controls do not change native add-on settings declared in `settings.sections`.

The apps list carries the controls in `ui.controls`, with only those the add-on is
entitled to now. A control whose grant was removed is dropped from the list, without an
error. Declaring a control grants nothing: the host operation, the limit and the audit
work the same with or without it. A manifest without `ui.controls` is valid, and an add-on
with a control has a **Settings** tab even when it declares no `settings.sections`.

A second add-on that runs commands through the same proxy gets the same block, row and
link with one manifest line, a [`network` declaration]({{% relref "/addons/contract-reference/manifest-extensions#network-capability" %}}) and one grant,
and no change to LocalAI:

```json
"ui": { "entry": "ui/devbox.js", "sdk": { "min": 2, "max": 2 }, "controls": ["network-policy", "tool-providers"] }
```

The grant is one more entry of `LOCALAI_ADDON_HOST_OPS`, next to the ones it has:
`devbox=egress.`. The setting replaces the default list, so keep its other entries.

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
  request for an undeclared tool is a 403 (see [Declared tools]({{% relref "/addons/contract-reference/proposals-and-resources#declared-tools" %}})).

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
from the proposer's own browser session, as in [Proposals and confirmation]({{% relref "/addons/contract-reference/proposals-and-resources#proposals-and-confirmation" %}}).
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
