// Pure rules of the Add-ons drawer and the Pinned section: how an app or a
// built-in entry becomes a tile, how tiles are searched and pinned. No React,
// no network.
import { localizedText } from '../components/addons/addonState.js'

// The sidebar shows at most this many pins. The drawer has no cap.
export const PIN_CAP = 8

function detailPathFor(id) {
  return `/app/addons?detail=${encodeURIComponent(id)}`
}

// A tile for an installed add-on. A tile never opens the main page of an
// add-on that is not running: the main page needs a live process.
export function tileFromApp(app, { language = 'en' } = {}) {
  const running = app.enabled === true && app.state === 'running'
  const state = running ? 'running' : 'stopped'
  const statusLine = app.enabled === true && (app.state === 'starting' || app.state === 'failed') ? app.state : ''
  const target = running && typeof app.mainPage === 'string' ? app.mainPage : ''
  return {
    id: app.id,
    source: 'app',
    title: localizedText(app, 'title', language) || app.id,
    titleKey: '',
    description: localizedText(app, 'description', language) || '',
    icon: app.icon || '',
    iconName: '',
    state,
    statusLine,
    target,
    detailPath: target ? '' : detailPathFor(app.id),
    app,
  }
}

// A tile for a built-in page add-on. Its page is a core route.
export function tileFromBuiltin(entry) {
  return {
    id: entry.id,
    source: 'builtin',
    title: entry.id,
    titleKey: entry.titleKey || '',
    description: '',
    icon: '',
    iconName: entry.icon || 'puzzle',
    state: 'running',
    statusLine: '',
    target: entry.mainPath,
    detailPath: '',
    app: null,
  }
}

export function buildTiles(apps, builtin, { language = 'en' } = {}) {
  const first = (builtin || []).map(tileFromBuiltin)
  const taken = new Set(first.map(t => t.id))
  const rest = [...(apps || [])]
    .filter(a => !taken.has(a.id))
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
    .map(a => tileFromApp(a, { language }))
  return [...first, ...rest]
}

// titleOf draws the displayed title (a built-in title is a translation).
export function filterTiles(tiles, query, titleOf = (t) => t.title) {
  const q = String(query || '').trim().toLowerCase()
  if (!q) return tiles
  return tiles.filter(t => titleOf(t).toLowerCase().includes(q) || t.description.toLowerCase().includes(q))
}

// The pinned tiles, in the order the user chose. A pin without a tile (hidden,
// removed) is left out silently.
export function resolvePins(ids, tiles) {
  const byId = new Map(tiles.map(t => [t.id, t]))
  return (ids || []).map(id => byId.get(id)).filter(Boolean)
}

// Pin or unpin. At the cap, a new pin is refused and the caller asks the user
// to unpin one.
export function togglePin(ids, id, cap = PIN_CAP) {
  if (ids.includes(id)) return { ids: ids.filter(x => x !== id), needsUnpin: false }
  if (ids.length >= cap) return { ids, needsUnpin: true }
  return { ids: [...ids, id], needsUnpin: false }
}

export function movePin(ids, id, delta) {
  const i = ids.indexOf(id)
  const j = i + delta
  if (i < 0 || j < 0 || j >= ids.length) return ids
  const next = [...ids]
  ;[next[i], next[j]] = [next[j], next[i]]
  return next
}
