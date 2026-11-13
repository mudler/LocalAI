// The default models of the user (LocalAI/defaults.json): one model per kind of
// work. Pure helpers, so the node unit tests load them. The server applies the
// same rules in core/services/studio/wellknown/defaults.go.

import { CAP_CHAT, CAP_IMAGE, CAP_TTS, CAP_TRANSCRIPT, CAP_EMBEDDINGS, CAP_RERANK, CAP_VIDEO, CAP_VISION } from './capabilities.js'

export const ASK = 'ask'

// The kinds of work, in the order the editor shows them. key is the key of
// defaults.json; cap is the capability a model needs to be offered for it.
export const DEFAULT_KINDS = [
  { key: 'chat', cap: CAP_CHAT, icon: 'chat' },
  { key: 'image', cap: CAP_IMAGE, icon: 'image' },
  { key: 'tts', cap: CAP_TTS, icon: 'volume' },
  { key: 'stt', cap: CAP_TRANSCRIPT, icon: 'mic' },
  { key: 'embeddings', cap: CAP_EMBEDDINGS, icon: 'database' },
  { key: 'video', cap: CAP_VIDEO, icon: 'video' },
  { key: 'rerank', cap: CAP_RERANK, icon: 'sort' },
  { key: 'vision', cap: CAP_VISION, icon: 'eye' },
]

// defaultChoice reads the choice for one kind from the data of defaults.json
// and of ui.json. A missing key and a null are the same: unset. Only for chat,
// an unset choice falls back to the older defaultModel of ui.json, which is
// read and never written.
//
// It answers { mode: 'model' | 'ask' | 'unset', model, from }.
export function defaultChoice(defaults, ui, key) {
  const v = defaults?.[key]
  if (typeof v === 'string' && v.trim()) {
    if (v.trim().toLowerCase() === ASK) return { mode: 'ask', model: '', from: 'defaults' }
    return { mode: 'model', model: v.trim(), from: 'defaults' }
  }
  const old = ui?.defaultModel
  if (key === 'chat' && typeof old === 'string' && old.trim()) return { mode: 'model', model: old.trim(), from: 'ui' }
  return { mode: 'unset', model: '', from: '' }
}

// chatStartModel is the model a new chat starts with: the chat default, or ''
// when the user wants to be asked or made no choice. What the picker of a new
// chat does then is startPick.
export function chatStartModel(defaults, ui) {
  const c = defaultChoice(defaults, ui, 'chat')
  return c.mode === 'model' ? c.model : ''
}

// startPick is what the model picker of a new chat starts with, from the chat
// choice (defaultChoice for chat, or null before the files are read), the
// models that can be chosen and the last model used. It answers
// { model, note, missing }:
//   ask      defaults.json says ask: no model, the person chooses
//   missing  defaults.json names a model that is not installed: no model, and
//            the note names it, so the picker never sends to another model
//            without a word
// With no chat key in defaults.json (and the older defaultModel of ui.json
// absent or gone), it is the last model used, else the first one.
export function startPick(choice, usable, remembered) {
  if (choice?.mode === 'ask') return { model: '', note: 'ask', missing: '' }
  if (choice?.mode === 'model') {
    if (usable.includes(choice.model)) return { model: choice.model, note: '', missing: '' }
    if (choice.from === 'defaults') return { model: '', note: 'missing', missing: choice.model }
  }
  const model = remembered && usable.includes(remembered) ? remembered : (usable[0] || '')
  return { model, note: '', missing: '' }
}

// modelsFor lists the usable models of a kind (not disabled, with its
// capability), by name, each with the backend that runs it.
export function modelsFor(models, cap) {
  const short = String(cap || '').replace(/^FLAG_/, '').toLowerCase()
  return (models || [])
    .filter(m => m && !m.disabled && Array.isArray(m.capabilities) && (m.capabilities.includes(cap) || m.capabilities.includes(short)))
    .map(m => ({ id: m.id, backend: m.backend || '' }))
    .sort((a, b) => a.id.localeCompare(b.id))
}

// rowState is what a row of the editor shows for one kind, from the choice and
// the installed models of that kind:
//   none      no model of the kind is installed (and the user did not choose ask)
//   missing   the choice names a model that is not installed
//   ask       the user is asked each time
//   model     an installed model is the default
//   automatic no choice: LocalAI picks an installed model
export function rowState(choice, installed) {
  if (choice.mode === 'ask') return 'ask'
  if (choice.mode === 'model') return installed.some(m => m.id === choice.model) ? 'model' : 'missing'
  return installed.length === 0 ? 'none' : 'automatic'
}

// valueFor is the value a save sends for a pick: a model name, ask, or null,
// which removes the key.
export function valueFor(pick) {
  if (pick === ASK) return ASK
  const v = typeof pick === 'string' ? pick.trim() : ''
  return v || null
}
