// The Effective view of the agent form: a plain list of what the agent can do
// right now, worked out from the form as it stands (saved or not). No React,
// so a node test drives it. The component turns each line into words with the
// `text` key and its `vars`.
//
// A line: { id, state, text, vars, section, code?, fix? }
//   state    allowed | asks | off | blocked | info
//   section  the card of the form the line explains (a key of GROUPS)
//   code     the reason code, when a server check or a switch gave one
//   fix      { kind: 'section', section } to fix it on this form, or
//            { kind: 'link', to } to fix it on another page
import { MEMORY_GRANT, memoryLevelOf, stricterLevel } from './chatAgents.js'
import { addonNow, addonSkillState } from './agentUses.js'

const TOOL_GROUPS = new Set(['builtins', 'addons', 'servers', 'connectors'])
const list = (x) => (Array.isArray(x) ? x : [])

// The order lines are shown in: what needs the person first.
const RANK = { blocked: 0, allowed: 1, asks: 2, info: 3, off: 4 }

// How many names a line spells out before "and N more".
export const NAME_LIMIT = 5

// The rows of the tool groups as one flat list, each marked as a tool or a
// grant, with the options of a row following it.
export function flatRows(groups) {
  const out = []
  for (const g of list(groups)) {
    const kind = TOOL_GROUPS.has(g.key) ? 'tool' : 'grant'
    for (const r of list(g.rows)) {
      out.push({ ...r, kind, group: g.key })
      for (const o of list(r.options)) out.push({ ...o, kind: 'grant', group: g.key, parent: r.id, parentLabel: r.label })
    }
  }
  return out
}

// "A, B, C" and how many more were left out.
function names(items) {
  const shown = items.slice(0, NAME_LIMIT)
  return { names: shown.join(', '), more: Math.max(items.length - shown.length, 0) }
}

const unattended = (name) => name.startsWith('act.')

// The reason and the fix of a refusal the server named in an access preview.
// open_agent is this form; the rest are other pages.
export function fixOf(fix, binding) {
  if (!fix) return null
  if (fix.kind === 'open_agent') return { kind: 'section', section: 'capabilities' }
  if (fix.kind === 'open_server_setting') return { kind: 'link', to: '/app/settings' }
  return { kind: 'link', to: binding?.to || '/app/chats/connectors' }
}

// The order of the chain, so the refusal nearest the agent comes first: a
// problem on this form is shown before one on the connection or the sender.
const CHAIN = ['no_agent', 'agent_unavailable', 'server_setting_off', 'tools_switch_off', 'agent_permission_off', 'connection_permission_off', 'sender_not_listed', 'sender_needs_pairing', 'channel_not_trusted', 'role_not_granted', 'kind_not_running', 'context_rule', 'unknown_capability']

// What an access preview says about one connection: how many capabilities some
// kind of sender may use, and the refusals, nearest the agent first, one per
// capability (the refusal of the sender kind that gets furthest).
export function summariseAccess(view) {
  const caps = new Map()
  for (const s of list(view?.subjects)) {
    for (const d of list(s.decisions)) {
      const have = caps.get(d.capability)
      if (d.allowed) { caps.set(d.capability, { allowed: true }); continue }
      if (have?.allowed) continue
      const rank = CHAIN.indexOf(d.code)
      if (!have || (rank >= 0 && rank > CHAIN.indexOf(have.code))) caps.set(d.capability, { allowed: false, code: d.code, message: d.message || d.reason || '', fix: d.fix, blockedBy: d.blocked_by, capability: d.capability })
    }
  }
  const all = [...caps.values()]
  const blocked = all.filter((c) => !c.allowed).sort((a, b) => CHAIN.indexOf(a.code) - CHAIN.indexOf(b.code))
  return { allowed: all.filter((c) => c.allowed).length, blocked }
}

// Builds the lines.
//   form       the agent form
//   rows       flatRows(toolGroups)
//   skills     the rows of the Skills list ({ on }), without the skills of add-ons
//   addons     the add-on rows of the form (with the account and resource picks)
//   skillRows  the skill rows of the Skills list (to tell add-on skills apart)
//   memory     the person's memory settings ({ enabled, save_mode }) or null
//   access     one entry per connection of this agent: { id, name, to, status, view }
//   isEdit     the agent exists on the server
export function effectiveLines({ form, rows, skills, memory, access, isEdit, addons = [], skillRows = [], mcpUnknown = [] }) {
  const lines = []
  const add = (l) => lines.push(l)

  // Model.
  add(form.model
    ? { id: 'model', state: 'info', text: 'model.fixed', vars: { model: form.model }, section: 'identity' }
    : { id: 'model', state: 'info', text: 'model.any', section: 'identity' })

  // Tools: what is on and works, and what is on and cannot work.
  // An add-on that is on and cannot work has a line of its own, with the
  // reason; it is not counted as a tool the agent can use.
  const now = new Map(list(addons).filter((a) => !a.gone && !a.off).map((a) => [a.id, addonNow(a)]))
  const stuck = new Set([...now].filter(([, n]) => n.tone === 'warn').map(([id]) => id))
  const tools = rows.filter((r) => r.kind === 'tool' && r.on && !(r.group === 'addons' && stuck.has(r.id)))
  const working = tools.filter((r) => r.usable !== false)
  if (working.length === 0) add({ id: 'tools', state: 'off', text: 'tools.none', section: 'capabilities' })
  else add({ id: 'tools', state: 'allowed', text: 'tools.some', vars: { count: working.length, ...names(working.map((r) => r.label)) }, section: 'capabilities' })
  for (const r of tools.filter((x) => x.usable === false)) {
    add({ id: `tool-${r.id}`, state: 'blocked', text: 'tools.blocked', vars: { name: r.label, reason: r.reason || '' }, section: 'capabilities', code: 'tool_unusable', fix: { kind: 'section', section: 'capabilities' } })
  }

  // Add-ons: the account each one uses, the resources it may use, and why one
  // cannot work.
  for (const a of list(addons).filter((x) => !x.gone && !x.off)) {
    const n = now.get(a.id)
    const name = a.title || a.id
    if (n.tone === 'warn') {
      add({ id: `addon-${a.id}`, state: 'blocked', text: `addons.${n.key}`, vars: { name, noun: n.vars.noun, plural: n.vars.plural }, section: 'capabilities', code: n.code, fix: { kind: 'section', section: 'capabilities' } })
      continue
    }
    if (a.credentialed) {
      const used = list(a.connections).filter((c) => list(a.enabled_connections).includes(c.id))
      const who = (used.length ? used : list(a.connections).slice(0, 1)).map((c) => c.name).join(', ')
      if (who) add({ id: `addon-account-${a.id}`, state: 'allowed', text: 'addons.account', vars: { name, names: who }, section: 'capabilities' })
    }
    if (a.resources) {
      const picked = list(a.agent_resources)
      const w = picked.filter((r) => r.mode === 'write').length
      const vars = { name, plural: a.resources.noun_plural || a.resources.noun || '', n: picked.length, w }
      if (n.code === 'agent_resources_read_only') add({ id: `addon-resources-${a.id}`, state: 'info', text: 'addons.resourcesRead', vars, section: 'capabilities', code: n.code })
      else add({ id: `addon-resources-${a.id}`, state: 'allowed', text: 'addons.resources', vars, section: 'capabilities' })
    }
  }

  // An MCP server the agent holds that nothing defined matches is ignored.
  for (const name of list(mcpUnknown)) {
    add({ id: `mcp-${name}`, state: 'blocked', text: 'mcp.ignored', vars: { name }, section: 'capabilities', code: 'mcp_not_defined', fix: { kind: 'section', section: 'capabilities' } })
  }

  // Skills. Those that come with an add-on are named apart; a ticked one whose
  // add-on is off does nothing, and the line says so.
  const skillOn = list(skills).filter((s) => s.on)
  if (skillOn.length) add({ id: 'skills', state: 'allowed', text: 'skills.some', vars: { count: skillOn.length, ...names(skillOn.map((s) => s.label || s.id)) }, section: 'capabilities' })
  const withAddons = addonSkillState(form, skillRows)
  if (withAddons.live.length) add({ id: 'addon-skills', state: 'allowed', text: 'skills.withAddons', vars: { count: withAddons.live.length, ...names(withAddons.live.map((s) => s.name)) }, section: 'capabilities' })
  for (const s of withAddons.dead) {
    add({ id: `skill-dead-${s.addon}-${s.name}`, state: 'blocked', text: 'skills.addonOff', vars: { name: s.name, addon: s.addon_title || s.addon }, section: 'capabilities', code: 'skill_addon_off', fix: { kind: 'section', section: 'capabilities' } })
  }

  // Files. One line for the three choices: the switch off is No files.
  const files = tools.find((r) => r.id === 'files')
  if (list(form.builtins_off).includes('files')) {
    add({ id: 'files', state: 'off', text: 'files.none', section: 'capabilities' })
  } else if (files && files.usable !== false) {
    const n = list(form.folders).length
    add({ id: 'files', state: 'allowed', text: n ? 'files.folders' : 'files.all', vars: { count: n }, section: 'capabilities' })
  }

  // Grants. Memory has its own line; a grant under a switch that is off cannot
  // work; one a connection does not allow yet is named with the connection.
  const grants = rows.filter((r) => r.kind === 'grant' && r.on && !r.gone && r.id !== MEMORY_GRANT)
  const held = []
  const acts = []
  for (const g of grants) {
    if (g.blocked) {
      add({ id: `grant-${g.id}`, state: 'blocked', text: 'grants.parentOff', vars: { name: g.label, parent: g.parentLabel || '' }, section: 'capabilities', code: 'tools_switch_off', fix: { kind: 'section', section: 'capabilities' } })
      continue
    }
    for (const n of list(g.notes)) {
      add({ id: `grant-${g.id}-${n.key}`, state: 'blocked', text: 'grants.connection', vars: { name: g.label, connection: n.text }, section: 'identity', code: 'connection_permission_off', fix: n.link?.to ? { kind: 'link', to: n.link.to } : null })
    }
    ;(unattended(g.id) ? acts : held).push(g.label)
  }
  if (held.length) add({ id: 'grants', state: 'allowed', text: 'grants.some', vars: { count: held.length, ...names(held) }, section: 'capabilities' })
  if (acts.length) add({ id: 'acts', state: 'allowed', text: 'grants.unattended', vars: { count: acts.length, ...names(acts) }, section: 'capabilities' })

  // Memory.
  const choice = form.memory || ''
  const mem = memoryLevelOf(form)
  const level = stricterLevel(memory?.save_mode, form.memory_save)
  if (memory && memory.enabled === false) {
    add({ id: 'memory-off', state: 'off', text: 'memory.userOff', section: 'memory', code: 'memory_off_user', fix: { kind: 'link', to: '/app/account?tab=memory' } })
  } else if (mem === 'off') {
    add({ id: 'memory', state: 'off', text: 'memory.none', section: 'memory' })
  } else {
    add({ id: 'memory', state: 'allowed', text: `memory.${choice || 'asBefore'}`, section: 'memory' })
    // Reads without the right to remember is a choice, not a fault: the
    // control says "Reads notes", and so does this line. An agent from before
    // the choice has no line here, because what it reads is not decided yet.
    if (mem === 'saves') add({ id: 'memory-level', state: level === 'auto' ? 'allowed' : level === 'never' ? 'off' : 'asks', text: level === 'auto' && form.memory_save === 'auto' ? 'memory.level.agentAuto' : `memory.level.${level}`, section: 'memory' })
    else if (choice) add({ id: 'memory-level', state: 'info', text: 'memory.readsOnly', section: 'memory' })
  }

  // Knowledge.
  const cols = list(form.collections)
  if (cols.length) add({ id: 'knowledge', state: 'allowed', text: 'knowledge.some', vars: { count: cols.length, ...names(cols) }, section: 'memory' })
  else add({ id: 'knowledge', state: 'off', text: 'knowledge.none', section: 'memory' })

  // Connections.
  if (!isEdit) {
    add({ id: 'connections', state: 'info', text: 'connections.unsaved', section: 'identity' })
  } else if (access.length === 0) {
    add({ id: 'connections', state: 'info', text: 'connections.none', section: 'identity' })
  }
  for (const a of access) {
    if (a.status === 'loading') { add({ id: `conn-${a.id}`, state: 'info', text: 'connections.loading', vars: { name: a.name }, section: 'identity' }); continue }
    if (a.status === 'error') { add({ id: `conn-${a.id}`, state: 'info', text: 'connections.error', vars: { name: a.name }, section: 'identity', fix: { kind: 'link', to: a.to } }); continue }
    const sum = summariseAccess(a.view)
    add({ id: `conn-${a.id}`, state: sum.allowed ? 'allowed' : 'off', text: 'connections.summary', vars: { name: a.name, allowed: sum.allowed, blocked: sum.blocked.length }, section: 'identity' })
    // An agent that only reads has no right to remember on purpose: the
    // connection cannot be blamed for it, so that refusal is not listed.
    const blocked = sum.blocked.filter((b) => !(b.capability === MEMORY_GRANT && b.code === 'agent_permission_off' && choice && mem !== 'saves'))
    for (const b of blocked.slice(0, 2)) {
      add({ id: `conn-${a.id}-${b.capability}`, state: 'blocked', text: 'connections.blocked', vars: { name: a.name, capability: rows.find((r) => r.id === b.capability)?.label || b.capability, message: b.message }, section: 'identity', code: b.code, fix: fixOf(b.fix, a) })
    }
  }

  // The memory line above speaks for the app. A messaging connection whose
  // own permission for saving notes is off (not the agent's choice) saves none,
  // and the line says where.
  if (mem === 'saves' && !(memory && memory.enabled === false)) {
    const cut = list(access).filter((a) => a.status === 'ok' && summariseAccess(a.view).blocked.some((b) => b.capability === MEMORY_GRANT && b.code !== 'agent_permission_off')).map((a) => a.name)
    if (cut.length) add({ id: 'memory-connection', state: 'info', text: 'memory.connectionLimit', vars: { count: cut.length, ...names(cut) }, section: 'memory' })
  }

  return lines
    .map((l, i) => ({ l, i }))
    .sort((x, y) => RANK[x.l.state] - RANK[y.l.state] || x.i - y.i)
    .map((x) => x.l)
}

// The count of lines that are blocked, for the summary and the nav dot.
export const blockedCount = (lines) => lines.filter((l) => l.state === 'blocked').length
