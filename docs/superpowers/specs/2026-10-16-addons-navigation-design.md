# Add-ons navigation design

## Status

- Status: approved for implementation planning. One question stays open (see "Open questions and decisions"). Questions (a) and (b) and the rulings (d) to (j) were decided on 2026-10-16. The changelog at the end lists every change of that day.
- Date: 2026-10-16.
- Owners: Ettore Di Giacinto (maintainer) and the AI agent that drafted this spec.
- Branch: `feat/addons-tables` (PR 12702).
- Scope: documents only. This change adds no source code.

### Approved decisions

1. Top-level navigation has four items: Chat, Models, Add-ons, Settings.
2. The Add-ons item opens a drawer page. It shows the installed add-ons as a grid of tiles. Each tile has an icon, a name and a state (running or stopped). The page has a search box, a Browse add-ons button and an "Add a new add-on" entry.
3. Each tile has a pin control. Pinned add-ons appear in the sidebar under a Pinned section. Pinning is per user. The sidebar shows a short pinned list. The drawer shows all installed add-ons.
4. Studio is an add-on. It contains Images, Audio and Video. Talk is an add-on. Both open from the drawer and can be pinned. Neither is a top-level item.
5. Fine-tuning and quantization move out of core into two add-ons, `fine-tuning` and `quantization`, in this change. The API routes (`/api/fine-tuning/*`, `/api/quantization/*`) and the job managers stay in core unless a later spec moves them. The add-ons get the UI and a tool surface. Where an add-on cannot yet reach a backend, a model or the models folder, this change adds host operations; they are new contract surface (see "Host operations for the engine").
6. Faces and voices become two add-ons with their own lifecycle. The Voice add-on holds the voice library management (Voice Library, Voice Profile Create), voice recognition (Voice Recognition) and a new text-to-speech generation page. The Face add-on holds Face Recognition and its library management (the known-faces list and the enrol sheet). Core keeps only the API and the stores behind it: `/v1/face/*`, `/v1/voice/*`, `/api/voice-profiles`, the face and voice registries and the voice profile store. Studio keeps its own audio generation. The Voice text-to-speech page is a separate page that calls the same core text-to-speech route. Voice and Face do not ship with LocalAI (decision 10).
7. Agents, Memory and Skills are candidates for later add-ons. They are out of scope here.
8. Add-ons call core routes directly, with the signed-in user's own session (question (a), decided).
9. Shared UI comes from the SDK, as primitives and per-component decisions (question (b), decided). Views and pages stay in the add-on that owns them.
10. Voice, Face, fine-tuning and quantization do not ship with LocalAI (question (e), decided). They are add-on packages, built for every supported operating system and architecture, listed in the Browse add-ons catalog and installed by an administrator. Studio and Talk are the only built-in add-ons.
11. Voice and Face get no tab in the Build hub (question (d), decided). A user finds them in Browse add-ons and, once installed, in the drawer.
12. Existing accounts get Studio and Talk pinned once, at upgrade. New accounts start with no pins (question (g), decided).

## Background

The React UI sidebar lists many destinations. `core/http/react-ui/src/utils/destinations.js` defines the inline items (Home, Models, Chat, Studio, Talk). `core/http/react-ui/src/components/hub/hubConfig.js` defines two hubs, Build and Operate, with their tabs. `core/http/react-ui/src/components/Sidebar.jsx` draws them (the `sections` array near line 24 and `topItems`).

Add-ons already exist. The Add-ons page is one tab of the Build hub (`hubConfig.js:67-75`). An add-on that declares navigation pages adds links to that tab (`hubConfig.js:233`, `registryState.js:63`).

Built-in features that could be add-ons sit beside the real add-ons. The sidebar has no single place for "extra tools". New tools need a new sidebar row, or a tab in a hub. This design gives them one place.

## Goals

- Keep the top level small and stable: four items.
- Put every optional tool behind one entry, the Add-ons drawer.
- Let each user keep the few add-ons they use daily one click away (pins).
- Reuse the add-on contract fields and the Apps permission rules. Add no new permission model.
- Keep every old URL working through a redirect.

## Non-goals

- Moving API routes. `/v1/*`, `/api/*` and their handlers stay where they are.
- Changing the add-on runtime, the process supervisor or the add-on contract beyond what navigation needs. This spec asks for no new manifest field (see "Manifest fields for navigation"). The one exception is the set of host operations for the fine-tuning and quantization tools (see "Host operations for the engine"), which is new contract surface and is marked as such.
- Moving Agents, Memory or Skills.
- Changing the add-on catalog, signing or install flow.
- Redesigning the pages that move. They keep their current behavior.

## Navigation model

### Top-level items

| Item | Opens | Visible to |
|---|---|---|
| Chat | `/app/chat` | everyone |
| Models | `/app/models` | administrators (as today, `destinations.js:9`) |
| Add-ons | `/app/addons` (the drawer page) | users with the `addons` feature |
| Settings | `/app/settings` | administrators (as today, `router.jsx:266`) |

Other current destinations (Home, Build hub tabs, Operate hub tabs) are not top-level items in this layout. Their placement stays as it is today unless a section below moves them. The sidebar change for those destinations is out of scope for this spec.

### The drawer page

The Add-ons item opens the drawer page at `/app/addons`. The route exists today (`router.jsx:243`). This spec changes what it shows.

The page has these parts:

- A grid of tiles, one per installed add-on the viewer may see.
- A search box. It filters tiles by title and description.
- A Browse add-ons button. It opens the catalog of installable add-ons.
- An "Add a new add-on" entry. It starts the install flow. Only administrators see it, because installing is an administrator action.

A tile shows:

- The icon. It comes from the `icon` URL of the app (`core/services/addons/apps/apps.go:71`). A tile without an icon shows a generic icon.
- The title. It is the localized `title`, or the add-on ID when the manifest has none.
- The state: running or stopped.
- A pin control.

Clicking a tile opens the main page of the add-on.

### Tile states

The Apps view derives the state of an add-on (`apps.go:76-81`, `state()` at `apps.go:236`). The raw process states are `running`, `starting`, `failed` and `stopped`. The tile maps them as follows.

| Tile state | Source | What the tile shows |
|---|---|---|
| Installed, running | installed, enabled, process `running` | Running badge. Click opens the main page. |
| Installed, stopped | installed, and disabled or process `stopped` | Stopped badge. Click opens a detail view. An administrator sees a Start action there. |
| Starting or failed | process `starting` or `failed` | Shown as stopped, with a short status line. Click opens the detail view. |
| Not installed | catalog entry only | Not a tile. It appears only in Browse add-ons. |

A tile never opens the main page of a stopped add-on. The main page needs a running process.

### Manifest fields for navigation

The contract already has the fields navigation needs. They live in `UI` in `pkg/addons/contract/manifest.go`:

| Field | Line | Use in this design |
|---|---|---|
| `ui.title` | `manifest.go:197` | Tile name and sidebar label. Plain text, at most 80 characters (`MaxTitleChars`, `manifest.go:22`). May be localized. |
| `ui.icon` | `manifest.go:194` | Tile and sidebar icon. A packaged image (`.png`, `.webp`, `.jpg`), at most 256 KiB (`manifest.go:25-36`). |
| `ui.mainPage` | `manifest.go:192` | The page a tile opens. It must be one of `ui.pages` (validated at `manifest.go:350`). |
| `ui.pages` | `manifest.go:181` | All pages of the add-on. |
| `ui.navigation` | `manifest.go:182` | Pages that also appear as extra links. Each must be a declared page (`manifest.go:353-356`). |

Rules:

- A tile opens `ui.mainPage`. The Apps view already exposes the page only for an enabled add-on with a valid main page (`apps.go:218`).
- An add-on without `ui.mainPage` has a tile with no Open action. This is the existing behavior.
- `ui.navigation` keeps its current meaning: extra entries. In the new layout they appear inside the add-on detail view and in the sidebar group of a pinned add-on. They do not appear as separate top-level items. The registry already builds their paths as `/app/addons/<id>/<page>` (`registryState.js:63-77`).

This spec adds no manifest field. If implementation shows that a field is needed, for example a "group" or "order", that is a contract change. It needs its own spec.

### Built-in add-ons and packaged add-ons

Studio and Talk are shipped with LocalAI and appear in the drawer like any installed add-on. Voice, Face, fine-tuning and quantization are not shipped with LocalAI (see "Distribution"); once installed they appear in the drawer too. The navigation contract is the same for all of them: a title, an icon, a main page and a state. There are two kinds.

- **Page add-ons** (Studio, Talk). Their pages are core React pages that stay in the core bundle. A server-side built-in catalog lists them and applies the gates in "Permissions". They have no process and no lifecycle of their own. Their tile state is "running" while the feature is enabled.
- **Packaged add-ons** (Voice, Face, fine-tuning, quantization). They are ordinary add-on packages under the existing contract: a manifest, a packaged UI module and an executable. The documented rule "every add-on requires an executable" stays. The executable of Voice and Face is a small protocol server that declares no tools and serves nothing. The executable of fine-tuning and quantization serves typed tools and reaches the job engine in core through host operations. They have their own version, their own signature, and install, enable, disable, update and removal through the existing add-on flow. Their tile state is the process state, as for any add-on.

This choice adds no manifest field. The host operations of the fine-tuning and quantization tools are the only change to the contract (see "Host operations for the engine").

### Distribution

Voice, Face, fine-tuning and quantization are add-on packages and are not part of the LocalAI image or binary. Each package is built for every supported operating system and architecture. The supported set is the one the packaging code accepts and the Tables package already lists:

| Platform | Runner that builds and checks it natively |
|---|---|
| `linux/amd64` | `ubuntu-24.04` |
| `linux/arm64` | `ubuntu-24.04-arm` |
| `darwin/arm64` | `macos-14` (Apple Silicon) |
| `darwin/amd64` | `macos-15-intel` (Intel macOS) |

Windows is not supported: the package format has no `windows` platform and LocalAI's own release builds do not target it. `darwin/amd64` is listed because the package format accepts it; LocalAI's own release builds ignore it, so the maintainers confirm before it is enabled.

The rule stays as it is for Tables: **a platform is enabled only in the change that shows its native check passing on a runner of that platform.** The check starts the packaged executable on that runner. A platform that is listed and not enabled is never built, and the release workflow fails closed when an add-on has no enabled platform.

Discovery. A user finds each package in the Browse add-ons catalog, through a catalog entry that the maintainers render from a template and publish. Once an administrator installs it, it appears in the drawer and can be pinned. Nothing in the sidebar or in a hub points to an add-on that is not installed. The old URLs keep opening the same content: while the add-on is not installed, an old route opens the core page, and once it is installed and running, the route opens the add-on (see "Redirects").

### The Pinned section

The sidebar gets a Pinned section below the four top-level items. It shows the add-ons the current user pinned, in the order the user chose. It has a cap of eight entries. When the user pins a ninth, the UI asks them to unpin one. The drawer has no cap.

Each pinned entry shows the icon and the title. It opens the main page of the add-on. A pinned add-on that is stopped shows a muted icon. Clicking it opens the detail view, like a tile.

An empty Pinned section is hidden.

## Pinning

### Where the state lives

Pin state is per user. It has two parts: the set of pinned add-on IDs and their order.

The pin list is stored on the server, keyed by user ID. Pins follow the user across browsers and devices. A new table holds one row per user and pin: user ID, add-on ID, position. A user can read and write only their own rows.

When authentication is off, LocalAI has one local owner (`auth.AddonLocalOwnerID`, `core/http/auth/addons.go:30-46`). Pins then belong to that owner.

Browser storage (`localStorage`) is not enough. The sidebar already uses it for layout state (`Sidebar.jsx:15-16`), which is fine for a per-browser convenience. A pin is a user setting and must survive a browser change.

The pin state must work with several frontends. The table lives in the shared database, so every frontend reads the same list. This follows the distributed-state guide (`.agents/distributed-state.md`). No per-process cache may hold the only copy.

### The one-time seed

In plain words. When LocalAI is upgraded to the version that has pins, every account that already exists gets Studio and Talk pinned, once. They keep the sidebar entries they had before the upgrade. After that nothing is pinned for them again, so if one of them unpins Studio, it stays unpinned. An account created after the upgrade starts with no pins at all; it finds Studio and Talk in the drawer and pins what it uses.

How it works. The seed runs once, when LocalAI starts the first version that has pins, not when a user reads the pin list. A marker in the shared database records that it ran, so a restart, a second frontend and a later release do not run it again; with several frontends that start together, exactly one runs it. It pins only for the accounts that exist at that moment. The pin routes never add a pin of their own. With authentication off there is one local owner, and that owner counts as an existing account: it gets the seed on the first start of that version, and the marker is kept in the pins file next to the pins.

### API

Two routes are needed. They are user-scoped and need no admin role:

- `GET /api/addons/pins` returns the ordered list of pinned add-on IDs for the caller.
- `PUT /api/addons/pins` replaces the list. The body is the ordered list of IDs. The server drops IDs the caller may not see (see "Permissions").

The route names are `GET /api/addons/pins`, `PUT /api/addons/pins` and, for the built-in tiles, `GET /api/addons/builtin`. `builtin` and `pins` become reserved add-on IDs.

Registration. The three routes are registered by their own function, `RegisterAddonNavigationRoutes`, which the server calls right after `RegisterAddonRoutes`. `RegisterAddonRoutes` and `RegisterAddonRoutesFor` are not changed. The reason: the tests of the existing add-on routes require every `/api/addons*` route that those functions register to hold the `addons` feature, to be in `RouteFeatureRegistry` and to be documented in the `addons` API guide. The navigation routes are user-scoped by design and gate themselves (the built-in catalog applies the gate of each entry, and the pins route filters what the caller may see), so they are not entries of the `addons` feature in the registry. This keeps "API routes are unchanged. Their tests pass without edits" true.

The rest of the checklist in `.agents/api-endpoints-and-auth.md` applies: a swagger tag (`AddonNavigation`), an `/api/instructions` guide (`addon-navigation`), the docs page and the route tests. No MCP tool is added: pins are a per-user UI preference, not an administrator action.

### Disabled or removed while pinned

The server does not delete pin rows when an add-on is disabled, stopped, updated or removed. The sidebar resolves each pinned ID against the Apps list at render time.

| Event | Sidebar | Drawer | Pin row |
|---|---|---|---|
| Add-on stopped or disabled | Entry stays, muted. | Tile shows stopped. | Kept. |
| Add-on removed (uninstalled) | Entry is hidden. | No tile. | Kept for 30 days, then deleted by a cleanup job. If the add-on is installed again, the pin returns. |
| Add-on updated to a new generation | Entry stays. | Tile updates. | Kept. The pin stores the add-on ID, not the generation. |
| Add-on hidden from this user (access change) | Entry is hidden. | No tile. | Kept. |

The 30-day window is a starting value. The implementation plan may change it. It must not be shorter than the time an administrator needs to reinstall after a mistake. The start of the window is stored with the pin, in the database and in the pins file alike, so a restart does not restart the window.

A hidden pin never shows an error. The user just does not see it.

### A user without access

A user who cannot see an add-on never sees its pin. The server filters the pin list the same way it filters the Apps list. The pins route returns only IDs the caller may see. The route does not reveal whether a hidden ID exists.

If an administrator disables an add-on, non-administrators lose the tile and the pin entry at once, because the Apps list stops returning it (`apps.go:182`). The administrator still sees the tile, stopped, so they can start it again.

## Permissions

The drawer reuses the Apps surface rules. It adds no new rule.

The rules live in `Build` in `core/services/addons/apps/apps.go:176-189`:

- An add-on that is not installed is never listed.
- A disabled add-on is listed only for an administrator.
- Every other installed add-on is listed for the caller. The list is sorted by ID.

The HTTP layer is `ListApps` in `core/http/endpoints/addons/apps.go:31`. It resolves the caller with the same actor checks as every other add-on route (`core/http/endpoints/addons/gateway.go:74`). Start and stop need an administrator (`gateway.go:87`).

The `addons` feature gates access to the surface. It is declared in `core/http/auth/permissions.go:37` and is a general feature (`permissions.go:75`). The UI route is wrapped in `<Feature feature="addons">` (`router.jsx:243`). A user without the feature sees no Add-ons item and no drawer.

Built-in add-ons keep their current feature gates. Their pages move behind the drawer, but the gate does not change:

| Page | Gate today | Gate after |
|---|---|---|
| Fine-tuning | `fine_tuning` (`permissions.go:38`) | `fine_tuning`, plus `addons` to see the tile (add-on `fine-tuning`; the page shows the off notice without `fine_tuning`) |
| Quantization | `quantization` (`permissions.go:39`) | `quantization`, plus `addons` to see the tile (add-on `quantization`; the page shows the off notice without `quantization`) |
| Face Recognition | `face_recognition` (`permissions.go:61`) | same, plus `addons` (Face add-on) |
| Voice Recognition | `voice_recognition` (`permissions.go:62`) | same, plus `addons` (Voice add-on) |
| Voice Library, Voice Profile Create | administrator (`router.jsx:239`, `router.jsx:298`) | administrator, plus `addons` (Voice add-on) |
| Voice text-to-speech page (new) | `audio_speech` on `/v1/audio/speech` (`features.go`) | same, plus `addons` (Voice add-on) |
| Studio, Talk | none in the router (`router.jsx:212-216`); each tab inside Studio has its own gate (`Studio.jsx:26-32`) | no `addons` gate; inner gates unchanged |

The four packaged add-ons (Voice, Face, fine-tuning, quantization) are listed by the Apps view, so the `addons` gate applies to them as to any packaged add-on. A tile is not hidden by the feature of its page, because the manifest has no field for that and this change adds none. Their pages are gated again inside the add-on by the feature of each page (a user without the feature sees the off notice), and the server checks every core call (see "Core routes from add-ons").

**Decision: the gate rule for Studio and Talk.** The Studio and Talk tiles ignore the `addons` gate. They are page add-ons, listed by the server-side built-in catalog, which applies only their own gates (none for Studio and Talk, the inner Studio gates unchanged). The Add-ons item and the drawer show for a viewer who holds the `addons` feature or who sees at least one built-in tile. A viewer without the `addons` feature sees the built-in tiles only: no packaged add-on tile, no Browse, no Add entry, and the Apps routes still answer 403. The rule that no user loses access to a page they can open today holds. This replaces the line "A user without the `addons` feature sees no Add-ons item" for the built-in tiles only.

### Core routes from add-ons

**Decision (question (a)).** An add-on UI may call core routes directly: `/v1/face/*`, `/v1/voice/*`, `/api/voice-profiles`, `/v1/audio/speech` and any other core route the add-on page needs.

- The call is made with the signed-in user's own session. It goes through the same middleware as any browser request: `auth.Middleware` resolves the session cookie (`core/http/auth/middleware.go:41`), `auth.RequireRouteFeature` applies `RouteFeatureRegistry` (`core/http/auth/features.go:109-135`, middleware at `middleware.go:248`), `auth.RequireAdmin` guards admin routes (`middleware.go:122`) and `auth.CSRFMiddleware` applies the fetch-metadata check (`core/http/auth/csrf.go:12`).
- The add-on gets no extra or elevated credential. It never sees a token, a cookie value or an `Authorization` header, and it cannot widen what the user may do. An admin-only route stays admin-only for that user: a non-administrator gets 403.
- The mechanism is an SDK helper, `sdk.core.fetch(path, init)`. It wraps the browser `fetch` with `credentials: 'same-origin'`, `mode: 'same-origin'` and `redirect: 'error'`. The browser, not the add-on, sets the `Sec-Fetch-Site` header that the existing checks read (`csrf.go:19-22`, `core/http/endpoints/addons/gateway.go:248-258`). The helper refuses an absolute URL, a path with `..`, the prefix `/api/addons/` (typed add-on calls have their own bound identity) and the prefix `/api/auth/` (account credentials). It compares the prefixes on the decoded path, so `/api/%61ddons/apps` is refused, and it refuses an encoded `/` or `\` in the first two segments, where it could hide a boundary (`/api/addons%2Fapps`). An encoded `/` in a later segment is an identifier and is kept (`/api/voice-profiles/a%2Fb`). It refuses every call after the add-on is withdrawn.
- No new permission model and no new contract field.

### Host operations for the engine

**New contract surface.** The UI of the fine-tuning and quantization add-ons needs nothing new: it calls `/api/fine-tuning/*` and `/api/quantization/*` with the user's session, as decided in question (a), including the SSE progress stream, the dataset upload and the download link. Their model-facing tools run in the add-on process, and today that process has no path to a backend, a model or the models folder, no credential for core routes, no progress or cancel operation for a long job, and no way to receive bytes beyond a 1 MiB payload. The job managers, the stores and the API stay in core, so the tools reach them through host operations.

| Operation | Scope | What it does |
|---|---|---|
| Backend job operation (`finetune.start`, `.get`, `.list`, `.checkpoints`; `quantize.start`, `.get`, `.list`) | read, apply | Starts and reads engine jobs as the calling user. The host validates the model reference, the backend and the argument fields. A result never carries a server path. |
| Progress and cancel (`finetune.progress`, `.stop`; `quantize.progress`, `.stop`) | read, apply | A bounded wait of at most 25 seconds for the next progress event, and a cancel. The contract has no progress stream, so a caller loops. |
| Model registration (`finetune.export`, `quantize.import`) | apply | The host registers the model from the job's own output through the existing export and import services and returns only the model name. The operation takes no path. |
| Inline dataset (`finetune.dataset`) | apply | Stores a small text dataset (up to the payload limit) and returns a reference that a start operation resolves for the same user only. Larger datasets use the UI upload. |

Rules for every host operation:

- An add-on process calls a host operation on a loopback endpoint of its own incarnation, with a credential of its own. Both arrive in the startup object as two optional fields. The protocol version stays 1, and an add-on that ignores the fields works as before.
- A call names the operation ID of a request the host is serving for that add-on at that moment. The host looks up the envelope it issued. No live envelope, an expired authority or a scope below the scope of the operation is refused. The operation runs as the actor of that envelope, never as a user named in the arguments, and the host refuses an argument with an identity or authority name.
- The host checks the feature of the operation (`fine_tuning` or `quantization`) for that actor, as the route middleware does for the same request today. The user key passed to a service is the one the route passes (empty with authentication off), so a job started by a tool appears in the UI list of the same user.
- Only add-ons that a host setting names get the endpoint. It is a host setting, not a manifest field and not a model argument. By default it names `fine-tuning` and `quantization`.
- Job state stays in the core stores, which every frontend shares. A frontend that does not run the add-on process offers no endpoint, so the operation fails closed.
- A model controls only a job that a tool of the add-on started. A job is a resource of the add-on (the `create` tool reserves it); jobs started in the UI are listed but not controllable by a tool.

Finding recorded for the maintainers: `UploadDatasetEndpoint` reads the whole upload without a size bound. This change does not alter that route; the host operation bounds itself.

## Shared UI in the SDK

**Decision (question (b)).** The SDK holds shared primitives. Views and pages always live in the add-on that owns them, never in the SDK. For a domain widget, the rule is: used by one add-on, it stays in that add-on; used by more than one add-on, it moves to the SDK. Core keeps a single copy: the host component files stay where they are and the SDK re-exports them, so core pages render what they rendered before.

The SDK surface is versioned with the add-on contract. This change raises the host SDK version from 1 to 2. An add-on declares the SDK range it needs in `ui.sdk`, which exists today (`manifest.go:180`). An add-on that needs SDK 2 and runs on a host that provides SDK 1 is refused at install with the error "the add-on needs SDK 2 to 2 and this host provides SDK 1". An add-on built for SDK 1 only (`max: 1`) is refused by a version 2 host, so the existing manifests in this repository (Tables and the test fixtures) declare `min: 1, max: 2`. The manifests of add-ons that use the SDK 2 primitives (Voice, Face, fine-tuning, quantization) declare `min: 2, max: 2`: they cannot run on an SDK 1 host, and the install error above says so. The existing UI tests that pin SDK 1 are updated in the same change.

### Classification of the components the four pages import

Counted from the code: `grep` for each import under `core/http/react-ui/src`. "Add-ons" are the add-ons that use the component after the move: Studio (page add-on), Voice and Face.

| Component | Importers | Add-ons that use it | Decision | Reason |
|---|---|---|---|---|
| `Icon` | 200 files | all | SDK, already exported (`sdk.Icon`) | Primitive. |
| `LoadingSpinner` | 46 | all | SDK, already exported (`Spinner`) | Primitive. |
| `EmptyState` | `VoiceLibrary.jsx`, `hostServices.js` | Voice | SDK, already exported (`EmptyState`) | Primitive. |
| `StatusPill` | 3 | all | SDK, already exported (`Badge`) | Primitive. |
| `SideSheet` | 9 | Voice | SDK, already available as `sdk.layers.openPanel` | Primitive; the host owns layers. |
| `PageHeader` | 17 | Voice, Face | SDK, new (`ui.PageHeader`) | Primitive used by two add-ons and 15 core pages. |
| `ConfirmDialog` | 25 | Voice | SDK, new (`ui.ConfirmDialog`) | Dialog primitive; focus and Escape handling must be one copy. |
| `ErrorWithTraceLink` | `MoreFaceTools`, `MoreVoiceTools` | Voice, Face | SDK, new (`ui.ErrorWithTraceLink`) | Primitive used by two add-ons. |
| `UnsavedChangesGuard` | 5 | Voice | not exported | The SDK already has `sdk.markDirty`. |
| `HomeUndoToast` | 13 | Face, Voice | not exported as a component | `sdk.notify` has no undo action. Each add-on draws a small undo bar of its own from `ui.Button` (see "Additions accepted on 2026-10-16"). |
| `ModelSelector` | `VoiceRecognition`, `FaceRecognition`, `AgentTaskDetails`, `studio/Workspace` | Studio, Voice, Face | SDK, new (`ui.ModelSelector`) | Form control used by three add-ons. |
| `biometrics/MediaInput` | `ThreeDGen`, `VideoGen`, `MoreFaceTools`, `MoreVoiceTools` | Studio, Face, Voice | SDK, new (`ui.MediaInput`) | More than one add-on. |
| `audio/WaveformPlayer` | `TTS`, `Sound`, `AudioTransform`, `VoiceLibrary`, `VoiceProfileCreate`, `ClipInput`, `BackendTraceDetail` | Studio, Voice | SDK, new (`ui.WaveformPlayer`) | More than one add-on, and core uses it. |
| `hooks/useMediaCapture` | `AudioTransform`, `MediaInput`, `ClipInput` | Studio, Voice, Face (through `MediaInput`) | SDK, new (`hooks.useMediaCapture`) | More than one add-on. |
| `hooks/useObjectUrl` | `ThreeDGen`, `Diarization`, `AudioTransform`, `ClipInput` | Studio, Voice | SDK, new (`hooks.useObjectUrl`) | More than one add-on. |
| `hooks/useDelayedAction` | `identity/useRegistry`, `users/ApiKeysPanel` | Voice, Face (through `useRegistry`) | SDK, new (`hooks.useDelayedAction`) | More than one add-on. |
| `biometrics/DistributionBars` | `MoreFaceTools`, `MoreVoiceTools` | Face, Voice | SDK, new (`ui.DistributionBars`) | More than one add-on. |
| `biometrics/EmbeddingInspector` | `MoreFaceTools`, `MoreVoiceTools` | Face, Voice | SDK, new (`ui.EmbeddingInspector`) | More than one add-on. |
| `biometrics/TabSwitch` | `MoreFaceTools`, `MoreVoiceTools` | Face, Voice | SDK, new (`ui.TabSwitch`) | More than one add-on. |
| `biometrics/BoundingBoxCanvas` | `MoreFaceTools`, `Workbench` | Face | stays in the Face add-on | One add-on: the voice side of `Workbench` never draws a box (`Workbench.jsx:310`). |
| `biometrics/WaveformStrip` | `MoreVoiceTools` | Voice | stays in the Voice add-on | One add-on. |
| `biometrics/MatchGauge` | none | none | stays in core, not exported | No importer. Not part of this change. |
| `audio/Spectrogram` | `AudioTransform` | Studio | stays in core | One add-on, and Studio is a page add-on in the core bundle. |
| `identity/ClipInput` | `Workbench`, `EnrolSheet`, `VoiceProfileCreate` | Face, Voice | stays in the add-ons, in one shared source directory | Two add-ons use it, but it depends on the identity helpers (`utils/identity`, `utils/referenceAudio`), which are not generic. It is built from `MediaInput`, `WaveformPlayer` and `useMediaCapture` from the SDK. |
| `identity/Workbench`, `KnownList`, `EnrolSheet`, `Verdict`, `DistanceScale`, `ErrorNote`, `useRegistry` | `Workbench` and its children | Face, Voice | stay in the add-ons, in the same shared source directory | These are views. A view never goes in the SDK. Both add-ons build from one source copy, not two. |
| `identity/MoreFaceTools`, `FeatureOff`, `ModelNeeded` | pages | Face (`MoreFaceTools`), both (`ModelNeeded`) | stay in the add-ons | Views. |
| `identity/MoreVoiceTools` | `VoiceRecognition` | Voice | stays in the Voice add-on | View. |

The shared source directory is `addons/internal/identity-ui/src`. Each add-on bundles what it imports from it. The SDK never contains it. The fine-tuning and quantization add-ons share the views of their job pages in the same way, in `addons/internal/tools-ui/src`.

### Additions accepted on 2026-10-16

The first draft of the SDK list left out four things that the add-on pages need. They are accepted and are part of SDK 2. Each is below with one line.

- **`sdk.viewer`**: `isAdmin()` and `hasFeature(name)`, a read-only view of the role and the feature flags the host already shows in its own UI, so a page can show an off notice. The server still decides every call.
- **`sdk.hooks.audioBufferToWavBlob`**: a pure encoder from decoded audio to a WAV blob, which the reference-audio helper of the Voice add-on needs. The SDK now has four hooks: `useMediaCapture`, `useObjectUrl`, `useDelayedAction`, `audioBufferToWavBlob`.
- **26 more icon names** in the icon allowlist: `arrow-left`, `arrow-right`, `chart-bar`, `circle-dot`, `clipboard`, `code`, `cube`, `filter-off`, `gender`, `globe`, `headphones`, `help-circle`, `mic`, `play`, `plug-off`, `save`, `shield`, `smile`, `waveform`, `camera`, `stop`, `circle`, `image`, `spinner`, `user-shield`, `mask`. All exist in the host icon sprite.
- **The undo bar**: a small bar with a text and an Undo button for a delayed action, which `sdk.notify` cannot draw. It is built by each add-on from `ui.Button` and `hooks.useDelayedAction`; it is not an SDK component, so the classification above does not change.

## Migration

### Current layout

Routes below come from `core/http/react-ui/src/utils/destinations.js`, `core/http/react-ui/src/components/hub/hubConfig.js` (both drawn by `Sidebar.jsx`) and `core/http/react-ui/src/router.jsx`. All paths are under `/app`.

### Mapping

| Old route | Today | New home |
|---|---|---|
| `/app/chat`, `/app/chat/:model` | Create group | Chat (top level). No change. |
| `/app/models` | top item | Models (top level). No change. |
| `/app/settings` | Operate hub | Settings (top level). No change. |
| `/app/studio`, `/app/studio/:tab` | Create group | Studio add-on, main page. Same path. |
| `/app/image`, `/app/image/:model` | page | Studio add-on, Images tab. |
| `/app/video`, `/app/video/:model` | page | Studio add-on, Video tab. |
| `/app/3d`, `/app/3d/:model` | page | Studio add-on, 3D tab. |
| `/app/tts`, `/app/tts/:model` | page | Studio add-on, Audio area. |
| `/app/sound`, `/app/sound/:model` | page | Studio add-on, Audio area. |
| `/app/diarization`, `/app/diarization/:model` | page | Studio add-on, Audio area. |
| `/app/transform`, `/app/transform/:model` | page | Studio add-on, Audio area. |
| `/app/talk` | Create group | Talk add-on, main page. Same path. |
| `/app/fine-tune` | Build hub | Fine-tuning add-on, page `jobs` (`/app/addons/fine-tuning/jobs`) while it is installed and running; the core page otherwise. |
| `/app/quantize` | Build hub | Quantization add-on, page `jobs` (`/app/addons/quantization/jobs`) while it is installed and running; the core page otherwise. |
| `/app/face`, `/app/face/:model` | Build hub | Face add-on, page `recognition` (`/app/addons/face/recognition`) while it is installed and running; the core page otherwise. The model travels as `?model=`. |
| `/app/voice`, `/app/voice/:model` | Build hub | Voice add-on, page `recognition` (`/app/addons/voice/recognition`) while it is installed and running; the core page otherwise. The model travels as `?model=`. |
| `/app/voice-library` | Build hub (match) | Voice add-on, page `library` (`/app/addons/voice/library`) while it is installed and running; the core page otherwise. Whether it also has a drawer tile of its own is open, see question (c). |
| `/app/voice-library/new` | page | Voice add-on, page `create` (`/app/addons/voice/create`) while it is installed and running; the core page otherwise. |
| (new) | none | Voice add-on, page `speech`: text-to-speech generation. No old route. |
| `/app/addons` | Build hub tab | Add-ons (top level), drawer page. |
| `/app/addons/:addonId/*` | add-on pages | unchanged |
| `/app/build`, `/app/agents`, `/app/skills`, `/app/collections`, `/app/agent-jobs`, `/app/import-model` | Build hub | unchanged; out of scope |
| `/app/operate` and its tabs | Operate hub | unchanged; out of scope |

The Studio route has a legacy `?tab=` redirect already (`Studio.jsx:78-81`). Keep it.

### Redirects

Every old route must keep opening the same content. There are two cases.

1. The path stays the same (`/app/studio`, `/app/talk`). No redirect. The page is now rendered by the add-on host, not by the core router.
2. The path changes (for example `/app/image` to a path under `/app/addons/<id>/`). The router keeps a redirect entry for the old path. It uses `<Navigate replace>` like the existing redirects (`router.jsx:174-183`). The redirect keeps the model segment, the query string and the hash.

Rules for redirects:

- Redirects are permanent in the app. They are not removed in the release that adds them. Removal needs a separate decision.
- A redirect to a path the viewer cannot open ends on the normal "no access" or "not found" page. It does not reveal the add-on.
- Deep links with a model (`/app/image/:model`) keep the model. A redirect into an add-on carries the model as the query parameter `?model=<id>` and not as a path segment: an add-on path segment refuses `/`, `:`, `%` and long identifiers, and a model identifier can hold them. The add-on reads `?model=` first and the first path segment second.
- A moved page opens in its add-on only while the add-on is installed, enabled and compatible. Otherwise the old route opens the core page, so a user who has not installed the add-on loses nothing.
- Bookmarks to `/app/browse/*` keep working through `BrowseRedirect` (`router.jsx:180-183`).

### Sidebar after migration

- The Create group is removed. Chat moves to the top level. Studio and Talk leave the sidebar unless the user pins them. Home and the Build and Operate hub entries stay where they are today.
- The Build and Operate hubs keep their tabs, except for the tabs that moved to add-ons. The fine-tuning, quantization, Face, Voices and Add-ons tabs leave the Build hub. Voice and Face get no hub entry of any kind (question (d)): the add-ons are not shipped with LocalAI, so a user finds them in Browse add-ons and, once installed, in the drawer. The Build overview page keeps its cards for fine-tuning and quantization; they link to the old routes, which open the add-on while it is usable and the core page otherwise. The hub's `addonNavigation` second row (`hubConfig.js:74`) goes away: its job moves to the drawer and to the pinned group.
- Users who had Studio or Talk in the sidebar see them in the drawer. At upgrade, the server pins Studio and Talk once for the accounts that exist then, so the layout does not change under them. The seed runs once for the deployment and never again, so a later unpin sticks, and an account created after the upgrade starts with no pins (see "The one-time seed").

### Documentation

The change that implements this spec must update the pages under `docs/content/` that describe the sidebar and the add-ons surface, including `docs/content/features/addons.md`. This follows the docs-with-code rule in `AGENTS.md`.

## Open questions and decisions

Question (c) is not decided and needs an answer before the work that depends on it is built. Questions (a) and (b) and the rulings (d) to (j) are decided.

### (a) Direct API calls from the face and voice add-ons

Decided on 2026-10-16. See "Core routes from add-ons".

### (b) Shared UI components

Decided on 2026-10-16. See "Shared UI in the SDK".

### (c) Where the Voice Library entry lives

Voice Library lists the enrolled speech voices. The approved placement puts it in the Voice add-on, as the page `library`. Open: whether it also has a drawer tile of its own, or is reached only from inside the Voice add-on. No decision yet. The implementation plan does not start this work.

### (d) Voice and Face in the Build hub

Decided on 2026-10-16. **Conflict record.** The first draft said two things that cannot both hold while the add-ons are not installed: the Face and Voices tabs leave the Build hub (Migration), and no user loses access to a page they can open today (Risks and test plan). The first draft also said Voice and Face are "shipped with LocalAI" without saying how. **Ruling:** Voice and Face have no hub tab and no other fixed entry. They are not shipped (question (e)). A user finds them in Browse add-ons and, once installed, in the drawer. No user loses access to a page: until the add-on is installed, `/app/voice`, `/app/face` and the other old routes open the core page, and afterwards they open the add-on. The same rule is applied to fine-tuning and quantization, which also leave the Build hub (the Build overview cards stay).

### (e) Distribution of Voice, Face, fine-tuning and quantization

Decided on 2026-10-16. They are add-on packages and do not ship with LocalAI. See "Distribution": the platform list, the runner of each platform, and the rule that a platform is enabled only after its native check passes.

### (f) Fine-tuning and quantization

Decided on 2026-10-16. They move in this change, as two add-ons. The API and the job managers stay in core; the add-ons get the UI and a tool surface; the host operations they need are new contract surface, each with its own tests. See "Host operations for the engine".

### (g) The pin seed

Decided on 2026-10-16. Existing accounts get Studio and Talk pinned once, at upgrade; new accounts start with no pins. See "The one-time seed".

### (h) SDK additions

Decided on 2026-10-16. The viewer, the WAV helper, the 26 icon names and the undo bar are accepted. See "Additions accepted on 2026-10-16".

### (i) Where the navigation routes are registered

Decided on 2026-10-16, taking the smallest change that keeps the existing route tests unedited: `RegisterAddonNavigationRoutes`, a function of its own. See "API". The maintainers may instead edit the three route tests and register the routes inside `RegisterAddonRoutes`; that is a larger change and is not planned.

### (j) What the legacy switch restores

Decided on 2026-10-16. The switch brings back the old sidebar and the old Add-ons hub tab. The redirects of the moved generator pages do not depend on it, and the old page components stay in the bundle (Studio uses them). See "Rollback".

## Test plan

### What must be tested

Route mapping:

- Every row of the migration table resolves. A table-driven test renders the router for each old path and asserts the final location and the rendered page.
- `/app/image/:model` and the other model routes keep the model after redirect.
- Query strings and hashes survive a redirect.
- The `?tab=` Studio redirect still works.

Pin storage:

- Pin, unpin and reorder round-trip through the server.
- A second user does not see the first user's pins.
- With authentication off, the local owner's pins persist across a reload.
- Two frontends on one database return the same list (use the existing fake-bus pattern from the distributed-state guide).
- The cap of eight is enforced in the UI. The server rejects a list over a hard limit.
- The upgrade seed pins Studio and Talk once, for the accounts that exist at the upgrade, and never again. An account created after the upgrade has no pins. A read or a save never adds a pin. With authentication off, the local owner is seeded once and an unpin sticks across a restart. Two frontends that start together seed once between them.
- The start of the 30-day window of a removed add-on survives a restart in both stores.

Permission filtering:

- A non-administrator does not see disabled add-ons, in the drawer or in the Pinned section.
- An administrator sees disabled add-ons as stopped, with a Start action.
- A user without the `addons` feature sees the built-in tiles (Studio and Talk) and no packaged add-on tile, no Browse and no Add entry. `GET /api/addons/apps` answers 403 for that user.
- A non-administrator who calls an admin-only core route from an add-on gets 403. A user without the feature of a core route gets 403 from it.
- A user without `face_recognition` sees the off notice in the Face add-on, and every core face call answers 403. A user without `fine_tuning` sees the off notice in the fine-tuning add-on, and every core fine-tuning call answers 403. The tile itself shows for anyone who holds the `addons` feature, because the manifest has no field to hide it.
- A host operation is refused without a live request of the add-on, with an expired authority, with a scope below its own, for an actor without the feature, and with an argument that names a user. A job started by a tool appears in the UI list of the same user. A result never carries a server path.
- No user loses access to a page they can open before the change.
- The pins route never returns an ID the caller cannot see.

Tile states:

- Running, stopped, starting and failed map to the states in the table.
- A stopped tile does not open the main page.
- Removing a pinned add-on hides the pin. Reinstalling inside the window restores it.

Redirects:

- Each redirect entry is covered. A test fails when a route in the old table is missing.
- A moved route opens the core page while its add-on is not installed and the add-on afterwards. A model identifier with `/` and `:` reaches the add-on through `?model=`.
- `sdk.core.fetch` refuses the denied prefixes written with percent-encoding.

Packaging:

- Each of the four packages lists every platform of the table in "Distribution" with its runner, and a platform is enabled exactly when its native check is recorded as passed. A test fails otherwise.
- The release workflow runs one set of legs for each add-on, builds no platform that is not enabled, and fails closed when an add-on has no enabled platform on a publishing event.
- Each package has a catalog template, so Browse add-ons can list it.

Quality gates:

- Existing UI tests and the coverage ratchet pass. Coverage is not lowered (`AGENTS.md`). The tests that pin SDK 1 are updated with the SDK change.
- The add-on inventory of `scripts/addons/suites.json` matches the specs of each suite.
- The drawer and the Pinned section pass the accessibility checks the UI already runs: keyboard focus, labels on the pin control, and a visible focus state.
- The layout works at phone width.

### Definition of done

- The sidebar shows Chat, Models, Add-ons, Settings and the Pinned section.
- The drawer lists installed add-ons with the states above, search, Browse and Add.
- Studio and Talk open from the drawer. Fine-tuning, quantization, Voice and Face are add-on packages that do not ship with LocalAI: they are listed in Browse add-ons, built for every platform of "Distribution" whose native check passed, and open from the drawer once installed. The only open item is whether Voice Library has a tile of its own (question (c)).
- Every old route in the table opens the same content.
- Pins are stored per user on the server and survive a browser change.
- No permission check is weaker than before.
- API routes are unchanged. Their tests pass without edits.
- The docs under `docs/content/` are updated in the same change.

## Risks and rollback

| Risk | Effect | Mitigation |
|---|---|---|
| A moved page loses a permission gate | A user sees a page they should not | Keep the gate table above. Test every row. The server still checks every API call. |
| A user loses access to Studio or Talk because they lack the `addons` feature | Users cannot reach a page they use | Decide the gate rule in the plan. Test that no user loses access. |
| Old bookmarks break | Dead links | Keep all redirects. Test all of them. |
| Pin table adds state to every frontend | Divergence between frontends | Store in the shared database. Test with two frontends. |
| The drawer hides tools that users used to reach in one click | Slower daily use | Pin Studio and Talk once, at upgrade, for the accounts that exist then. Allow eight pins. |
| The SDK version rises from 1 to 2 | An add-on built for SDK 1 only is refused | The manifests in this repository declare `min: 1, max: 2`. The refusal message names both ranges. |
| An add-on calls a core route with the user's session | The add-on code has the full API reach of the user | Gates stay on the server. The helper refuses `/api/addons/` and `/api/auth/`. Test the 403 paths. |
| Third-party add-ons expect `ui.navigation` to appear in the Build hub | Their links move | Keep the paths. Show the links in the add-on detail view and in the pinned group. |
| A user who has not installed Voice, Face, fine-tuning or quantization no longer has a hub tab for it | The user does not know the tool exists | The old URLs keep opening the core page; the Build overview keeps its cards for fine-tuning and quantization; each package is listed in Browse add-ons. |
| A platform is enabled before its native check passes | A broken package ships for that platform | A test fails when a platform is enabled without a recorded native check; the release workflow builds only enabled platforms. |
| An add-on process calls a host operation | The add-on reaches the job engine | A call needs a live request of that add-on, runs as that request's actor, passes the feature check, and is allowed only for add-ons the host setting names. |
| The pin seed pins for the wrong accounts | An account created after the upgrade gets pins it did not ask for | The seed runs once at upgrade, for the accounts that exist then, and the pin routes never seed. Tested for both cases. |

### Rollback

- Ship the new layout behind one UI flag, so the old sidebar can return without a rebuild. Remove the flag after one release.
- The old page components stay in the bundle during that release. The flag restores the old sidebar and the old Add-ons hub tab; it does not restore the old generator routes. The redirects of the moved generator pages (`/app/image` and its siblings) do not depend on the flag, because Studio draws the same pages and the redirects keep every old URL working. The old routes of Voice, Face, fine-tuning and quantization keep opening their core pages while the add-on is not installed. Rollback is a flag change for the navigation, and a code revert for the generator redirects.
- The pin table is additive. Rolling back leaves it unused. No down-migration is needed.
- If a redirect loops, remove only that redirect entry. The old page component is still present.

## Changelog

Changes of 2026-10-16, after the pre-flight review of the plan. One line each, with the review row it resolves ("scan C#"); a line that says "plan only" changed the plan and not this spec.

- Decisions 5, 6 and the Built-in add-ons section: fine-tuning and quantization are two packaged add-ons, moved in this change; the API and the job managers stay in core (scan C22, maintainer ruling).
- New section "Host operations for the engine": the four host operations, their rules, marked as new contract surface (scan C22, maintainer ruling).
- New decisions 10 to 12 and the section "Distribution": Voice, Face, fine-tuning and quantization are not shipped; platform list `linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64`, each with its runner; a platform is enabled only after its native check (scan C19, maintainer ruling).
- Question (d) and the Migration section: no hub tab for Voice or Face; discovery through Browse and the drawer; old routes open the core page until the add-on is installed (scan C18, maintainer ruling).
- New subsection "The one-time seed" and the Sidebar bullet: seed once at upgrade for existing accounts, new accounts start with no pins (scan C23, maintainer ruling).
- New subsection "Additions accepted on 2026-10-16" and the `HomeUndoToast` row: the viewer, the WAV helper, 26 icon names and the undo bar are accepted (scan C20, maintainer ruling).
- Section "API": the three routes are registered by `RegisterAddonNavigationRoutes`, so the existing route tests pass without edits (scan C1).
- Section "Disabled or removed while pinned": the start of the retention window is stored with the pin and survives a restart (scan C2).
- Section "Core routes from add-ons": the helper compares the denied prefixes on the decoded path and refuses an encoded separator in the first two segments (scan C16).
- Section "Redirects": the model of a moved route travels as `?model=`, and a moved page opens in its add-on only while the add-on is usable (scan C17).
- Section "Shared UI in the SDK": add-ons that use SDK 2 declare `min: 2, max: 2`; the tests that pin SDK 1 are updated with the SDK change (scan C14).
- Section "Rollback": the legacy switch restores the old sidebar and hub tab; the generator redirects do not depend on it (scan C13).
- Test plan and gate table: the tile of a packaged add-on shows for anyone with the `addons` feature and its page shows the off notice; tests added for the seed, the host operations, the platforms and the inventory (scan C19, C22, C15).
- Plan only: pin store keeps the owner and the missing mark in the file (scan C2); the two-frontends test moves to the service task (scan C3); `ClipInput` is built in the Voice library task (scan C8); the suite inventory is updated by Tasks 1, 4 and 9 (scan C15).
- Section "Extension points": the `app.nav` slot is drawn on the Add-ons drawer page below the tile grid, and in the Build hub row only under the legacy switch (Task 6 review, maintainer ruling).
- Section "Redirects" (Task 7 review, maintainer ruling): the redirect into Studio keeps the model as a path segment (`/app/studio/<tab>/<model>`), not `?model=`, because Studio is a core route. Revisit if Studio moves under the add-on host; cost if wrong: a redirect with a model needs a rewrite then.
- 2026-10-18: model registration through host operations (`finetune.export`, `quantize.import`) is administrator-only, on top of the feature (owner ruling). With an account database the role decides in every mode, so local-owner counts as administrator only when there is no account database; a feature-holding user who is not an administrator is refused with 403.
- 2026-10-18 (Task 24 review, maintainer ruling): `addons/internal/tools-ui/src/{tools.js,tools.css,format.js}` are byte copies of the core files (`core/http/react-ui/src/utils/tools.js`, `components/tools/tools.css`, and `formatBytes` of `utils/format.js`), an exception to one copy: the add-on builder refuses a bundle that imports a core UI file, so a re-export is not possible. A node test in tools-ui fails when a copy and its core file differ.
