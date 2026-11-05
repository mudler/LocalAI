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
import { MEMORY_GRANT, memorySaves, stricterLevel } from './chatAgents.js'

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
//   skills     the rows of the Skills list ({ on })
//   memory     the person's memory settings ({ enabled, save_mode }) or null
//   access     one entry per connection of this agent: { id, name, to, status, view }
//   isEdit     the agent exists on the server
export function effectiveLines({ form, rows, skills, memory, access, isEdit }) {
  const lines = []
  const add = (l) => lines.push(l)

  // Model.
  add(form.model
    ? { id: 'model', state: 'info', text: 'model.fixed', vars: { model: form.model }, section: 'identity' }
    : { id: 'model', state: 'info', text: 'model.any', section: 'identity' })

  // Tools: what is on and works, and what is on and cannot work.
  const tools = rows.filter((r) => r.kind === 'tool' && r.on)
  const working = tools.filter((r) => r.usable !== false)
  if (working.length === 0) add({ id: 'tools', state: 'off', text: 'tools.none', section: 'capabilities' })
  else add({ id: 'tools', state: 'allowed', text: 'tools.some', vars: { count: working.length, ...names(working.map((r) => r.label)) }, section: 'capabilities' })
  for (const r of tools.filter((x) => x.usable === false)) {
    add({ id: `tool-${r.id}`, state: 'blocked', text: 'tools.blocked', vars: { name: r.label, reason: r.reason || '' }, section: 'capabilities', code: 'tool_unusable', fix: { kind: 'section', section: 'capabilities' } })
  }

  // Skills.
  const skillOn = list(skills).filter((s) => s.on)
  if (skillOn.length) add({ id: 'skills', state: 'allowed', text: 'skills.some', vars: { count: skillOn.length, ...names(skillOn.map((s) => s.label || s.id)) }, section: 'capabilities' })

  // Files.
  const files = tools.find((r) => r.id === 'files')
  if (files && files.usable !== false) {
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
      add({ id: `grant-${g.id}-${n.key}`, state: 'blocked', text: 'grants.connection', vars: { name: g.label, connection: n.text }, section: 'connections', code: 'connection_permission_off', fix: n.link?.to ? { kind: 'link', to: n.link.to } : null })
    }
    ;(unattended(g.id) ? acts : held).push(g.label)
  }
  if (held.length) add({ id: 'grants', state: 'allowed', text: 'grants.some', vars: { count: held.length, ...names(held) }, section: 'capabilities' })
  if (acts.length) add({ id: 'acts', state: 'allowed', text: 'grants.unattended', vars: { count: acts.length, ...names(acts) }, section: 'capabilities' })

  // Memory.
  const choice = form.memory || ''
  const level = stricterLevel(memory?.save_mode, form.memory_save)
  if (memory && memory.enabled === false) {
    add({ id: 'memory-off', state: 'off', text: 'memory.userOff', section: 'memory', code: 'memory_off_user', fix: { kind: 'link', to: '/app/account?tab=memory' } })
  } else if (choice === 'none') {
    add({ id: 'memory', state: 'off', text: 'memory.none', section: 'memory' })
  } else {
    add({ id: 'memory', state: 'allowed', text: `memory.${choice || 'asBefore'}`, section: 'memory' })
    const saves = memorySaves(choice) || choice === ''
    const granted = list(form.builtins).includes(MEMORY_GRANT)
    if (saves && !granted && choice) add({ id: 'memory-grant', state: 'blocked', text: 'memory.noGrant', section: 'memory', code: 'agent_permission_off', fix: { kind: 'section', section: 'memory' } })
    else if (saves) add({ id: 'memory-level', state: level === 'auto' ? 'allowed' : level === 'never' ? 'off' : 'asks', text: `memory.level.${level}`, section: 'memory' })
  }

  // Knowledge.
  const cols = list(form.collections)
  if (cols.length) add({ id: 'knowledge', state: 'allowed', text: 'knowledge.some', vars: { count: cols.length, ...names(cols) }, section: 'memory' })
  else add({ id: 'knowledge', state: 'off', text: 'knowledge.none', section: 'memory' })

  // Connections.
  if (!isEdit) {
    add({ id: 'connections', state: 'info', text: 'connections.unsaved', section: 'connections' })
  } else if (access.length === 0) {
    add({ id: 'connections', state: 'info', text: 'connections.none', section: 'connections' })
  }
  for (const a of access) {
    if (a.status === 'loading') { add({ id: `conn-${a.id}`, state: 'info', text: 'connections.loading', vars: { name: a.name }, section: 'connections' }); continue }
    if (a.status === 'error') { add({ id: `conn-${a.id}`, state: 'info', text: 'connections.error', vars: { name: a.name }, section: 'connections', fix: { kind: 'link', to: a.to } }); continue }
    const sum = summariseAccess(a.view)
    add({ id: `conn-${a.id}`, state: sum.allowed ? 'allowed' : 'off', text: 'connections.summary', vars: { name: a.name, allowed: sum.allowed, blocked: sum.blocked.length }, section: 'connections' })
    for (const b of sum.blocked.slice(0, 2)) {
      add({ id: `conn-${a.id}-${b.capability}`, state: 'blocked', text: 'connections.blocked', vars: { name: a.name, capability: rows.find((r) => r.id === b.capability)?.label || b.capability, message: b.message }, section: 'connections', code: b.code, fix: fixOf(b.fix, a) })
    }
  }

  return lines
    .map((l, i) => ({ l, i }))
    .sort((x, y) => RANK[x.l.state] - RANK[y.l.state] || x.i - y.i)
    .map((x) => x.l)
}

// The count of lines that are blocked, for the summary and the nav dot.
export const blockedCount = (lines) => lines.filter((l) => l.state === 'blocked').length
