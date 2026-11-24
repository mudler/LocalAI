// Pure rules of the dock: the panel beside a chat that shows pages of the
// workspace in tabs. No React, no network, no DOM: the dock, the chat page and
// the unit tests use the same functions. The server keeps the dock with the
// chat (GET and PUT /api/chats/{id}/dock) and checks the same bounds
// (core/services/studio/session/dock.go).
//
// A dock is {open, tabs: [{page, folder}], active, width}: folder is 0 for a
// page of the user's home and the id of a shared folder otherwise; width is
// the width in CSS pixels, 0 for the default.

export const DOCK_DEFAULT_WIDTH = 452
export const DOCK_MIN_WIDTH = 320
// The dock takes at most this share of the window, so the thread keeps room.
export const DOCK_MAX_SHARE = 0.6
export const DOCK_STORED_MAX = 4096
export const MAX_DOCK_TABS = 6
// One step of the keyboard on the edge, and the step with Shift.
export const DOCK_KEY_STEP = 16
export const DOCK_KEY_BIG_STEP = 64

const PAGE_NAME = /^[a-z0-9][a-z0-9-]{0,63}$/

const isObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v)

export const emptyDock = () => ({ open: false, tabs: [], active: 0, width: 0 })

// dockKey names a tab, or a ref of the tiles ({name, folder}).
export function dockKey(t) {
  const name = t?.page ?? t?.name ?? ''
  const folder = Number.isInteger(t?.folder) && t.folder > 0 ? t.folder : 0
  return folder ? `${folder}/${name}` : name
}

export function isDockKey(k) {
  const m = /^(?:(\d+)\/)?(.*)$/.exec(String(k || ''))
  return !!m && PAGE_NAME.test(m[2])
}

// tabOf reads a tab or a ref of the tiles into a tab, or null.
function tabOf(t) {
  const page = t?.page ?? t?.name
  if (typeof page !== 'string' || !PAGE_NAME.test(page)) return null
  if (t.folder !== undefined && t.folder !== null && t.folder !== 0 && !(Number.isInteger(t.folder) && t.folder > 0)) return null
  return { page, folder: Number.isInteger(t.folder) && t.folder > 0 ? t.folder : 0 }
}

// refOf is the ref of the tiles ({name, folder}) of a tab.
export const refOf = (tab) => (tab ? { name: tab.page, folder: tab.folder || 0 } : null)

// clampDockWidth is the width the panel takes in a window this wide: the
// stored width (the default for 0), at least DOCK_MIN_WIDTH and at most
// DOCK_MAX_SHARE of the window. A window too narrow for both keeps the
// minimum: the phone layout is a bottom sheet anyway.
export function clampDockWidth(width, viewport) {
  const w = Number.isFinite(width) && width > 0 ? Math.round(width) : DOCK_DEFAULT_WIDTH
  const max = Math.floor((Number.isFinite(viewport) ? viewport : 0) * DOCK_MAX_SHARE)
  if (max < DOCK_MIN_WIDTH) return DOCK_MIN_WIDTH
  return Math.min(max, Math.max(DOCK_MIN_WIDTH, w))
}

// keyStep is the width after a key on the edge (a separator: the arrows move
// it, Home and End go to the bounds, Enter resets it), or null for another
// key. The edge is on the left of the dock, so ArrowLeft widens it.
export function keyStep(key, width, viewport, big = false) {
  const step = big ? DOCK_KEY_BIG_STEP : DOCK_KEY_STEP
  switch (key) {
    case 'ArrowLeft': return clampDockWidth(width + step, viewport)
    case 'ArrowRight': return clampDockWidth(width - step, viewport)
    case 'Home': return DOCK_MIN_WIDTH
    case 'End': return Math.max(DOCK_MIN_WIDTH, Math.floor((Number.isFinite(viewport) ? viewport : 0) * DOCK_MAX_SHARE))
    case 'Enter': return DOCK_DEFAULT_WIDTH
    default: return null
  }
}

// readDock reads the dock the server gives (or nothing) into a dock: valid
// tabs, each once, at most MAX_DOCK_TABS, an active index of a tab and a
// width in bounds (0 otherwise).
export function readDock(raw) {
  if (!isObject(raw)) return emptyDock()
  const seen = new Set()
  const tabs = []
  for (const t of Array.isArray(raw.tabs) ? raw.tabs : []) {
    const tab = tabOf(t)
    if (!tab || seen.has(dockKey(tab))) continue
    seen.add(dockKey(tab))
    tabs.push(tab)
  }
  const list = tabs.slice(0, MAX_DOCK_TABS)
  const active = Number.isInteger(raw.active) && raw.active >= 0 && raw.active < list.length ? raw.active : 0
  const width = Number.isInteger(raw.width) && raw.width >= DOCK_MIN_WIDTH && raw.width <= DOCK_STORED_MAX ? raw.width : 0
  return { open: raw.open === true && list.length > 0, tabs: list, active, width }
}

// openTab puts the page of ref in front and opens the panel: a page already
// docked comes to the front, a new one goes at the end, and the oldest tab
// goes when the dock is full.
export function openTab(dock, ref) {
  const tab = tabOf(ref)
  if (!tab) return dock
  const d = dock || emptyDock()
  const at = d.tabs.findIndex((t) => dockKey(t) === dockKey(tab))
  if (at >= 0) return { ...d, open: true, active: at }
  const tabs = [...d.tabs, tab].slice(-MAX_DOCK_TABS)
  return { ...d, open: true, tabs, active: tabs.length - 1 }
}

// closeTab removes the tab at index i. The page in front stays in front when
// it is another one; when it is this one, the next tab (or the one before, at
// the end) comes to the front. The last tab closes the panel.
export function closeTab(dock, i) {
  if (!dock || i < 0 || i >= dock.tabs.length) return dock
  const tabs = dock.tabs.filter((_, j) => j !== i)
  if (tabs.length === 0) return { ...dock, open: false, tabs, active: 0 }
  let active = dock.active
  if (i < active) active -= 1
  else if (i === active) active = Math.min(i, tabs.length - 1)
  return { ...dock, tabs, active }
}

export const selectTab = (dock, i) => (dock && i >= 0 && i < dock.tabs.length ? { ...dock, active: i } : dock)
export const setDockOpen = (dock, open) => ({ ...(dock || emptyDock()), open: !!open })
export const setDockWidth = (dock, width) => ({ ...(dock || emptyDock()), width: Math.min(DOCK_STORED_MAX, Math.max(DOCK_MIN_WIDTH, Math.round(width) || DOCK_DEFAULT_WIDTH)) })

// dockToServer is the body of PUT /api/chats/{id}/dock.
export function dockToServer(dock) {
  const d = dock || emptyDock()
  return {
    open: !!d.open,
    tabs: d.tabs.map((t) => (t.folder ? { page: t.page, folder: t.folder } : { page: t.page })),
    active: d.active,
    width: d.width || 0,
  }
}

export function sameDock(a, b) {
  const x = a || emptyDock()
  const y = b || emptyDock()
  return x.open === y.open && x.active === y.active && (x.width || 0) === (y.width || 0) &&
    x.tabs.length === y.tabs.length && x.tabs.every((t, i) => dockKey(t) === dockKey(y.tabs[i]))
}
