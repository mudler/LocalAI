import { PREFIX, splitAgentMCP, joinAgentMCP } from './chatMCP.js'
import { skillKey } from './chatSkills.js'
import { SAVED_AGENTS_GROUP, savedAgentsOn } from './toolOptions.js'

// A saved agent holds its tools as grants (add-ons, MCP servers, connectors,
// skills: what is listed is on) and as switches of the built-in groups
// (builtins_off: groups that are off; builtins_on: opt-in groups that start
// on). The Tools list of a chat is drawn from rows that have an `off` flag.
// These functions turn the agent form into such rows and a switch back into
// the form, so the agent form can draw the same list as the chat. No React,
// so a node test drives them.

const list = (x) => (Array.isArray(x) ? x : [])

// The built-in groups as the draft endpoint lists them, with the agent's
// switches applied. An opt-in group is on only when the agent starts with it.
export function agentBuiltins(groups, form) {
  const off = new Set(list(form.builtins_off))
  const on = new Set(list(form.builtins_on))
  // Saved agents is one switch with two stored values: the opt-in group and the
  // grant of the same name. Either one turns it on, as the server does at save.
  const savedOn = savedAgentsOn(form)
  return list(groups).map((b) => ({ ...b, off: b.group === SAVED_AGENTS_GROUP ? !savedOn : b.opt_in ? !on.has(b.group) : off.has(b.group) }))
}

// The builtins_off and builtins_on of the form after these groups are switched
// on or off. Groups the server does not list stay as they are.
export function builtinsAfter(form, groups, names, on) {
  const optIn = new Set(list(groups).filter((b) => b.opt_in).map((b) => b.group))
  const pick = new Set(names)
  const keep = (l, isOptIn) => list(l).filter((g) => !pick.has(g) || optIn.has(g) !== isOptIn)
  let off = keep(form.builtins_off, false)
  let onList = keep(form.builtins_on, true)
  for (const g of names) {
    if (optIn.has(g)) {
      if (on) onList = [...onList, g]
    } else if (!on) {
      off = [...off, g]
    }
  }
  return { builtins_off: dedupe(off), builtins_on: dedupe(onList) }
}

// The grant that goes with the saved-agents switch. The switch wins: turning
// it off takes the grant away too (else the server would turn the group back
// on at save). Turning it on adds only the group, never the grant, because the
// first grant of an agent turns off what it does by default on its messaging
// connections; an agent that already holds the grant keeps it.
export function savedAgentsGrantAfter(form, names, on) {
  const builtins = list(form.builtins)
  if (on || !names.includes(SAVED_AGENTS_GROUP)) return builtins
  return builtins.filter((g) => g !== SAVED_AGENTS_GROUP)
}

const dedupe = (l) => l.filter((x, i) => l.indexOf(x) === i)

// The add-ons the agent grants, as rows. A granted add-on this account cannot
// see stays in the list, marked, so it can be taken away.
export function agentAddons(addons, form) {
  const granted = new Set(list(form.addons))
  const seen = new Set(list(addons).map((a) => a.id))
  const gone = [...granted].filter((id) => !seen.has(id)).map((id) => ({ id, tools: 0, gone: true }))
  const named = form.connections || {}
  // The row of an add-on with a credential shows the connections the agent
  // names, not the ones a new chat would use; an ID that is not one of the
  // person's connections is left out.
  const withConnection = (a) => (a.credentialed
    ? { ...a, enabled_connections: list(a.connections).map((c) => c.id).filter((id) => connectionIds(named[a.id]).includes(id)), stored_connections: connectionIds(named[a.id]).length, connection_by: '', needs_connection: false }
    : a)
  // The resources the agent names for an add-on that scopes them per agent.
  const withResources = (a) => ({ ...a, agent_resources: list(form.resources?.[a.id]) })
  return [...list(addons).map((a) => ({ ...withResources(withConnection(a)), off: !granted.has(a.id) })), ...gone.map((a) => ({ ...a, off: false }))]
}

// The binding IDs of one entry of a connections object. A server older than
// sets, or a saved form, may hold a single string.
export function connectionIds(entry) {
  return [].concat(entry || []).filter((id) => typeof id === 'string' && id.trim()).map((id) => id.trim())
}

// The connections of the form after the agent names the set for one add-on
// (an empty list stops naming).
export function connectionsAfter(form, addonId, bindingIds) {
  const next = { ...(form.connections || {}) }
  const ids = connectionIds(bindingIds)
  if (ids.length) next[addonId] = ids
  else delete next[addonId]
  return next
}

export function addonsAfter(form, ids, on) {
  const rest = list(form.addons).filter((x) => !ids.includes(x))
  return on ? dedupe([...rest, ...ids]) : rest
}

// The connectors the agent grants, as rows (same rule for a missing one).
export function agentConnectors(connectors, form) {
  const granted = new Set(splitAgentMCP(form.mcp_servers).connectors)
  const seen = new Set(list(connectors).map((c) => c.id))
  const gone = [...granted].filter((id) => !seen.has(id)).map((id) => ({ id, name: id, usable: true, gone: true, off: false }))
  // A connector that is off now can still be granted: the agent may run when
  // it is back, so the row stays usable and says why in a note.
  return [...list(connectors).map((c) => ({ ...c, usable: true, hint: c.usable === false ? c.reason || '' : '', off: !granted.has(c.id) })), ...gone]
}

// keys are connector:<id>, as the Tools list names them.
export function connectorsAfter(form, keys, on) {
  const ids = keys.filter((k) => k.startsWith(PREFIX)).map((k) => k.slice(PREFIX.length))
  const { servers, connectors } = splitAgentMCP(form.mcp_servers)
  const rest = connectors.filter((x) => !ids.includes(x))
  return joinAgentMCP(servers, on ? dedupe([...rest, ...ids]) : rest)
}

// The skills of the picker as rows: on when the agent grants the skill by
// name. A granted skill that no longer exists stays in the list, marked.
export function agentSkills(skills, form) {
  const granted = new Set(list(form.skills))
  const seen = new Set(list(skills).map((s) => s.name))
  const gone = [...granted].filter((n) => !seen.has(n)).map((n) => ({ id: `${GONE}${n}`, name: n, enabled: true, gone: true }))
  return [...list(skills).map((s) => ({ ...s, enabled: granted.has(s.name) })), ...gone]
}

const GONE = 'gone:'

// ids are the keys of the skill rows (layer:name); the agent stores names.
export function skillsAfter(form, skills, ids, on) {
  const names = new Set(list(skills).filter((s) => ids.has(skillKey(s))).map((s) => s.name))
  const rest = list(form.skills).filter((x) => !names.has(x))
  return on ? dedupe([...rest, ...names]) : rest
}

// The builtin grants of the picker as rows: every grant the server declares,
// on when the agent holds it. A grant the agent holds but this server does not
// declare (an import from a newer server) stays in the list, marked, so it can
// be taken away. Pass readOnlyHeld to list only the held ones (a chat shows
// what its agent holds, not what it could hold).
export function agentGrants(catalog, builtins, { heldOnly = false } = {}) {
  const held = new Set(list(builtins))
  const known = new Set(list(catalog).map((g) => g.name))
  const gone = [...held].filter((n) => !known.has(n)).map((n) => ({ name: n, title: n, description: '', sensitive: false, gone: true, on: true }))
  const rows = [...list(catalog).map((g) => ({ ...g, on: held.has(g.name) })), ...gone]
  return heldOnly ? rows.filter((g) => g.on) : rows
}

// The builtins list of the form after these grants are switched on or off.
export function grantsAfter(form, names, on) {
  const rest = list(form.builtins).filter((x) => !names.includes(x))
  return on ? dedupe([...rest, ...names]) : rest
}
