// Pure helpers for the add-on pages. No React and no network.
import { updateView } from './updateState.js'

// Tabs: My add-ons is for everyone; Find add-ons and Sources are management and
// the server checks administration again on every call they make. What the old
// Installed tab showed is now the Details of an add-on in My add-ons, so an old
// ?tab=installed address lands on My add-ons.
export const TABS = ['apps', 'browse', 'libraries', 'access']
export const ADMIN_TABS = new Set(['browse', 'libraries', 'access'])

export function resolveTab(requested, isAdmin) {
  if (requested && TABS.includes(requested) && (isAdmin || !ADMIN_TABS.has(requested))) return requested
  return 'apps'
}

export function visibleTabs(isAdmin) {
  return TABS.filter(tab => isAdmin || !ADMIN_TABS.has(tab))
}

// The first-run card: shown to someone who can install add-ons, once the list
// has loaded and nothing is installed. It goes away with the first install.
export function showFirstRun({ apps, isAdmin, canList = true }) {
  return Boolean(isAdmin) && Boolean(canList) && Array.isArray(apps) && apps.length === 0
}

// What the My add-ons list shows. Nothing installed is a first run for someone
// who can install, and plain emptiness for anyone else. A search that finds
// nothing says so: it never claims that nothing is installed.
export function listState({ apps, shownCount, query, isAdmin, canList = true }) {
  if (showFirstRun({ apps, isAdmin, canList })) return 'first-run'
  if (shownCount > 0) return 'list'
  if (String(query || '').trim() && Array.isArray(apps) && apps.length > 0) return 'no-match'
  return 'none'
}

// The add-ons to offer when nothing is installed: the first entries of the
// sources that install as add-ons and are not installed yet. The sources do
// not mark any entry as featured, so this is their own order.
export function featuredEntries(entries, installedIds = [], limit = 3) {
  const have = new Set(installedIds)
  return (Array.isArray(entries) ? entries : []).filter(e => e && e.kind === 'addon' && !have.has(e.id)).slice(0, limit)
}

// The management link of an app opens the Details of that add-on.
export function resolveManage(params, isAdmin) {
  if (!isAdmin) return ''
  const id = params.get('manage') || ''
  return /^[a-z][a-z0-9-]{0,63}$/.test(id) ? id : ''
}

const STATES = new Set(['running', 'starting', 'failed', 'stopped', 'update-pending'])

// The chip of an app: the server's observation, never a guess. A state the page
// does not know is shown as failed, as the owner does, never as running.
export function stateChip(app) {
  const raw = app && typeof app.state === 'string' ? app.state : ''
  const key = STATES.has(raw) ? raw : 'failed'
  const tone = key === 'running' ? 'ok' : key === 'starting' || key === 'update-pending' ? 'warn' : key === 'failed' ? 'error' : 'idle'
  return { key, tone }
}

// Start and stop are offered from the owner's own view only; the server
// refuses anyone else whatever this says.
export function appActions(app) {
  const admin = app && app.admin
  if (!admin) return { start: false, stop: false, manage: '' }
  return { start: Boolean(admin.canStart), stop: Boolean(admin.canStop), manage: typeof admin.managementUrl === 'string' ? admin.managementUrl : '' }
}

// The localized title or description of an app: the user's language, then the
// default text the server sent, then the identifier.
export function localizedText(app, field, language) {
  if (!app) return ''
  const map = field === 'title' ? app.titles : app.descriptions
  const base = String(language || '').toLowerCase()
  if (map && typeof map === 'object') {
    for (const [tag, text] of Object.entries(map)) {
      if (tag.toLowerCase() === base && text) return text
    }
    const short = base.split('-')[0]
    for (const [tag, text] of Object.entries(map)) {
      if (tag.toLowerCase().split('-')[0] === short && text) return text
    }
  }
  const fallback = field === 'title' ? (app.title || app.name || app.id) : app.description
  return fallback || ''
}

// An installed record with its title and description in the user's language,
// for the dialogs and cards that name an add-on. The English text stays in
// englishTitle and englishDescription, which the search reads too.
export function localizeRecord(rec, language) {
  if (!rec) return rec
  return {
    ...rec,
    title: localizedText(rec, 'title', language) || rec.title,
    description: localizedText(rec, 'description', language),
    englishTitle: rec.title,
    englishDescription: rec.description || '',
  }
}

// A library entry with its title and description in the user's language. The
// entry carries the other languages in titles and descriptions (from the
// library); a language without text, and English, keep the entry's own text.
export function localizeEntry(entry, language) {
  if (!entry) return entry
  return {
    ...entry,
    title: localizedText({ id: entry.id, name: entry.title, title: entry.title, titles: entry.titles }, 'title', language) || entry.title,
    description: localizedText({ description: entry.description, descriptions: entry.descriptions }, 'description', language),
    englishTitle: entry.title,
    englishDescription: entry.description || '',
  }
}

// One page of library entries in the alphabetical order of the user's language,
// by the title that the person reads. The id breaks a tie, so the order is
// stable. It orders the page as drawn; the library still pages by its own key.
export function sortEntriesByTitle(entries, language) {
  let collator
  try { collator = new Intl.Collator(language || undefined, { sensitivity: 'base', numeric: true }) } catch { collator = new Intl.Collator(undefined, { sensitivity: 'base', numeric: true }) }
  const title = (e) => localizeEntry(e, language).title || e.id
  return [...(entries || [])].sort((a, b) => collator.compare(title(a), title(b)) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
}

// The name to show for an add-on the list does not carry (not installed, or
// not usable by this viewer): its identifier with a capital first letter, so
// the page says "Voice" and not "voice".
export function addonDisplayName(id) {
  const text = String(id || '').trim()
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : ''
}

// Tool summary of a headless add-on: the names the server sent and how many
// more there are.
export function toolSummary(app, limit = 4) {
  const tools = Array.isArray(app?.tools) ? app.tools : []
  const count = Number.isFinite(app?.toolCount) ? app.toolCount : tools.length
  return { shown: tools.slice(0, limit), more: Math.max(0, count - Math.min(limit, tools.length)), count }
}

// What a library entry says the add-on will add, before it is installed. It
// reads the same fields as an installed add-on (toolCount, tools), so the
// count goes through the same code path. The page comes from ui.mainPage; the
// tools are the chat tools of the manifest, never its admin tools.
export function addedByEntry(entry) {
  return addedByAddon({ hasUI: Boolean(entry && entry.ui && entry.ui.mainPage), toolCount: entry?.toolCount, tools: entry?.tools })
}

// What an add-on adds to LocalAI, from the facts the list already carries: a
// page (an add-on with a UI) and chat tools (the tools it offers the model).
// Nothing here is guessed: no field, no line.
export function addedByAddon(app) {
  const tools = toolSummary(app)
  return { page: Boolean(app && (app.hasUI || app.mainPage)), tools: tools.count, toolNames: tools.shown, toolsMore: tools.more }
}

// Screenshots an add-on ships, when the host lists them. The list carries none
// today, so the preview shows no slot; a path is used only when it is served by
// the host itself, as icons are.
export function screenshotsOf(app, limit = 6) {
  const list = Array.isArray(app && app.screenshots) ? app.screenshots : []
  return list.filter(src => typeof src === 'string' && src.startsWith('/') && !src.startsWith('//')).slice(0, limit)
}

export function shortDigest(digest) {
  const text = String(digest || '')
  const [algorithm, value] = text.includes(':') ? text.split(':') : ['', text]
  return value ? `${algorithm ? `${algorithm}:` : ''}${value.slice(0, 12)}` : ''
}

// The state of an update, from the owner's authoritative check only. The page
// never compares versions: with no `update` object, or one it cannot read, the
// status is unknown, and a failed check is never current.
export function updateStatus(installed) {
  const view = updateView(installed && installed.update)
  const checkedAt = view.checkedAt || ''
  switch (view.state) {
    case 'update-available': return { kind: 'available', version: view.version || '', checkedAt }
    case 'up-to-date': return { kind: 'current', checkedAt }
    case 'incompatible': return { kind: 'incompatible', version: view.version || '', checkedAt, reasons: view.reasons }
    case 'check-failed': return { kind: 'failed', checkedAt, error: view.error }
    case 'integrity-error': return { kind: 'integrity', checkedAt }
    default: return { kind: 'unknown' }
  }
}

export function formatBytes(bytes) {
  const n = Number(bytes) || 0
  if (n < 1024) return `${n} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let value = n / 1024
  let i = 0
  while (value >= 1024 && i < units.length - 1) { value /= 1024; i += 1 }
  return `${value >= 10 ? Math.round(value) : Math.round(value * 10) / 10} ${units[i]}`
}

export function rangeText(range) {
  if (!range || typeof range !== 'object') return ''
  return range.min === range.max ? String(range.min) : `${range.min}-${range.max}`
}

// Join a release of the catalog with the installed list: the key of an entry
// is its library, add-on and version.
export function entryKey(entry) {
  return `${entry.sourceId}/${entry.id}/${entry.version}`
}

export function parseEntryKey(key) {
  const parts = String(key || '').split('/')
  if (parts.length !== 3 || parts.some(p => !p)) return null
  return { sourceId: parts[0], id: parts[1], version: parts[2] }
}

// ---- Installed list: attention, order, filter, identity --------------------

// What on an installed add-on needs a person: a failure first, then an update,
// then a start that is still running. An add-on someone turned off is not a
// problem. `installed` is the owner's record (administrators only); without it
// an update is known only from the list's own notice.
export function attentionOf(app, installed) {
  if (!app) return ''
  if (app.enabled !== false && app.state === 'failed') return 'failed'
  if (app.update === 'update-pending' || updateView(installed && installed.update).state === 'update-available') return 'update'
  if (app.enabled !== false && app.state === 'starting') return 'starting'
  return ''
}

const ATTENTION_RANK = { failed: 0, update: 1, starting: 2 }

// The list order: what needs attention first (failed, update, starting), then
// the order the caller gave (pinned first, then the catalog). The sort is
// stable, so two add-ons with the same rank keep their place.
export function sortByAttention(tiles, installedById = {}) {
  const rank = (tile) => {
    const a = tile.app ? attentionOf(tile.app, installedById[tile.id]) : ''
    return a in ATTENTION_RANK ? ATTENTION_RANK[a] : 3
  }
  return tiles.map((tile, i) => ({ tile, i, r: rank(tile) }))
    .sort((x, y) => (x.r - y.r) || (x.i - y.i))
    .map(x => x.tile)
}

export const STATE_FILTERS = ['all', 'running', 'off']

// The segmented filter of the installed list. "Running" is a live process;
// "Off" is everything else (turned off, starting, failed).
export function filterByState(tiles, filter) {
  if (filter === 'running') return tiles.filter(t => t.state === 'running')
  if (filter === 'off') return tiles.filter(t => t.state !== 'running')
  return tiles
}

// A hue bucket (0-11) from an add-on id, so an add-on without an icon still
// has a tile of its own colour, the same on every visit.
export function hueOf(id) {
  let h = 0
  for (const ch of String(id || '')) h = (h * 31 + ch.codePointAt(0)) >>> 0
  return h % 12
}

// The first letter of a name, for the identity tile of an add-on with no icon.
export function monogram(name) {
  const text = String(name || '').trim()
  return text ? Array.from(text)[0].toUpperCase() : '?'
}

// A version as people read it. A development build has no release number, so
// it says so instead of showing 0.0.0-development.
export function versionLabel(version) {
  const v = String(version || '').trim()
  if (!v) return { dev: false, text: '' }
  if (/^0\.0\.0([-+].*)?$/.test(v) || /(^|[-+.])dev(elopment)?$/i.test(v)) return { dev: true, text: v }
  return { dev: false, text: v.startsWith('v') ? v : `v${v}` }
}

// The built-in skills that a library entry replaces, each with the installed
// add-on that already replaces it, if any. The install dialog names every one:
// the add-on's own skill serves in the chats instead of the built-in skill.
export function skillShadowNotes(entry, installed) {
  const want = Array.isArray(entry?.skillShadows) ? entry.skillShadows : []
  return want.map(skill => {
    const other = Object.values(installed || {}).find(a => a && a.id !== entry.id && Array.isArray(a.skillShadows) && a.skillShadows.includes(skill))
    return { skill, name: other ? other.title || other.id : '' }
  })
}

// The skills an installed add-on ships, for its details: the name of each and
// whether it replaces the built-in skill of that name.
export function skillsOf(app) {
  const list = Array.isArray(app?.skills) ? app.skills : []
  return list.filter(s => s && typeof s.name === 'string').map(s => ({ name: s.name, replaces: s.shadows === s.name }))
}

// ---- Operations on the owner ----------------------------------------------

const OPERATION_KINDS = new Set(['install', 'upgrade', 'uninstall', 'enable', 'disable'])
const OPERATION_PHASES = new Set(['queued', 'downloading', 'backing-up', 'installing', 'migrating', 'starting', 'working'])

// The operation the owner runs on an add-on now, as the list reports it. A kind
// or phase this page does not know is shown as plain work, never dropped:
// the add-on is still being changed.
export function operationOf(app) {
  const op = app && typeof app === 'object' ? app.operation : null
  if (!op || typeof op !== 'object') return null
  return {
    kind: OPERATION_KINDS.has(op.kind) ? op.kind : 'working',
    phase: OPERATION_PHASES.has(op.phase) ? op.phase : 'working',
    since: typeof op.since === 'string' ? op.since : '',
  }
}

// The titles of the add-ons the owner is upgrading, for the busy banner.
export function upgradingTitles(apps) {
  return (Array.isArray(apps) ? apps : [])
    .filter(a => a && a.operation && a.operation.kind === 'upgrade')
    .map(a => a.title || a.id)
}

// Why the list on screen may be out of date: 'busy' (the owner did not answer
// in time), 'unreachable' or '' when it is current.
export function staleKind({ error, stale }) {
  if (!stale) return ''
  if (error && error.kind === 'busy') return 'busy'
  if (error) return 'unreachable'
  return 'busy'
}

// ---- The last list a browser got -------------------------------------------

const LAST_KEY = 'localai.addons.lastKnownApps.v1'

// A page loaded while the owner is busy has no list in memory. The last list
// this browser tab got for the same person stands in for it, marked stale.
// Storage can be absent or full: every call is allowed to do nothing.
export function saveLastKnown(storage, actor, apps) {
  try {
    storage.setItem(LAST_KEY, JSON.stringify({ actor, apps, at: Date.now() }))
  } catch { /* the page works without it */ }
}

export function loadLastKnown(storage, actor, maxAgeMs = 24 * 3600 * 1000, now = Date.now()) {
  try {
    const raw = JSON.parse(storage.getItem(LAST_KEY) || 'null')
    if (!raw || raw.actor !== actor || !Array.isArray(raw.apps) || !Number.isFinite(raw.at) || now - raw.at > maxAgeMs) return null
    return raw.apps
  } catch {
    return null
  }
}
