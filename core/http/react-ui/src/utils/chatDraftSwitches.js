import { offList, skillKey } from './chatSkills.js'

// The switches of a chat that does not exist yet. The composer of a new chat
// (Home, the empty chat) lists what the chat could use with the draft
// endpoints, and the person switches things there before the first message.
// The choices are kept here and go with the create request, so the chat is
// stored with them (POST /api/chats, switches).

// The choices of a draft: what the person switched, by kind and key. A list
// read again (another model or agent) keeps them for the rows it still has.
export function emptyChoices() {
  return { addons: {}, mcp: {}, builtins: {}, skills: {} }
}

// The choices with one more: kind is addons, mcp or builtins (value: off) or
// skills (value: enabled).
export function withChoice(choices, kind, key, value) {
  return { ...choices, [kind]: { ...choices[kind], [key]: value } }
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
    addons: addons.map((a) => (has(choices.addons, a.id) ? { ...a, off: choices.addons[a.id] } : a)),
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
  const out = {
    addons_off: addons.filter((a) => a.off).map((a) => a.id).sort(),
    mcp_off: [...mcpOff].sort(),
    builtins_off: builtins.filter((b) => b.off && !b.opt_in).map((b) => b.group).sort(),
    builtins_on: builtins.filter((b) => b.opt_in && !b.off).map((b) => b.group).sort(),
    skills_off: offList(skills),
  }
  for (const k of Object.keys(out)) if (out[k].length === 0) delete out[k]
  return Object.keys(out).length ? out : null
}
