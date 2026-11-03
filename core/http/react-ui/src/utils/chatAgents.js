// Helpers of the saved agents page. No React, so a node test drives them.

// builtins_off and builtins_on are the switches of the built-in tool groups
// (see utils/agentTools.js); they are lists like the grants.
export const LIST_KEYS = ['mcp_servers', 'addons', 'collections', 'skills', 'builtins_off', 'builtins_on', 'builtins']

// The grants that an agent without a grant list holds for its messaging
// connections (not sensitive, so implied). As soon as the list names any
// grant, only the named ones work: these four are then off until added. The
// server rule is core/services/studio/connectorgrants.go (grantGated).
export const CONNECTOR_GRANTS = ['triggers.propose', 'triggers.run', 'talkback.channels', 'files.upload']

// The notice for the grants list: set when the saved agent had no list, the
// form now has one, and some connector grants are missing from it. Returns
// the missing names, or an empty array when nothing would turn off.
export function missingConnectorGrants(saved, current) {
  const had = (saved || []).length > 0
  const now = current || []
  if (had || now.length === 0) return []
  return CONNECTOR_GRANTS.filter(g => !now.includes(g))
}

// The grants list with a name switched on or off.
export function setGrant(list, name, on) {
  const rest = (list || []).filter(x => x !== name)
  return on ? [...rest, name] : rest
}


export function emptyAgent() {
  return { id: '', name: '', description: '', instructions: '', model: '', mcp_servers: [], addons: [], collections: [], skills: [], builtins_off: [], builtins_on: [], builtins: [], network: 'off', folders: [] }
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
// { initialFolders } (the folder rows as read) the body carries `folders`
// when the form changed them; without, it never does.
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
  // The connections the agent's chats may use for each add-on with a
  // credential (add-on ID to a list of binding IDs). The server keeps the
  // stored choices when a write leaves the field out, so the form sends it
  // once the agent has one or the person changed it ({} clears).
  if (form.connections && typeof form.connections === 'object') {
    body.connections = Object.fromEntries(Object.entries(form.connections)
      .map(([k, ids]) => [k, [].concat(ids || []).filter((id) => typeof id === 'string' && id.trim()).map((id) => id.trim()).filter((id, i, a) => a.indexOf(id) === i)])
      .filter(([, ids]) => ids.length))
  }
  if (opts && 'initialFolders' in opts) {
    const folders = foldersPayload(opts.initialFolders, form.folders)
    if (folders) body.folders = folders
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

// An agent has up to MAX_FOLDERS folders. The server sends them as `folders`
// (the first is also `folder`). Where a folder is inside another one, it works
// at the mode of the outer one (`effective`).
export const MAX_FOLDERS = 8

// The folder rows of an agent as the server sent them.
export function folderRows(agent) {
  if (Array.isArray(agent?.folders) && agent.folders.length) return agent.folders
  const f = agent?.folder
  return f && typeof f === 'object' ? [f] : []
}

const stateOf = (f) => (['ok', 'missing', 'unset'].includes(f?.state) ? f.state : 'ok')

// The folder state of an agent: 'none', 'ok', 'missing' or 'unset'. With
// several folders the worst one counts: the agent does not start before all
// are fixed.
export function folderState(agent) {
  const rows = folderRows(agent)
  if (!rows.length) return 'none'
  const states = rows.map(stateOf)
  return states.includes('missing') ? 'missing' : states.includes('unset') ? 'unset' : 'ok'
}

// The last name of a folder path; '' for "/" (all the files).
export function folderName(path) {
  const parts = (path || '').split('/').filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
}

const holds = (outer, inner) => outer === '/' || inner === outer || inner.startsWith(outer + '/')

// The mode each row works at, by the server's rule: a folder inside another
// folder of the list takes the mode of the outermost one; a row without a
// path keeps its own. Returns a list of 'read' | 'write' in row order.
export function effectiveModes(rows) {
  const modeOf = (r) => (r?.mode === 'write' ? 'write' : 'read')
  return (rows || []).map((r, i) => {
    if (!r?.path) return modeOf(r)
    let outer = -1
    rows.forEach((o, j) => {
      if (j === i || !o?.path || o.path === r.path || !holds(o.path, r.path)) return
      if (outer < 0 || o.path.length < rows[outer].path.length) outer = j
    })
    return modeOf(outer >= 0 ? rows[outer] : r)
  })
}

// The folders field of a create or replace request, or undefined when the
// rows did not change, so a save keeps the stored folders. `initial` is the
// rows as read from the server, `current` what the form holds now. No rows
// where there were some sends [] (all my files). A row without a path cannot
// be sent: the editor stops the save first (see foldersUnchosen).
export function foldersPayload(initial, current) {
  const key = (rows) => JSON.stringify((rows || []).map(r => [r.path || '', r.mode === 'write' ? 'write' : 'read']))
  if (key(initial) === key(current)) return undefined
  const rows = (current || []).filter(r => r.path)
  return rows.map(r => ({ path: r.path, mode: FOLDER_MODES.includes(r.mode) ? r.mode : 'read' }))
}

// Whether the rows were changed and one has no folder chosen.
export function foldersUnchosen(initial, current) {
  return foldersPayload(initial, current) !== undefined && (current || []).some(r => !r.path)
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
    if (folderState(a) !== 'ok') continue
    for (const f of folderRows(a)) {
      if (!f.path) continue
      const list = marks.get(f.path) || []
      list.push({ id: a.id, name: a.name, mode: (f.effective || f.mode) === 'write' ? 'write' : 'read' })
      marks.set(f.path, list)
    }
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
  const rows = folderRows(agent)
  const line = (f) => {
    const path = f.path === '/' ? t('agentFolder.home') : f.path
    return t((f.effective || f.mode) === 'write' ? 'agentFolder.toolsWrite' : 'agentFolder.toolsRead', { path })
  }
  if (rows.length === 1) return line(rows[0])
  return t('agentFolder.toolsMany', { n: rows.length, list: rows.map(f => `${f.path === '/' ? t('agentFolder.home') : f.path} (${t((f.effective || f.mode) === 'write' ? 'agentFolder.write' : 'agentFolder.read')})`).join(', ') })
}
