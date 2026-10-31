// Pure rules of the page tiles of Home: a widget of type page in home.json
// runs a page of the workspace in its compact mode. No React, no network, no
// DOM here; the tile component and the unit tests use the same functions.

export const PAGE_TILE_SIZES = ['small', 'medium', 'large']
// The server takes at most this many page widgets in home.json, and Home keeps
// at most this many frames alive at once.
export const MAX_PAGE_TILES = 3

const PAGE_NAME = /^[a-z0-9][a-z0-9-]{0,63}$/

// tileRef reads the page a widget names: {name, folder} where folder is the id
// of a shared folder or 0 for a page of the user's home; null when the widget
// names no valid page.
export function tileRef(w) {
  const name = typeof w?.page === 'string' ? w.page : ''
  if (!PAGE_NAME.test(name)) return null
  const folder = Number.isInteger(w.folder) && w.folder > 0 ? w.folder : 0
  return { name, folder }
}

// tileSize is the size of a page's tile: its widget block's, medium otherwise.
export function tileSize(page) {
  const s = page?.widget?.size
  return PAGE_TILE_SIZES.includes(s) ? s : 'medium'
}

// tileTitle is what the bar of the tile says: the widget's own title, else the
// widget block's, else the page's title, else its name.
export function tileTitle(w, page) {
  for (const t of [w?.title, page?.widget?.title, page?.title]) {
    if (typeof t === 'string' && t.trim()) return t.trim()
  }
  return tileRef(w)?.name || ''
}

// tileState says what a tile shows, from what the read of the page answered:
//
//   loading     the page is being read
//   missing     no such page, or the user cannot read it any more
//   noWidget    the page exists but its page.json has no widget block
//   needsAllow  a shared page whose current version the viewer has not allowed
//   off         an administrator turned shared pages off
//   error       the read failed for another reason (Retry)
//   ready       the page runs
//
// read is {loading, error, page, consent} where error carries the HTTP status
// and the error type of the server.
export function tileState(read) {
  if (!read || read.loading) return 'loading'
  const e = read.error
  if (e) {
    if (e.type === 'shared_pages_off') return 'off'
    if (e.status === 404 || e.status === 400) return 'missing'
    if (e.status === 403 && e.type !== 'page_not_allowed') return 'missing'
    return 'error'
  }
  const p = read.page
  if (!p) return 'missing'
  if (!p.widget) return 'noWidget'
  if (p.source === 'shared' && read.consent !== 'allowed') return 'needsAllow'
  return 'ready'
}

// tilePagePath is the route of the full page.
export function tilePagePath(ref) {
  if (!ref) return '/app/pages'
  return ref.folder
    ? `/app/pages/shared/${encodeURIComponent(ref.folder)}/${encodeURIComponent(ref.name)}`
    : `/app/pages/${encodeURIComponent(ref.name)}`
}

// eligiblePages are the pages that can be a tile: the ones with a widget
// block, own pages first, each with the ref a widget stores.
export function eligiblePages(own, shared) {
  const out = []
  for (const p of Array.isArray(own) ? own : []) {
    if (p?.widget && PAGE_NAME.test(p.name || '')) out.push({ key: p.name, ref: { name: p.name, folder: 0 }, page: p })
  }
  for (const p of Array.isArray(shared) ? shared : []) {
    if (p?.widget && PAGE_NAME.test(p.name || '') && Number.isInteger(p.folder_id) && p.folder_id > 0) {
      out.push({ key: `${p.folder_id}/${p.name}`, ref: { name: p.name, folder: p.folder_id }, page: p })
    }
  }
  return out
}

// refKey is the key of eligiblePages for a widget's ref.
export const refKey = (ref) => (ref ? (ref.folder ? `${ref.folder}/${ref.name}` : ref.name) : '')

// The life of a tile's frame. A tile reserves its height from the start and
// makes its frame only once it scrolls into view; a frame that scrolls a long
// way off (out of the near margin) is taken down, and one that is near but out
// of sight, or in a hidden tab, stays and hears that it is not visible so the
// page can pause. Home keeps at most MAX_PAGE_TILES frames alive.
//
//   prev      {alive, visible} of the frame now
//   inView    the tile is on screen
//   near      the tile is within the margin around the screen
//   hidden    the tab is hidden
//   slot      a frame slot is free for this tile (or it holds one already)
export function nextFrame(prev, { inView, near, hidden, slot }) {
  const alive = prev?.alive || false
  if (!alive) {
    if (inView && !hidden && slot) return { alive: true, visible: true }
    return { alive: false, visible: false }
  }
  if (!near && !inView) return { alive: false, visible: false }
  return { alive: true, visible: Boolean(inView && !hidden) }
}

// createFrameSlots hands out at most max frame slots. A tile that gets none
// waits; a release tells the waiting tiles (subscribe), and the first of them
// to ask again gets the slot.
export function createFrameSlots(max = MAX_PAGE_TILES) {
  const held = new Set()
  const listeners = new Set()
  return {
    acquire(id) {
      if (held.has(id)) return true
      if (held.size >= max) return false
      held.add(id)
      return true
    },
    release(id) {
      if (!held.delete(id)) return
      for (const fn of [...listeners]) fn()
    },
    holds: (id) => held.has(id),
    held: () => held.size,
    subscribe(fn) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
  }
}
