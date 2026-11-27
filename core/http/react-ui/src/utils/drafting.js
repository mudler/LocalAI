// The rules of the Drafting section of Runtime tuning: which drafting mode a
// model config holds, what picking a mode writes, whether a draft model fits
// the model, and the words of its memory. Pure functions over the normalized
// schema (utils/engineSettings normalizeSchema, `schema.drafting`), so node
// tests drive every rule without a browser.
//
// Nothing here knows llama.cpp: the modes, the kinds each one reads back and
// the fields it draws come from the server's drafting capability.

import { readItem, writeItem } from './engineSettings.js'
import { formatBytes } from './format.js'

export const NEEDS_MTP_HEAD = 'mtp_head'
// The mode shown for a kind no mode reads back, such as two kinds mixed.
export const CUSTOM_MODE = 'custom'

// The backend reads underscores and two old names too (grpc-server.cpp).
function normalizeKind(kind) {
  const k = String(kind).trim().toLowerCase().replace(/_/g, '-')
  if (k === 'draft') return 'draft-simple'
  if (k === 'eagle3') return 'draft-eagle3'
  return k
}

/** The kinds of a spec_type value, normalized: "ngram_mod, draft" -> ['ngram-mod', 'draft-simple']. */
export function draftKinds(value) {
  if (value === undefined || value === null) return []
  return String(value).split(',').map(normalizeKind).filter(Boolean)
}

/** Whether the model has what a mode needs. `facts` is { mtpLayers }. */
export function modeAvailable(mode, facts) {
  if (!mode.needs) return true
  if (mode.needs === NEEDS_MTP_HEAD) return Number(facts?.mtpLayers) > 0
  return false
}

/**
 * The mode a value map holds: { mode, auto, kinds, file }.
 *
 *   - a draft model file: the mode that takes a model;
 *   - no kind: Off, or the automatic mode when the model has what it needs
 *     (`auto` true: the backend turns it on by itself);
 *   - the off kind ("none"): Off;
 *   - kinds that one mode reads back: that mode;
 *   - anything else: CUSTOM_MODE, kept as written.
 */
export function readDraftMode(schema, values, facts) {
  const d = schema.drafting
  const kinds = draftKinds(readItem(values, d.typeItem))
  const file = readItem(values, d.modelItem)
  const off = d.modes[0].id
  if (file) {
    const m = d.modes.find(x => x.model)
    return { mode: m ? m.id : CUSTOM_MODE, auto: false, kinds, file }
  }
  if (kinds.length === 0) {
    const auto = d.modes.find(m => m.auto && modeAvailable(m, facts))
    return auto ? { mode: auto.id, auto: true, kinds, file } : { mode: off, auto: false, kinds, file }
  }
  if (kinds.every(k => k === 'none' || k === d.offType)) return { mode: off, auto: false, kinds, file }
  const hit = d.modes.find(m => m.types.length > 0 && kinds.every(k => m.types.includes(k)))
  return { mode: hit ? hit.id : CUSTOM_MODE, auto: false, kinds, file }
}

/** The modes the choice offers: every mode the model can run, and the current one. */
export function offeredModes(schema, facts, current) {
  return schema.drafting.modes.filter(m => modeAvailable(m, facts) || m.id === current)
}

const itemById = (schema, id) => schema.items.find(i => i.id === id)

/**
 * Writes a mode into a draft value map.
 *
 *   off     no draft model, no kind (the off kind when the model would draft
 *           by itself), and none of the fields the modes draw or set.
 *   other   the mode's first kind, or no kind for a draft model (the backend
 *           reads it from the file; a draft-* kind already written stays),
 *           the draft model file for a mode that takes one, and the values
 *           the mode sets for each of its fields that is unset.
 *
 * `file` is the draft model file for a mode that takes one.
 */
export function writeDraftMode(schema, draft, modeId, { facts, file } = {}) {
  const d = schema.drafting
  const mode = d.modes.find(m => m.id === modeId)
  if (!mode) return
  const kinds = draftKinds(readItem(draft, d.typeItem))
  if (mode === d.modes[0]) {
    writeItem(draft, d.modelItem, undefined)
    const drafts = d.modes.some(m => m.auto && modeAvailable(m, facts))
    writeItem(draft, d.typeItem, drafts && d.offType ? d.offType : undefined)
    const touched = new Set(d.modes.flatMap(m => [...m.fields, ...Object.keys(m.set)]))
    if (d.streams) touched.add(d.streams.item.id)
    for (const id of touched) {
      const item = itemById(schema, id)
      if (item) writeItem(draft, item, undefined)
    }
    return
  }
  if (mode.model) {
    const keep = kinds.length > 0 && kinds.every(k => k.startsWith('draft-') && !d.modes.some(m => !m.model && m.types.includes(k)))
    if (!keep) writeItem(draft, d.typeItem, undefined)
    writeItem(draft, d.modelItem, file || undefined)
  } else {
    writeItem(draft, d.modelItem, undefined)
    writeItem(draft, d.typeItem, mode.types[0])
  }
  for (const [id, v] of Object.entries(mode.set)) {
    const item = itemById(schema, id)
    if (item && readItem(draft, item) === undefined) writeItem(draft, item, v)
  }
}

// --- draft model candidates ------------------------------------------------------------

// The family of a model name: its first word, letters and digits, so
// "qwen3-0.6b" and "Qwen3-32B-Q4" share "qwen3" and "llama-3.2-1b" is "llama".
export function nameFamily(name) {
  const m = String(name || '').toLowerCase().match(/^[a-z]+[0-9]*/)
  return m ? m[0] : ''
}

/**
 * The installed models a draft can come from, from GET /api/models/capabilities:
 * local models other than this one, with a text capability, that an engine
 * with drafting serves (`servedBy(backend)` says). Models of the same family
 * as this one come first. Each is { id, backend, sameFamily }.
 */
export function draftCandidates(models, self, servedBy) {
  const family = nameFamily(self)
  const out = []
  for (const m of Array.isArray(models) ? models : []) {
    if (!m?.id || m.id === self || m.remote || m.alias_of || m.alias_error || m.source === 'registry-only') continue
    const caps = Array.isArray(m.capabilities) ? m.capabilities : []
    if (caps.length && !caps.some(c => c === 'chat' || c === 'completion' || c === 'FLAG_CHAT' || c === 'FLAG_COMPLETION')) continue
    if (!servedBy(m.backend)) continue
    out.push({ id: m.id, backend: m.backend || '', sameFamily: Boolean(family) && nameFamily(m.id) === family })
  }
  return out.sort((a, b) => (a.sameFamily === b.sameFamily ? a.id.localeCompare(b.id) : a.sameFamily ? -1 : 1))
}

// llama.cpp refuses a draft whose vocabulary differs by more than this many
// tokens (SPEC_VOCAB_MAX_SIZE_DIFFERENCE in common/speculative.cpp).
export const VOCAB_TOLERANCE = 128

// "gemma4" and its draft-only "gemma4-assistant" are one family.
const archFamily = (a) => String(a || '').replace(/-assistant$/, '')

/**
 * Whether a draft model fits the model, from the facts of both estimates
 * ({ architecture, vocabSize }) and their names:
 *   { kind: 'ok' | 'mismatch' | 'family' | 'unknown', ... }
 *   ok        the vocabularies match (and `sameArch` says if the
 *             architectures do too)
 *   mismatch  the vocabularies differ too much: the model will not load
 *   family    no vocabulary to compare, but the architecture or the name
 *             family is the same
 *   unknown   nothing to compare
 */
export function draftFit(main, draft, names = {}) {
  const a = Number(main?.vocabSize) || 0
  const b = Number(draft?.vocabSize) || 0
  const sameArch = Boolean(main?.architecture) && archFamily(main.architecture) === archFamily(draft?.architecture)
  if (a && b) {
    if (Math.abs(a - b) > VOCAB_TOLERANCE) return { kind: 'mismatch', main: a, draft: b }
    return { kind: 'ok', vocab: b, sameArch, architecture: draft?.architecture || '' }
  }
  if (sameArch) return { kind: 'family', architecture: main.architecture }
  const fam = nameFamily(names.main)
  if (fam && fam === nameFamily(names.draft)) return { kind: 'family', architecture: fam }
  return { kind: 'unknown' }
}

/** The facts the Drafting section reads from a VRAM estimate answer. */
export function modelFacts(answer) {
  if (!answer || typeof answer !== 'object') return null
  return {
    architecture: answer.architecture ? String(answer.architecture) : '',
    mtpLayers: Number(answer.mtp_layers) || 0,
    vocabSize: Number(answer.vocab_size) || 0,
    contextLength: Number(answer.context_length) || 0,
  }
}

/**
 * The request that estimates the memory of a draft model: the installed
 * model's own estimate at the context of the model, with the draft KV cache
 * types (f16 when unset, as llama.cpp). Null without a draft model name.
 */
export function draftEstimateBody(schema, values, draftName) {
  if (!draftName) return null
  const body = { model: draftName }
  const ctx = schema.items.find(i => i.id === 'context_size')
  const n = ctx ? Number(readItem(values, ctx)) : NaN
  if (Number.isFinite(n) && n > 0) body.context_size = n
  for (const [field, id] of [['cache_type_k', 'options.draft_cache_type_k'], ['cache_type_v', 'options.draft_cache_type_v']]) {
    const item = schema.items.find(i => i.id === id)
    if (!item) continue
    body[field] = String(readItem(values, item) ?? 'f16')
  }
  return body
}

/**
 * The memory words of a draft model estimate:
 *   { total, weights, kv, context, approximate } as text, or null when the
 * answer has no size.
 */
export function draftMemory(answer) {
  const total = Number(answer?.vram_bytes)
  const weights = Number(answer?.size_bytes)
  if (!Number.isFinite(total) || total <= 0) return null
  const kv = Number(answer?.runtime?.kv_cache_bytes)
  return {
    total: formatBytes(total),
    weights: Number.isFinite(weights) && weights > 0 ? formatBytes(weights) : '',
    kv: Number.isFinite(kv) && kv > 0 ? formatBytes(kv) : '',
    context: Number(answer?.context_length) || Number(answer?.runtime?.context_length) || 0,
    approximate: Boolean(answer?.runtime?.approximate),
  }
}
