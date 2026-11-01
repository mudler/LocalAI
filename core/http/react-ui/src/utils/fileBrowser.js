// The Files page as data: the kind of an entry, its order, the filter of the
// open folder, dates in words, the selection rules and the names of new and
// pasted files. Pure helpers, so the node unit tests load them.

// The type of a row dragged inside the page, so a folder knows a move from
// files dropped from outside.
export const DRAG_TYPE = 'application/x-localai-paths'

const KIND_BY_EXT = {
  image: /^(png|jpe?g|gif|webp|svg|bmp|avif|heic)$/,
  pdf: /^pdf$/,
  sheet: /^(xlsx?|ods|csv|tsv|numbers)$/,
  doc: /^(docx?|odt|rtf|pages)$/,
  slides: /^(pptx?|odp|key)$/,
  archive: /^(zip|tar|gz|tgz|bz2|xz|7z|rar)$/,
  audio: /^(mp3|wav|ogg|flac|m4a|aac|opus)$/,
  video: /^(mp4|webm|mov|mkv|avi)$/,
  code: /^(json|ya?ml|xml|html?|css|jsx?|tsx?|py|go|rs|c|h|cpp|sh|sql|toml|ini|java|rb|php)$/,
  text: /^(txt|md|markdown|log)$/,
}

// The icon of each kind, from the shared icon kit.
export const KIND_ICON = {
  folder: 'folder', image: 'image', pdf: 'file-text', sheet: 'columns', doc: 'file-text', slides: 'monitor',
  archive: 'boxes', audio: 'music', video: 'video', code: 'file-code', text: 'file-text', file: 'file',
}

export function extOf(name) {
  const m = /\.([A-Za-z0-9]{1,10})$/.exec(name || '')
  return m ? m[1].toLowerCase() : ''
}

// kindOf says what an entry is, for its icon, its thumbnail and the Type
// column: folder, image, pdf, sheet, doc, slides, archive, audio, video,
// code, text or file.
export function kindOf(entry) {
  if (entry?.kind === 'dir') return 'folder'
  const ext = extOf(entry?.name)
  for (const [kind, re] of Object.entries(KIND_BY_EXT)) {
    if (re.test(ext)) return kind
  }
  return 'file'
}

// The thumbnail of an image costs one request of the image itself, so only
// formats every browser draws and files up to 8 MB get one.
export const THUMB_LIMIT = 8 * 1024 * 1024
export function hasThumb(entry) {
  return entry?.kind === 'file' && /^(png|jpe?g|gif|webp|avif)$/.test(extOf(entry.name)) && (entry.size || 0) <= THUMB_LIMIT
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })
export const compareNames = (a, b) => collator.compare(a || '', b || '')

const time = (e) => (e?.modified ? Date.parse(e.modified) || 0 : 0)

// sortEntries orders a folder: folders before files, then by the chosen
// column. Ties fall back to the name, so the order never jumps.
//   key  name | size | modified | type
//   dir  asc | desc
export function sortEntries(entries, { key = 'name', dir = 'asc' } = {}) {
  const sign = dir === 'desc' ? -1 : 1
  const by = {
    name: (a, b) => compareNames(a.name, b.name),
    size: (a, b) => (a.size || 0) - (b.size || 0),
    modified: (a, b) => time(a) - time(b),
    type: (a, b) => compareNames(`${kindOf(a)}.${extOf(a.name)}`, `${kindOf(b)}.${extOf(b.name)}`),
  }[key] || ((a, b) => compareNames(a.name, b.name))
  return [...(entries || [])].sort((a, b) => {
    const folders = (b.kind === 'dir') - (a.kind === 'dir')
    if (folders) return folders
    return sign * by(a, b) || compareNames(a.name, b.name)
  })
}

// filterEntries keeps the entries whose name has every word of the query, in
// any case and any order.
export function filterEntries(entries, query) {
  const words = String(query || '').toLowerCase().split(/\s+/).filter(Boolean)
  if (words.length === 0) return entries || []
  return (entries || []).filter(e => {
    const name = (e.name || '').toLowerCase()
    return words.every(w => name.includes(w))
  })
}

// relativeDate says when, in words: "just now", "5 minutes ago", "yesterday"
// for the last week, else a short date. exact is the full date for a tooltip.
export function relativeDate(iso, { now = Date.now(), locale } = {}) {
  const at = Date.parse(iso || '')
  if (!at) return { label: '', exact: '' }
  const exact = new Intl.DateTimeFormat(locale, { dateStyle: 'full', timeStyle: 'short' }).format(at)
  const secs = Math.round((at - now) / 1000)
  const abs = Math.abs(secs)
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  let label
  if (abs < 45) label = rtf.format(0, 'second')
  else if (abs < 3600) label = rtf.format(Math.round(secs / 60), 'minute')
  else if (abs < 86400) label = rtf.format(Math.round(secs / 3600), 'hour')
  else if (abs < 7 * 86400) label = rtf.format(Math.round(secs / 86400), 'day')
  else {
    const sameYear = new Date(at).getFullYear() === new Date(now).getFullYear()
    label = new Intl.DateTimeFormat(locale, sameYear ? { month: 'short', day: 'numeric' } : { dateStyle: 'medium' }).format(at)
  }
  return { label, exact }
}

// rangeOf returns the paths between anchor and target in the shown order,
// both included, for a Shift selection. A missing anchor selects the target.
export function rangeOf(order, anchor, target) {
  const a = order.indexOf(anchor)
  const b = order.indexOf(target)
  if (b < 0) return []
  if (a < 0) return [target]
  return order.slice(Math.min(a, b), Math.max(a, b) + 1)
}

// nextIndex moves the keyboard cursor. In a grid, Up and Down move by a row
// of cols items.
export function nextIndex(key, index, count, cols = 1) {
  if (count === 0) return -1
  const step = { ArrowDown: cols, ArrowUp: -cols, ArrowRight: cols > 1 ? 1 : 0, ArrowLeft: cols > 1 ? -1 : 0 }[key]
  if (key === 'Home') return 0
  if (key === 'End') return count - 1
  if (step === undefined) return index
  if (index < 0) return 0
  return Math.max(0, Math.min(count - 1, index + step))
}

// findByPrefix is type-to-find: the first name after the cursor that starts
// with the typed text, wrapping around.
export function findByPrefix(names, text, from = 0) {
  const t = String(text || '').toLowerCase()
  if (!t) return -1
  for (let i = 0; i < names.length; i++) {
    const j = (from + i) % names.length
    if ((names[j] || '').toLowerCase().startsWith(t)) return j
  }
  return -1
}

// uniqueName gives name, or "name 2", "name 3"… before the extension, so a
// new file or folder never lands on an existing one.
export function uniqueName(name, taken) {
  const used = new Set([...(taken || [])].map(n => n.toLowerCase()))
  if (!used.has(name.toLowerCase())) return name
  const ext = extOf(name)
  const base = ext ? name.slice(0, -(ext.length + 1)) : name
  for (let i = 2; ; i++) {
    const next = ext ? `${base} ${i}.${ext}` : `${base} ${i}`
    if (!used.has(next.toLowerCase())) return next
  }
}

// nameProblem says why a name cannot be used, as a key of the workspace
// namespace (naming.empty, naming.slash, naming.taken), or '' when it can.
export function nameProblem(name, taken, current = '') {
  const n = String(name || '').trim()
  if (!n) return 'naming.empty'
  if (n.includes('/') || n.includes('\\') || n === '.' || n === '..') return 'naming.slash'
  if (n.toLowerCase() !== current.toLowerCase() && [...(taken || [])].some(x => x.toLowerCase() === n.toLowerCase())) return 'naming.taken'
  return ''
}

// pastedName names a pasted file. Browsers call every pasted screenshot
// "image.png"; it gets the time instead, so two pastes do not collide.
export function pastedName(name, now = new Date()) {
  if (name && !/^image\.(png|jpe?g|gif|webp)$/i.test(name)) return name
  const ext = extOf(name) || 'png'
  const pad = (n) => String(n).padStart(2, '0')
  return `Pasted image ${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ${pad(now.getHours())}.${pad(now.getMinutes())}.${pad(now.getSeconds())}.${ext}`
}

// The view settings of the page, kept per user in the browser.
export const VIEW_DEFAULTS = { view: 'list', key: 'name', dir: 'asc' }
export function parseViewPrefs(raw) {
  try {
    const v = JSON.parse(raw || '{}')
    return {
      view: v.view === 'grid' ? 'grid' : 'list',
      key: ['name', 'size', 'modified', 'type'].includes(v.key) ? v.key : 'name',
      dir: v.dir === 'desc' ? 'desc' : 'asc',
    }
  } catch {
    return { ...VIEW_DEFAULTS }
  }
}
