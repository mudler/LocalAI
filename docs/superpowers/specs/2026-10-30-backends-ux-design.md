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
