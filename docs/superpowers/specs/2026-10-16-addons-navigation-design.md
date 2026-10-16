# Add-ons navigation design

## Status

- Status: approved for implementation planning. Three questions stay open (see "Open questions").
- Date: 2026-10-16.
- Owners: Ettore Di Giacinto (maintainer) and the AI agent that drafted this spec.
- Branch: `feat/addons-tables` (PR 12702).
- Scope: documents only. This change adds no source code.

### Approved decisions

1. Top-level navigation has four items: Chat, Models, Add-ons, Settings.
2. The Add-ons item opens a drawer page. It shows the installed add-ons as a grid of tiles. Each tile has an icon, a name and a state (running or stopped). The page has a search box, a Browse add-ons button and an "Add a new add-on" entry.
3. Each tile has a pin control. Pinned add-ons appear in the sidebar under a Pinned section. Pinning is per user. The sidebar shows a short pinned list. The drawer shows all installed add-ons.
4. Studio is an add-on. It contains Images, Audio and Video. Talk is an add-on. Both open from the drawer and can be pinned. Neither is a top-level item.
5. Fine-tuning and quantization move out of core into an add-on. The API routes stay in core unless a later spec moves them.
6. Four UI pages become UI-only add-ons: Face Recognition, Voice Recognition, Voice Library and Voice Profile Create. The `/v1/face` and `/v1/voice` APIs and the enrolled registries stay in core. The placement is approved. The shared-component and API-access questions are open.
7. Agents, Memory and Skills are candidates for later add-ons. They are out of scope here.

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
- Changing the add-on runtime, the process supervisor or the add-on contract beyond what navigation needs. This spec asks for no new manifest field (see "Manifest fields for navigation").
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

### Built-in add-ons

Studio, Talk, the fine-tuning and quantization tools and the four voice and face pages are shipped with LocalAI. They appear in the drawer like any installed add-on. Whether each one is a packaged add-on with a process or a UI-only add-on is part of the implementation plan. The navigation contract is the same either way: a title, an icon, a main page and a state.

A UI-only add-on has no process. Its tile state is "running" while the feature is enabled and "stopped" when an administrator disables it.

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

### API

Two routes are needed. They are user-scoped and need no admin role:

- `GET /api/addons/pins` returns the ordered list of pinned add-on IDs for the caller.
- `PUT /api/addons/pins` replaces the list. The body is the ordered list of IDs. The server drops IDs the caller may not see (see "Permissions").

The final route names are set in the implementation plan. The endpoints must follow the checklist in `.agents/api-endpoints-and-auth.md`, including the feature registry in `core/http/auth/features.go`.

### Disabled or removed while pinned

The server does not delete pin rows when an add-on is disabled, stopped, updated or removed. The sidebar resolves each pinned ID against the Apps list at render time.

| Event | Sidebar | Drawer | Pin row |
|---|---|---|---|
| Add-on stopped or disabled | Entry stays, muted. | Tile shows stopped. | Kept. |
| Add-on removed (uninstalled) | Entry is hidden. | No tile. | Kept for 30 days, then deleted by a cleanup job. If the add-on is installed again, the pin returns. |
| Add-on updated to a new generation | Entry stays. | Tile updates. | Kept. The pin stores the add-on ID, not the generation. |
| Add-on hidden from this user (access change) | Entry is hidden. | No tile. | Kept. |

The 30-day window is a starting value. The implementation plan may change it. It must not be shorter than the time an administrator needs to reinstall after a mistake.

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
| Fine-tuning | `fine_tuning` (`permissions.go:38`) | `fine_tuning`, plus `addons` to see the tile |
| Quantization | `quantization` (`permissions.go:39`) | `quantization`, plus `addons` to see the tile |
| Face Recognition | `face_recognition` (`permissions.go:61`) | same, plus `addons` |
| Voice Recognition | `voice_recognition` (`permissions.go:62`) | same, plus `addons` |
| Voice Library, Voice Profile Create | administrator (`router.jsx:239`, `router.jsx:298`) | administrator, plus `addons` |
| Studio, Talk | none in the router (`router.jsx:212-216`); each tab inside Studio has its own gate (`Studio.jsx:26-32`) | `addons` to see the tile; inner gates unchanged |

A built-in add-on tile is shown only when the viewer passes both its own gate and the `addons` gate. This keeps a user who today reaches Studio without the `addons` feature from losing access. The implementation plan must decide how: either Studio and Talk tiles ignore the `addons` gate, or the `addons` feature is granted by default for these built-ins. This is a detail to settle in the plan. The rule is that no user loses access to a page they can open today.

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
| `/app/fine-tune` | Build hub | Fine-tuning and quantization add-on. |
| `/app/quantize` | Build hub | Fine-tuning and quantization add-on. |
| `/app/face`, `/app/face/:model` | Build hub | Face Recognition add-on. |
| `/app/voice`, `/app/voice/:model` | Build hub | Voice Recognition add-on. |
| `/app/voice-library` | Build hub (match) | Voice Library add-on (placement open, see question c). |
| `/app/voice-library/new` | page | Voice Profile Create add-on. |
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
- Deep links with a model (`/app/image/:model`) keep the model.
- Bookmarks to `/app/browse/*` keep working through `BrowseRedirect` (`router.jsx:180-183`).

### Sidebar after migration

- The Create group is removed. Chat moves to the top level. Studio and Talk leave the sidebar unless the user pins them.
- The Build and Operate hubs keep their tabs, except for the tabs that moved to add-ons. The fine-tuning, quantization, Face, Voices and Add-ons tabs leave the Build hub. The hub's `addonNavigation` second row (`hubConfig.js:74`) goes away: its job moves to the drawer and to the pinned group.
- Users who had Studio or Talk in the sidebar see them in the drawer. On first load after the upgrade, the server pins Studio and Talk for existing users so the layout does not change under them. This one-time seed runs once per user and never again, so a later unpin sticks.

### Documentation

The change that implements this spec must update the pages under `docs/content/` that describe the sidebar and the add-ons surface, including `docs/content/features/addons.md`. This follows the docs-with-code rule in `AGENTS.md`.

## Open questions

These are not decided. Each one needs an answer before the add-on that depends on it is built.

### (a) Direct API calls from the face and voice add-ons

The Face Recognition and Voice Recognition pages call `/v1/face/*` and `/v1/voice/*`. The Voice Library page uses `/api/voice-profiles`. These routes stay in core and are gated by the user's feature permissions (`core/http/auth/features.go:109-135`).

Open: may a UI-only add-on call these core routes directly from the browser? If yes, how does the call authenticate? Two options exist: the add-on page uses the user's session, as core pages do today; or the call goes through the add-on gateway with a declared scope. The first is simple but gives the add-on code the full API reach of the user. The second needs a new contract concept. No decision yet.

### (b) Shared UI components

The moved pages use shared components: the media input, the waveform player, the media capture hook and the page header. Open: does the add-on SDK (`core/http/react-ui/src/addons/sdk.js`, `hostComponents.jsx`) export them, or does each add-on copy them? Export keeps one copy and a consistent look, but it widens the SDK surface and its version contract. Copying avoids the contract change but forks the code. No decision yet.

### (c) Where the Voice Library entry lives

Voice Library lists the enrolled speech voices. It can be a tile of its own, a tab of the Voice Recognition add-on, or a page of the Studio Audio area. The approved placement says it is a UI-only add-on. Open: whether its drawer entry is a separate tile or is reached from another add-on. No decision yet.

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
- The first-run seed pins Studio and Talk once and never again.

Permission filtering:

- A non-administrator does not see disabled add-ons, in the drawer or in the Pinned section.
- An administrator sees disabled add-ons as stopped, with a Start action.
- A user without the `addons` feature sees no Add-ons item and gets the no-access page on `/app/addons`.
- A user without `face_recognition` does not see the Face tile. A user without `fine_tuning` does not see the fine-tuning tile.
- No user loses access to a page they can open before the change.
- The pins route never returns an ID the caller cannot see.

Tile states:

- Running, stopped, starting and failed map to the states in the table.
- A stopped tile does not open the main page.
- Removing a pinned add-on hides the pin. Reinstalling inside the window restores it.

Redirects:

- Each redirect entry is covered. A test fails when a route in the old table is missing.

Quality gates:

- Existing UI tests and the coverage ratchet pass. Coverage is not lowered (`AGENTS.md`).
- The drawer and the Pinned section pass the accessibility checks the UI already runs: keyboard focus, labels on the pin control, and a visible focus state.
- The layout works at phone width.

### Definition of done

- The sidebar shows Chat, Models, Add-ons, Settings and the Pinned section.
- The drawer lists installed add-ons with the states above, search, Browse and Add.
- Studio, Talk, fine-tuning and quantization open from the drawer. The four voice and face pages open from the drawer once questions (a) to (c) are answered.
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
| The drawer hides tools that users used to reach in one click | Slower daily use | Seed Studio and Talk as pins. Allow eight pins. |
| Open questions (a) to (c) delay the voice and face pages | Those pages stay in core longer | Ship the navigation first. Move those four pages in a later change. |
| Third-party add-ons expect `ui.navigation` to appear in the Build hub | Their links move | Keep the paths. Show the links in the add-on detail view and in the pinned group. |

### Rollback

- Ship the new layout behind one UI flag, so the old sidebar can return without a rebuild. Remove the flag after one release.
- Moved pages keep their old route entries and components during that release. Rollback is then a flag change.
- The pin table is additive. Rolling back leaves it unused. No down-migration is needed.
- If a redirect loops, remove only that redirect entry. The old page component is still present.
