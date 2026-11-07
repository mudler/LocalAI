// The "What it can use" section of the agent form: the rules that are not
// drawing. No React, so a node test drives them. Everything here reads fields
// the form and the draft lists already have; the one saved field this section
// owns is `resources` (add-on ID to [{ id, mode }]).
//
// Nothing here names an add-on, a noun or a provider: the words come from the
// rows the server sends (title, noun, noun_plural, language).
import { connectionIds } from './agentTools.js'

const list = (x) => (Array.isArray(x) ? x : [])
const dedupe = (l) => l.filter((x, i) => l.indexOf(x) === i)

export const RESOURCE_MODES = ['read', 'write']

// --- Right now ---
//
// One sentence per add-on row: what applies to a chat of this agent. It
// mirrors the choice of the server, core/services/studio/connectorcred.go
// ConnectionsOf, for a chat that runs a saved agent (rules 2 to 5 there; a chat
// started by a messaging binding of the add-on uses that binding alone and is
// not decided here). Keep the table in agentuses.test.mjs in step with it.
//
//   add-on off                              off
//   no credential                           ok, needs no account
//   credential, the person has no account   warn: none is offered
//   the agent names accounts, some exist    ok: those accounts
//   the agent names accounts, none exist    warn: the server uses an empty set
//   the agent names none, one account       ok: the only account
//   the agent names none, two or more       warn: a chat asks, a run gets none
//
// Then, for an add-on that scopes its resources per agent, the list decides:
// empty is none (warn), only read entries cannot run a tool that changes data.
//
// Returns { tone: off|ok|warn, key, vars, code? }. `key` names the sentence
// (create.uses.now.<key>); `code` is the reason code of a warn.
export function addonNow(a, { total = null } = {}) {
  const tl = list(a.tool_list)
  const writes = tl.filter((x) => x.kind === 'write').length
  const count = Number(a.tools) || tl.length
  const split = tl.length > 0
  const vars = { count, read: tl.length - writes, write: writes, split }
  const accounts = list(a.connections)
  const stored = Number(a.stored_connections) || 0
  const named = accounts.filter((c) => list(a.enabled_connections).includes(c.id))
  const names = named.map((c) => c.name).join(', ')
  if (a.off) {
    return { tone: 'off', key: named.length ? 'offKept' : 'off', vars: { ...vars, names } }
  }
  if (a.credentialed) {
    if (accounts.length === 0) return { tone: 'warn', key: 'noConnection', vars, code: 'addon_no_connection' }
    if (stored > 0 && named.length === 0) return { tone: 'warn', key: 'stale', vars, code: 'addon_account_gone' }
    if (stored === 0 && accounts.length >= 2) return { tone: 'warn', key: 'pick', vars, code: 'addon_account_missing' }
  }
  if (a.resources) {
    const picked = list(a.agent_resources)
    const noun = a.resources.noun || ''
    const plural = a.resources.noun_plural || noun
    if (picked.length === 0) return { tone: 'warn', key: 'resourcesNone', vars: { ...vars, noun, plural }, code: 'agent_resources_none' }
    const changes = picked.filter((r) => r.mode === 'write').length
    const rv = { ...vars, noun, plural, n: picked.length, total, w: changes }
    if (changes === 0 && writes > 0) return { tone: 'ok', key: 'resourcesRead', vars: rv, code: 'agent_resources_read_only' }
    return { tone: 'ok', key: 'resources', vars: rv }
  }
  if (!a.credentialed) return { tone: 'ok', key: 'noAccountNeeded', vars }
  if (named.length === 0) return { tone: 'ok', key: 'only', vars: { ...vars, names: accounts[0].name } }
  return { tone: 'ok', key: named.length > 1 ? 'usingMany' : 'using', vars: { ...vars, names } }
}

// --- Skills that come with an add-on ---

// The skill rows of one add-on (as the Skills list sends them) that the agent
// can switch: not a skill the host grants, not one whose file is gone.
export const addonSkillRows = (rows, addonId) =>
  list(rows).filter((s) => s.layer === 'addon' && s.addon === addonId && !s.gone && !s.locked)

// The fields of the form after an add-on is switched on or off. Switching on
// turns on the skills that come with it; switching off turns off only those
// (a skill is "with" an add-on when its row says so; nothing more is stored).
// `turned` names the skills that changed, for the Undo/Keep notice.
export function addonSwitch(form, addonId, on, skillRows) {
  const names = addonSkillRows(skillRows, addonId).map((s) => s.name)
  const had = new Set(list(form.skills))
  const rest = list(form.addons).filter((x) => x !== addonId)
  if (on) {
    return {
      addons: [...rest, addonId],
      skills: dedupe([...list(form.skills), ...names]),
      turned: names.filter((n) => !had.has(n)),
    }
  }
  const drop = new Set(names)
  return { addons: rest, skills: list(form.skills).filter((n) => !drop.has(n)), turned: names.filter((n) => had.has(n)) }
}

// Ticking an account on an add-on that is off switches it on, as the chat
// does; so does ticking a resource. Unticking the last account does not
// switch anything off. Returns the fields to merge into the form.
export function accountsAfter(form, addonId, bindingIds, skillRows) {
  const ids = connectionIds(bindingIds)
  const connections = { ...(form.connections || {}) }
  if (ids.length) connections[addonId] = ids
  else delete connections[addonId]
  const wasOff = !list(form.addons).includes(addonId)
  if (ids.length && wasOff) return { connections, ...addonSwitch(form, addonId, true, skillRows), switchedOn: true }
  return { connections, switchedOn: false, turned: [] }
}

// The skills of the add-ons, as seen from the form: for each add-on skill that
// is ticked, whether its add-on is on. A ticked skill of an add-on that is off
// does nothing; the Effective view says so.
export function addonSkillState(form, skillRows) {
  const granted = new Set(list(form.skills))
  const addons = new Set(list(form.addons))
  const rows = list(skillRows).filter((s) => s.layer === 'addon' && !s.gone && !s.locked)
  return {
    live: rows.filter((s) => granted.has(s.name) && addons.has(s.addon)),
    dead: rows.filter((s) => granted.has(s.name) && !addons.has(s.addon)),
  }
}

// The skills the host grants (the ones of the memory provider): not switches.
export const grantedSkills = (rows) => list(rows).filter((s) => s.locked && s.provider)
export const switchableSkills = (rows) => list(rows).filter((s) => !(s.locked && s.provider))

// --- Resources ---

export const pickedResources = (form, addonId) => list(form.resources?.[addonId])

// The resources field after one entry is ticked (mode read or write) or
// unticked (mode null). An empty list is removed: the server reads a missing
// list and an empty one the same way, none.
export function resourcesAfter(form, addonId, resourceId, mode) {
  const rest = pickedResources(form, addonId).filter((e) => e.id !== resourceId)
  const had = pickedResources(form, addonId).find((e) => e.id === resourceId)
  const next = { ...(form.resources || {}) }
  const entries = RESOURCE_MODES.includes(mode)
    ? (had ? pickedResources(form, addonId).map((e) => (e.id === resourceId ? { id: e.id, mode } : e)) : [...rest, { id: resourceId, mode }])
    : rest
  if (entries.length) next[addonId] = entries
  else delete next[addonId]
  return next
}

// The resources of the form as the server takes them: valid modes only, each
// ID once, no empty list.
export function resourcesPayload(resources) {
  const out = {}
  for (const [addon, entries] of Object.entries(resources || {})) {
    const seen = new Set()
    const kept = []
    for (const e of list(entries)) {
      const id = typeof e?.id === 'string' ? e.id.trim() : ''
      if (!id || seen.has(id)) continue
      seen.add(id)
      kept.push({ id, mode: e.mode === 'write' ? 'write' : 'read' })
    }
    if (kept.length) out[addon] = kept
  }
  return out
}

// What the memory skills line says: on while the agent reads notes.
export function memorySkillsState(form, memory) {
  if (memory && memory.enabled === false) return 'offUser'
  if (form.memory === 'none') return 'off'
  return 'on'
}

// --- MCP servers ---
//
// One list over what the host defines: the user's MCP connectors (stored as
// connector:<id>) and the MCP servers of the fixed model (stored as plain
// names). Both are the saved field mcp_servers. A stored name that matches
// nothing defined is a row of its own, "unknown", never dropped and never
// kept silently: the form says it is ignored and offers to remove it.
//
// connectors: the rows of agentConnectors ({ id, name, off, hint, gone })
// servers:    the names of the model's servers, when it has any
// stored:     the plain names the agent holds
// checkable:  whether the model is fixed and listed its servers, so a plain
//             name can be told defined from not defined
export function mcpRows({ connectors, servers, stored, checkable }) {
  const rows = []
  for (const c of list(connectors)) {
    rows.push({ key: `c-${c.id}`, kind: c.gone ? 'unknown' : 'connector', id: c.id, name: c.name || c.id, on: !c.off, hint: c.hint || '', ref: `connector:${c.id}` })
  }
  const known = new Set(list(servers))
  for (const name of list(servers)) rows.push({ key: `s-${name}`, kind: 'server', id: name, name, on: list(stored).includes(name), hint: '', ref: name })
  for (const name of list(stored).filter((n) => !known.has(n))) {
    rows.push({ key: `s-${name}`, kind: checkable ? 'unknown' : 'server', id: name, name, on: true, hint: '', ref: name, unverified: !checkable })
  }
  return rows
}

// The rows whose name contains the query, ignoring case.
export function filterMcp(rows, query) {
  const q = (query || '').trim().toLowerCase()
  return q ? rows.filter((r) => r.name.toLowerCase().includes(q)) : rows
}

// The names the agent holds that nothing defined matches, as the user knows them.
export const mcpUnknown = (rows) => rows.filter((r) => r.kind === 'unknown').map((r) => r.name)
