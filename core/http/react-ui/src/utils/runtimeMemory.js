// The memory figures of the quick setup of Runtime tuning: what a set of
// runtime settings costs beside the weights (the KV cache and the compute
// buffers), from POST /api/models/vram-estimate. Pure functions, so node tests
// drive them without a browser.
//
// The schema says which settings feed the estimate (schema.estimate: request
// field -> setting id), so nothing here knows an engine.

import { readItem } from './engineSettings.js'
import { formatBytes } from './format.js'

// A change smaller than this is shown as no change: the estimate is not that
// precise.
export const SAME_BYTES = 16 * 1024 * 1024

const NUMBER_FIELDS = new Set(['context_size', 'gpu_layers', 'batch', 'ubatch'])

// A default the server writes as one value ("f16") is that value; a default in
// words ("backend default") is not.
const defaultValue = (item) => {
  const d = item?.default
  return d !== undefined && d !== null && d !== '' && !/\s/.test(String(d)) ? String(d) : undefined
}

/**
 * The request body that estimates the memory of a value map, or null when
 * the engine names no setting for the estimate. An unset batch is sent as 0
 * (the host default) and an unset ubatch as 0 (the same as the batch), so a
 * draft that clears them is estimated with the defaults, not with the values
 * saved in the file. An unset cache type is sent as the default the schema
 * states.
 */
export function estimateBody(schema, model, values) {
  const map = schema?.estimate || {}
  if (!model || Object.keys(map).length === 0) return null
  const body = { model }
  for (const [field, id] of Object.entries(map)) {
    const item = schema.items.find(i => i.id === id)
    if (!item) continue
    const v = readItem(values, item)
    if (NUMBER_FIELDS.has(field)) {
      const n = Number(v)
      if (v !== undefined && Number.isFinite(n)) body[field] = n
      else if (field === 'batch' || field === 'ubatch') body[field] = 0
    } else if (v !== undefined) {
      body[field] = String(v)
    } else if (defaultValue(item)) {
      body[field] = defaultValue(item)
    }
  }
  return body
}

/** The runtime bytes of an estimate answer (KV cache plus compute), or null. */
export function runtimeBytes(answer) {
  const r = answer?.runtime
  if (!r) return null
  const kv = Number(r.kv_cache_bytes)
  const compute = Number(r.compute_bytes)
  if (!Number.isFinite(kv) || !Number.isFinite(compute)) return null
  return kv + compute
}

/**
 * The change from one estimate answer to another:
 *   { kind: 'more' | 'less' | 'same', bytes, text, approximate }
 * or null when either has no runtime part.
 */
export function memoryChange(from, to) {
  const a = runtimeBytes(from)
  const b = runtimeBytes(to)
  if (a === null || b === null) return null
  const diff = b - a
  const approximate = Boolean(from.runtime.approximate || to.runtime.approximate)
  if (Math.abs(diff) < SAME_BYTES) return { kind: 'same', bytes: 0, text: '', approximate }
  return {
    kind: diff > 0 ? 'more' : 'less',
    bytes: diff,
    text: formatBytes(Math.abs(diff)),
    approximate,
  }
}

/** The KV cache and compute figures of one answer, as text, or null. */
export function memoryNow(answer) {
  const r = answer?.runtime
  if (!r) return null
  return {
    kv: formatBytes(Number(r.kv_cache_bytes) || 0),
    compute: formatBytes(Number(r.compute_bytes) || 0),
    context: r.context_length,
    approximate: Boolean(r.approximate),
  }
}

// --- memory kinds and concurrency profiles ---------------------------------

/**
 * The memory kind the quick setup uses: the person's choice, else the one
 * the server detected, else the schema's fallback (the cautious discrete
 * rules). `source` says which.
 */
export function memoryKind(schema, host, override) {
  const known = (k) => Boolean(k && schema?.memory?.kinds?.some(x => x.kind === k))
  if (known(override)) return { kind: override, source: 'override' }
  if (known(host?.kind)) return { kind: host.kind, source: 'detected' }
  return { kind: schema?.memory?.fallback || 'discrete', source: 'fallback' }
}

/** The whole estimate of an answer (weights and runtime), or null. */
export function totalBytes(answer) {
  const n = Number(answer?.vram_bytes)
  return Number.isFinite(n) && n > 0 ? n : null
}

/**
 * Whether a concurrency profile can be offered on a memory kind, and with
 * which settings:
 *   { state: 'ok' | 'unsupported' | 'small' | 'nofit', tight, need, limit }
 *
 *   profile   a normalized concurrency profile
 *   kind      the memory kind
 *   memory    the device memory in bytes (0: unknown, so no size check)
 *   fraction  the share of it an estimate may take
 *   answers   { plain, tight, now } estimate answers (any may be missing)
 *
 * A profile whose estimate does not fit uses its tight settings when it has
 * them. It is refused only when what it would use still does not fit and
 * needs more than the current values, so a model that already does not fit
 * (partial GPU offload) does not lose every choice.
 */
export function profileFit(profile, kind, memory, fraction, answers = {}) {
  const m = profile?.memory?.[kind]
  if (m?.unsupported) return { state: 'unsupported', tight: false }
  if (memory > 0 && m?.minMemory > memory) return { state: 'small', tight: false, need: m.minMemory }
  const limit = memory > 0 ? memory * (fraction || 1) : 0
  if (!limit) return { state: 'ok', tight: false }
  const now = totalBytes(answers.now)
  const over = (a) => {
    const b = totalBytes(a)
    return b !== null && b > limit
  }
  const worse = (a) => over(a) && (now === null || totalBytes(a) > now)
  if (!over(answers.plain)) return { state: 'ok', tight: false }
  const hasTight = Boolean(m && Object.keys(m.tight || {}).length)
  const used = hasTight ? answers.tight : answers.plain
  if (!worse(used)) return { state: 'ok', tight: hasTight }
  return { state: 'nofit', tight: hasTight, need: totalBytes(used), limit }
}

/** The context tokens each of `streams` requests gets on average, or null. */
export function tokensPerStream(context, streams) {
  const c = Number(context)
  if (!streams || !Number.isFinite(c) || c <= 0) return null
  return Math.floor(c / streams)
}
