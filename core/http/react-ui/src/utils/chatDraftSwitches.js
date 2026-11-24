import { offList, skillKey } from './chatSkills.js'

// The switches of a chat that does not exist yet. The composer of a new chat
// (Home, the empty chat) lists what the chat could use with the draft
// endpoints, and the person switches things there before the first message.
// The choices are kept here and go with the create request, so the chat is
// stored with them (POST /api/chats, switches).

// The choices of a draft: what the person switched, by kind and key. A list
// read again (another model or agent) keeps them for the rows it still has.
export function emptyChoices() {
  return { addons: {}, mcp: {}, builtins: {}, skills: {}, connections: {} }
}

// The choices with one more: kind is addons, mcp or builtins (value: off),
// skills (value: enabled) or connections (value: the list of binding IDs, empty to clear).
export function withChoice(choices, kind, key, value) {
  return { ...choices, [kind]: { ...choices[kind], [key]: value } }
}

// An add-on row with the connections the draft enabled. The server answers a
// draft list for the choices it was sent, so this only keeps the screen right
// until that answer comes; ids the row does not list are ignored, and a set
// that the agent names is not changed.
function withConnection(a, connections = {}) {
  if (!a.credentialed || a.connection_by === 'agent' || !Object.prototype.hasOwnProperty.call(connections, a.id)) return a
  const known = new Set((a.connections || []).map((c) => c.id))
  const ids = (a.connections || []).map((c) => c.id).filter((id) => [].concat(connections[a.id] || []).includes(id) && known.has(id))
  if (ids.length === 0) return a
  return { ...a, enabled_connections: ids, connection_by: 'chat', needs_connection: false }
}

// The lists as the server gave them for the draft, with the choices applied.
export function applyChoices({ addons = [], servers = [], connectors = [], mcpOff = [], builtins = [], skills = [] }, choices, ref = (id) => id) {
  const off = new Set(mcpOff)
  for (const [k, v] of Object.entries(choices.mcp)) {
    if (v) off.add(k)
    else off.delete(k)
  }
  const has = (map, key) => Object.prototype.hasOwnProperty.call(map, key)
  return {
    addons: addons.map((a) => withConnection(has(choices.addons, a.id) ? { ...a, off: choices.addons[a.id] } : a, choices.connections)),
    servers: servers.map((s) => (has(choices.mcp, s.name) ? { ...s, off: choices.mcp[s.name] } : s)),
    connectors: connectors.map((c) => (has(choices.mcp, ref(c.id)) ? { ...c, off: choices.mcp[ref(c.id)] } : c)),
    mcpOff: [...off].sort(),
    builtins: builtins.map((b) => (has(choices.builtins, b.group) ? { ...b, off: choices.builtins[b.group] } : b)),
    skills: skills.map((s) => (has(choices.skills, skillKey(s)) ? { ...s, enabled: choices.skills[skillKey(s)] } : s)),
  }
}

// The switches of the create request, from what the composer shows: each list
// in the form its own PUT takes. null when nothing differs from a plain chat,
// so the request carries no switches.
export function draftSwitches({ addons = [], mcpOff = [], builtins = [], skills = [] }) {
  // The connections the person enabled in the composer, for an add-on that
  // lets the chat choose (an agent's own set is the agent's).
  const connections = Object.fromEntries(addons.filter((a) => a.credentialed && a.connection_by === 'chat' && (a.enabled_connections || []).length).map((a) => [a.id, a.enabled_connections]))
  const out = {
    addons_off: addons.filter((a) => a.off).map((a) => a.id).sort(),
    mcp_off: [...mcpOff].sort(),
    builtins_off: builtins.filter((b) => b.off && !b.opt_in).map((b) => b.group).sort(),
    builtins_on: builtins.filter((b) => b.opt_in && !b.off).map((b) => b.group).sort(),
    skills_off: offList(skills),
  }
  for (const k of Object.keys(out)) if (out[k].length === 0) delete out[k]
  if (Object.keys(connections).length) out.connections = connections
  return Object.keys(out).length ? out : null
}

// The switches of the draft add-ons request, from the choices so far. Only a
// group that the list knows is sent, since the server refuses a name it does
// not: builtins is the list as the server gave it (it says which groups are
// opt-in). null when no choice changes anything, so the request is the plain one.
export function draftListSwitches(choices, builtins = []) {
  const groups = new Map(builtins.map((b) => [b.group, !!b.opt_in]))
  const out = {
    addons_off: Object.entries(choices.addons).filter(([, off]) => off).map(([id]) => id).sort(),
    builtins_off: Object.entries(choices.builtins).filter(([g, off]) => off && groups.get(g) === false).map(([g]) => g).sort(),
    builtins_on: Object.entries(choices.builtins).filter(([g, off]) => !off && groups.get(g) === true).map(([g]) => g).sort(),
  }
  for (const k of Object.keys(out)) if (out[k].length === 0) delete out[k]
  const connections = Object.fromEntries(Object.entries(choices.connections || {}).filter(([, ids]) => [].concat(ids || []).length))
  if (Object.keys(connections).length) out.connections = connections
  return Object.keys(out).length ? out : null
}

// The choices that give back the switches of a draft: the inverse of
// draftSwitches for the lists it keeps (a saved setup holds that object).
// Opt-in groups are stored as builtins_on and become "not off" (false).
// Names the lists do not have are harmless: applyChoices skips them.
export function choicesFromTools(tools) {
  let c = emptyChoices()
  const t = tools || {}
  for (const g of t.builtins_off || []) c = withChoice(c, 'builtins', g, true)
  for (const g of t.builtins_on || []) c = withChoice(c, 'builtins', g, false)
  for (const id of t.addons_off || []) c = withChoice(c, 'addons', id, true)
  for (const n of t.mcp_off || []) c = withChoice(c, 'mcp', n, true)
  for (const k of t.skills_off || []) c = withChoice(c, 'skills', k, false)
  return c
}

// Whether the answer of the add-ons list may replace the rows on screen.
// An existing chat's switch bumps the epoch, shows its rows at once and takes
// the confirmed rows from its own response, so an older list answer (epoch
// changed since it started) must not overwrite them. A draft has no response
// of its own to take rows from: its rows come from the list again and the
// choices are applied to them, so a late answer is the right one to show.
export function addonRowsCurrent({ draft, epoch, current }) {
  return draft || epoch === current
}
