// Pure helpers for the add-on pages. No React and no network.
import { updateView } from './updateState.js'

// Tabs. Apps is for everyone; the other three are management and the server
// checks administration again on every call they make.
export const TABS = ['apps', 'browse', 'installed', 'libraries']
export const ADMIN_TABS = new Set(['browse', 'installed', 'libraries'])

export function resolveTab(requested, isAdmin) {
  if (requested && TABS.includes(requested) && (isAdmin || !ADMIN_TABS.has(requested))) return requested
  return 'apps'
}

export function visibleTabs(isAdmin) {
  return TABS.filter(tab => isAdmin || !ADMIN_TABS.has(tab))
}

// The management link of an app opens the Installed tab on that add-on.
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

// Tool summary of a headless add-on: the names the server sent and how many
// more there are.
export function toolSummary(app, limit = 4) {
  const tools = Array.isArray(app?.tools) ? app.tools : []
  const count = Number.isFinite(app?.toolCount) ? app.toolCount : tools.length
  return { shown: tools.slice(0, limit), more: Math.max(0, count - Math.min(limit, tools.length)), count }
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
