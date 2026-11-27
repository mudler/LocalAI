// One-click setup: install the models a talking head needs from the gallery
// and write a pipeline model that joins them. Every call is a core route that
// the web UI's own model pages use, through the SDK fetch with the signed-in
// person's session, so the server decides who may install (administrators):
//
//   GET  /api/models?term=<name>       the gallery entry: id, installed, licence
//   GET  /api/models/estimate/<id>     its download size
//   GET  /api/models/variants/<id>     the builds to choose from, if it has any
//   POST /api/models/install/<id>      start the install; answers a job id
//   GET  /models/jobs/<job id>         the progress of that job
//   GET  /api/operations               installs already running (a tab closed mid-setup)
//   POST /api/operations/<job id>/cancel
//   POST /models/import                write the pipeline model config
//
// `request(path, init)` returns a fetch Response.

// The parts of a talking head, in install order. The avatar is first: it is
// the part only this page needs, so a missing licence answer stops early.
export const PARTS = ['avatar', 'chat', 'stt', 'tts', 'vad']

// Gallery entries known to run on a CPU with little memory. The first of each
// list is the default pick. The avatar list will also hold quantized builds;
// an entry that declares variants gets a build picker instead.
export const CANDIDATES = {
  avatar: ['avatarforcing'],
  chat: ['llama-3.2-1b-instruct:q4_k_m', 'gemma-3-1b-it', 'qwen3-0.6b'],
  stt: ['whisper-tiny-en-q5_1', 'whisper-base-en-q5_1', 'whisper-base-q5_1'],
  tts: ['vits-piper-en_US-amy-sherpa', 'kokoro'],
  vad: ['silero-vad', ''],
}

// The part may be left out (an empty pick).
export const OPTIONAL = new Set(['vad'])

export const PIPELINE_NAME = 'talking-head'

export function defaultPicks() {
  return Object.fromEntries(PARTS.map((p) => [p, CANDIDATES[p][0]]))
}

// A failed setup call. `forbidden` is true when the server refused the
// person (not an administrator, or signed out).
export class SetupError extends Error {
  constructor(message, { status, forbidden } = {}) {
    super(message)
    this.name = 'SetupError'
    this.status = status
    this.forbidden = Boolean(forbidden)
  }
}

const enc = encodeURIComponent

export function createSetupApi(request) {
  const call = async (path, init) => {
    const response = await request(path, init)
    if (!response.ok) {
      let message = `HTTP ${response.status}`
      try {
        const data = await response.json()
        message = data?.error?.message || (typeof data?.error === 'string' ? data.error : '') || data?.message || message
      } catch { /* not JSON */ }
      throw new SetupError(message, { status: response.status, forbidden: response.status === 401 || response.status === 403 })
    }
    const type = response.headers?.get?.('content-type') || ''
    return type.includes('json') ? response.json() : null
  }
  const post = (path, body, signal) => call(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body ?? {}), signal })
  return {
    // The gallery entry whose name is exactly `name`, or null.
    async find(name) {
      const data = await call(`/api/models?term=${enc(name)}&items=50&page=1`)
      return (data?.models || []).find((m) => m.name === name) || null
    },
    estimate: (id) => call(`/api/models/estimate/${enc(id)}`),
    variants: (id) => call(`/api/models/variants/${enc(id)}`),
    async install(id, variant, signal) {
      const data = await post(`/api/models/install/${enc(id)}${variant ? `?variant=${enc(variant)}` : ''}`, {}, signal)
      if (!data?.jobID) throw new SetupError('the server started no install job')
      return data.jobID
    },
    job: (jobID, signal) => call(`/models/jobs/${enc(jobID)}`, { signal }),
    // The running operations: { operations: [{ fullName, jobID, ... }] }.
    async running() {
      const data = await call('/api/operations')
      return (data?.operations || []).filter((op) => !op.isDeletion && !op.error && op.jobID)
    },
    cancel: (jobID) => post(`/api/operations/${enc(jobID)}/cancel`, {}),
    importConfig: (config, signal) => post('/models/import', config, signal),
  }
}

// The pipeline model: transcription, chat model and voice, and the voice
// detection model when one is picked. Talk can use the same pipeline.
export function pipelineConfig(name, picks) {
  const pipeline = { transcription: picks.stt, llm: picks.chat, tts: picks.tts }
  if (picks.vad) pipeline.vad = picks.vad
  return { name, pipeline }
}

// What the setup would do with these picks: for each part its gallery entry,
// whether it is installed already (then it is skipped), its size and its
// builds. `installed` holds the names of the installed models; a model that
// is installed but not in the gallery counts as installed too.
export async function planSetup(api, picks, installed = new Set()) {
  const items = []
  // An install of a part may still run from an earlier visit: follow it
  // instead of starting a second one.
  const running = await (api.running ? api.running().catch(() => []) : [])
  for (const part of PARTS) {
    const name = picks[part]
    if (!name) { if (OPTIONAL.has(part)) continue; items.push({ part, name: '', error: 'none' }); continue }
    const item = { part, name, installed: installed.has(name) }
    try {
      const entry = await api.find(name)
      if (entry) {
        Object.assign(item, { id: entry.id, license: entry.license || '', installed: item.installed || Boolean(entry.installed) })
        const op = running.find((o) => o.fullName === entry.id || o.id === entry.id)
        if (op && !item.installed) item.jobID = op.jobID
        if (!item.installed) {
          const [size, variants] = await Promise.all([
            api.estimate(entry.id).catch(() => null),
            entry.has_variants ? api.variants(entry.id).catch(() => null) : null,
          ])
          item.sizeBytes = size?.sizeBytes || 0
          item.sizeDisplay = size?.sizeDisplay || ''
          item.variants = variants?.variants?.length ? variants.variants : []
          item.variant = variants?.auto_selected || ''
        }
      } else if (!item.installed) {
        item.error = 'notInGallery'
      }
    } catch (err) {
      if (err.forbidden) throw err
      item.error = 'lookup'
      item.message = err.message
    }
    items.push(item)
  }
  return items
}

// The size of one part: the footprint of the chosen build when the entry
// has builds (the entry's own estimate is the size of its first file), else
// the estimate. 0 when unknown.
export function itemBytes(item) {
  const build = item.variant && (item.variants || []).find((v) => v.model === item.variant)
  return build?.memory_bytes || item.sizeBytes || 0
}

export function totalBytes(items) {
  return items.filter((i) => !i.installed && !i.error).reduce((sum, i) => sum + itemBytes(i), 0)
}

// True when the size of a part to download is not known, so a total is a floor.
export function sizeUnknown(items) {
  return items.some((i) => !i.installed && !i.error && !itemBytes(i))
}

export function formatBytes(n) {
  if (!n) return ''
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1000 && i < units.length - 1) { v /= 1000; i++ }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

const abortError = () => {
  const err = new Error('cancelled')
  err.name = 'AbortError'
  return err
}

const wait = (ms, signal) => new Promise((resolve, reject) => {
  if (signal?.aborted) { reject(abortError()); return }
  const timer = setTimeout(resolve, ms)
  signal?.addEventListener('abort', () => { clearTimeout(timer); reject(abortError()) }, { once: true })
})

// Polls one install job until it ends. onProgress gets { progress (0-100),
// message, current, total }. An abort cancels the job on the server.
export async function waitForJob(api, jobID, { onProgress = () => {}, signal, interval = 1000, sleep = wait } = {}) {
  let misses = 0
  for (;;) {
    if (signal?.aborted) { await api.cancel(jobID).catch(() => {}); throw abortError() }
    let status
    try {
      status = await api.job(jobID, signal)
      misses = 0
    } catch (err) {
      if (err.name === 'AbortError') { await api.cancel(jobID).catch(() => {}); throw err }
      if (err.forbidden) throw err
      // A status can be missing for a moment while the job is admitted.
      if (++misses > 5) throw err
    }
    if (status) {
      if (status.cancelled) throw abortError()
      if (status.error) throw new SetupError(String(status.error))
      onProgress({ progress: Math.round(status.progress || 0), message: status.message || '', current: status.current_bytes || 0, total: status.total_bytes || 0 })
      if (status.processed) return status
    }
    try {
      await sleep(interval, signal)
    } catch (err) {
      await api.cancel(jobID).catch(() => {})
      throw err
    }
  }
}

// Installs the parts of `items` that are not installed, one after the
// other, then writes the pipeline unless a model of that name exists.
// onItem(key, patch) reports each step: key is a part or 'pipeline', patch
// has state (installing | done | skipped | failed | cancelled), progress and
// error. A failure stops the run; the caller retries with the same items, and
// the parts already done are skipped because they are installed then.
export async function runSetup({ api, items, pipeline, pipelineExists, onItem = () => {}, signal, interval, sleep }) {
  for (const item of items) {
    if (item.installed || item.done) { onItem(item.part, { state: 'skipped' }); continue }
    if (item.error) throw new SetupError(item.message || item.error)
    onItem(item.part, { state: 'installing', progress: 0 })
    try {
      const jobID = item.jobID || await api.install(item.id, item.variant, signal)
      onItem(item.part, { jobID })
      await waitForJob(api, jobID, { signal, interval, sleep, onProgress: (p) => onItem(item.part, { state: 'installing', ...p }) })
      item.done = true
      onItem(item.part, { state: 'done', progress: 100 })
    } catch (err) {
      onItem(item.part, err.name === 'AbortError' ? { state: 'cancelled' } : { state: 'failed', error: err.message })
      throw err
    }
  }
  if (pipelineExists) { onItem('pipeline', { state: 'skipped' }); return }
  onItem('pipeline', { state: 'installing' })
  try {
    await api.importConfig(pipeline, signal)
    onItem('pipeline', { state: 'done' })
  } catch (err) {
    onItem('pipeline', err.name === 'AbortError' ? { state: 'cancelled' } : { state: 'failed', error: err.message })
    throw err
  }
}

// The pipeline the setup writes. A pipeline with the same parts is reused;
// otherwise the first free name: a model that is not a pipeline is never
// overwritten.
export function choosePipeline(pipelines, installedNames, picks) {
  const same = (pipelines || []).find((p) => !p.any_to_any && p.transcription === picks.stt && p.llm === picks.chat && p.tts === picks.tts && (p.vad || '') === (picks.vad || ''))
  if (same) return { name: same.name, exists: true }
  const taken = new Set([...(installedNames || []), ...(pipelines || []).map((p) => p.name)])
  let name = PIPELINE_NAME
  for (let n = 2; taken.has(name); n++) name = `${PIPELINE_NAME}-${n}`
  return { name, exists: false }
}
