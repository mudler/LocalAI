import { API_CONFIG } from './config'

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
  const url = new URL(endpoint, window.location.origin)
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
  const response = await fetch(endpoint, {
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
  const response = await fetch(API_CONFIG.endpoints.chatCompletions, {
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
    const response = await fetch(API_CONFIG.endpoints.modelsImport, {
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

// Resources API
export const resourcesApi = {
  get: () => fetchJSON(API_CONFIG.endpoints.resources),
}

// Operations API
export const operationsApi = {
  list: () => fetchJSON(API_CONFIG.endpoints.operations),
  cancel: (jobID) => postJSON(API_CONFIG.endpoints.cancelOperation(jobID), {}),
}

// Settings API
export const settingsApi = {
  get: () => fetchJSON(API_CONFIG.endpoints.settings),
  save: (body) => postJSON(API_CONFIG.endpoints.settings, body),
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
    const response = await fetch(API_CONFIG.endpoints.p2pToken)
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
    const response = await fetch(API_CONFIG.endpoints.tts, {
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
    const response = await fetch(API_CONFIG.endpoints.audioSpeech, {
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
    const response = await fetch(API_CONFIG.endpoints.soundGeneration, {
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
    const response = await fetch(API_CONFIG.endpoints.audioTranscriptions, {
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
