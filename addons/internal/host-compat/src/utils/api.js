// The core API calls the Studio and Talk pages make, reached with the
// signed-in person's own session (sdk.core.fetch). The shapes are the ones of
// core/http/react-ui/src/utils/api.js (the bodies, the endpoints from
// utils/config.js and the error handling); only the transport changed.
import { API_CONFIG } from './config.js'
import { apiUrl } from './basePath.js'
import { getSdk } from '../runtime.js'

const enc = encodeURIComponent

// A call to a core route: the SDK fetch, or a plain fetch for an address the
// SDK does not take (a blob: or data: URL, a full address).
async function coreFetch(path, init) {
  return getSdk().core.fetch(path, init)
}

// Reads a file the core serves (a generated image, audio or mesh) or a local
// blob. The result is the fetch response.
export async function fetchUrl(url, init) {
  const text = String(url)
  if (text.startsWith('/')) {
    const base = apiUrl('')
    return coreFetch(base && text.startsWith(`${base}/`) ? text.slice(base.length) : text, init)
  }
  return fetch(text, init)
}

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

function withParams(endpoint, params) {
  if (!params) return endpoint
  const q = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') q.set(key, value)
  })
  const text = q.toString()
  return text ? `${endpoint}?${text}` : endpoint
}

async function fetchJSON(endpoint, options = {}) {
  const response = await coreFetch(endpoint, {
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

export const modelsApi = {
  list: (params) => fetchJSON(withParams(API_CONFIG.endpoints.models, params)),
  // variant is optional. Omitting it lets the server auto-select the best
  // build for this host, which is what the listing's auto_variant predicted.
  install: (id, variant) => postJSON(
    variant
      ? `${API_CONFIG.endpoints.installModel(id)}?variant=${enc(variant)}`
      : API_CONFIG.endpoints.installModel(id),
    {},
  ),
  estimate: (id, contexts) => fetchJSON(
    withParams(API_CONFIG.endpoints.modelEstimate(id), contexts?.length ? { contexts: contexts.join(',') } : {}),
  ),
}

export const imageApi = {
  upscale: async ({ model, image, scale }) => {
    const form = new FormData()
    form.append('model', model)
    form.append('image', image)
    form.append('scale', String(scale))
    return handleResponse(await coreFetch('/v1/images/upscale', { method: 'POST', body: form }))
  },
  generate: (body) => postJSON(API_CONFIG.endpoints.imageGenerations, body),
}

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
    const response = await coreFetch(API_CONFIG.endpoints.threeDRemesh, { method: 'POST', body: form })
    await handleResponse(response)
    return response.blob()
  },
}

// Shared response handling for audio-blob endpoints: throws on non-2xx (with
// the API error message when present); returns the blob, the server path of
// the saved file (from Content-Disposition) and the saved input paths the
// audio transform endpoint reports in X-Audio-Input-Url / X-Audio-Reference-Url.
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
  const response = await coreFetch(endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  return parseAudioBlobResponse(response)
}

async function postMultipartAudioBlob(endpoint, formData) {
  const response = await coreFetch(endpoint, { method: 'POST', body: formData })
  return parseAudioBlobResponse(response)
}

export const ttsApi = {
  generate: (body) => postAudioBlob(API_CONFIG.endpoints.tts, body),
  generateV1: (body) => postAudioBlob(API_CONFIG.endpoints.audioSpeech, body),
}

export const soundApi = {
  generate: (body) => postAudioBlob(API_CONFIG.endpoints.soundGeneration, body),
}

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
  // A WebSocket address cannot go through fetch: the browser opens it with the
  // page's own session.
  streamUrl: () => apiUrl(API_CONFIG.endpoints.audioTransformStream).replace(/^http/, 'ws'),
}

export const voiceApi = {
  register: (body) => postJSON(API_CONFIG.endpoints.voiceRegister, body),
}

export const realtimeApi = {
  call: (body) => postJSON(API_CONFIG.endpoints.realtimeCalls, body),
  pipelineModels: () => fetchJSON(API_CONFIG.endpoints.pipelineModels),
}

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
    return handleResponse(await coreFetch('/v1/audio/diarization', { method: 'POST', body }))
  },
}
