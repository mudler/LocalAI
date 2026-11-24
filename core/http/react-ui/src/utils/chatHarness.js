// What the chat page does when the model of a chat has a window that is too
// small for the chat agent. The server decides that and sends it as the
// `harness` block of a chat ({ ok, window, minimum, recommended, can_fix,
// admin, source }); nothing here knows the numbers. The functions are pure so
// the choices can be read, and tested, without a page.

import { fitFor } from './modelLedger.js'

// The block to act on, or null when the chat is fine or the server said
// nothing (an older server, or a chat that is not open yet).
export function harnessProblem(harness) {
  if (!harness || harness.ok !== false) return null
  if (!(harness.minimum > 0)) return null
  return harness
}

// The windows an administrator may set on the model, largest first: the
// recommended one, a step of three quarters of it, and the minimum. Windows
// above what the model was trained for are left out. [] when the model cannot
// reach the minimum at all.
export function contextLadder({ minimum, recommended, maxContext = 0 }) {
  const step = Math.round((recommended * 0.75) / 256) * 256
  const steps = [recommended, step, minimum]
  const out = []
  for (const value of steps) {
    if (!(value >= minimum && value <= recommended)) continue
    if (maxContext > 0 && value > maxContext) continue
    if (!out.includes(value)) out.push(value)
  }
  return out.sort((a, b) => b - a)
}

// Which window to offer. readings maps a window to the estimate of the memory
// it needs ({ bytes } or null/false when the server could not say). The first
// window of the ladder that fits wins:
//
//   { kind: 'fits', value, bytes, wanted }  wanted is true when value is the
//                                           recommended window itself
//   { kind: 'unknown', value }              no reading for any window: the
//                                           administrator may still choose
//   { kind: 'none' }                        nothing from the ladder fits, or
//                                           the model cannot reach the minimum
export function pickWindow({ ladder, readings, budget, ramAvailable = null }) {
  if (!ladder.length) return { kind: 'none' }
  let anyReading = false
  for (const value of ladder) {
    const reading = readings[value]
    if (!reading || !(reading.bytes > 0)) continue
    anyReading = true
    const fit = fitFor(reading.bytes, budget, ramAvailable)
    if (fit?.state === 'fits') return { kind: 'fits', value, bytes: reading.bytes, wanted: value === ladder[0] }
  }
  if (!anyReading) return { kind: 'unknown', value: ladder[0] }
  return { kind: 'none' }
}

// The installed models a regular user can move the chat to: chat models whose
// window is at least the minimum, other than the current one, the largest
// window first. A model whose window the server did not report is left out.
export function modelsWithWindow(models, { minimum, current }) {
  return (models || [])
    .filter(m => m && m.id !== current && !m.disabled && !m.alias_error && m.context_size >= minimum)
    .sort((a, b) => b.context_size - a.context_size)
    .map(m => ({ id: m.id, window: m.context_size }))
}
