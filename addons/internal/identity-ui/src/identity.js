// SPDX-License-Identifier: MIT
// Pure helpers for the Voices and Faces pages: how a distance reads in plain
// words, how the scale is drawn, what the browser keeps about a person, and
// how a failed call is explained. No React here, so node can test it.

// The cut-offs the server applies when a request does not send one.
export const VOICE_CUTOFF = 0.25
export const FACE_CUTOFF = 0.35

// Distance in plain words. A distance is how far apart two voiceprints (or
// faceprints) are: 0 means identical. The word says how far inside or outside
// the cut-off a result sits. It is not a probability.
export function strength(distance, cutoff) {
  if (!(cutoff > 0) || !Number.isFinite(distance)) return 'far'
  const ratio = distance / cutoff
  if (ratio <= 0.6) return 'strong'
  if (ratio <= 0.9) return 'likely'
  if (ratio <= 1) return 'close'
  if (ratio <= 1.25) return 'near'
  return 'far'
}

export const isWithin = (distance, cutoff) => Number.isFinite(distance) && distance <= cutoff

// Sort by distance and mark each row against the cut-off. The server marks
// matches against the cut-off it was sent; marking again here lets the cut-off
// slider move without another call.
export function rankMatches(matches, cutoff) {
  return [...(matches || [])]
    .filter(m => Number.isFinite(m.distance))
    .sort((a, b) => a.distance - b.distance)
    .map((m, i) => ({ ...m, rank: i + 1, within: isWithin(m.distance, cutoff), word: strength(m.distance, cutoff) }))
}

// The top of the scale: far enough to show the cut-off and every dot, rounded
// up to a tenth so the ticks land on round numbers.
export function scaleMax(cutoff, distances = []) {
  const far = Math.max(cutoff * 2.4, ...distances.map(d => d * 1.12), 0.1)
  return Math.min(2, Math.ceil(far * 10) / 10)
}

export function scaleTicks(max) {
  const step = max > 1 ? 0.5 : max > 0.5 ? 0.1 : 0.05
  const out = []
  for (let v = 0; v <= max + 1e-9; v += step) out.push(Math.round(v * 1000) / 1000)
  return out
}

export const pct = (value, max) => `${Math.max(0, Math.min(100, (value / max) * 100))}%`

// Label rows so close dots do not print on top of each other: a label that
// would sit within `gap` percent of the last one on its row drops to the next
// of three rows, and the row with the most room when all three are crowded.
export function labelRows(positions, gap = 24) {
  const last = [-Infinity, -Infinity, -Infinity]
  return positions.map((p) => {
    let row = last.findIndex(l => p - l >= gap)
    if (row === -1) row = last.indexOf(Math.min(...last))
    last[row] = p
    return row
  })
}

export function ageText(iso, now = Date.now(), locale) {
  const t = new Date(iso).getTime()
  if (!Number.isFinite(t)) return ''
  const days = Math.round((now - t) / 86400000)
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  if (Math.abs(days) >= 1) return rtf.format(-days, 'day')
  const hours = Math.round((now - t) / 3600000)
  if (hours >= 1) return rtf.format(-hours, 'hour')
  return rtf.format(0, 'second') === 'now' ? 'just now' : rtf.format(0, 'second')
}

export function parseLabels(text) {
  const out = {}
  if (!text) return out
  for (const line of text.split('\n')) {
    const idx = line.indexOf(':')
    if (idx === -1) continue
    const k = line.slice(0, idx).trim()
    const v = line.slice(idx + 1).trim()
    if (k) out[k] = v
  }
  return out
}

export const labelsText = (labels) => Object.entries(labels || {}).map(([k, v]) => `${k}: ${v}`).join(', ')

export const initials = (name) => (name || '?').trim().split(/\s+/).map(p => p[0] || '').join('').slice(0, 2).toUpperCase() || '?'

// The browser-side list of people. The server has no list call, so this is a
// record of what this browser enrolled, kept in localStorage. `keep` is the
// slice of stored fields: a name, labels, a time, and only when the person
// chose it, a copy of the sample.
export function loadList(key, storage = globalThis.localStorage) {
  try {
    const parsed = JSON.parse(storage.getItem(key) || '[]')
    return Array.isArray(parsed) ? parsed : []
  } catch { return [] }
}

export function saveList(key, list, storage = globalThis.localStorage) {
  try { storage.setItem(key, JSON.stringify(list.slice(0, 50))) } catch { /* quota or blocked */ }
}

// Which saved people did the last search not return? Only a search that asked
// for more people than it got back can say so: then the server returned
// everything it holds for this model. Otherwise nothing is claimed.
export function missingFromServer(entries, result) {
  if (!result || !result.complete) return new Set()
  const seen = new Set(result.ids)
  return new Set(entries.filter(e => !seen.has(e.id)).map(e => e.id))
}

// A plain sentence for a failed call. `kind` is 'voice' or 'face'.
export function explainError(err, kind) {
  const status = err?.status
  const message = String(err?.message || '')
  if (kind === 'face' && /no face|face not|could not (find|detect)/i.test(message)) {
    return { title: 'No face found in this photo', body: 'Try a sharper, front-facing photo with the face in clear light.' }
  }
  if (kind === 'voice' && /too short|no speech|empty audio|no audio/i.test(message)) {
    return { title: 'The clip has too little speech', body: 'Use a clip with at least a few seconds of one person speaking.' }
  }
  if (status === 404 || /model .*(not found|not loaded)|no such model/i.test(message)) {
    return { title: 'The server cannot find this model', body: 'Pick another model, or install it from the model list.' }
  }
  if (status === 501 || status === 412) {
    return { title: 'This model cannot do that', body: 'It does not support this call. Pick a model made for recognition.' }
  }
  if (status === 400 || status === 422) {
    return { title: 'The server could not read the sample', body: 'Check that the file is a normal audio or image file, then try again.' }
  }
  return { title: 'The call failed', body: 'The server answered with an error. Try again; if it repeats, open the trace.' }
}

// Read a clip in the browser: how long it is and how loud. Nothing is sent.
export function judgeClip({ seconds, peak, clipped }, minSeconds = 3) {
  const notes = []
  if (!(seconds > 0)) return { level: 'unknown', notes }
  if (seconds < minSeconds) notes.push(`Short: ${seconds.toFixed(1)} s. Three seconds or more of speech works better.`)
  if (peak != null && peak < 0.05) notes.push('Very quiet. Move closer to the microphone or raise the level.')
  if (clipped) notes.push('The sound is clipped, which distorts the voice.')
  return { level: notes.length ? 'warn' : 'good', notes }
}
