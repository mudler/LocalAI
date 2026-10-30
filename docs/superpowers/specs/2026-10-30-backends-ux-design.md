# Backends page: fewer button rows

Date: 2026-10-30. Page: `/app/backends` (`src/pages/Backends.jsx`,
`components/operate/BackendsTable.jsx`, `pages/operate.css`,
`components/upgrade/*`, `components/catalog/KindLibraries.jsx`).

Feedback from a live test: the page is full of buttons. About four rows of
controls stack above the list, and the page is hard to scan and use.

## 1. Critique

### Evidence

The page ran on the e2e `ui-test-server` with the operate fixtures
(`e2e/operate-fixtures.js`, `page.route` stubs). Screenshots at 1440x900 and
390x844, light and dark, in these states: Installed with two updates, an open
row, Catalog with an install in progress, search active, no search results,
nothing installed, first run, catalog load error, installed load error, 30
installed backends (top and scrolled), and cluster mode.

What the screenshots show:

- **Desktop, Installed with updates.** Under the hub title and its two tab rows
  (Status … API, then Backends · Activity · Logs · Failover) the page adds three
  more rows before the first backend: "Manage libraries"; the
  Installed/Catalog switch, the search, **Update all (2)** (filled), **Check
  for updates** and **From URL**; and a second segmented control (All 3 · User
  3 · System 0 · Updates 2) with the **Variants** and **Development** chips.
  The two segmented controls look the same, so the page reads as four levels of
  tabs. The first backend starts at y = 450.
- **Desktop, Catalog.** The same, with seven type chips (All, Chat, Image,
  Video, TTS, STT, Vision) in the third row: 15 controls above the list.
- **Phone.** Nine rows of controls before the first backend: Manage libraries,
  the view switch, search, Update all and Check, From URL, the state control,
  the two chips (and in Catalog two rows of type chips). The first backend
  starts at y = 600 of 844.
- **Open row.** The detail repeats the row's **Update** as a filled **Update to
  v0.9.7**. In Catalog an installed backend shows **Update**, **Reinstall** and
  **Remove** in the detail, while the same backend in Installed has them in
  the row's menu.
- **Nothing installed.** The state control shows All 0 · User 0 · System 0
  and the two chips over an empty state that has nothing to filter.
- **Installed load error.** A plain red line with "Retry". The catalog load
  error uses the `LoadError` callout. Two error styles on one page.
- **Many installed, scrolled.** The controls scroll away. To search or filter
  you go back to the top.

### Control inventory (before)

The page is admin only (`RequireAdmin`). Controls marked **install** show only
when the server allows installs (`--disable-backend-install` hides them).
Frequency is an estimate for an operator.

| Row | Control | What it does | Who | How often | Notes |
|---|---|---|---|---|---|
| H1 | Hub tabs (Status … API) | Administration sections | admin | every visit | Hub layout, not this page |
| H2 | Backends · Activity · Logs · Failover | Runtime sections | admin | every visit | Hub layout, not this page |
| 1 | Library chips (All + one per source) | Filter by library source | admin | rare | Only with library sources. A filter, shown as a row of its own |
| 1 | Manage libraries | Opens the library sources panel | admin | rare (setup) | Settings links here with `?libraries=1` |
| 2 | Installed · Catalog | The two views, with counts | admin | every visit | The page's one tab level |
| 2 | Search | Filters the current view | admin | often | |
| 2 | Update all (N), filled | Opens the update dialog | admin, install | when updates exist | 4th way to say "updates exist" (also: row state, Updates filter, hub badge) |
| 2 | Check for updates | Asks the server to check now | admin, install | rare | The server checks every 6 h |
| 2 | From URL | Shows the install-from-URL form | admin, install | rare | Custom images only |
| 3 | All · User · System · Updates · Offline (Installed) | State filter | admin | sometimes | Looks like a second tab level |
| 3 | All · Chat · Image · Video · TTS · STT · Vision (Catalog) | Type filter | admin | sometimes | 7 chips, 2 rows on a phone |
| 3 | Variants | Shows variant builds | admin | rare | Expert refinement |
| 3 | Development | Shows development builds | admin | rare | Expert refinement |
| 4 | Row: Install / Update / Choose nodes / Install on all / Retry / Cancel | The one action of the row | admin, install | often | Correct idea |
| 4 | Row: menu (Reinstall, Delete) | Rare row actions | admin | rare | Installed view only; Catalog has none |
| 4 | Row: chevron | Opens the row | admin | often | |
| 5 | "6 of 6" and the pager | Count and pages | admin | Catalog | The count sits under the list |
| D | Update to vX (filled) | Same as the row's Update | admin, install | | Duplicate |
| D | Reinstall, Remove (Catalog detail) | Rare row actions | admin, install | rare | Elsewhere they are in the row menu |
| D | Logs link, node controls | Per-node remove and reinstall | admin | rare | Belong in the detail |

Visible controls at the default state (Installed, two updates, desktop):
**12 above the list in 3 rows** (Catalog: 15), plus 3 per row.
On a phone: **9 rows of controls** above the first backend.

### What is wrong

1. **Too many rows of equal weight.** Rare controls (From URL, Check for updates,
   Manage libraries, Variants, Development) sit beside the common ones at the
   same size. Nothing tells the eye what matters.
2. **A second tab level that is a filter.** The state control is drawn as a
   segmented control, like the view switch above it. With the two hub rows it
   reads as four tab levels.
3. **Updates are said five times**: the hub badge, the filled Update all, the
   Updates filter, the row state, and the row button. The filled button is the
   only filled control on the page, so it wins over the list itself.
4. **Rows are not alike.** Installed rows have a menu; Catalog rows of the same
   backend have their rare actions in the detail, and the detail repeats Update.
5. **Filters that cannot filter.** On an empty list the state control shows
   zeros. The active filter is visible only as a pressed chip somewhere in a
   row; there is no count of results at the top.
6. **The controls scroll away** on a long list.
7. **Two error styles** for the two lists.

What works and stays: one list with two views, the row state words, one action
per row, the row opening in place, the URL keeping view, search, filter, open
row and target node, the update dialog that runs updates one after another.

## 2. Design

### Rules

- At most two rows of controls above the list, and only the first holds
  buttons. The second is a text line: the result count, the active filters as
  removable chips, and the update line when there are updates.
- One primary action per zone. The toolbar has none: what a person does here
  most is read the list. A row's action is a secondary `sm` button. The first
  run card and the empty state carry the page's only filled button.
- Rare actions go into one overflow menu. Filters go into one filter menu.
- Nothing is removed. Every action keeps its handler, its API call, its
  admin and install gating, and its URL parameter.

### Zones

**Toolbar (row A), sticky.** `Installed 3 · Catalog 6` (the page's one tab
level, unchanged), search, **Filters** (with the number of active filters),
and **More** (kebab). It sticks to the top of the scroll area, with the count
line under it, so search and filters stay in reach on a long list.

**Filters menu.** A popover anchored to the button (a bottom sheet on a
phone). Native radios and checkboxes, so arrow keys, Space and Tab work as
usual; Escape and a click outside close it and focus returns to the button.

- Installed: *State*: All, User, System, Updates, Offline nodes (each with
  its count; Updates and Offline only when they exist).
- Catalog: *Type*: All, Chat, Image, Video, TTS, STT, Vision.
- Both: *Show*: Variant builds, Development builds (with the hidden count).
- Both, when library sources exist: *Library*: All libraries, one per source.

**More menu** (`ActionMenu`): Check for updates, Install from URL…, Manage
libraries…. Install items hide when installs are off. "Install from URL…"
opens the form in a side sheet (`SideSheet`) instead of pushing the list down.
"Manage libraries…" opens the existing library panel; `?libraries=1` still
opens it on load.

**Count line (row B).** "3 backends" or "1 of 3 backends", the active filters
as chips with a remove button each (`Remove filter: User`), and on the right,
when updates exist and installs are on: "↑ 2 updates available · **Review**".
Review opens the existing `BulkUpgradeDialog`. While it runs the line says
"Updating 1 of 2…", and after it the summary, as `BulkUpgradeTrigger` does
today. The line is text with one ghost button, not a filled button.

**Rows.** The same in both views: the one action (Install, Update, Choose
nodes…, Install on all, Retry, Cancel), then a row menu, then the chevron.
The menu holds Reinstall backend and Delete backend in both views (Catalog
had them only in the open row). Install on specific nodes stays in the menu of
a cluster row. System backends keep the Protected badge and no menu.

**Open row.** Facts, used by, node controls and the Logs link. The duplicate
filled "Update to vX" and the Catalog Reinstall/Remove buttons go: the row
holds them. The node-scoped Reinstall and Remove from node stay in the open
row, because they act on one node.

**States.** Nothing installed: the empty state only, no filters (there is
nothing to filter). An installed list that failed to load: `LoadError` like
the catalog. No results: the existing no-results empty state with "Clear
filters".

**Bulk actions.** Updating several backends is the only bulk action. The
update dialog already is the selection (checkboxes, select all and none), so
the list gets no select mode of its own.

**Phone.** Row A wraps to two lines: the view switch and More, then search
and Filters. The filter menu is a bottom sheet, the same look as the Models
filter sheet. Row actions stay as on desktop: one action, menu, chevron.

### Before and after (desktop, Installed with two updates)

```
BEFORE                                                         controls
 [Manage libraries]                                                 1
 [Installed 3|Catalog 6] [search…]  [Update all (2)] [Check] [From URL]  5
 [All 3|User 3|System 0|Updates 2]            [Variants] [Development]   6
 llama-cpp   v0.9.4  ↑ Update 0.9.7                 [Update] ⋮ ›
 …
AFTER
 [Installed 3|Catalog 6] [search…………………]          [Filters] ⋮           4   (sticky)
 3 backends                       ↑ 2 updates available · [Review]      1
 llama-cpp   v0.9.4  ↑ Update 0.9.7                 [Update] ⋮ ›
 …
```

Phone: nine rows of controls become three (view switch and More; search and
Filters; the count line).

### Keyboard and focus

- Filters: Enter or Space opens; focus moves to the checked radio of the first
  group; Tab moves between groups; arrows move inside a group; Escape closes
  and focus returns to the button.
- More and row menus: `ActionMenu` (arrows, Home, End, Enter, Escape, focus
  back to the trigger).
- A removable chip is a button named "Remove filter: User".

### Out of scope

The hub's title and its two tab rows belong to the hub layout (S4) and stay.
