// Pure helpers of the Plugins tab. No React and no network; tested in
// tests/unit/plugins.test.mjs.

export const VIEWS = ['browse', 'installed', 'marketplaces']

export function resolveView(requested) {
  return VIEWS.includes(requested) ? requested : 'browse'
}

// The plugin of a review link (?plugin=name@marketplace), or null when the
// value is not a plugin ID.
export function reviewTarget(id) {
  const m = /^([^@/\s]+)@([^@/\s]+)$/.exec(String(id || ''))
  return m ? { name: m[1], marketplace: m[2] } : null
}

// The first 7 characters of a commit or of the hex of a digest.
export function shortSha(sha) {
  const s = String(sha || '')
  const hex = s.startsWith('sha256:') ? s.slice(7) : s
  return hex.slice(0, 7)
}

// The host part of a URL, or '' when it is not one.
export function hostOf(url) {
  try { return new URL(url).host } catch { return '' }
}

// What a plugin contributes, as chips: { key, count | host }. model is the
// plugins.Model of an inspect or an install (skills, commands, agents,
// mcpServers, hooks). hosts are the hosts of its remote MCP servers.
export function contributions(model, { hosts = [] } = {}) {
  if (!model) return []
  const out = []
  const n = (k) => (Array.isArray(model[k]) ? model[k].length : 0)
  if (n('skills')) out.push({ key: 'skills', count: n('skills') })
  if (n('commands')) out.push({ key: 'commands', count: n('commands') })
  if (n('agents')) out.push({ key: 'agents', count: n('agents') })
  const remote = (model.mcpServers || []).filter((s) => s && s.type !== 'stdio')
  const seen = new Set()
  for (const h of [...hosts, ...remote.map((s) => hostOf(s.url))]) {
    if (h && !seen.has(h)) { seen.add(h); out.push({ key: 'connects', host: h }) }
  }
  if (n('hooks')) out.push({ key: 'hooks', count: n('hooks') })
  return out
}

// Whether a plugin is known to run code: true, false, or null when nothing
// says yet (a catalog entry no one installed).
export function runsCodeOf(item) {
  if (!item) return null
  if (typeof item.runsCode === 'boolean') return item.runsCode
  return null
}

// The one control of a browse card:
//   unsupported   the entry has a source LocalAI does not fetch
//   installing    an install of it is queued or running
//   update        an install of it has a newer version (and the viewer may approve it)
//   installed     the viewer has it (for everyone or for themselves)
//   install       the viewer may install it
//   admin-only    a user and a plugin that runs code: an administrator installs it
//   off           a user and personal installs are off
export function cardAction(item, { isAdmin = false, userInstalls = true, busy = false } = {}) {
  if (!item) return 'unsupported'
  if (item.supported === false) return 'unsupported'
  if (busy) return 'installing'
  const installs = Array.isArray(item.installs) ? item.installs : []
  if (installs.length > 0) {
    const mine = installs.find((i) => i.state === 'update_available' && (isAdmin || i.scope === 'user'))
    if (mine) return 'update'
    return 'installed'
  }
  if (isAdmin) return 'install'
  if (runsCodeOf(item) === true) return 'admin-only'
  if (!userInstalls) return 'off'
  return 'install'
}

// The scopes the review offers. A user installs for themselves only; an
// administrator installs a plugin that runs code for everyone only.
export function scopeChoices({ isAdmin, runsCode }) {
  if (!isAdmin) return ['user']
  if (runsCode) return ['instance']
  return ['instance', 'user']
}

// The installs in their two groups, each sorted by name.
export function groupInstalls(list) {
  const byName = (a, b) => String(a.name || a.plugin).localeCompare(String(b.name || b.plugin))
  const all = Array.isArray(list) ? list : []
  return {
    instance: all.filter((i) => i.scope === 'instance').sort(byName),
    user: all.filter((i) => i.scope !== 'instance').sort(byName),
  }
}

// What the viewer may do with an install (spec 9.2). The server checks again.
export function installRights(install, { isAdmin = false, userId = '' } = {}) {
  const own = install && install.scope === 'user' && install.owner && install.owner === userId
  const change = Boolean(isAdmin || own)
  return {
    update: change && install?.state === 'update_available',
    rollback: change && Boolean(install?.previousDigest),
    remove: change,
  }
}

// The state chip of an install.
export function installChip(install) {
  if (install?.needsReenable) return { key: 'needsReenable', tone: 'warn' }
  if (install?.state === 'update_available') return { key: 'update', status: 'update-available' }
  if (install?.state === 'blocked') return { key: 'blocked', tone: 'error' }
  return null
}

// The state of a marketplace row.
export function marketplaceChip(m) {
  if (!m) return { key: 'off', tone: 'idle' }
  if (m.lastError) return { key: 'error', tone: 'error' }
  if (m.suspended) return { key: 'suspended', tone: 'idle' }
  if (!m.enabled) return { key: 'off', tone: 'idle' }
  if (!m.refreshedAt) return { key: 'pending', tone: 'accent' }
  return { key: 'ready', tone: 'ok' }
}

// The categories of a page of entries, sorted, without empty ones.
export function categoriesOf(items) {
  return [...new Set((items || []).map((i) => i.category).filter(Boolean))].sort()
}

// The entries a "Text only" filter keeps: an entry known to run code goes.
export function textOnly(items, on) {
  return on ? (items || []).filter((i) => runsCodeOf(i) !== true) : items || []
}

// The i18n key of a refused call. A type the page knows has its own words;
// anything else shows the server's message.
const ERROR_TYPES = new Set([
  'invalid_token', 'needs_admin', 'code_for_everyone', 'blocked', 'not_human_surface', 'forbidden', 'not_found',
  'conflict', 'instance_installed', 'no_update', 'needs_reenable', 'unsupported', 'content_refused', 'limit_reached',
  'fetch_failed', 'plugins_unavailable', 'network', 'rate_limited',
])
export function errorKey(err) {
  const type = err && typeof err.type === 'string' ? err.type : ''
  return ERROR_TYPES.has(type) ? `plugins.errors.${type}` : ''
}

// The reason of a skipped component, as an i18n key, from its code.
export function skippedKey(code) {
  const c = String(code || '')
  if (c.startsWith('unsupported-')) return 'plugins.skipped.unsupported'
  if (c.startsWith('ignored-')) return 'plugins.skipped.ignored'
  if (c.startsWith('invalid-') || c === 'not-found' || c === 'read-error') return 'plugins.skipped.invalid'
  return ''
}

// An update needs every changed code file ticked before it goes, when the
// preview asks for a code review, and one more tick for the code components
// the new version adds (hooks, servers, bin files), which may be in no file.
export function updateReady(preview, ticked, itemsTicked = false) {
  if (!preview) return false
  if (!preview.needsCodeReview) return true
  if ((preview.codeItems || []).length > 0 && !itemsTicked) return false
  const set = ticked instanceof Set ? ticked : new Set(ticked || [])
  return (preview.codeFiles || []).every((f) => set.has(f))
}

// The code reasons an update adds.
export function addedReasons(preview) {
  const old = new Set(preview?.oldCodeReasons || [])
  return (preview?.newCodeReasons || []).filter((r) => !old.has(r))
}

// A unified diff as lines with their kind, for a text rendering.
export function diffLines(diff) {
  return String(diff || '').replace(/\n$/, '').split('\n').map((text) => {
    let kind = 'ctx'
    if (text.startsWith('+++') || text.startsWith('---')) kind = 'file'
    else if (text.startsWith('@@')) kind = 'hunk'
    else if (text.startsWith('+')) kind = 'add'
    else if (text.startsWith('-')) kind = 'del'
    return { kind, text }
  })
}

// The plugin operations of the shared operations list, by plugin ID.
export function pluginOps(operations) {
  const out = new Map()
  for (const op of operations || []) if (op && op.isPlugin && op.name) out.set(op.name, op)
  return out
}

// The value of a userConfig field as the form holds it (a string for inputs).
export function configForm(options, config) {
  const c = config && typeof config === 'object' ? config : {}
  const out = {}
  for (const o of options || []) {
    if (o.sensitive) continue
    const v = c[o.key] !== undefined ? c[o.key] : o.default
    out[o.key] = o.type === 'boolean' ? Boolean(v) : v === undefined || v === null ? '' : String(v)
  }
  return out
}

// The config a form saves: numbers as numbers, empty optional fields left out.
export function configPayload(options, form) {
  const out = {}
  for (const o of options || []) {
    if (o.sensitive) continue
    const v = form[o.key]
    if (o.type === 'boolean') { out[o.key] = Boolean(v); continue }
    if (v === '' || v === undefined) continue
    out[o.key] = o.type === 'number' && Number.isFinite(Number(v)) ? Number(v) : v
  }
  return out
}

// What one hook handler runs or calls, for the review: the command with its
// arguments, or the URL an http hook sends the payloads to. The other kinds
// show their type.
export function hookHandlerText(h) {
  if (!h) return ''
  if (h.type === 'http') return `http ${h.url || ''}`.trim()
  if (h.command) return [h.command, ...(h.args || [])].join(' ')
  return h.type || ''
}

// What an MCP server connects to or starts, with its headersHelper command.
export function mcpServerText(s) {
  if (!s) return ''
  const base = s.url || [s.command, ...(s.args || [])].filter(Boolean).join(' ')
  return s.headersHelper ? `${base} · headersHelper: ${s.headersHelper}` : base
}

// Whether a queued install or update shows in the installs list: an install
// of the plugin, at another commit than before for an update. A user cannot
// read the operations list, so the Plugins tab watches the list instead.
// before is the commit of the install before an update, or '' for an
// install.
export function jobLanded(list, plugin, before = '') {
  return (Array.isArray(list) ? list : []).some((i) => i && i.plugin === plugin && (!before || (i.sha && i.sha !== before)))
}
