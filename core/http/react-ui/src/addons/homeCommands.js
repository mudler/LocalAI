// SPDX-License-Identifier: MIT
// Installed DTOs only. No module loading, catalog data, or executable targets.
import { localizedText } from '../components/addons/addonState.js'
import { sdkAccepts, ICON_NAMES } from './sdk.js'
const name = value => typeof value === 'string' && /^[a-z][a-z0-9-]*$/.test(value) && value.length <= 64
// Apps resolves omitted labels from an 80-character UI title, then appends
// " — " and a ValidName page (64 characters) for secondary destinations.
// Explicit manifest labels retain the backend's separate 60-character cap.
const maxResolvedLabelChars = 80 + 3 + 64
const text = value => typeof value === 'string' && value.length > 0 && value.trim() === value && [...value].length <= maxResolvedLabelChars && !/[\p{Cc}\p{Cf}<>]/u.test(value)

// contract.ValidIconPath accepts only these ASCII package paths and image types.
// PathEscape leaves that alphabet unchanged; encoded alternatives are not DTOs.
function iconPath(value) {
  return value.length <= 256 && /^[a-zA-Z0-9_./-]+$/.test(value) &&
    value.split('/').every(part => part !== '' && part !== '.' && part !== '..') &&
    /\.(png|webp|jpe?g)$/i.test(value)
}
const object = value => value && typeof value === 'object' && !Array.isArray(value)
const compare = (a, b) => a < b ? -1 : a > b ? 1 : 0

function assetBase(app) {
  if (!name(app.id) || typeof app.ui?.entry !== 'string') return null
  const prefix = `/api/addons/${encodeURIComponent(app.id)}/assets/`
  const at = app.ui.entry.indexOf(prefix)
  if (at < 0 || (at > 0 && !/^\/[a-zA-Z0-9_/-]*$/.test(app.ui.entry.slice(0, at)))) return null
  const rest = app.ui.entry.slice(at + prefix.length)
  if (!/^[a-f0-9]{64}\/(?:[a-zA-Z0-9_.-]+\/)*[a-zA-Z0-9_.-]+$/.test(rest) || rest.split('/').some(p => p === '..' || p === '.')) return null
  return app.ui.entry.slice(0, at + prefix.length + 65)
}

export function homeAppStatus(app) {
  if (!app.enabled) return 'installed-disabled'
  if (app.state === 'failed') return 'installed-failed'
  if (app.state !== 'running') return app.state === 'starting' ? 'loading' : 'stopped'
  if (!app.ui) return 'no-ui'
  if (!object(app.ui) || !object(app.ui.sdk) || !sdkAccepts(app.ui.sdk)) return 'incompatible'
  if (!Number.isSafeInteger(app.generation) || app.generation < 1 || !assetBase(app)) return 'invalid-metadata'
  return 'ready'
}

function declarations(app, language) {
  const ui = app.ui
  const commands = ui.homeCommands || []
  if (!Array.isArray(commands) || commands.length > 8 || !Array.isArray(ui.pages) || !ui.pages.every(name) || new Set(ui.pages).size !== ui.pages.length) return null
  const ids = new Set()
  const out = []
  for (const c of commands) {
    if (!object(c) || Object.keys(c).some(k => !['id', 'alias', 'page', 'label', 'labels', 'icon'].includes(k)) || !name(c.id) || ids.has(c.id)) return null
    ids.add(c.id)
    if ((c.alias !== undefined && !name(c.alias)) || !name(c.page) || !ui.pages.includes(c.page) || !text(c.label)) return null
    if (c.labels !== undefined && (!object(c.labels) || Object.entries(c.labels).some(([k, v]) => !/^[a-zA-Z][a-zA-Z0-9-]*$/.test(k) || !text(v)))) return null
    const base = assetBase(app)
    if (typeof c.icon !== 'string' || (!ICON_NAMES.includes(c.icon) && !(c.icon.startsWith(base) && iconPath(c.icon.slice(base.length))))) return null
    const canonical = `/addon:${app.id}:${c.id}`
    out.push({ id: canonical.slice(1), addonId: app.id, commandId: c.id, generation: app.generation, entry: ui.entry,
      page: c.page, label: localizedText({ title: c.label, titles: c.labels }, 'title', language),
      icon: ICON_NAMES.includes(c.icon) ? c.icon : 'puzzle', iconUrl: ICON_NAMES.includes(c.icon) ? undefined : c.icon,
      alias: c.alias, cmd: canonical, canonical, group: 'addons', kind: 'navigate', where: ['home'] })
  }
  return out
}

export function projectHomeCommands(snapshot, { nativeNames = [], skillNames = [], language = 'en' } = {}) {
  const result = { status: 'ready', commands: [], diagnostics: [], states: [] }
  if (!snapshot.allowed) return { ...result, status: snapshot.known === false ? 'loading' : 'forbidden' }
  if (snapshot.status === 'error' || snapshot.error) return { ...result, status: 'list-error' }
  if (snapshot.status !== 'ready') return { ...result, status: 'loading' }
  const apps = Array.isArray(snapshot.apps) ? [...snapshot.apps] : []
  if (apps.some(a => !object(a) || !name(a.id))) return { ...result, status: 'invalid-metadata' }
  apps.sort((a, b) => compare(a.id, b.id))
  const duplicate = new Set(apps.filter((a, i) => apps.some((b, j) => i !== j && a.id === b.id)).map(a => a.id))
  const all = []
  for (const app of apps) {
    let status = duplicate.has(app.id) ? 'invalid-metadata' : homeAppStatus(app)
    if (status === 'ready') {
      const commands = declarations(app, language)
      if (!commands) status = 'invalid-metadata'
      else all.push(...commands)
    }
    result.states.push({ addonId: app.id, status })
  }
  all.sort((a, b) => compare(a.addonId, b.addonId) || compare(a.commandId, b.commandId))
  const native = new Set(nativeNames), skills = new Set(skillNames)
  for (const c of all) {
    result.commands.push(c)
    if (!c.alias) continue
    const peers = all.filter(x => x.alias === c.alias)
    const reason = native.has(c.alias) ? 'native-name' : skills.has(c.alias) ? 'skill-name' : peers.length > 1 ? 'addon-alias-collision' : null
    if (reason) result.diagnostics.push({ alias: c.alias, addonId: c.addonId, commandId: c.commandId, reason, competing: peers.map(x => x.canonical).sort() })
    else result.commands.push({ ...c, id: `${c.id}:alias`, cmd: `/${c.alias}` })
  }
  result.diagnostics.sort((a, b) => compare(a.alias, b.alias) || compare(a.addonId, b.addonId) || compare(a.commandId, b.commandId))
  if (!apps.length) result.status = 'absent'
  return result
}

// Captured menu entries are claims, never authority. Re-project the current
// installed snapshot and match its generation, immutable entry and destination.
export function resolveHomeCommand(input, snapshot, options) {
  const cmd = typeof input === 'string' ? input.trim() : input?.cmd
  if (typeof cmd !== 'string' || /\s/.test(cmd)) return null
  const live = projectHomeCommands(snapshot, options).commands.find(c => c.cmd === cmd)
  if (!live) return null
  if (typeof input !== 'string' && (live.generation !== input.generation || live.entry !== input.entry || live.page !== input.page || live.addonId !== input.addonId || live.commandId !== input.commandId)) return null
  return { ...live, path: `/app/addons/${encodeURIComponent(live.addonId)}/${encodeURIComponent(live.page)}` }
}

// Shared by the registry and tests: only a fresh actor-filtered installed read
// can authorize navigation. No module is mounted by this dispatch function.
export async function dispatchHomeCommand(command, { refresh, current, navigate }, options) {
  const actor = current().actor
  const apps = await refresh()
  const now = current()
  if (!apps || !now.allowed || actor !== now.actor || now.list.apps !== apps) return false
  const live = resolveHomeCommand(command, { ...now.list, allowed: true }, options)
  if (!live) return false
  navigate(live.path)
  return true
}
