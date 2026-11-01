# UI primitives

The shared components every page uses for buttons, page frames, empty and error
states, status, toasts, loading and detail panes. Import them from one place:

```jsx
import { Button, EmptyState, LoadError, Page, PageHeader, StatusChip } from '../components/ui'
```

All of them read the tokens in `src/theme.css` (type, space, radius, control
height, motion). Their rules are in `ui.css` in this folder. `main.jsx` loads
`ui.css` after `theme.css` and before `App.css`, so a page class on the same
element still wins over a primitive's base rule.

To see every component in every state, open `/app/ui-catalog` (admin only, not
in the navigation). `e2e/ui-primitives.spec.js` checks the catalog.

## Rules for every slice

- Do not write a new button, empty state, error state, status label or toast.
  Use the component. If it cannot do what you need, extend it here.
- No raw `font-size`, `border-radius` or `@keyframes` in page CSS. Use the
  tokens. `tests/unit/designtokens.test.mjs` counts them, and the counts may only
  go down.
- A page load that fails shows `<LoadError>` in the region that failed. It never
  shows the empty state, and it is never only a toast.
- Toasts are only for the result of an action the person just took.
- Motion uses the S0 tokens (`--dur-*`, `--ease-*`) and moves only `transform`
  and `opacity`. Under reduced motion everything finishes at once (the global
  rule in `index.css`).

## Button

`<Button variant size icon iconEnd busy disabled to href>`

| Prop | Values | Notes |
|---|---|---|
| `variant` | `primary`, `secondary` (default), `ghost`, `danger`, `danger-ghost` | |
| `size` | `md` (default, `--ctl-h`, 36 px), `sm` (`--ctl-h-sm`, 28 px), `lg` (48 px) | 40 px and 32 px on a touch screen |
| `icon`, `iconEnd` | a kit icon id | 16 px (14 px in `sm`), 6 px gap |
| `busy` | boolean | keeps the width, shows a spinner, sets `aria-busy` |
| `to` / `href` | a router path / an address | renders `<Link>` / `<a>` |

An `icon` with no children is a square icon button. Give it an `aria-label`.

Do:
- One `primary` per region. Write the label as a verb first, in sentence case:
  "Add MCP server", not "New MCP Server".
- `danger` (a solid error fill) only for the confirm button of a dialog.
- `danger-ghost` for "Remove…" in a pane or a menu.
- `sm` in rows, toolbars and panes.

Don't:
- Put a destructive action next to a primary at the same weight.
- Style a button with a page class to change its height, font or radius.

Markup that cannot use the component (an add-on page, a library slot) uses
`buttonClass({ variant, size, iconOnly })` from `buttonClass.js`.

### Legacy classes

The legacy classes look the same as the kit button, so old markup converges
with no edit. Move to `<Button>` when you edit the file.

| Legacy class | Same as |
|---|---|
| `btn` | `dk-btn` (secondary look) |
| `btn btn-primary` | `<Button variant="primary">` / `dk-btn dk-btn--primary` |
| `btn btn-secondary` | `<Button>` / `dk-btn dk-btn--secondary` |
| `btn btn-ghost` | `<Button variant="ghost">` |
| `btn btn-danger` | `<Button variant="danger">` |
| `btn btn-danger-ghost` | `<Button variant="danger-ghost">` |
| `btn-sm` | `size="sm"` / `dk-btn--sm` |
| `dk-btn` with no variant | `<Button>` (secondary) |

`btn-full`, `btn--warning` and `btn-help` are unchanged.

## Page and PageHeader

```jsx
<Page>                       {/* width="content" (1120 px) or "full" */}
  <PageHeader
    title="Agents"
    supporting="Saved agents: instructions, a model, and what a chat with them may use."
    actions={<Button variant="primary" icon="plus">New agent</Button>}
  />
  …
</Page>
```

- One title style: 28/34 semibold (24/30 on a phone). One left edge: the
  `.page` gutter (32 px, 16 px on a phone). Two widths: `content`
  (`--page-max-content`) and `full`.
- The supporting line is one sentence, up to 64 characters a line.
- The actions sit at the bottom right. On an empty page the empty state carries
  the primary action, so the header leaves it out.
- No eyebrow or breadcrumb above the title.
- `Page` renders `.page page--content` (or `page--full`). A page that already
  has `.page` can add `page--content` instead.

Legacy: `.page-title` and `.page-header*` already have the new style. A bespoke
`h1` class (`tf-title`, `sw-title`, `ag-title` and others) moves to `PageHeader`
in the page's slice.

## EmptyState

`<EmptyState variant title body actions facts steps icon level />`

| Variant | Use |
|---|---|
| `first-run` (default) | nothing here yet: a dashed outline, the primary action and one ghost "learn" link on the right, 2-3 `facts` |
| `no-results` | a search or a filter found nothing: one line and a ghost "Clear filters" |
| `unavailable` | the feature is off or not allowed: muted, one sentence, a link to the setting |

- `facts`: `[{ text }, { text, ok: false }]` (a check or a cross).
- `steps`: numbered steps, only where order matters (Memory, Triggers).
- The title is a statement ("No MCP servers yet"), the body says what it is for
  and how to start.

Legacy: `.empty-state*` now uses the same type (a 17 px title, body text, 56ch).
`.dk-empty-title` and `.dk-empty-text` too. Replace them with `<EmptyState>` in
the page's slice.

## LoadError and useLoad

```jsx
const servers = useLoad(() => mcpApi.list(), [])
if (servers.status === 'loading') return <SkeletonRows />
if (servers.status === 'error') return (
  <LoadError title="Could not load your MCP servers." error={servers.error} onRetry={servers.retry} />
)
if (servers.data.length === 0) return <EmptyState … />
```

- `LoadError` is an error `Callout` with `role="alert"`: the bold `title` says
  what failed, then "The server answered “…”." and "Nothing was changed."
  (`nothingChanged={false}` drops it), and a **Try again** button. If
  `onRetry` returns a promise, the button stays busy until it settles.
- `useLoad(loader, deps)` returns `{ status, data, error, retry, reload }`.
  `status` is `loading`, `ready` or `error`. An answer to an older request is
  ignored. `reload()` keeps the data on screen while it loads again.

## Callout

`<Callout tone title actions icon role>` children `</Callout>`

`tone` is `info` (default), `ok`, `warn` or `error`. The `title` is the first
sentence in bold. `actions` hold `sm` buttons on the right; on a phone they wrap
under the text. Use it for something on this page that needs attention ("Tables
1.0.0 is available"). Amber (`warn`) is only for something that needs a
decision.

## StatusChip

`<StatusChip status tone label dot />`

One status vocabulary (`status.js`). Each word has a tone, an icon and its
words, so a state never depends on colour alone:

| `status` | Tone | Words |
|---|---|---|
| `running` | ok | Running |
| `ready` | ok | Ready |
| `starting` | accent (spinning icon) | Starting |
| `stopped` | idle | Stopped |
| `failed` | error | Failed |
| `update-available` | warn | Update available |
| `no-runs` | idle | No runs yet |

Aliases: `healthy`, `online`, `primary` → running; `loading`, `installing` →
starting; `idle`, `offline`, `missing` → stopped; `error`, `unhealthy`,
`degraded`, `down` → failed; `warning`, `draining`, `fallback`, `recovering` →
warn. An unknown word is idle.

For a chip outside the vocabulary pass `tone` and the words:
`<StatusChip tone="warn">Low disk</StatusChip>`. `dot` draws a 6 px dot in
place of the icon, for dense rows.

Do: one state per chip ("Starting", not "Off, starting"). Don't: use tone
colours for decoration.

`StatusPill` is the old name. It draws a `StatusChip` and keeps its classes
(`status-pill status-pill--success|warning|error|info|muted`) for existing
selectors. Use `StatusChip` in new code.

## Switch

`<Switch checked onChange label labelledBy describedBy disabled />`

The kit switch (`dk-switch`, 44 x 26 px) as a controlled button with
`role="switch"` and `aria-checked`. Space and Enter toggle it. `onChange`
receives the new value. Give it a name with `label`, or with `labelledBy` when
a visible label is on screen. A switch applies at once; do not pair it with a
Save button. For a choice that needs Save, use a checkbox.

Do: put it at the end of a 36 px row, name on the left. Don't: use it to run an
action ("Disable all" is a button).

## Toast

Pages get `addToast` from the outlet context:

```jsx
const { addToast } = useOutletContext()
addToast(`Deleted “${name}”.`, 'success', undefined, {
  action: { label: t('common:ui.undo'), onClick: restore },
})
```

`addToast(message, type, duration, options)`:

- `type`: `info`, `success`, `warning`, `error`. The surface is always the
  inverse surface; the type sets the icon and its colour.
- `duration`: leave it out. The defaults are 4 s for news, 8 s with an action
  or for a warning, and never for an error (it stays until closed). Hover and
  focus pause the timer.
- `options.action`: `{ label, onClick }`, an Undo button. A 2 px line shows the
  time left. A click runs the action and closes the toast.
- `options.link`: `{ href, text }`. `options.testId` for specs.

At most 3 show at once; a new one pushes the oldest out. On a chat page the
stack rises above the composer. Any other bar fixed at the bottom of the window
can register with `useToastAnchor(ref)` (from `components/Toast`).

Do: say what happened and offer Undo instead of a confirm dialog for anything
reversible. Don't: report a page load failure in a toast.

## Skeleton, SkeletonRows and LoadingSpinner

- `<SkeletonRows count={3} />`: rows in the shape of a list row. They appear
  after 200 ms, so a fast answer never flashes them. Use for a first load only;
  a refresh keeps the old content.
- `<Skeleton variant="line|title|block|circle" count width height />`: single
  blocks, for a custom shape.
- `<LoadingSpinner size="sm|md|lg" />`: a short wait with no shape to show.
- The sweep is a transform on a pseudo-element. Under reduced motion the blocks
  stand still.

## RouteFallback and ShellFallback

- `RouteFallback` is the Suspense fallback of lazy routes: a title, a line and
  three rows in the page frame, after 200 ms. The sidebar and header stay.
- `ShellFallback` is the boot screen (`main.jsx`): the sidebar column (collapsed
  if the person left it so), the phone header and the content placeholder. A
  cold start never shows a blank window with a centred spinner.

## SearchField and Segmented

The list toolbar: a search box that fills the row, and one choice of a few.

```jsx
<div className="my-toolbar">
  <SearchField value={q} onChange={setQ} label="Search tasks" />
  <Segmented label="Show tasks" value={filter} onChange={setFilter}
    options={[{ id: 'all', label: 'All', count: 4 }, { id: 'off', label: 'Off', count: 1 }]} />
</div>
```

- `SearchField` needs a `label`; it is the accessible name and the placeholder.
- `Segmented` is a filter, not navigation: pressed buttons in a tray, with an
  optional count after each label. Each button has `data-filter={id}`. Use tabs
  to change views.
- On a phone the page can let the tray fill the row and scroll sideways.

## SidePane

```jsx
<PaneLayout>
  <div>…the list…</div>
  <SidePane open={!!selected} title={selected?.name} meta={…} footer={…} onClose={() => setSelected(null)}>
    …
  </SidePane>
</PaneLayout>
```

- 360 px, or 400 px with `wide`. The header (17/600 title, a meta row, close)
  stays; the body scrolls; the `footer` stays at the bottom.
- From 1280 px it docks and pushes the content. Below that it is an overlay
  with a scrim (a dialog, rendered under `<body>`). Below 640 px it is a bottom
  sheet at 90 % of the height.
- Escape and a click on the scrim close it, and focus returns to the control
  that opened it.
- It slides in with transform and opacity; the content width changes in one
  step.
- For a modal form (create, edit), use `SideSheet` (`components/SideSheet.jsx`),
  which also keeps Tab inside.
