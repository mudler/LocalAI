// The data model behind the Studio front page.
//
// Studio has seven workspaces and each one already records what it made, in its
// own store: five browser-storage lists (useMediaHistory), 3D in IndexedDB
// (use3DHistory), and, from this change, one for diarization. This module turns
// those into one list of "work items", links items that were made from each
// other, and lays the links out as a lineage board. It is plain functions with
// no React and no storage, so it can be tested with `node --test`.
//
// Parent links are written by the workspace that makes the result: `parentId`
// is the id of the result it was made from and `edge` says how. Results made
// before the links existed have none and stand alone.

export const TYPE_ORDER = ['images', 'video', 'threed', 'tts', 'sound', 'transform', 'diarization']

// What the front page needs to know about each workspace that the workspaces
// themselves do not export:
//   media   the useMediaHistory key (null for 3D, which has its own store)
//   input   'text' when the workspace starts from a sentence, 'file' when it
//           needs a picture or a recording
//   source  the kind of file it can start from: 'image', 'audio' or null
//   sizes   the sizes its form offers, when it has a size control
//   tag     the gallery tag used to suggest a model when none is installed
export const TYPE_INFO = {
  images: { media: 'image', kind: 'image', input: 'text', source: 'image', sizes: ['256x256', '512x512', '768x768', '1024x1024'], defaultSize: '512x512', maxCount: 4, tag: 'text-to-image' },
  video: { media: 'video', kind: 'video', input: 'text', source: 'image', sizes: ['256x256', '512x512', '768x768', '1024x1024', '832x480', '1280x720'], defaultSize: '512x512', tag: 'text-to-video' },
  threed: { media: null, kind: 'mesh', input: 'file', source: 'image', needsSource: true, tag: 'image-to-3d' },
  tts: { media: 'tts', kind: 'audio', input: 'text', source: null, tag: 'tts' },
  sound: { media: 'sound', kind: 'audio', input: 'text', source: null, tag: 'music' },
  transform: { media: 'audio-transform', kind: 'audio', input: 'file', source: 'audio', needsSource: true, tag: 'audio-transform' },
  diarization: { media: 'diarization', kind: 'speakers', input: 'file', source: 'audio', needsSource: true, tag: 'diarization' },
}

export const TYPE_ICON = {
  images: 'image', video: 'video', threed: 'cube', tts: 'headphones', sound: 'music', transform: 'waveform', diarization: 'users',
}

const TYPE_BY_MEDIA = Object.fromEntries(
  Object.entries(TYPE_INFO).filter(([, v]) => v.media).map(([key, v]) => [v.media, key]),
)

export const EDGE_KINDS = ['take', 'animate', 'to-3d', 'variation', 'transform', 'diarize']

// What each kind of result can be sent on to. `supported` is whether the
// destination workspace accepts that result as a starting point today. The
// unsupported steps are kept in the list on purpose: the lineage view shows
// them disabled with the reason instead of hiding what a person might expect.
//   hint  names the reason: the destination takes no `source`
export const NEXT_STEPS = {
  images: [
    { to: 'video', edge: 'animate', supported: true },
    { to: 'threed', edge: 'to-3d', supported: true },
    { to: 'images', edge: 'variation', supported: true },
  ],
  video: [
    { to: 'sound', edge: 'soundtrack', supported: false, source: 'video' },
  ],
  threed: [
    { to: 'video', edge: 'turntable', supported: false, source: 'mesh' },
  ],
  tts: [
    { to: 'transform', edge: 'transform', supported: true },
    { to: 'diarization', edge: 'diarize', supported: true },
  ],
  sound: [
    { to: 'transform', edge: 'transform', supported: true },
    { to: 'diarization', edge: 'diarize', supported: true },
  ],
  transform: [
    { to: 'diarization', edge: 'diarize', supported: true },
  ],
  diarization: [],
}

// Every kind of result can be run again as a new take, but a few need the
// original input and the workspace no longer has it.
//   threed        the picture is kept only as a small thumbnail
//   diarization   the recording is not kept
// Everything else carries what it needs in the stored entry.
export function canTake(item) {
  if (!item) return { ok: false }
  if (item.type === 'threed') return { ok: false, reason: 'noInput' }
  if (item.type === 'diarization') return { ok: false, reason: 'noInput' }
  if (item.type === 'transform' && !item.inputUrl) return { ok: false, reason: 'noInput' }
  return { ok: true }
}

// --- Suggesting a type from the words ---------------------------------------

// First match wins, so the narrow cases sit above the broad ones. No network,
// no model: a short list of words people use for each kind of work. A wrong
// guess costs one click, so it only ever suggests and never switches.
const SUGGESTIONS = [
  ['diarization', /\b(who (spoke|said|talks?)|speakers?|diari[sz]\w*)\b/i],
  ['transform', /\b(clean up|denoise|noise reduction|change my voice|voice conver\w*|remove (the )?background noise)\b/i],
  ['threed', /\b(3-?d|mesh|glb|turn it into an? object)\b/i],
  ['video', /\b(video|clip|footage|timelapse|time-lapse|dolly|pan(ning)?|tracking shot|drone shot|camera move|animate)\b/i],
  ['sound', /\b(music|ambient|ambience|lo-?fi|soundtrack|beat|piano|jingle|sound effects?|melody|song)\b/i],
  ['tts', /\b(read (it |this )?(aloud|out)|narrat\w*|voice-?over|speak|say|text to speech|tts)\b/i],
  ['images', /\b(image|picture|photo(graph)?|painting|illustration|poster|portrait|logo|drawing|sketch|render|wallpaper|icon)\b/i],
]

export const MIN_SUGGEST_LENGTH = 8

export function suggestType(text) {
  const words = String(text || '').trim()
  if (words.length < MIN_SUGGEST_LENGTH) return null
  for (const [type, pattern] of SUGGESTIONS) {
    if (pattern.test(words)) return type
  }
  return null
}

// --- Work items -------------------------------------------------------------

export function parseSize(size) {
  const m = /^(\d{2,5})\s*[x×]\s*(\d{2,5})$/i.exec(String(size || '').trim())
  return m ? { width: Number(m[1]), height: Number(m[2]) } : null
}

// Width over height for a tile. Only what the entry recorded: an entry with no
// size is drawn square rather than guessed.
export function aspectOf(item) {
  const size = parseSize(item?.params?.size)
  return size ? size.width / size.height : 1
}

const clip = (value, max) => {
  const s = String(value ?? '')
  return s.length > max ? s.slice(0, max) : s
}

// One normalised item from a stored history entry. `type` is the Studio key.
export function toWorkItem(type, entry) {
  const results = Array.isArray(entry?.results) ? entry.results : []
  const outputs = results.filter(r => r && r.url && (!r.kind || r.kind === 'output'))
  const input = results.find(r => r && r.url && r.kind === 'input')
  const title = type === 'threed'
    ? (entry.label || entry.name || '')
    : (entry.prompt || '')
  return {
    id: String(entry.id),
    type,
    title: clip(title, 2000),
    model: entry.model || '',
    createdAt: Number(entry.createdAt) || 0,
    parentId: entry.parentId ? String(entry.parentId) : null,
    edge: entry.edge || null,
    url: outputs[0]?.url || '',
    urls: outputs.map(r => r.url),
    inputUrl: input?.url || '',
    thumb: type === 'threed' ? (entry.inputThumb || '') : '',
    params: entry.params && typeof entry.params === 'object' ? entry.params : {},
    elapsedMs: Number(entry.elapsedMs) || 0,
    outputType: entry.outputType || '',
  }
}

// Every stored entry as one list, newest first. `media` is the object that
// readAllMediaHistory() returns; `threeD` is the array use3DHistory() holds.
export function collectWork(media, threeD, { favourites = [] } = {}) {
  const fav = new Set(favourites)
  const out = []
  for (const [key, entries] of Object.entries(media || {})) {
    const type = TYPE_BY_MEDIA[key]
    if (!type || !Array.isArray(entries)) continue
    for (const entry of entries) {
      if (entry && entry.id != null) out.push(toWorkItem(type, entry))
    }
  }
  for (const entry of Array.isArray(threeD) ? threeD : []) {
    if (entry && entry.id != null) out.push(toWorkItem('threed', entry))
  }
  return out
    .map(item => ({ ...item, favourite: fav.has(item.id) }))
    .sort((a, b) => b.createdAt - a.createdAt)
}

export function countByType(items) {
  const counts = { all: items.length, favourites: 0 }
  for (const key of TYPE_ORDER) counts[key] = 0
  for (const item of items) {
    counts[item.type] += 1
    if (item.favourite) counts.favourites += 1
  }
  return counts
}

export function filterWork(items, filter) {
  if (filter === 'favourites') return items.filter(i => i.favourite)
  if (TYPE_INFO[filter]) return items.filter(i => i.type === filter)
  return items
}

// --- Projects ---------------------------------------------------------------

// Results linked by a parent that still exists form one project. A link to a
// result that has since been deleted is ignored, so the child stands as a root.
function parentMap(items) {
  const ids = new Set(items.map(i => i.id))
  const parents = new Map()
  // A link that would close a loop is dropped; storage can be edited by hand.
  const loops = (child, parent) => {
    for (let cur = parent, n = 0; cur && n < 1000; cur = parents.get(cur), n += 1) if (cur === child) return true
    return false
  }
  for (const item of items) {
    if (item.parentId && item.parentId !== item.id && ids.has(item.parentId) && !loops(item.id, item.parentId)) {
      parents.set(item.id, item.parentId)
    }
  }
  return parents
}

function components(items) {
  const parents = parentMap(items)
  const root = new Map()
  const find = (id) => {
    let cur = id
    const seen = new Set()
    while (root.has(cur) && root.get(cur) !== cur && !seen.has(cur)) { seen.add(cur); cur = root.get(cur) }
    return cur
  }
  for (const item of items) root.set(item.id, item.id)
  for (const [child, parent] of parents) {
    const a = find(child)
    const b = find(parent)
    if (a !== b) root.set(a, b)
  }
  const groups = new Map()
  for (const item of items) {
    const key = find(item.id)
    if (!groups.has(key)) groups.set(key, [])
    groups.get(key).push(item)
  }
  return [...groups.values()]
}

// The result that stands for a project on a tile: the newest one with a picture
// to show, else the newest.
function coverOf(members) {
  const byNew = [...members].sort((a, b) => b.createdAt - a.createdAt)
  return byNew.find(m => m.type === 'images' || m.thumb) || byNew.find(m => m.type === 'video') || byNew[0]
}

// The masonry's tiles, newest activity first. A set of two or more linked
// results becomes one project tile; the rest stay single.
export function groupWork(items) {
  const tiles = components(items).map((members) => {
    const sorted = [...members].sort((a, b) => a.createdAt - b.createdAt)
    if (members.length === 1) return { kind: 'single', id: members[0].id, item: members[0], latest: members[0].createdAt }
    const root = sorted.find(m => !m.parentId || !members.some(o => o.id === m.parentId)) || sorted[0]
    return {
      kind: 'project',
      id: root.id,
      items: sorted,
      cover: coverOf(members),
      title: root.title,
      latest: sorted[sorted.length - 1].createdAt,
      favourite: members.some(m => m.favourite),
    }
  })
  return tiles.sort((a, b) => b.latest - a.latest)
}

// The project a result belongs to, as the same list the tile holds.
export function projectOf(items, id) {
  const group = components(items).find(members => members.some(m => m.id === id))
  if (!group) return null
  return [...group].sort((a, b) => a.createdAt - b.createdAt)
}

// --- Lineage board ----------------------------------------------------------

export const BOARD = { nodeW: 156, srcH: 128, resH: 134, gapX: 96, gapY: 22, pad: 28 }

// The node an item's edge starts from. A result with no parent grows from its
// own prompt or file. A take shares the origin of the result it repeats, so two
// takes of one prompt hang off the same prompt node. Anything else grows from
// its parent result.
function originOf(item, byId, parents) {
  const parent = parents.has(item.id) ? byId.get(parents.get(item.id)) : null
  if (!parent) return `src:${item.id}`
  if (item.edge === 'take') return originOf(parent, byId, parents)
  return `res:${parent.id}`
}

// Nodes, edges and the chain of the selection, positioned on a grid. `ghost` is
// an optional dashed node hung off the selected result: { id, from, type,
// label, missing }. Positions are plain numbers in board pixels.
export function layoutLineage(members, { selectedId = null, ghost = null } = {}) {
  const byId = new Map(members.map(m => [m.id, m]))
  const parents = parentMap(members)
  const nodes = new Map()
  const edges = []

  // Sources first, so a take can find the prompt it shares whatever the order.
  for (const m of members) {
    if (parents.has(m.id)) continue
    const info = TYPE_INFO[m.type]
    nodes.set(`src:${m.id}`, {
      id: `src:${m.id}`, kind: 'source', input: info.input, type: m.type,
      title: info.input === 'text' ? m.title : '', createdAt: m.createdAt, children: [],
    })
  }
  for (const m of members) {
    const from = originOf(m, byId, parents)
    nodes.set(`res:${m.id}`, { id: `res:${m.id}`, kind: 'result', item: m, children: [] })
    edges.push({ id: `${from}>res:${m.id}`, from, to: `res:${m.id}`, label: parents.has(m.id) ? m.edge : null })
  }
  if (ghost) {
    nodes.set(ghost.id, { id: ghost.id, kind: 'ghost', ghost, children: [] })
    edges.push({ id: `${ghost.from}>${ghost.id}`, from: ghost.from, to: ghost.id, label: ghost.edge, ghost: true })
  }
  for (const e of edges) nodes.get(e.from)?.children.push(e.to)

  // Children in the order they were made.
  const stamp = (id) => {
    const n = nodes.get(id)
    return n.kind === 'result' ? n.item.createdAt : n.kind === 'ghost' ? Infinity : n.createdAt
  }
  for (const n of nodes.values()) n.children.sort((a, b) => stamp(a) - stamp(b))

  // Depth is distance from the root node; rows come from a leaf-counting walk.
  const hasParent = new Set(edges.map(e => e.to))
  const roots = [...nodes.values()].filter(n => !hasParent.has(n.id)).sort((a, b) => stamp(a.id) - stamp(b.id))
  let row = 0
  const place = (id, depth) => {
    const n = nodes.get(id)
    n.depth = depth
    if (n.children.length === 0) { n.row = row; row += 1; return }
    n.children.forEach(c => place(c, depth + 1))
    const kids = n.children.map(c => nodes.get(c).row)
    n.row = (Math.min(...kids) + Math.max(...kids)) / 2
  }
  roots.forEach(r => place(r.id, 0))

  const { nodeW, srcH, resH, gapX, gapY, pad } = BOARD
  const rowH = Math.max(srcH, resH) + gapY
  let width = 0
  let height = 0
  for (const n of nodes.values()) {
    const h = n.kind === 'source' ? srcH : resH
    n.w = nodeW
    n.h = h
    n.x = pad + n.depth * (nodeW + gapX)
    n.y = pad + n.row * rowH + (rowH - gapY - h) / 2
    width = Math.max(width, n.x + n.w + pad)
    height = Math.max(height, n.y + n.h + pad)
  }

  // The chain: the selection and everything above it, back to the root.
  const parentOf = new Map(edges.map(e => [e.to, e.from]))
  const chain = new Set()
  let cur = selectedId ? `res:${selectedId}` : null
  while (cur && nodes.has(cur) && !chain.has(cur)) { chain.add(cur); cur = parentOf.get(cur) }
  if (ghost && chain.has(ghost.from)) chain.add(ghost.id)

  const placed = edges.map(e => {
    const a = nodes.get(e.from)
    const b = nodes.get(e.to)
    const x1 = a.x + a.w
    const y1 = a.y + a.h * 0.42
    const x2 = b.x
    const y2 = b.y + b.h * 0.42
    const mid = (x1 + x2) / 2
    return {
      ...e, x1, y1, x2, y2,
      d: `M${x1} ${y1} C${mid} ${y1} ${mid} ${y2} ${x2} ${y2}`,
      lx: mid, ly: (y1 + y2) / 2,
      chain: chain.has(e.from) && chain.has(e.to),
    }
  })

  return { nodes: [...nodes.values()], edges: placed, chain, width: Math.max(width, 320), height: Math.max(height, 200) }
}

// The result next to the selection in a direction, for walking the board with
// the arrow keys. Left and right follow the lineage, up and down walk the
// results in the same column.
export function neighbour(layout, selectedId, direction) {
  const results = layout.nodes.filter(n => n.kind === 'result')
  const here = results.find(n => n.item.id === selectedId)
  if (!here) return results[0]?.item.id || null
  if (direction === 'left') {
    const edge = layout.edges.find(e => e.to === here.id)
    const from = edge && layout.nodes.find(n => n.id === edge.from)
    if (from?.kind === 'result') return from.item.id
    return selectedId
  }
  if (direction === 'right') {
    const child = here.children.map(id => layout.nodes.find(n => n.id === id)).find(n => n?.kind === 'result')
    return child ? child.item.id : selectedId
  }
  const column = results.filter(n => n.depth === here.depth).sort((a, b) => a.y - b.y)
  const i = column.findIndex(n => n.id === here.id)
  const next = column[i + (direction === 'down' ? 1 : -1)]
  return next ? next.item.id : selectedId
}

// The dashed "suggested" step: the first step the destination accepts and a
// model is installed for, else the first one it accepts. `installed` is a set of
// type keys that have a model.
export function suggestNext(type, installed) {
  const steps = (NEXT_STEPS[type] || []).filter(s => s.supported)
  return steps.find(s => installed.has(s.to)) || steps[0] || null
}

// --- Hand-off ---------------------------------------------------------------

// The workspaces read these query parameters. Nothing else is carried: a prompt,
// a model, a size, a count, the id of the result it grows from and how.
export function handoffPath(type, { prompt, model, size, count, from, edge } = {}) {
  const q = new URLSearchParams()
  if (prompt && String(prompt).trim()) q.set('prompt', String(prompt).trim())
  if (model) q.set('model', model)
  if (size) q.set('size', size)
  if (count && Number(count) > 1) q.set('n', String(count))
  if (from) q.set('from', from)
  if (edge) q.set('edge', edge)
  const qs = q.toString()
  return `/app/studio/${type}${qs ? `?${qs}` : ''}`
}

export function readHandoff(params) {
  const get = (k) => params.get(k) || ''
  const n = parseInt(get('n'), 10)
  return {
    prompt: get('prompt'),
    model: get('model'),
    size: get('size'),
    count: Number.isFinite(n) && n > 0 ? n : 0,
    from: get('from'),
    edge: get('edge'),
  }
}

// The file a destination starts from when it grows out of `item`.
export function sourceUrlFor(item, edge) {
  if (!item) return ''
  if (edge === 'take') return item.inputUrl || ''
  return item.url || ''
}

// --- Favourites -------------------------------------------------------------

export const MAX_FAVOURITES = 500

// Add or remove an id, newest last, bounded. Returns a new array.
export function toggleId(ids, id) {
  if (ids.includes(id)) return ids.filter(x => x !== id)
  const next = [...ids, id]
  return next.length > MAX_FAVOURITES ? next.slice(next.length - MAX_FAVOURITES) : next
}

// Keep only ids that still name a stored result.
export function pruneIds(ids, items) {
  const live = new Set(items.map(i => i.id))
  return ids.filter(id => live.has(id))
}

// --- Masonry ----------------------------------------------------------------

// Number of columns for a container width.
export function columnCount(width) {
  if (!(width > 0)) return 1
  return Math.max(1, Math.min(4, Math.floor(width / 270)))
}

// Estimated height of a tile in column units, used only to balance columns. A
// picture tile is as tall as its aspect makes it; a sound tile is a short bar.
export function tileWeight(tile) {
  const item = tile.kind === 'project' ? tile.cover : tile.item
  const kind = TYPE_INFO[item.type].kind
  let body = 1
  if (kind === 'image' || kind === 'video') body = Math.min(1.8, Math.max(0.55, 1 / aspectOf(item)))
  else if (kind === 'audio' || kind === 'speakers') body = 0.45
  else body = 0.9
  return body + (tile.kind === 'project' ? 0.5 : 0.3)
}

// Deal tiles into columns, each to the shortest column so far. Order is kept
// within a column so the newest sit at the top.
export function dealColumns(tiles, count) {
  const cols = Array.from({ length: count }, () => ({ h: 0, tiles: [] }))
  for (const tile of tiles) {
    const target = cols.reduce((a, b) => (b.h < a.h ? b : a))
    target.tiles.push(tile)
    target.h += tileWeight(tile)
  }
  return cols.map(c => c.tiles)
}

// A repeatable pseudo-waveform for an audio tile, so one tile always looks the
// same. It is a placeholder, not the sound.
export function waveBars(id, count = 48) {
  let h = 2166136261
  for (let i = 0; i < String(id).length; i++) { h ^= String(id).charCodeAt(i); h = Math.imul(h, 16777619) }
  const bars = []
  for (let i = 0; i < count; i++) {
    h ^= h << 13; h ^= h >>> 17; h ^= h << 5
    const r = ((h >>> 0) % 1000) / 1000
    const envelope = 0.45 + 0.55 * Math.sin((i / count) * Math.PI)
    bars.push(Math.round((0.15 + r * 0.85 * envelope) * 100) / 100)
  }
  return bars
}
