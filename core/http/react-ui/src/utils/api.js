import { API_CONFIG } from './config'
import { apiUrl } from './basePath'
import { chatsListPath } from './chatsQuery'

const enc = encodeURIComponent
const userQ = (userId) => userId ? `?user_id=${enc(userId)}` : ''

async function handleResponse(response) {
  if (!response.ok) {
    let errorMessage = `HTTP ${response.status}`
    let errorBody = null
    try {
      const data = await response.json()
      errorBody = data
      if (data?.error?.message) errorMessage = data.error.message
      else if (data?.error) errorMessage = data.error
    } catch (_e) {
      // response wasn't JSON
    }
    const err = new Error(errorMessage)
    // Preserve the parsed body + status so handlers can pattern-match on
    // structured responses (e.g. the import form's ambiguity picker).
    err.status = response.status
    err.body = errorBody
    throw err
  }
  const contentType = response.headers.get('content-type')
  if (contentType && contentType.includes('application/json')) {
    return response.json()
  }
  return response
}

function buildUrl(endpoint, params) {
  const url = new URL(apiUrl(endpoint), window.location.origin)
  if (params) {
    Object.entries(params).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') {
        url.searchParams.set(key, value)
      }
    })
  }
  return url.toString()
}

async function fetchJSON(endpoint, options = {}) {
  const response = await fetch(apiUrl(endpoint), {
    headers: { 'Content-Type': 'application/json', ...options.headers },
    ...options,
  })
  return handleResponse(response)
}

async function postJSON(endpoint, body, options = {}) {
  return fetchJSON(endpoint, {
    method: 'POST',
    body: JSON.stringify(body),
    ...options,
  })
}

// Models API
export const modelsApi = {
  list: (params) => fetchJSON(buildUrl(API_CONFIG.endpoints.models, params)),
  listV1: () => fetchJSON(API_CONFIG.endpoints.modelsList),
  listNativeCapabilities: () => fetchJSON('/v1/models/capabilities'),
  listCapabilities: () => fetchJSON(API_CONFIG.endpoints.modelsCapabilities),
  listAliases: () => fetchJSON(API_CONFIG.endpoints.modelsAliases),
  // variant is optional. Omitting it lets the server auto-select the best
  // build for this host, which is what the listing's auto_variant predicted.
  install: (id, variant) => postJSON(
    variant
      ? `${API_CONFIG.endpoints.installModel(id)}?variant=${encodeURIComponent(variant)}`
      : API_CONFIG.endpoints.installModel(id),
    {}
  ),
  delete: (id) => postJSON(API_CONFIG.endpoints.deleteModel(id), {}),
  estimate: (id, contexts) => fetchJSON(
    buildUrl(API_CONFIG.endpoints.modelEstimate(id),
      contexts?.length ? { contexts: contexts.join(',') } : {})
  ),
  // Companion to estimate: the listing reports only has_variants, so the
  // description is fetched per entry, on demand.
  variants: (id) => fetchJSON(API_CONFIG.endpoints.modelVariants(id)),
  getConfig: (id) => postJSON(API_CONFIG.endpoints.modelConfig(id), {}),
  getConfigJson: (name) => fetchJSON(API_CONFIG.endpoints.modelConfigJson(name)),
  getJob: (uid) => fetchJSON(API_CONFIG.endpoints.modelJob(uid)),
  apply: (body) => postJSON(API_CONFIG.endpoints.modelsApply, body),
  deleteByName: (name) => postJSON(API_CONFIG.endpoints.modelsDelete(name), {}),
  reload: () => postJSON(API_CONFIG.endpoints.modelsReload, {}),
  importUri: (body) => postJSON(API_CONFIG.endpoints.modelsImportUri, body),
  importConfig: async (content, contentType = 'application/x-yaml') => {
    const response = await fetch(apiUrl(API_CONFIG.endpoints.modelsImport), {
      method: 'POST',
      headers: { 'Content-Type': contentType },
      body: content,
    })
    return handleResponse(response)
  },
  getJobStatus: (uid) => fetchJSON(API_CONFIG.endpoints.modelsJobStatus(uid)),
  getStorage: () => fetchJSON(API_CONFIG.endpoints.modelsStorage),
  getEditConfig: (name) => fetchJSON(API_CONFIG.endpoints.modelEditGet(name)),
  editConfig: (name, body) => postJSON(API_CONFIG.endpoints.modelEdit(name), body),
  toggleState: (name, action) => fetchJSON(API_CONFIG.endpoints.modelToggleState(name, action), { method: 'PUT' }),
  togglePinned: (name, action) => fetchJSON(API_CONFIG.endpoints.modelTogglePinned(name, action), { method: 'PUT' }),
  getConfigMetadata: (section) => fetchJSON(
    section ? `${API_CONFIG.endpoints.configMetadata}?section=${section}`
            : API_CONFIG.endpoints.configMetadata
  ),
  getAutocomplete: (provider) => fetchJSON(API_CONFIG.endpoints.configAutocomplete(provider)),
  estimateVram: (body, options) => postJSON(API_CONFIG.endpoints.vramEstimate, body, options),
  patchConfig: (name, patch) => fetchJSON(API_CONFIG.endpoints.configPatch(name), {
    method: 'PATCH',
    body: JSON.stringify(patch),
  }),
  backendUsecases: () => fetchJSON('/api/backends/usecases'),
}

// Backends API
export const backendsApi = {
  list: (params) => fetchJSON(buildUrl(API_CONFIG.endpoints.backends, params)),
  listInstalled: () => fetchJSON(API_CONFIG.endpoints.backendsInstalled),
  listKnown: () => fetchJSON(API_CONFIG.endpoints.backendsKnown),
  install: (id) => postJSON(API_CONFIG.endpoints.installBackend(id), {}),
  delete: (id) => postJSON(API_CONFIG.endpoints.deleteBackend(id), {}),
  installExternal: (body) => postJSON(API_CONFIG.endpoints.installExternalBackend, body),
  getJob: (uid) => fetchJSON(API_CONFIG.endpoints.backendJob(uid)),
  deleteInstalled: (name) => postJSON(API_CONFIG.endpoints.deleteInstalledBackend(name), {}),
  checkUpgrades: () => fetchJSON(API_CONFIG.endpoints.backendsUpgrades),
  forceCheckUpgrades: () => postJSON(API_CONFIG.endpoints.backendsUpgradesCheck, {}),
  upgrade: (name) => postJSON(API_CONFIG.endpoints.upgradeBackend(name), {}),
}

// Chats API: conversations the server keeps. A message sent while a turn runs
// waits and reaches the model, with any others, at its next request.
export const chatsApi = {
  list: (limit, kind) => fetchJSON(chatsListPath(API_CONFIG.endpoints.chats, limit, kind)),
  // With an agent, its model (when it has one) replaces model. With assistant,
  // the chat gets the LocalAI management tools (admin only, setting on).
  create: (model, title, agentId, assistant) => postJSON(API_CONFIG.endpoints.chats, {
    ...(model ? { model } : {}), ...(title ? { title } : {}), ...(agentId ? { agent_id: agentId } : {}),
    ...(assistant ? { assistant: true } : {}),
  }),
  get: (id) => fetchJSON(API_CONFIG.endpoints.chat(id)),
  delete: (id) => fetchJSON(API_CONFIG.endpoints.chat(id), { method: 'DELETE' }),
  rename: (id, title) => chatsApi.update(id, { title }),
  // Changes the title, the model or the settings (system prompt, sampling,
  // reasoning effort, context size). A title or a model answers 409 while a
  // turn runs; settings apply from the next turn.
  update: (id, fields) => fetchJSON(API_CONFIG.endpoints.chat(id), { method: 'PATCH', body: JSON.stringify(fields) }),
  // Regenerate and edit start a new turn; both answer 409 while one runs.
  regenerate: (id) => postJSON(`${API_CONFIG.endpoints.chat(id)}/regenerate`, {}),
  editMessage: (id, seq, text) => postJSON(`${API_CONFIG.endpoints.chat(id)}/messages/${Number(seq)}/edit`, { text }),
  fork: (id, fromSeq, title) => postJSON(`${API_CONFIG.endpoints.chat(id)}/fork`, {
    ...(fromSeq ? { from_seq: Number(fromSeq) } : {}), ...(title ? { title } : {}),
  }),
  search: (text, limit) => fetchJSON(`${API_CONFIG.endpoints.chats}/search${q({ q: text, limit })}`),
  exportUrl: (id, format) => apiUrl(`${API_CONFIG.endpoints.chat(id)}/export${q({ format })}`),
  // chat is the JSON export (GET .../export?format=json); model replaces its model.
  importChat: (chat, model) => postJSON(`${API_CONFIG.endpoints.chats}/import`, { chat, ...(model ? { model } : {}) }),
  // The raw localStorage value of the old chat page, sent as it is.
  importBrowser: (raw) => fetchJSON(`${API_CONFIG.endpoints.chats}/import/browser`, { method: 'POST', body: raw }),
  subAgents: (id) => fetchJSON(`${API_CONFIG.endpoints.chat(id)}/agents`),
  subAgentMessages: (id, agentId) => fetchJSON(`${API_CONFIG.endpoints.chat(id)}/agents/${encodeURIComponent(agentId)}/messages`),
  stopSubAgent: (id, agentId) => postJSON(`${API_CONFIG.endpoints.chat(id)}/agents/${encodeURIComponent(agentId)}/stop`, {}),
  // The server returns at most 1000 entries a call, oldest first, so a long
  // chat is read page by page: the newest messages are on the last page.
  messages: async (id, after = 0) => {
    let all = []
    for (;;) {
      const page = await fetchJSON(`${API_CONFIG.endpoints.chatMessages(id)}?after=${after}&limit=1000`)
      const got = page.messages || []
      all = all.concat(got)
      if (got.length < 1000) return { ...page, messages: all }
      after = got[got.length - 1].seq
    }
  },
  // One line of the transcript, by its seq: the card of a live tool call. The same
  // route and checks as the whole transcript, so it is the chat owner's only.
  card: async (id, seq) => (await fetchJSON(`${API_CONFIG.endpoints.chatMessages(id)}?after=${Number(seq) - 1}&limit=1`)).messages || [],
  // With files the message goes as a form: the server saves the files in the
  // chat's folder of the user's workspace and the model gets their paths.
  send: async (id, text, files = []) => {
    if (!files.length) return postJSON(API_CONFIG.endpoints.chatMessages(id), { text })
    const form = new FormData()
    form.append('text', text)
    files.forEach(f => form.append('files', f, f.name))
    const res = await fetch(apiUrl(API_CONFIG.endpoints.chatMessages(id)), { method: 'POST', body: form })
    return handleResponse(res)
  },
  editPending: (id, pid, text) => fetchJSON(API_CONFIG.endpoints.chatPending(id, pid), { method: 'PATCH', body: JSON.stringify({ text }) }),
  cancelPending: (id, pid) => fetchJSON(API_CONFIG.endpoints.chatPending(id, pid), { method: 'DELETE' }),
  sendNow: (id) => postJSON(API_CONFIG.endpoints.chatSendNow(id), {}),
  stop: (id) => postJSON(API_CONFIG.endpoints.chatStop(id), {}),
  eventsUrl: (id) => apiUrl(API_CONFIG.endpoints.chatEvents(id)),
}

// MCP Apps of a chat: the page that a tool's MCP server offers, and the tool
// calls of that page. The server holds the MCP connection and checks that the
// chat may use the server on every call.
export const chatAppsApi = {
  resource: (chat, server, uri) => fetchJSON(`/api/chats/${encodeURIComponent(chat)}/mcp-apps/resource?server=${encodeURIComponent(server)}&uri=${encodeURIComponent(uri)}`),
  call: (chat, server, tool, args) => postJSON(`/api/chats/${encodeURIComponent(chat)}/mcp-apps/call`, { server, tool, arguments: args || {} }),
}

// Files API: the user's own workspace. Paths are inside the user's home, "/".
const q = (params) => {
  const sp = new URLSearchParams()
  Object.entries(params).forEach(([k, v]) => { if (v !== undefined && v !== null && v !== '') sp.set(k, String(v)) })
  const s = sp.toString()
  return s ? `?${s}` : ''
}
export const filesApi = {
  list: (path = '/') => fetchJSON(API_CONFIG.endpoints.files + q({ path })),
  // The address that downloads a file, or with inline shows a safe type.
  contentUrl: (path, inline = false) => apiUrl(API_CONFIG.endpoints.filesContent) + q({ path, inline: inline ? 1 : undefined }),
  readText: async (path, limit = 64 * 1024) => {
    const res = await fetch(filesApi.contentUrl(path), { headers: { Range: `bytes=0-${limit - 1}` } })
    if (!res.ok) { await handleResponse(res) }
    return res.text()
  },
  // Sends the file as the request body. Without base, a file that exists is
  // kept and the new one gets a conflict name (see conflict_of in the answer).
  upload: async (path, file, { base } = {}) => {
    const res = await fetch(apiUrl(API_CONFIG.endpoints.filesContent) + q({ path, base }), {
      method: 'PUT', headers: { 'Content-Type': 'application/octet-stream' }, body: file,
    })
    return handleResponse(res)
  },
  mkdir: (path) => postJSON(API_CONFIG.endpoints.filesFolders, { path }),
  move: (from, to) => postJSON(API_CONFIG.endpoints.filesMove, { from, to }),
  remove: (path) => fetchJSON(API_CONFIG.endpoints.files + q({ path }), { method: 'DELETE' }),
  trash: () => fetchJSON(API_CONFIG.endpoints.filesTrash),
  undo: (rev, skipConflicts = false) => postJSON(API_CONFIG.endpoints.filesUndo, { rev, skip_conflicts: skipConflicts }),
  history: (path = '/') => fetchJSON(API_CONFIG.endpoints.filesHistory + q({ path })),
  search: (query, path = '/') => fetchJSON(API_CONFIG.endpoints.filesSearch + q({ q: query, path })),
}

// Chat skills: folders with a SKILL.md in four layers, switched per chat.
const enc2 = encodeURIComponent
export const chatSkillsApi = {
  list: (chat) => fetchJSON(`/api/chats/skills${chat ? `?chat=${enc2(chat)}` : ''}`),
  get: (layer, name) => fetchJSON(`/api/chats/skills/${enc2(layer)}/${enc2(name)}`),
  create: (body) => postJSON('/api/chats/skills', body),
  update: (layer, name, body) => fetchJSON(`/api/chats/skills/${enc2(layer)}/${enc2(name)}`, { method: 'PUT', body: JSON.stringify(body) }),
  remove: (layer, name) => fetchJSON(`/api/chats/skills/${enc2(layer)}/${enc2(name)}`, { method: 'DELETE' }),
  setOff: (chat, off) => fetchJSON(`/api/chats/${enc2(chat)}/skills`, { method: 'PUT', body: JSON.stringify({ off }) }),
}

// Chat add-ons: the add-ons whose tools a chat may use, switched per chat.
export const chatAddonsApi = {
  list: (chat) => fetchJSON(`/api/chats/${enc2(chat)}/addons`),
  setOff: (chat, off) => fetchJSON(`/api/chats/${enc2(chat)}/addons`, { method: 'PUT', body: JSON.stringify({ off }) }),
}

// The MCP servers that a model's own configuration names (it connects to them to
// list their tools, through the sessions the chat reuses).
export const modelMCPServersApi = {
  list: (model) => fetchJSON(`/v1/mcp/servers/${enc2(model)}`),
}

// MCP connectors: the MCP servers a user defines for their chats. Tokens,
// headers and environments only go in; the server never returns them.
export const chatMCPConnectorsApi = {
  list: () => fetchJSON('/api/chats/mcp-connectors'),
  create: (body) => postJSON('/api/chats/mcp-connectors', body),
  update: (id, body) => fetchJSON(`/api/chats/mcp-connectors/${enc2(id)}`, { method: 'PUT', body: JSON.stringify(body) }),
  remove: (id) => fetchJSON(`/api/chats/mcp-connectors/${enc2(id)}`, { method: 'DELETE' }),
  enable: (id) => postJSON(`/api/chats/mcp-connectors/${enc2(id)}/enable`, {}),
  disable: (id) => postJSON(`/api/chats/mcp-connectors/${enc2(id)}/disable`, {}),
  test: (id) => postJSON(`/api/chats/mcp-connectors/${enc2(id)}/test`, {}),
}

// The MCP servers and connectors of a chat, with their switches. The add-ons
// have their own calls above.
export const chatToolsApi = {
  list: (chat) => fetchJSON(`/api/chats/${enc2(chat)}/tools`),
  setMcpOff: (chat, off) => fetchJSON(`/api/chats/${enc2(chat)}/mcp`, { method: 'PUT', body: JSON.stringify({ off }) }),
}

// Saved agents: instructions and exact lists of what a chat may use.
export const chatAgentsApi = {
  list: () => fetchJSON('/api/chats/agents'),
  get: (id) => fetchJSON(`/api/chats/agents/${enc2(id)}`),
  create: (body) => postJSON('/api/chats/agents', body),
  update: (id, body) => fetchJSON(`/api/chats/agents/${enc2(id)}`, { method: 'PUT', body: JSON.stringify(body) }),
  remove: (id) => fetchJSON(`/api/chats/agents/${enc2(id)}`, { method: 'DELETE' }),
  // The agent file of the server (kind localai-agent). Import takes it, the
  // body of a create, or an export of the old agents; it always makes a new
  // agent and says in `warnings` what it changed.
  exportFile: (id) => fetchJSON(`/api/chats/agents/${enc2(id)}/export`),
  importFile: (data) => postJSON('/api/chats/agents/import', data),
  templates: () => fetchJSON('/api/chats/agents/templates'),
  fromTemplate: (id, name) => postJSON(`/api/chats/agents/templates/${enc2(id)}/create`, name ? { name } : {}),
  runs: (id, limit) => fetchJSON(`/api/chats/agents/${enc2(id)}/runs${q({ limit })}`),
}

// The administrator's view of every user's agents, triggers and connectors.
export const chatAdminApi = {
  agents: () => fetchJSON('/api/chats/admin/agents'),
  deleteAgent: (id) => fetchJSON(`/api/chats/admin/agents/${enc2(id)}`, { method: 'DELETE' }),
  triggers: () => fetchJSON('/api/chats/admin/triggers'),
  triggerRuns: (id) => fetchJSON(`/api/chats/admin/triggers/${enc2(id)}/runs`),
  disableTrigger: (id) => postJSON(`/api/chats/admin/triggers/${enc2(id)}/disable`, {}),
  deleteTrigger: (id) => fetchJSON(`/api/chats/admin/triggers/${enc2(id)}`, { method: 'DELETE' }),
  connectors: () => fetchJSON('/api/chats/admin/connectors'),
  disableConnector: (id) => postJSON(`/api/chats/admin/connectors/${enc2(id)}/disable`, {}),
  deleteConnector: (id) => fetchJSON(`/api/chats/admin/connectors/${enc2(id)}`, { method: 'DELETE' }),
}

// Agent tasks and their jobs (the mcp_jobs feature): a prompt template run in
// the background by a model or a saved agent, from the page or over the API.
export const agentJobsApi = {
  tasks: (allUsers) => fetchJSON(`/api/agent/tasks${q({ all_users: allUsers ? 'true' : undefined })}`),
  task: (id) => fetchJSON(`/api/agent/tasks/${enc2(id)}`),
  createTask: (task) => postJSON('/api/agent/tasks', task),
  updateTask: (id, task) => fetchJSON(`/api/agent/tasks/${enc2(id)}`, { method: 'PUT', body: JSON.stringify(task) }),
  deleteTask: (id) => fetchJSON(`/api/agent/tasks/${enc2(id)}`, { method: 'DELETE' }),
  run: (taskId, parameters) => postJSON('/api/agent/jobs/execute', { task_id: taskId, parameters: parameters || {} }),
  jobs: ({ taskId, status, limit, allUsers } = {}) => fetchJSON(`/api/agent/jobs${q({ task_id: taskId, status, limit, all_users: allUsers ? 'true' : undefined })}`),
  job: (id) => fetchJSON(`/api/agent/jobs/${enc2(id)}`),
  cancel: (id) => postJSON(`/api/agent/jobs/${enc2(id)}/cancel`, {}),
  rerun: (id) => postJSON(`/api/agent/jobs/${enc2(id)}/rerun`, {}),
  deleteJob: (id) => fetchJSON(`/api/agent/jobs/${enc2(id)}`, { method: 'DELETE' }),
}

// The messaging connectors. The add-on of a platform adds and edits bindings
// through the same routes; this page only lists them and switches them on or off.
export const chatConnectorsApi = {
  list: () => fetchJSON('/api/chats/connectors'),
  kinds: () => fetchJSON('/api/chats/connectors/kinds'),
  enable: (id) => postJSON(`/api/chats/connectors/${enc2(id)}/enable`, {}),
  disable: (id) => postJSON(`/api/chats/connectors/${enc2(id)}/disable`, {}),
}

// Triggers: work that starts by itself, as the user, in a new chat.
export const chatTriggersApi = {
  list: () => fetchJSON('/api/chats/triggers'),
  create: (body) => postJSON('/api/chats/triggers', body),
  update: (id, body) => fetchJSON(`/api/chats/triggers/${enc2(id)}`, { method: 'PUT', body: JSON.stringify(body) }),
  remove: (id) => fetchJSON(`/api/chats/triggers/${enc2(id)}`, { method: 'DELETE' }),
  enable: (id) => postJSON(`/api/chats/triggers/${enc2(id)}/enable`, {}),
  disable: (id) => postJSON(`/api/chats/triggers/${enc2(id)}/disable`, {}),
  run: (id) => postJSON(`/api/chats/triggers/${enc2(id)}/run`, {}),
  runs: (id) => fetchJSON(`/api/chats/triggers/${enc2(id)}/runs`),
  preview: (when, tz) => fetchJSON(`/api/chats/triggers/preview${q({ when, tz, n: 3 })}`),
  proposals: (chat) => fetchJSON(`/api/chats/triggers/proposals${q({ chat })}`),
  confirm: (id, timeZone) => postJSON(`/api/chats/triggers/proposals/${enc2(id)}/confirm`, timeZone ? { time_zone: timeZone } : {}),
  dismiss: (id) => fetchJSON(`/api/chats/triggers/proposals/${enc2(id)}`, { method: 'DELETE' }),
  editProposal: (id, body) => fetchJSON(`/api/chats/triggers/proposals/${enc2(id)}`, { method: 'PUT', body: JSON.stringify(body) }),
}

// The inbox: what ended while the user was elsewhere.
export const chatNotificationsApi = {
  list: (unread) => fetchJSON(`/api/chats/notifications${q({ unread: unread ? 1 : undefined, limit: 50 })}`),
  read: (body) => postJSON('/api/chats/notifications/read', body),
  remove: (ids) => fetchJSON('/api/chats/notifications', { method: 'DELETE', body: JSON.stringify({ ids }) }),
  eventsUrl: () => apiUrl('/api/chats/notifications/events'),
}

// What the conversion of the old chat, agents and skills did, for the viewer.
export const chatConversionApi = {
  get: () => fetchJSON('/api/chats/conversion'),
}

// Browser push for the inbox.
export const chatPushApi = {
  key: () => fetchJSON('/api/chats/push/key'),
  subscribe: (subscription) => postJSON('/api/chats/push/subscribe', subscription),
  unsubscribe: (endpoint) => fetchJSON('/api/chats/push/subscribe', { method: 'DELETE', body: JSON.stringify({ endpoint }) }),
}

// Resources API
export const resourcesApi = {
  get: () => fetchJSON(API_CONFIG.endpoints.resources),
}

// Operations API
export const operationsApi = {
  list: () => fetchJSON(API_CONFIG.endpoints.operations),
  cancel: (jobID) => postJSON(API_CONFIG.endpoints.cancelOperation(jobID), {}),
  pause: (jobID) => postJSON(API_CONFIG.endpoints.pauseOperation(jobID), {}),
  dismiss: (jobID) => postJSON(API_CONFIG.endpoints.dismissOperation(jobID), {}),
  history: () => fetchJSON(API_CONFIG.endpoints.operationsHistory),
  clearHistory: () => fetchJSON(API_CONFIG.endpoints.operationsHistory, { method: 'DELETE' }),
}

// Settings API
export const settingsApi = {
  get: () => fetchJSON(API_CONFIG.endpoints.settings),
  save: (body) => postJSON(API_CONFIG.endpoints.settings, body),
}

// Branding / whitelabeling
// /api/branding is public (no auth) — the login page reads it before the
// user signs in. Asset uploads/deletes still require admin privileges.
export const brandingApi = {
  get: () => fetchJSON('/api/branding'),
  uploadAsset: (kind, file) => {
    const fd = new FormData()
    fd.append('file', file)
    return fetch(apiUrl(`/api/branding/asset/${enc(kind)}`), {
      method: 'POST',
      body: fd,
      credentials: 'include',
    }).then(handleResponse)
  },
  deleteAsset: (kind) => fetch(apiUrl(`/api/branding/asset/${enc(kind)}`), {
    method: 'DELETE',
    credentials: 'include',
  }).then(handleResponse),
}

// Backend Logs API
export const backendLogsApi = {
  listModels: () => fetchJSON(API_CONFIG.endpoints.backendLogs),
  getLines: (modelId) => fetchJSON(API_CONFIG.endpoints.backendLogsModel(modelId)),
  clear: (modelId) => postJSON(API_CONFIG.endpoints.clearBackendLogs(modelId), {}),
}

// Traces API
//
// The list endpoints return a bounded page with the heavy request/response
// bodies stripped; the total buffered count arrives in X-Total-Count and the
// full record is fetched per trace when a row is expanded. Polling the
// unbounded form used to move tens of megabytes every few seconds.
export const DEFAULT_TRACE_PAGE_SIZE = 50

async function fetchTracePage(endpoint, { limit = DEFAULT_TRACE_PAGE_SIZE, offset = 0, full = false } = {}) {
  const response = await fetch(buildUrl(endpoint, { limit, offset, full: full ? 'true' : undefined }), {
    headers: { 'Content-Type': 'application/json' },
  })
  const items = await handleResponse(response)
  const list = Array.isArray(items) ? items : []
  const total = parseInt(response.headers.get('X-Total-Count') || '', 10)
  return { items: list, total: Number.isNaN(total) ? list.length : total }
}

export const tracesApi = {
  get: (opts) => fetchTracePage(API_CONFIG.endpoints.traces, opts),
  // Counted totals, so a dashboard does not fetch the whole list to size it.
  // `hours` picks the window the server counts over (1 to 168).
  summary: (hours) => fetchJSON(hours ? `${API_CONFIG.endpoints.tracesSummary}?hours=${hours}` : API_CONFIG.endpoints.tracesSummary),
  getOne: (id) => fetchJSON(API_CONFIG.endpoints.trace(id)),
  clear: () => postJSON(API_CONFIG.endpoints.clearTraces, {}),
  getBackend: (opts) => fetchTracePage(API_CONFIG.endpoints.backendTraces, opts),
  getBackendOne: (id) => fetchJSON(API_CONFIG.endpoints.backendTrace(id)),
  clearBackend: () => postJSON(API_CONFIG.endpoints.clearBackendTraces, {}),
}

// Prometheus exposition text from /metrics (admin only). Returns the status and
// the text, so a page can tell a route that is off (404) from one that refused
// the caller (401, 403) and from a network failure (status 0).
export const metricsApi = {
  scrape: async () => {
    try {
      const response = await fetch(apiUrl('/metrics'))
      return { status: response.status, text: response.ok ? await response.text() : '' }
    } catch {
      return { status: 0, text: '' }
    }
  },
}

// P2P API
export const p2pApi = {
  getWorkers: () => fetchJSON(API_CONFIG.endpoints.p2pWorkers),
  getFederation: () => fetchJSON(API_CONFIG.endpoints.p2pFederation),
  getStats: () => fetchJSON(API_CONFIG.endpoints.p2pStats),
  getToken: async () => {
    const response = await fetch(apiUrl(API_CONFIG.endpoints.p2pToken))
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    return response.text()
  },
}

// Image generation
export const imageApi = {
  upscale: async ({ model, image, scale }) => {
    const form = new FormData()
    form.append('model', model)
    form.append('image', image)
    form.append('scale', String(scale))
    return handleResponse(await fetch(apiUrl('/v1/images/upscale'), { method: 'POST', body: form }))
  },
  generate: (body) => postJSON(API_CONFIG.endpoints.imageGenerations, body),
}

// Video generation
export const videoApi = {
  generate: (body) => postJSON(API_CONFIG.endpoints.video, body),
}

export const threeDApi = {
  animate: (body) => postJSON(API_CONFIG.endpoints.threeDAnimate, body),
  generate: (body) => postJSON(API_CONFIG.endpoints.threeDGenerations, body),
  remesh: async (mesh, model, detail) => {
    const form = new FormData()
    form.append('model', model)
    form.append('detail', String(detail))
    form.append('mesh', mesh, 'source.glb')
    const response = await fetch(apiUrl(API_CONFIG.endpoints.threeDRemesh), {
      method: 'POST',
      body: form,
    })
    await handleResponse(response)
    return response.blob()
  },
}

// parseAudioBlobResponse — shared response handling for audio-blob endpoints.
// Throws on non-2xx (with the API error message when present); returns the
// blob plus the parsed Content-Disposition filename mapped to the server's
// /generated-audio/ path so the UI can persist it in history. The audio
// transform endpoint also surfaces the persisted *input* paths via
// X-Audio-Input-Url / X-Audio-Reference-Url headers so the UI can replay
// past (input, reference, output) triples from history.
async function parseAudioBlobResponse(response) {
  if (!response.ok) {
    const data = await response.json().catch(() => ({}))
    throw new Error(data?.error?.message || `HTTP ${response.status}`)
  }
  let serverUrl = null
  const disposition = response.headers.get('content-disposition')
  if (disposition) {
    const match = disposition.match(/filename[^;=\n]*=["']?([^"';\n]*)["']?/)
    if (match && match[1]) serverUrl = '/generated-audio/' + match[1]
  }
  const inputUrl = response.headers.get('x-audio-input-url') || null
  const referenceUrl = response.headers.get('x-audio-reference-url') || null
  const blob = await response.blob()
  return { blob, serverUrl, inputUrl, referenceUrl }
}

async function postAudioBlob(endpoint, body) {
  const response = await fetch(apiUrl(endpoint), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  return parseAudioBlobResponse(response)
}

async function postMultipartAudioBlob(endpoint, formData) {
  const response = await fetch(apiUrl(endpoint), { method: 'POST', body: formData })
  return parseAudioBlobResponse(response)
}

// TTS
export const ttsApi = {
  generate: (body) => postAudioBlob(API_CONFIG.endpoints.tts, body),
  generateV1: (body) => postAudioBlob(API_CONFIG.endpoints.audioSpeech, body),
}

// Reusable voice-cloning profiles. The browser uploads multipart PCM-WAV;
// list/preview are available to TTS-authorized users while mutations are
// enforced as admin-only by the server.
export const voiceProfilesApi = {
  list: () => fetchJSON('/api/voice-profiles'),
  create: (formData) => fetch(apiUrl('/api/voice-profiles'), {
    method: 'POST',
    body: formData,
  }).then(handleResponse),
  delete: (id) => fetch(apiUrl(`/api/voice-profiles/${enc(id)}`), {
    method: 'DELETE',
  }).then(handleResponse),
  audioUrl: (id) => apiUrl(`/api/voice-profiles/${enc(id)}/audio`),
}

// Sound generation
export const soundApi = {
  generate: (body) => postAudioBlob(API_CONFIG.endpoints.soundGeneration, body),
}

// Audio transform (echo cancellation, noise suppression, voice conversion, etc.)
export const audioTransformApi = {
  process: ({ model, audioFile, referenceFile, format, sampleRate, params }) => {
    const fd = new FormData()
    fd.append('model', model)
    fd.append('audio', audioFile, audioFile?.name || 'audio.wav')
    if (referenceFile) fd.append('reference', referenceFile, referenceFile.name || 'reference.wav')
    if (format) fd.append('response_format', format)
    if (sampleRate) fd.append('sample_rate', String(sampleRate))
    if (params) {
      for (const [k, v] of Object.entries(params)) {
        if (v == null || v === '') continue
        fd.append(`params[${k}]`, String(v))
      }
    }
    return postMultipartAudioBlob(API_CONFIG.endpoints.audioTransformations, fd)
  },
  streamUrl: () => apiUrl(API_CONFIG.endpoints.audioTransformStream).replace(/^http/, 'ws'),
}

// Audio transcription
export const audioApi = {
  transcribe: async (formData) => {
    const response = await fetch(apiUrl(API_CONFIG.endpoints.audioTranscriptions), {
      method: 'POST',
      body: formData,
    })
    return handleResponse(response)
  },
}

// Face biometrics — backend spec: core/http/endpoints/localai/face_*.go
export const faceApi = {
  verify: (body) => postJSON(API_CONFIG.endpoints.faceVerify, body),
  analyze: (body) => postJSON(API_CONFIG.endpoints.faceAnalyze, body),
  embed: (body) => postJSON(API_CONFIG.endpoints.faceEmbed, body),
  register: (body) => postJSON(API_CONFIG.endpoints.faceRegister, body),
  identify: (body) => postJSON(API_CONFIG.endpoints.faceIdentify, body),
  forget: (body) => postJSON(API_CONFIG.endpoints.faceForget, body),
}

// Voice biometrics — backend spec: core/http/endpoints/localai/voice_*.go
export const voiceApi = {
  verify: (body) => postJSON(API_CONFIG.endpoints.voiceVerify, body),
  analyze: (body) => postJSON(API_CONFIG.endpoints.voiceAnalyze, body),
  embed: (body) => postJSON(API_CONFIG.endpoints.voiceEmbed, body),
  register: (body) => postJSON(API_CONFIG.endpoints.voiceRegister, body),
  identify: (body) => postJSON(API_CONFIG.endpoints.voiceIdentify, body),
  forget: (body) => postJSON(API_CONFIG.endpoints.voiceForget, body),
}

// Realtime / WebRTC
export const realtimeApi = {
  call: (body) => postJSON(API_CONFIG.endpoints.realtimeCalls, body),
  pipelineModels: () => fetchJSON(API_CONFIG.endpoints.pipelineModels),
}

// Backend control
export const backendControlApi = {
  shutdown: (body) => postJSON(API_CONFIG.endpoints.backendShutdown, body),
  // Pre-load a model (or all of a realtime pipeline's sub-models) into memory.
  // body: { model: "<name>" }. Inverse of shutdown.
  load: (body) => postJSON(API_CONFIG.endpoints.backendLoad, body),
}

// System info
export const systemApi = {
  version: () => fetchJSON(API_CONFIG.endpoints.version),
  info: () => fetchJSON(API_CONFIG.endpoints.system),
}

export const agentCollectionsApi = {
  list: (allUsers) => fetchJSON(`/api/collections${allUsers ? '?all_users=true' : ''}`),
  create: (name) => postJSON('/api/collections', { name }),
  upload: (name, formData, userId) => fetch(apiUrl(`/api/collections/${enc(name)}/upload${userQ(userId)}`), { method: 'POST', body: formData }).then(handleResponse),
  entries: (name, userId) => fetchJSON(`/api/collections/${enc(name)}/entries${userQ(userId)}`),
  entryContent: (name, entry, userId) => fetchJSON(`/api/collections/${enc(name)}/entries/${encodeURIComponent(entry)}${userQ(userId)}`),
  search: (name, query, maxResults, userId) => postJSON(`/api/collections/${enc(name)}/search${userQ(userId)}`, { query, max_results: maxResults }),
  reset: (name, userId) => postJSON(`/api/collections/${enc(name)}/reset${userQ(userId)}`),
  deleteEntry: (name, entry, userId) => fetchJSON(`/api/collections/${enc(name)}/entry/delete${userQ(userId)}`, { method: 'DELETE', body: JSON.stringify({ entry }), headers: { 'Content-Type': 'application/json' } }),
  sources: (name, userId) => fetchJSON(`/api/collections/${enc(name)}/sources${userQ(userId)}`),
  addSource: (name, url, interval, userId) => postJSON(`/api/collections/${enc(name)}/sources${userQ(userId)}`, {
    url,
    update_interval: interval === undefined ? undefined : Number(interval),
  }),
  removeSource: (name, url, userId) => fetchJSON(`/api/collections/${enc(name)}/sources${userQ(userId)}`, { method: 'DELETE', body: JSON.stringify({ url }), headers: { 'Content-Type': 'application/json' } }),
}

// Usage API
export const usageApi = {
  getMyUsage: (period) => fetchJSON(`/api/auth/usage?period=${period || 'month'}`),
  getAdminUsage: (period, userId) => {
    let url = `/api/auth/admin/usage?period=${period || 'month'}`
    if (userId) url += `&user_id=${encodeURIComponent(userId)}`
    return fetchJSON(url)
  },
  getMySources: (period) =>
    fetchJSON(`/api/auth/usage/sources?period=${period || 'month'}`),
  getAdminSources: (period, userId, apiKeyId) => {
    let url = `/api/auth/admin/usage/sources?period=${period || 'month'}`
    if (userId) url += `&user_id=${encodeURIComponent(userId)}`
    if (apiKeyId) url += `&api_key_id=${encodeURIComponent(apiKeyId)}`
    return fetchJSON(url)
  },
  getMyQuotas: () => fetchJSON('/api/auth/quota'),
}

// Admin Users API
export const adminUsersApi = {
  list: () => fetchJSON('/api/auth/admin/users'),
  setRole: (id, role) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}/role`, {
    method: 'PUT', body: JSON.stringify({ role }), headers: { 'Content-Type': 'application/json' },
  }),
  delete: (id) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  setStatus: (id, status) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}/status`, {
    method: 'PUT', body: JSON.stringify({ status }), headers: { 'Content-Type': 'application/json' },
  }),
  getPermissions: (id) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}/permissions`),
  setPermissions: (id, perms) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}/permissions`, {
    method: 'PUT', body: JSON.stringify(perms), headers: { 'Content-Type': 'application/json' },
  }),
  getFeatures: () => fetchJSON('/api/auth/admin/features'),
  setModels: (id, allowlist) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}/models`, {
    method: 'PUT', body: JSON.stringify(allowlist), headers: { 'Content-Type': 'application/json' },
  }),
  getQuotas: (id) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}/quotas`),
  setQuota: (id, quota) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}/quotas`, {
    method: 'PUT', body: JSON.stringify(quota), headers: { 'Content-Type': 'application/json' },
  }),
  deleteQuota: (id, quotaId) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}/quotas/${encodeURIComponent(quotaId)}`, {
    method: 'DELETE',
  }),
  resetPassword: (id, password, acknowledgeWeak = false) => fetchJSON(`/api/auth/admin/users/${encodeURIComponent(id)}/password`, {
    method: 'PUT',
    body: JSON.stringify({ password, acknowledge_weak_password: acknowledgeWeak }),
    headers: { 'Content-Type': 'application/json' },
  }),
}

// Profile API
export const profileApi = {
  get: () => fetchJSON('/api/auth/me'),
  updateName: (name) => fetchJSON('/api/auth/profile', {
    method: 'PUT', body: JSON.stringify({ name }), headers: { 'Content-Type': 'application/json' },
  }),
  updateProfile: (name, avatarUrl) => fetchJSON('/api/auth/profile', {
    method: 'PUT', body: JSON.stringify({ name, avatar_url: avatarUrl || '' }), headers: { 'Content-Type': 'application/json' },
  }),
  changePassword: (currentPassword, newPassword, acknowledgeWeak = false) => fetchJSON('/api/auth/password', {
    method: 'PUT',
    body: JSON.stringify({ current_password: currentPassword, new_password: newPassword, acknowledge_weak_password: acknowledgeWeak }),
    headers: { 'Content-Type': 'application/json' },
  }),
}

// Admin Invites API
export const adminInvitesApi = {
  list: () => fetchJSON('/api/auth/admin/invites'),
  create: (expiresInHours = 168) => postJSON('/api/auth/admin/invites', { expiresInHours }),
  delete: (id) => fetchJSON(`/api/auth/admin/invites/${encodeURIComponent(id)}`, { method: 'DELETE' }),
}

// API Keys
export const apiKeysApi = {
  list: () => fetchJSON('/api/auth/api-keys'),
  // expiresIn is "30d", "90d" or "1y"; without it the server applies its own default.
  create: (name, expiresIn) => postJSON('/api/auth/api-keys', expiresIn ? { name, expiresIn } : { name }),
  revoke: (id) => fetchJSON(`/api/auth/api-keys/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  // pausedUntil is an RFC3339 string or null; disabled pauses until resumed.
  setPause: (id, disabled, pausedUntil = null) => fetchJSON(`/api/auth/api-keys/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ disabled, paused_until: pausedUntil }),
  }),
}

// Fine-tuning API
export const fineTuneApi = {
  listBackends: () => fetchJSON('/api/fine-tuning/backends'),
  startJob: (data) => postJSON('/api/fine-tuning/jobs', data),
  listJobs: () => fetchJSON('/api/fine-tuning/jobs'),
  getJob: (id) => fetchJSON(`/api/fine-tuning/jobs/${enc(id)}`),
  stopJob: (id, saveCheckpoint) => fetchJSON(`/api/fine-tuning/jobs/${enc(id)}/stop?save_checkpoint=${saveCheckpoint ? 'true' : 'false'}`, { method: 'POST' }),
  deleteJob: (id) => fetchJSON(`/api/fine-tuning/jobs/${enc(id)}`, { method: 'DELETE' }),
  listCheckpoints: (id) => fetchJSON(`/api/fine-tuning/jobs/${enc(id)}/checkpoints`),
  exportModel: (id, data) => postJSON(`/api/fine-tuning/jobs/${enc(id)}/export`, data),
  uploadDataset: (file) => {
    const formData = new FormData()
    formData.append('file', file)
    return fetch(apiUrl('/api/fine-tuning/datasets'), { method: 'POST', body: formData }).then(handleResponse)
  },
  progressUrl: (id) => apiUrl(`/api/fine-tuning/jobs/${enc(id)}/progress`),
  downloadUrl: (id) => apiUrl(`/api/fine-tuning/jobs/${enc(id)}/download`),
}

// Quantization API
export const quantizationApi = {
  listBackends: () => fetchJSON('/api/quantization/backends'),
  startJob: (data) => postJSON('/api/quantization/jobs', data),
  listJobs: () => fetchJSON('/api/quantization/jobs'),
  getJob: (id) => fetchJSON(`/api/quantization/jobs/${enc(id)}`),
  stopJob: (id) => fetchJSON(`/api/quantization/jobs/${enc(id)}/stop`, { method: 'POST' }),
  deleteJob: (id) => fetchJSON(`/api/quantization/jobs/${enc(id)}`, { method: 'DELETE' }),
  importModel: (id, data) => postJSON(`/api/quantization/jobs/${enc(id)}/import`, data),
  progressUrl: (id) => apiUrl(`/api/quantization/jobs/${enc(id)}/progress`),
  downloadUrl: (id) => apiUrl(`/api/quantization/jobs/${enc(id)}/download`),
}

// Failover chains API. Health is pushed over /api/failover/events (SSE);
// list() seeds the view and backs the periodic resync.
export const failoverApi = {
  list: () => fetchJSON(API_CONFIG.endpoints.failoverChains),
  get: (name) => fetchJSON(API_CONFIG.endpoints.failoverChain(name)),
  pin: (name, target) => postJSON(API_CONFIG.endpoints.failoverPin(name), { target }),
  unpin: (name) => fetchJSON(API_CONFIG.endpoints.failoverPin(name), { method: 'DELETE' }),
  eventsUrl: () => API_CONFIG.endpoints.failoverEvents,
}

// Nodes API (distributed)
export const nodesApi = {
  list: () => fetchJSON(API_CONFIG.endpoints.nodes),
  get: (id) => fetchJSON(API_CONFIG.endpoints.node(id)),
  delete: (id) => fetchJSON(API_CONFIG.endpoints.node(id), { method: 'DELETE' }),
  drain: (id) => postJSON(API_CONFIG.endpoints.nodeDrain(id), {}),
  resume: (id) => postJSON(API_CONFIG.endpoints.nodeResume(id), {}),
  approve: (id) => postJSON(API_CONFIG.endpoints.nodeApprove(id), {}),
  getModels: (id) => fetchJSON(API_CONFIG.endpoints.nodeModels(id)),
  getBackends: (id) => fetchJSON(API_CONFIG.endpoints.nodeBackends(id)),
  // installBackend installs a gallery backend on a single node. opts can
  // override the gallery path and supply a direct URI (OCI image / URL / file
  // path) plus an optional name+alias, mirroring the standalone /backends/
  // install-external surface but scoped to one node.
  installBackend: (id, backend, opts = {}) => postJSON(API_CONFIG.endpoints.nodeBackendsInstall(id), {
    backend,
    ...(opts.uri ? { uri: opts.uri } : {}),
    ...(opts.name ? { name: opts.name } : {}),
    ...(opts.alias ? { alias: opts.alias } : {}),
    ...(opts.backend_galleries ? { backend_galleries: opts.backend_galleries } : {}),
  }),
  // upgradeBackend force-reinstalls a gallery backend on a single node. This
  // is a distinct endpoint from installBackend: the worker treats install as
  // "ensure installed" and no-ops when the backend already exists on disk,
  // so an upgrade dispatched through install would silently do nothing.
  upgradeBackend: (id, backend) => postJSON(API_CONFIG.endpoints.nodeBackendsUpgrade(id), { backend }),
  deleteBackend: (id, backend) => postJSON(API_CONFIG.endpoints.nodeBackendsDelete(id), { backend }),
  getBackendLogs: (id) => fetchJSON(API_CONFIG.endpoints.nodeBackendLogs(id)),
  getBackendLogLines: (id, modelId) => fetchJSON(API_CONFIG.endpoints.nodeBackendLogsModel(id, modelId)),
  unloadModel: (id, modelName) => postJSON(API_CONFIG.endpoints.nodeModelsUnload(id), { model_name: modelName }),
  getLabels: (id) => fetchJSON(API_CONFIG.endpoints.nodeLabels(id)),
  mergeLabels: (id, labels) => fetchJSON(API_CONFIG.endpoints.nodeLabels(id), { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(labels) }),
  deleteLabel: (id, key) => fetchJSON(API_CONFIG.endpoints.nodeLabelKey(id, key), { method: 'DELETE' }),
  // Set a sticky admin override for the per-node replica cap. The override
  // is preserved across worker restarts; call resetMaxReplicasPerModel to
  // hand control back to the worker's CLI flag.
  updateMaxReplicasPerModel: (id, value) => fetchJSON(API_CONFIG.endpoints.nodeMaxReplicasPerModel(id), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ value }),
  }),
  resetMaxReplicasPerModel: (id) => fetchJSON(API_CONFIG.endpoints.nodeMaxReplicasPerModel(id), {
    method: 'DELETE',
  }),
  // Set a sticky admin override for the per-node VRAM allocation budget. The
  // value is a string ("80%" or "12GB"); resolution to a byte ceiling happens
  // server-side. Call resetVramBudget to clear the override entirely.
  updateVramBudget: (id, value) => fetchJSON(API_CONFIG.endpoints.nodeVramBudget(id), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ value }),
  }),
  resetVramBudget: (id) => fetchJSON(API_CONFIG.endpoints.nodeVramBudget(id), {
    method: 'DELETE',
  }),
  listScheduling: () => fetchJSON(API_CONFIG.endpoints.nodesScheduling),
  allModels: () => fetchJSON(API_CONFIG.endpoints.nodesModels),
  setScheduling: (config) => postJSON(API_CONFIG.endpoints.nodesScheduling, config),
  deleteScheduling: (model) => fetchJSON(API_CONFIG.endpoints.nodesSchedulingModel(model), { method: 'DELETE' }),
}

// File to base64 helper
export function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const base64 = reader.result.split(',')[1] || reader.result
      resolve(base64)
    }
    reader.onerror = reject
    reader.readAsDataURL(file)
  })
}

// Multipart requests must let the browser set the boundary.
export const diarizationApi = {
  run: async ({ file, model, profiles = false }) => {
    const body = new FormData()
    body.append('file', file)
    body.append('model', model)
    if (profiles) {
      body.append('include_speaker_profiles', 'true')
      body.append('include_text', 'true')
      body.append('response_format', 'verbose_json')
    }
    return handleResponse(await fetch(apiUrl('/v1/audio/diarization'), { method: 'POST', body }))
  },
}
