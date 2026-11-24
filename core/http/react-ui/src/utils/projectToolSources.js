// What a project's tool defaults can name, read from what is installed. Pure
// rules, so a node test drives them. Each source is
//   { ready, options: [{ value, label, detail, usable }] }
// where value is the exact string the server matches (a skill name, an add-on
// ID, an MCP server name, a connector ID, a collection name) and label is what
// a person reads. ready is false while the source loads or when it failed: a
// source that is not ready claims nothing about an entry.
import { pickerSkills } from './chatSkills.js'

export const TOOL_KINDS = Object.freeze(['skills', 'addons', 'mcp_servers', 'connectors', 'collections'])

const list = (x) => (Array.isArray(x) ? x : [])
const byLabel = (a, b) => a.label.localeCompare(b.label, undefined, { sensitivity: 'base', numeric: true })

export const emptySources = () => Object.fromEntries(TOOL_KINDS.map(k => [k, { ready: false, options: [] }]))

// The skills a chat can switch: the picker's rows, without the locked ones
// (the server skips those) and without the invalid or shadowed ones.
export function skillOptions(res) {
  return pickerSkills(res?.skills).skills
    .filter(s => s.name)
    .map(s => ({ value: s.name, label: s.name, detail: s.description || '', usable: !s.locked }))
    .sort(byLabel)
}

// The add-ons that serve tools, by ID, with the title they declare.
export function addonOptions(panel) {
  return list(panel?.addons).filter(a => a.id).map(a => ({ value: a.id, label: a.title || a.id, detail: a.title && a.title !== a.id ? a.id : '', usable: true })).sort(byLabel)
}

// The user's MCP connectors by ID, with their name. One that is not usable now
// (switched off) stays in the list, marked, so the entry says why it does
// nothing.
export function connectorOptions(panel) {
  return list(panel?.connectors).filter(c => c.id).map(c => ({ value: c.id, label: c.name || c.id, detail: c.name && c.name !== c.id ? c.id : '', usable: c.usable !== false, reason: c.reason || '' })).sort(byLabel)
}

// The MCP servers that the configuration of any chat model names, merged by name.
export function serverOptions(perModel) {
  const seen = new Map()
  for (const names of list(perModel)) {
    for (const n of list(names)) if (n && !seen.has(n)) seen.set(n, { value: n, label: n, detail: '', usable: true })
  }
  return [...seen.values()].sort(byLabel)
}

export function collectionOptions(data) {
  const names = (Array.isArray(data?.collections) ? data.collections : []).map(c => (typeof c === 'string' ? c : c?.name)).filter(Boolean)
  return [...new Set(names)].map(n => ({ value: n, label: n, detail: '', usable: true })).sort(byLabel)
}

// The label of an entry for display: the option's label when the source knows
// it, else the entry itself.
export function labelOf(options, value) {
  return options.find(o => o.value === value)?.label || value
}
