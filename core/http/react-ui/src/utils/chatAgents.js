// Helpers of the saved agents page. No React, so a node test drives them.

export const LIST_KEYS = ['mcp_servers', 'addons', 'collections', 'skills']

export function emptyAgent() {
  return { id: '', name: '', description: '', instructions: '', model: '', mcp_servers: [], addons: [], collections: [], skills: [] }
}

// Adds a name to a list: trimmed, not empty, once.
export function addEntry(list, text) {
  const v = (text || '').trim()
  if (!v || list.includes(v)) return list
  return [...list, v]
}

export function removeEntry(list, name) {
  return list.filter(x => x !== name)
}

// The body of a create or replace request. The server refuses a bad one with the
// reason, so this only trims and drops what cannot be meant.
export function agentPayload(form) {
  const body = {
    name: (form.name || '').trim(),
    description: (form.description || '').trim(),
    instructions: form.instructions || '',
    model: (form.model || '').trim(),
  }
  for (const k of LIST_KEYS) body[k] = (form[k] || []).map(x => x.trim()).filter((x, i, a) => x && a.indexOf(x) === i)
  return body
}

// A short line for the list: what the agent grants.
export function grantSummary(agent) {
  const parts = []
  const n = (k) => (agent[k] || []).length
  if (n('addons')) parts.push(`${n('addons')} add-on${n('addons') === 1 ? '' : 's'}`)
  if (n('mcp_servers')) parts.push(`${n('mcp_servers')} MCP server${n('mcp_servers') === 1 ? '' : 's'}`)
  if (n('collections')) parts.push(`${n('collections')} collection${n('collections') === 1 ? '' : 's'}`)
  if (n('skills')) parts.push(`${n('skills')} skill${n('skills') === 1 ? '' : 's'}`)
  return parts.join(', ')
}
