// Helpers of the saved agents page. No React, so a node test drives them.

export const LIST_KEYS = ['mcp_servers', 'addons', 'collections', 'skills']

export function emptyAgent() {
  return { id: '', name: '', description: '', instructions: '', model: '', mcp_servers: [], addons: [], collections: [], skills: [], network: 'off', folder: null }
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
// reason, so this only trims and drops what cannot be meant. With
// { initialFolder } (the folder as read, or null) the body carries `folder`
// when the form changed it; without, it never does.
export function agentPayload(form, opts) {
  const body = {
    name: (form.name || '').trim(),
    description: (form.description || '').trim(),
    instructions: form.instructions || '',
    model: (form.model || '').trim(),
    // The network of the isolated commands of the agent's unattended runs.
    network: ['off', 'registries', 'all'].includes(form.network) ? form.network : 'off',
  }
  for (const k of LIST_KEYS) body[k] = (form[k] || []).map(x => x.trim()).filter((x, i, a) => x && a.indexOf(x) === i)
  if (opts && 'initialFolder' in opts) {
    const folder = folderPayload(opts.initialFolder, form.folder)
    if (folder) body.folder = folder
  }
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

// --- The folder of an agent ---
//
// The server reads an agent's folder as { path, name, mode, state }: mode is
// "read" or "write", state is "ok", "missing" (the folder was deleted) or
// "unset" (an import whose folder was not found). An agent without a folder
// has no `folder` key. The path is the folder as the person sees it in Files,
// "/" being all of their files.

export const FOLDER_MODES = ['read', 'write']

// The folder state of an agent: 'none', 'ok', 'missing' or 'unset'.
export function folderState(agent) {
  const f = agent?.folder
  if (!f || typeof f !== 'object') return 'none'
  return ['ok', 'missing', 'unset'].includes(f.state) ? f.state : 'ok'
}

// The last name of a folder path; '' for "/" (all the files).
export function folderName(path) {
  const parts = (path || '').split('/').filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
}

// The folder field of a create or replace request, or undefined when the
// folder did not change, so a save keeps the stored folder. `initial` is the
// folder as read from the server (or null), `current` what the form holds now
// (null = all my files). Clearing sends an empty path.
export function folderPayload(initial, current) {
  const a = initial && typeof initial === 'object' ? initial : null
  const b = current && typeof current === 'object' ? current : null
  if (!b) return a ? { path: '' } : undefined
  // Nothing chosen yet: a missing or unset folder the person did not replace
  // is kept as it is.
  if (!b.path) return undefined
  const mode = FOLDER_MODES.includes(b.mode) ? b.mode : 'read'
  if (a && a.state !== 'missing' && a.state !== 'unset' && a.path === b.path && a.mode === mode) return undefined
  return { path: b.path, mode }
}

// Whether a folder of a listing can be the folder of an agent: a folder of
// the person's own files, not one that LocalAI manages and not a shared one.
export function folderPickable(entry) {
  if (!entry || entry.kind !== 'dir') return false
  if (entry.managed || entry.shared) return false
  const p = entry.path || ''
  return p !== '/shared' && !p.startsWith('/shared/')
}

// The folders that agents use, for the marker of the Files page: a Map from
// the folder path to the agents that use it, { name, mode }, sorted by name.
// Only folders that exist are marked.
export function agentFolderMarks(agents) {
  const marks = new Map()
  for (const a of agents || []) {
    if (folderState(a) !== 'ok' || !a.folder.path) continue
    const list = marks.get(a.folder.path) || []
    list.push({ id: a.id, name: a.name, mode: a.folder.mode === 'write' ? 'write' : 'read' })
    marks.set(a.folder.path, list)
  }
  for (const list of marks.values()) list.sort((x, y) => x.name.localeCompare(y.name))
  return marks
}

// Whether an error of a chat request says that the folder of the chat's
// agent is missing (409 agent_folder_missing).
export function isFolderMissingError(err) {
  return !!err && err.status === 409 && err.body?.error?.type === 'agent_folder_missing'
}

// The line of the Tools menu of a scoped chat: which files its tools reach.
export function agentFolderLine(agent, t) {
  if (folderState(agent) !== 'ok') return ''
  const path = agent.folder.path === '/' ? t('agentFolder.home') : agent.folder.path
  return t(agent.folder.mode === 'write' ? 'agentFolder.toolsWrite' : 'agentFolder.toolsRead', { path })
}
