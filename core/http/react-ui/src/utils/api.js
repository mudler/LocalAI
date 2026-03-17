import { API_CONFIG } from './config'
import { apiUrl } from './basePath'

async function handleResponse(response) {
  if (!response.ok) {
    let errorMessage = `HTTP ${response.status}`
    try {
      const data = await response.json()
      if (data?.error?.message) errorMessage = data.error.message
      else if (data?.error) errorMessage = data.error
    } catch (_e) {
      // response wasn't JSON
    }
    throw new Error(errorMessage)
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

// SSE streaming for chat completions
export async function streamChat(body, signal) {
  const response = await fetch(apiUrl(API_CONFIG.endpoints.chatCompletions), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...body, stream: true }),
    signal,
  })

  if (!response.ok) {
    let errorMessage = `HTTP ${response.status}`
    try {
      const data = await response.json()
      if (data?.error?.message) errorMessage = data.error.message
    } catch (_e) { /* not JSON */ }
    throw new Error(errorMessage)
  }

  return response.body
}

// Models API
export const modelsApi = {
  list: (params) => fetchJSON(buildUrl(API_CONFIG.endpoints.models, params)),
  listV1: () => fetchJSON(API_CONFIG.endpoints.modelsList),
  listCapabilities: () => fetchJSON(API_CONFIG.endpoints.modelsCapabilities),
  install: (id) => postJSON(API_CONFIG.endpoints.installModel(id), {}),
  delete: (id) => postJSON(API_CONFIG.endpoints.deleteModel(id), {}),
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
  getEditConfig: (name) => fetchJSON(API_CONFIG.endpoints.modelEditGet(name)),
  editConfig: (name, body) => postJSON(API_CONFIG.endpoints.modelEdit(name), body),
}

// Backends API
export const backendsApi = {
  list: (params) => fetchJSON(buildUrl(API_CONFIG.endpoints.backends, params)),
  listInstalled: () => fetchJSON(API_CONFIG.endpoints.backendsInstalled),
  install: (id) => postJSON(API_CONFIG.endpoints.installBackend(id), {}),
  delete: (id) => postJSON(API_CONFIG.endpoints.deleteBackend(id), {}),
  installExternal: (body) => postJSON(API_CONFIG.endpoints.installExternalBackend, body),
  getJob: (uid) => fetchJSON(API_CONFIG.endpoints.backendJob(uid)),
  deleteInstalled: (name) => postJSON(API_CONFIG.endpoints.deleteInstalledBackend(name), {}),
}

// Chat API (non-streaming)
export const chatApi = {
  complete: (body) => postJSON(API_CONFIG.endpoints.chatCompletions, body),
  mcpComplete: (body) => postJSON(API_CONFIG.endpoints.mcpChatCompletions, body),
}

// MCP API
export const mcpApi = {
  listServers: (model) => fetchJSON(API_CONFIG.endpoints.mcpServers(model)),
  listPrompts: (model) => fetchJSON(API_CONFIG.endpoints.mcpPrompts(model)),
  getPrompt: (model, name, args) => postJSON(API_CONFIG.endpoints.mcpGetPrompt(model, name), { arguments: args }),
  listResources: (model) => fetchJSON(API_CONFIG.endpoints.mcpResources(model)),
  readResource: (model, uri) => postJSON(API_CONFIG.endpoints.mcpReadResource(model), { uri }),
}

// Resources API
export const resourcesApi = {
  get: () => fetchJSON(API_CONFIG.endpoints.resources),
}

// Operations API
export const operationsApi = {
  list: () => fetchJSON(API_CONFIG.endpoints.operations),
  cancel: (jobID) => postJSON(API_CONFIG.endpoints.cancelOperation(jobID), {}),
  dismiss: (jobID) => postJSON(API_CONFIG.endpoints.dismissOperation(jobID), {}),
}

// Settings API
export const settingsApi = {
  get: () => fetchJSON(API_CONFIG.endpoints.settings),
  save: (body) => postJSON(API_CONFIG.endpoints.settings, body),
}

// Backend Logs API
export const backendLogsApi = {
  listModels: () => fetchJSON(API_CONFIG.endpoints.backendLogs),
  getLines: (modelId) => fetchJSON(API_CONFIG.endpoints.backendLogsModel(modelId)),
  clear: (modelId) => postJSON(API_CONFIG.endpoints.clearBackendLogs(modelId), {}),
}

// Traces API
export const tracesApi = {
  get: () => fetchJSON(API_CONFIG.endpoints.traces),
  clear: () => postJSON(API_CONFIG.endpoints.clearTraces, {}),
  getBackend: () => fetchJSON(API_CONFIG.endpoints.backendTraces),
  clearBackend: () => postJSON(API_CONFIG.endpoints.clearBackendTraces, {}),
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

// Agent Jobs API
export const agentJobsApi = {
  listTasks: () => fetchJSON(API_CONFIG.endpoints.agentTasks),
  getTask: (id) => fetchJSON(API_CONFIG.endpoints.agentTask(id)),
  createTask: (body) => postJSON(API_CONFIG.endpoints.agentTasks, body),
  updateTask: (id, body) => fetchJSON(API_CONFIG.endpoints.agentTask(id), { method: 'PUT', body: JSON.stringify(body), headers: { 'Content-Type': 'application/json' } }),
  deleteTask: (id) => fetchJSON(API_CONFIG.endpoints.agentTask(id), { method: 'DELETE' }),
  executeTask: (name) => postJSON(API_CONFIG.endpoints.executeAgentTask(name), {}),
  listJobs: () => fetchJSON(API_CONFIG.endpoints.agentJobs),
  getJob: (id) => fetchJSON(API_CONFIG.endpoints.agentJob(id)),
  cancelJob: (id) => postJSON(API_CONFIG.endpoints.cancelAgentJob(id), {}),
  executeJob: (body) => postJSON(API_CONFIG.endpoints.executeAgentJob, body),
}

// Image generation
export const imageApi = {
  generate: (body) => postJSON(API_CONFIG.endpoints.imageGenerations, body),
}

// Video generation
export const videoApi = {
  generate: (body) => postJSON(API_CONFIG.endpoints.video, body),
}

// TTS
export const ttsApi = {
  generate: async (body) => {
    const response = await fetch(apiUrl(API_CONFIG.endpoints.tts), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!response.ok) {
      const data = await response.json().catch(() => ({}))
      throw new Error(data?.error?.message || `HTTP ${response.status}`)
    }
    return response.blob()
  },
  generateV1: async (body) => {
    const response = await fetch(apiUrl(API_CONFIG.endpoints.audioSpeech), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!response.ok) {
      const data = await response.json().catch(() => ({}))
      throw new Error(data?.error?.message || `HTTP ${response.status}`)
    }
    return response.blob()
  },
}

// Sound generation
export const soundApi = {
  generate: async (body) => {
    const response = await fetch(apiUrl(API_CONFIG.endpoints.soundGeneration), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!response.ok) {
      const data = await response.json().catch(() => ({}))
      throw new Error(data?.error?.message || `HTTP ${response.status}`)
    }
    return response.blob()
  },
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

// Realtime / WebRTC
export const realtimeApi = {
  call: (body) => postJSON(API_CONFIG.endpoints.realtimeCalls, body),
  pipelineModels: () => fetchJSON(API_CONFIG.endpoints.pipelineModels),
}

// Backend control
export const backendControlApi = {
  shutdown: (body) => postJSON(API_CONFIG.endpoints.backendShutdown, body),
}

// System info
export const systemApi = {
  version: () => fetchJSON(API_CONFIG.endpoints.version),
  info: () => fetchJSON(API_CONFIG.endpoints.system),
}

export const agentsApi = {
  list: () => fetchJSON('/api/agents'),
  create: (config) => postJSON('/api/agents', config),
  get: (name) => fetchJSON(`/api/agents/${encodeURIComponent(name)}`),
  getConfig: (name) => fetchJSON(`/api/agents/${encodeURIComponent(name)}/config`),
  update: (name, config) => fetchJSON(`/api/agents/${encodeURIComponent(name)}`, { method: 'PUT', body: JSON.stringify(config), headers: { 'Content-Type': 'application/json' } }),
  delete: (name) => fetchJSON(`/api/agents/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  pause: (name) => fetchJSON(`/api/agents/${encodeURIComponent(name)}/pause`, { method: 'PUT' }),
  resume: (name) => fetchJSON(`/api/agents/${encodeURIComponent(name)}/resume`, { method: 'PUT' }),
  status: (name) => fetchJSON(`/api/agents/${encodeURIComponent(name)}/status`),
  observables: (name) => fetchJSON(`/api/agents/${encodeURIComponent(name)}/observables`),
  clearObservables: (name) => fetchJSON(`/api/agents/${encodeURIComponent(name)}/observables`, { method: 'DELETE' }),
  chat: (name, message) => postJSON(`/api/agents/${encodeURIComponent(name)}/chat`, { message }),
  export: (name) => fetchJSON(`/api/agents/${encodeURIComponent(name)}/export`),
  import: (formData) => fetch(apiUrl('/api/agents/import'), { method: 'POST', body: formData }).then(handleResponse),
  configMeta: () => fetchJSON('/api/agents/config/metadata'),
}

export const agentCollectionsApi = {
  list: () => fetchJSON('/api/agents/collections'),
  create: (name) => postJSON('/api/agents/collections', { name }),
  upload: (name, formData) => fetch(apiUrl(`/api/agents/collections/${encodeURIComponent(name)}/upload`), { method: 'POST', body: formData }).then(handleResponse),
  entries: (name) => fetchJSON(`/api/agents/collections/${encodeURIComponent(name)}/entries`),
  entryContent: (name, entry) => fetchJSON(`/api/agents/collections/${encodeURIComponent(name)}/entries/${encodeURIComponent(entry)}`),
  search: (name, query, maxResults) => postJSON(`/api/agents/collections/${encodeURIComponent(name)}/search`, { query, max_results: maxResults }),
  reset: (name) => postJSON(`/api/agents/collections/${encodeURIComponent(name)}/reset`),
  deleteEntry: (name, entry) => fetchJSON(`/api/agents/collections/${encodeURIComponent(name)}/entry/delete`, { method: 'DELETE', body: JSON.stringify({ entry }), headers: { 'Content-Type': 'application/json' } }),
  sources: (name) => fetchJSON(`/api/agents/collections/${encodeURIComponent(name)}/sources`),
  addSource: (name, url, interval) => postJSON(`/api/agents/collections/${encodeURIComponent(name)}/sources`, { url, update_interval: interval }),
  removeSource: (name, url) => fetchJSON(`/api/agents/collections/${encodeURIComponent(name)}/sources`, { method: 'DELETE', body: JSON.stringify({ url }), headers: { 'Content-Type': 'application/json' } }),
}

// Skills API
export const skillsApi = {
  list: () => fetchJSON('/api/agents/skills'),
  search: (q) => fetchJSON(`/api/agents/skills/search?q=${encodeURIComponent(q)}`),
  get: (name) => fetchJSON(`/api/agents/skills/${encodeURIComponent(name)}`),
  create: (data) => postJSON('/api/agents/skills', data),
  update: (name, data) => fetchJSON(`/api/agents/skills/${encodeURIComponent(name)}`, { method: 'PUT', body: JSON.stringify(data), headers: { 'Content-Type': 'application/json' } }),
  delete: (name) => fetchJSON(`/api/agents/skills/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  import: (file) => { const fd = new FormData(); fd.append('file', file); return fetch(apiUrl('/api/agents/skills/import'), { method: 'POST', body: fd }).then(r => { if (!r.ok) throw new Error(`HTTP ${r.status}`); return r.json(); }); },
  exportUrl: (name) => apiUrl(`/api/agents/skills/export/${encodeURIComponent(name)}`),
  listResources: (name) => fetchJSON(`/api/agents/skills/${encodeURIComponent(name)}/resources`),
  getResource: (name, path, opts) => fetchJSON(`/api/agents/skills/${encodeURIComponent(name)}/resources/${path}${opts?.json ? '?encoding=base64' : ''}`),
  createResource: (name, path, file) => { const fd = new FormData(); fd.append('file', file); fd.append('path', path); return fetch(apiUrl(`/api/agents/skills/${encodeURIComponent(name)}/resources`), { method: 'POST', body: fd }).then(r => { if (!r.ok) throw new Error(`HTTP ${r.status}`); return r.json(); }); },
  updateResource: (name, path, content) => postJSON(`/api/agents/skills/${encodeURIComponent(name)}/resources/${path}`, { content }),
  deleteResource: (name, path) => fetchJSON(`/api/agents/skills/${encodeURIComponent(name)}/resources/${path}`, { method: 'DELETE' }),
  listGitRepos: () => fetchJSON('/api/agents/git-repos'),
  addGitRepo: (url) => postJSON('/api/agents/git-repos', { url }),
  syncGitRepo: (id) => postJSON(`/api/agents/git-repos/${encodeURIComponent(id)}/sync`, {}),
  toggleGitRepo: (id) => postJSON(`/api/agents/git-repos/${encodeURIComponent(id)}/toggle`, {}),
  deleteGitRepo: (id) => fetchJSON(`/api/agents/git-repos/${encodeURIComponent(id)}`, { method: 'DELETE' }),
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
