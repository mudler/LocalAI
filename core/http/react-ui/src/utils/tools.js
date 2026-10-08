// What the Build pages work out from data the server already returns: what this
// machine has, which tools it can run, what a job needs before it starts, how a
// source is spelled, and how a job's progress events read as a log.
//
// Nothing here estimates what a job will use or how long it will take. The
// server reports no model size before a job starts and no duration, so the
// checks state the facts that are known (free memory, free disk, whether a
// backend is installed, what the form holds) and say that the rest depends on
// the model and the hardware. A check carries a `key` and `vals` for the page to
// translate, so the wording stays out of this file.

const finite = (v) => (typeof v === 'number' && Number.isFinite(v) ? v : null)

// ---- This machine ------------------------------------------------------------

// The readings from GET /api/resources, in the shape the pages use. null when
// the answer is missing (the endpoint is for admins, so a user with only the
// tool's permission gets none). Every field is null rather than 0 when the
// server did not report it: 0 free bytes would be a claim.
export function machineFacts(resources) {
  if (!resources || typeof resources !== 'object') return null
  const gpus = Array.isArray(resources.gpus) ? resources.gpus.filter(Boolean) : []
  const hasGpu = resources.type === 'gpu' && gpus.length > 0
  const sum = (key) => gpus.reduce((n, g) => n + (finite(g[key]) ?? 0), 0)
  const ram = resources.ram || null
  const disk = resources.disk || null
  const cluster = resources.cluster?.enabled && finite(resources.cluster.total_memory) > 0 ? resources.cluster : null
  return {
    hasGpu,
    gpuName: hasGpu ? (gpus[0].name || 'GPU') + (gpus.length > 1 ? ` x${gpus.length}` : '') : '',
    vramTotal: hasGpu ? sum('total_vram') : null,
    vramFree: hasGpu ? sum('free_vram') : null,
    ramTotal: finite(ram?.total),
    ramFree: finite(ram?.available) ?? finite(ram?.free),
    diskTotal: finite(disk?.total),
    diskFree: finite(disk?.available),
    cluster: cluster
      ? { node: cluster.node_name || '', total: cluster.total_memory, isGpu: !!cluster.is_gpu, nodes: cluster.node_count || 0 }
      : null,
  }
}

// ---- Jobs ------------------------------------------------------------------------

export const FT_STAGES = ['queued', 'loading_model', 'loading_dataset', 'training', 'saving']
export const QZ_STAGES = ['queued', 'downloading', 'converting', 'quantizing']
export const TERMINAL = ['completed', 'failed', 'stopped']

export function isActive(job, stages) {
  return !!job && stages.includes(job.status)
}

// The newest job that is running, else the newest job when it failed. Used by the
// Build landing to put one line above the list.
export function jobToSurface(jobs, stages) {
  const list = Array.isArray(jobs) ? jobs : []
  const byNewest = [...list].sort((a, b) => Date.parse(b.created_at || 0) - Date.parse(a.created_at || 0))
  const running = byNewest.find(j => stages.includes(j.status))
  if (running) return { job: running, kind: 'running' }
  if (byNewest[0]?.status === 'failed') return { job: byNewest[0], kind: 'failed' }
  return null
}

// The phase a log line belongs to: info, warn or err.
export function logLevel(event) {
  const text = `${event?.message || ''}`
  if (event?.status === 'failed' || /\b(error|traceback|exception|failed|out of memory|oom)\b/i.test(text)) return 'err'
  if (/\bwarn(ing)?\b/i.test(text)) return 'warn'
  return 'info'
}

const num = (v, digits) => (Number.isFinite(v) ? v.toFixed(digits) : null)

// Lines to add to the log for one progress event. Only what the event holds:
// a status change, a new message, and for fine-tuning the step with its loss and
// learning rate. Returns [] when the event adds nothing.
export function logLines(event, previous, now = new Date()) {
  if (!event) return []
  const lines = []
  const level = logLevel(event)
  const tag = String(event.status || '').replace(/_/g, ' ')
  if (event.status && event.status !== previous?.status) {
    lines.push({ at: now, level: event.status === 'failed' ? 'err' : 'info', tag: 'status', text: tag })
  }
  const message = `${event.message || ''}`.trim()
  if (message && message !== `${previous?.message || ''}`.trim()) {
    lines.push({ at: now, level, tag: level === 'err' ? 'err' : level === 'warn' ? 'warn' : 'info', text: message })
  }
  if (event.total_steps > 0 && event.current_step > 0 && event.current_step !== previous?.current_step && event.loss > 0) {
    const parts = [`step ${event.current_step}/${event.total_steps}`, `loss ${num(event.loss, 4)}`]
    if (event.eval_loss > 0) parts.push(`eval_loss ${num(event.eval_loss, 4)}`)
    if (event.learning_rate > 0) parts.push(`lr ${event.learning_rate.toExponential(1)}`)
    lines.push({ at: now, level: 'info', tag: 'info', text: parts.join(' ') })
  }
  return lines
}

export const LOG_LIMIT = 400

export function appendLog(log, lines) {
  if (lines.length === 0) return log
  const next = log.concat(lines)
  return next.length > LOG_LIMIT ? next.slice(next.length - LOG_LIMIT) : next
}

export function logText(lines) {
  const pad = (n) => String(n).padStart(2, '0')
  return lines
    .map(l => `${pad(l.at.getHours())}:${pad(l.at.getMinutes())}:${pad(l.at.getSeconds())} ${l.tag.padEnd(6)} ${l.text}`)
    .join('\n')
}

// An out-of-memory failure, judged from the server's own message.
export function looksLikeMemoryFailure(message) {
  return /out of memory|\boom\b|cuda error: out of memory|cannot allocate memory|killed/i.test(`${message || ''}`)
}

// ---- The tool landing --------------------------------------------------------------

const names = (list) => (Array.isArray(list) ? list.map(b => b?.name || b).filter(Boolean) : [])

// What one tool needs from this machine, read from real data. `state` is
// 'ready' or 'needs-backend'; `needs` lists lines for the card, each with a tone
// ('ok', 'warn' or 'info') and a translation key.
//
//   facts      machineFacts(resources), or null
//   backends   { fineTune: [...] | null, quantize: [...] | null }; null when the
//              list could not be read
export function toolStatus(tool, { facts, backends }) {
  if (tool === 'import') {
    const needs = [{ tone: 'info', key: 'import.noSetup' }]
    if (facts?.diskFree != null) needs.push({ tone: 'ok', key: 'import.disk', vals: { free: facts.diskFree } })
    return { state: 'ready', needs }
  }
  if (tool === 'quantize') {
    const list = names(backends?.quantize)
    const needs = []
    if (backends?.quantize == null) {
      needs.push({ tone: 'info', key: 'quantize.backendUnknown' })
      return { state: 'ready', needs }
    }
    if (list.length === 0) {
      needs.push({ tone: 'warn', key: 'quantize.backendMissing' })
      return { state: 'needs-backend', needs }
    }
    needs.push({ tone: 'ok', key: 'quantize.backend', vals: { name: list[0] } })
    needs.push({ tone: 'info', key: 'quantize.cpu' })
    return { state: 'ready', needs }
  }
  if (tool === 'fine-tune') {
    const list = names(backends?.fineTune)
    const needs = []
    let state = 'ready'
    if (backends?.fineTune == null) {
      needs.push({ tone: 'info', key: 'fineTune.backendUnknown' })
    } else if (list.length === 0) {
      needs.push({ tone: 'warn', key: 'fineTune.backendMissing' })
      state = 'needs-backend'
    } else {
      needs.push({ tone: 'ok', key: 'fineTune.backend', vals: { name: list[0] } })
    }
    if (facts?.cluster) {
      needs.push({ tone: 'info', key: 'fineTune.cluster', vals: { total: facts.cluster.total, node: facts.cluster.node } })
    } else if (facts?.hasGpu) {
      needs.push({ tone: 'ok', key: 'fineTune.gpu', vals: { name: facts.gpuName, free: facts.vramFree, total: facts.vramTotal } })
    } else if (facts) {
      needs.push({ tone: 'warn', key: 'fineTune.noGpu' })
    }
    return { state, needs }
  }
  return { state: 'ready', needs: [] }
}

// ---- Pre-flight checks -------------------------------------------------------------

// A check: id, tone ('ok', 'warn', 'fail' or 'info'), key and vals for the text,
// an optional short `value`, and an optional `meter` { used, total } in bytes.
// A 'fail' blocks the start; a 'warn' does not.
const GB5 = 5 * 1024 ** 3

function backendCheck(list, wanted, kind) {
  if (list == null) return { id: 'backend', tone: 'info', key: `${kind}.backendUnknown` }
  const have = names(list)
  if (have.length === 0) return { id: 'backend', tone: 'warn', key: `${kind}.backendMissing`, vals: { name: wanted }, action: 'backends' }
  if (wanted && !have.includes(wanted)) return { id: 'backend', tone: 'warn', key: `${kind}.backendOther`, vals: { name: wanted, list: have.join(', ') }, action: 'backends' }
  return { id: 'backend', tone: 'ok', key: `${kind}.backendOk`, vals: { name: wanted || have[0] }, value: wanted || have[0] }
}

function diskCheck(facts, kind) {
  if (facts?.diskFree == null) return null
  const low = facts.diskFree < GB5
  return {
    id: 'disk',
    tone: low ? 'warn' : 'ok',
    key: low ? `${kind}.diskLow` : `${kind}.disk`,
    vals: { free: facts.diskFree },
    meter: facts.diskTotal > 0 ? { used: facts.diskTotal - facts.diskFree, total: facts.diskTotal } : null,
  }
}

export function fineTuneChecks({ form, facts, backends }) {
  const checks = []
  const model = `${form.model || ''}`.trim()
  const hasData = !!(`${form.datasetSource || ''}`.trim() || form.datasetFileName)
  checks.push(model
    ? { id: 'model', tone: 'ok', key: 'fineTune.modelOk', vals: { model }, value: model }
    : { id: 'model', tone: 'fail', key: 'fineTune.modelMissing' })
  checks.push(hasData
    ? { id: 'data', tone: 'ok', key: 'fineTune.dataOk', vals: { source: form.datasetFileName || form.datasetSource } }
    : { id: 'data', tone: 'fail', key: 'fineTune.dataMissing' })
  checks.push(backendCheck(backends?.fineTune, form.backend, 'fineTune'))

  const setup = {
    batch: form.batchSize,
    seq: form.maxSeqLength,
    accum: form.gradAccum,
    checkpointing: form.gradCheckpointing,
    full: form.trainingType === 'full',
  }
  if (facts?.cluster) {
    checks.push({ id: 'memory', tone: 'info', key: 'fineTune.memoryCluster', vals: { ...setup, total: facts.cluster.total, node: facts.cluster.node } })
  } else if (facts?.hasGpu) {
    checks.push({
      id: 'memory',
      tone: 'info',
      key: 'fineTune.memoryGpu',
      vals: { ...setup, name: facts.gpuName, free: facts.vramFree, total: facts.vramTotal },
      value: { free: facts.vramFree, total: facts.vramTotal },
      meter: { used: facts.vramTotal - facts.vramFree, total: facts.vramTotal },
    })
  } else if (facts) {
    checks.push({
      id: 'memory',
      tone: 'warn',
      key: 'fineTune.memoryCpu',
      vals: { ...setup, free: facts.ramFree, total: facts.ramTotal },
      meter: facts.ramTotal > 0 && facts.ramFree != null ? { used: facts.ramTotal - facts.ramFree, total: facts.ramTotal } : null,
    })
  } else {
    checks.push({ id: 'memory', tone: 'info', key: 'fineTune.memoryUnknown', vals: setup })
  }
  const disk = diskCheck(facts, 'fineTune')
  if (disk) checks.push(disk)
  if (form.trainingMethod === 'grpo' && !(form.rewardCount > 0)) {
    checks.push({ id: 'reward', tone: 'warn', key: 'fineTune.rewardMissing' })
  }
  if (form.resumeFrom) checks.push({ id: 'resume', tone: 'info', key: 'fineTune.resume', vals: { path: form.resumeFrom } })
  if (model.includes('/') && !form.hfToken) checks.push({ id: 'access', tone: 'info', key: 'fineTune.accessNone' })
  if (form.hfToken) checks.push({ id: 'access', tone: 'ok', key: 'fineTune.accessSet' })
  return checks
}

export function quantizeChecks({ form, facts, backends }) {
  const checks = []
  const model = `${form.model || ''}`.trim()
  checks.push(model
    ? { id: 'model', tone: 'ok', key: 'quantize.modelOk', vals: { model }, value: model }
    : { id: 'model', tone: 'fail', key: 'quantize.modelMissing' })
  if (form.useCustom && !`${form.customType || ''}`.trim()) {
    checks.push({ id: 'type', tone: 'fail', key: 'quantize.typeMissing' })
  }
  checks.push(backendCheck(backends?.quantize, form.backend, 'quantize'))
  if (facts?.ramTotal > 0 && facts.ramFree != null) {
    checks.push({
      id: 'memory',
      tone: 'info',
      key: 'quantize.memory',
      vals: { free: facts.ramFree, total: facts.ramTotal },
      meter: { used: facts.ramTotal - facts.ramFree, total: facts.ramTotal },
    })
  }
  const disk = diskCheck(facts, 'quantize')
  if (disk) checks.push(disk)
  if (model.includes('/') && !form.hfToken) checks.push({ id: 'access', tone: 'info', key: 'quantize.accessNone' })
  if (form.hfToken) checks.push({ id: 'access', tone: 'ok', key: 'quantize.accessSet' })
  return checks
}

export const blocked = (checks) => checks.some(c => c.tone === 'fail')
export const warned = (checks) => checks.filter(c => c.tone === 'warn').length

// ---- Import: what the source says about itself ---------------------------------------

// Reads the spelling of an import source, with no request. `kind` is one of
// 'huggingface', 'url', 'oci', 'ollama', 'local', 'config' (a YAML file) or
// 'unknown'. `ref` is the repository, file or image name; `file` the file name
// when the source names one. null for an empty field.
export function detectSource(input) {
  const uri = `${input || ''}`.trim()
  if (!uri) return null
  const lower = uri.toLowerCase()
  const fileOf = (path) => {
    const last = path.split(/[?#]/)[0].split('/').filter(Boolean).pop() || ''
    return /\.[a-z0-9]{2,6}$/i.test(last) ? last : ''
  }
  const isYaml = (name) => /\.ya?ml$/i.test(name)

  const hf = uri.match(/^(?:huggingface:\/\/|hf:\/\/|https?:\/\/(?:www\.)?huggingface\.co\/)(.+)$/i)
  if (hf) {
    const parts = hf[1].replace(/^\/+/, '').split('/').filter(Boolean)
    const ref = parts.slice(0, 2).join('/')
    const file = parts.length > 2 ? fileOf(parts.slice(2).join('/')) : ''
    return { kind: 'huggingface', ref, file, ok: parts.length >= 2, scheme: lower.startsWith('http') ? 'https' : lower.split('://')[0] }
  }
  if (/^(oci|ocifile):\/\//i.test(uri)) return { kind: 'oci', ref: uri.replace(/^[a-z]+:\/\//i, ''), file: '', ok: true, scheme: lower.split('://')[0] }
  if (/^ollama:\/\//i.test(uri)) return { kind: 'ollama', ref: uri.replace(/^ollama:\/\//i, ''), file: '', ok: true, scheme: 'ollama' }
  if (/^https?:\/\//i.test(uri)) {
    const file = fileOf(uri.replace(/^https?:\/\/[^/]+/i, ''))
    return { kind: isYaml(file) ? 'config' : 'url', ref: uri.replace(/^https?:\/\//i, ''), file, ok: true, scheme: lower.split('://')[0] }
  }
  if (/^file:\/\//i.test(uri) || uri.startsWith('/') || uri.startsWith('./') || uri.startsWith('~/')) {
    const path = uri.replace(/^file:\/\//i, '')
    const file = fileOf(path)
    return { kind: isYaml(file) ? 'config' : 'local', ref: path, file, ok: true, scheme: 'file' }
  }
  // owner/repo with no scheme: the importer tries it as a Hugging Face id.
  if (/^[\w.-]+\/[\w.-]+$/.test(uri)) return { kind: 'huggingface', ref: uri, file: '', ok: true, scheme: '' }
  return { kind: 'unknown', ref: uri, file: '', ok: false, scheme: '' }
}

// The request the Import form sends, built from its fields. One place, so the
// preview on the page is the request that goes out.
export function importRequest(uri, prefs, customPrefs, overrideBackend) {
  const out = {}
  const backend = overrideBackend !== undefined ? overrideBackend : prefs.backend
  if (backend) out.backend = backend
  if (prefs.name?.trim()) out.name = prefs.name.trim()
  if (prefs.description?.trim()) out.description = prefs.description.trim()
  if (prefs.quantizations?.trim()) out.quantizations = prefs.quantizations.trim()
  if (prefs.mmproj_quantizations?.trim()) out.mmproj_quantizations = prefs.mmproj_quantizations.trim()
  if (prefs.embeddings) out.embeddings = 'true'
  if (prefs.type?.trim()) out.type = prefs.type.trim()
  if (prefs.pipeline_type?.trim()) out.pipeline_type = prefs.pipeline_type.trim()
  if (prefs.scheduler_type?.trim()) out.scheduler_type = prefs.scheduler_type.trim()
  if (prefs.enable_parameters?.trim()) out.enable_parameters = prefs.enable_parameters.trim()
  if (prefs.cuda) out.cuda = true
  for (const cp of customPrefs || []) {
    if (cp.key.trim() && cp.value.trim()) out[cp.key.trim()] = cp.value.trim()
  }
  return { uri: `${uri || ''}`.trim(), preferences: Object.keys(out).length > 0 ? out : null }
}

// The request as YAML-like text for the preview. Scalars are quoted only when a
// plain scalar would be read as something else.
export function requestText(req) {
  const scalar = (v) => {
    if (typeof v !== 'string') return String(v)
    return /^[\w./:@~+-][\w ./:@~+,-]*$/.test(v) && !/^(true|false|null|yes|no|\d+)$/i.test(v) ? v : JSON.stringify(v)
  }
  const lines = [`uri: ${scalar(req.uri)}`]
  if (req.preferences) {
    lines.push('preferences:')
    for (const [k, v] of Object.entries(req.preferences)) lines.push(`  ${k}: ${scalar(v)}`)
  } else {
    lines.push('preferences: none (LocalAI decides)')
  }
  return lines.join('\n')
}

// The checks for an import that can be made before it starts. The size and the
// memory it needs are known only after the import starts, so they are not here.
export function importChecks({ source, prefs, backends, installedNames, facts }) {
  const checks = []
  if (!source) return checks
  checks.push(source.ok
    ? { id: 'source', tone: 'ok', key: 'import.sourceOk', vals: { kind: source.kind } }
    : { id: 'source', tone: 'warn', key: 'import.sourceUnknown' })
  const chosen = prefs.backend
  if (chosen) {
    const known = (backends || []).find(b => b.name === chosen)
    checks.push(known && !known.installed
      ? { id: 'backend', tone: 'info', key: 'import.backendDownload', vals: { name: chosen } }
      : { id: 'backend', tone: 'ok', key: 'import.backendChosen', vals: { name: chosen } })
  } else {
    checks.push({ id: 'backend', tone: 'info', key: 'import.backendAuto' })
  }
  const name = `${prefs.name || ''}`.trim()
  if (name) {
    const taken = (installedNames || []).includes(name)
    checks.push(taken
      ? { id: 'name', tone: 'warn', key: 'import.nameTaken', vals: { name } }
      : { id: 'name', tone: 'ok', key: 'import.nameFree', vals: { name } })
  } else {
    checks.push({ id: 'name', tone: 'info', key: 'import.nameAuto' })
  }
  if (facts?.diskFree != null) {
    checks.push({
      id: 'disk',
      tone: facts.diskFree < GB5 ? 'warn' : 'ok',
      key: 'import.disk',
      vals: { free: facts.diskFree },
      meter: facts.diskTotal > 0 ? { used: facts.diskTotal - facts.diskFree, total: facts.diskTotal } : null,
    })
  }
  if (facts) {
    const freeMemory = facts.cluster ? facts.cluster.total : facts.hasGpu ? facts.vramFree : facts.ramFree
    if (freeMemory != null) {
      checks.push({ id: 'memory', tone: 'info', key: 'import.memory', vals: { free: freeMemory, where: facts.cluster ? 'cluster' : facts.hasGpu ? 'gpu' : 'ram' } })
    }
  }
  return checks
}

// How an import's own estimate sits against this machine, once the server has
// returned one. 'fits', 'tight' (over 85% of the free memory) or 'over'; null
// when either number is missing.
export function fitFor(neededBytes, freeBytes) {
  if (!(neededBytes > 0) || !(freeBytes > 0)) return null
  const ratio = neededBytes / freeBytes
  if (ratio > 1) return 'over'
  if (ratio > 0.85) return 'tight'
  return 'fits'
}

// ---- Explorer ------------------------------------------------------------------------

// A token cut for display: its first and last characters. The token itself is
// what the copy button and the join commands carry.
export function shortToken(token) {
  const t = `${token || ''}`
  return t.length <= 16 ? t : `${t.slice(0, 6)}…${t.slice(-6)}`
}

export function workerCount(network) {
  return (network?.Clusters || []).reduce((n, c) => n + (c?.Workers?.length || 0), 0)
}

// Networks with the most workers first, then by name. The server sorts by cluster
// count; the page counts workers, which is what a person wants to join.
export function sortNetworks(list) {
  return [...(Array.isArray(list) ? list : [])].sort((a, b) =>
    workerCount(b) - workerCount(a) || `${a.name}`.localeCompare(`${b.name}`))
}

// The two commands that start a federated node on a network, as the server's own
// explorer page prints them.
export function joinCommands(token, networkId) {
  const env = `ADDRESS=":80" LOCALAI_P2P_NETWORK_ID=${networkId} LOCALAI_P2P_LOGLEVEL=debug TOKEN="${token}"`
  return {
    docker: `docker run -d --restart=always -e ADDRESS=":80" -e LOCALAI_P2P_NETWORK_ID=${networkId} -e LOCALAI_P2P_LOGLEVEL=debug --name local-ai -e TOKEN="${token}" --net host -ti localai/localai:master federated --debug`,
    cli: `${env} local-ai federated --debug`,
  }
}
