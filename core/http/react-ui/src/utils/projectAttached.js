// What a project attaches, as a chat or an agent of the project shows it: on,
// and not removable from the chat menu. The server applies the attachment
// (every turn, also in old chats); these helpers only mark the rows the menus
// already list, so a person sees why a switch is locked. Pure, so a node test
// drives them.
import { TOOL_KINDS } from './projectToolSources.js'

const list = (x) => (Array.isArray(x) ? x : [])

// The attached lists of a project read, always with the five keys.
export function attachedOf(project) {
  return Object.fromEntries(TOOL_KINDS.map(k => [k, list(project?.attached?.[k])]))
}

export const hasAttached = (attached) => TOOL_KINDS.some(k => list(attached?.[k]).length > 0)

// A skill of the project layer is always on for the project; so is a skill
// the project attaches.
export function markSkills(skills, attached) {
  const names = new Set(list(attached?.skills))
  return list(skills).map(s => (s.layer === 'project' || (names.has(s.name) && !s.shadowed)
    ? { ...s, enabled: true, locked: true, fromProject: true }
    : s))
}

const mark = (rows, keyOf, names) => {
  const set = new Set(list(names))
  return list(rows).map(r => (set.has(keyOf(r)) ? { ...r, off: false, readOnly: true, fromProject: true } : r))
}

export const markAddons = (rows, attached) => mark(rows, r => r.id, attached?.addons)
export const markServers = (rows, attached) => mark(rows, r => r.name, attached?.mcp_servers)
// Connectors are attached by id; a chat lists them by id too.
export const markConnectors = (rows, attached) => mark(rows, r => r.id, list(attached?.connectors).map(c => String(c).replace(/^connector:/, '')))

// The attached collections: a chat has no switch per collection, so the menu
// names them in one line.
export const attachedCollections = (attached) => list(attached?.collections)
