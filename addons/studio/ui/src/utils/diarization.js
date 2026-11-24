// What a diarization run says, as numbers and as the files people already use
// for it. Plain functions with no React, so they can be tested with `node --test`.
//
// A segment is { start, end, speaker, label, name?, text? } in seconds. `label`
// is the stable key of a speaker inside one run; `speaker` is the id the server
// prints (SPEAKER_00).

const num = (v) => (Number.isFinite(Number(v)) ? Number(v) : 0)

// m:ss, or h:mm:ss for a long recording.
export function clock(seconds) {
  const total = Math.max(0, Math.round(num(seconds)))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`
}

export function runLength(result) {
  return Math.max(0, ...(result?.segments || []).map(s => num(s.end)))
}

// One row per speaker, in order of first speech: how long they spoke and what
// share of all speech that is. Overlapping speech counts for each speaker.
export function speakerRows(result) {
  const segments = result?.segments || []
  const rows = new Map()
  for (const seg of segments) {
    const key = String(seg.label ?? seg.speaker)
    if (!rows.has(key)) rows.set(key, { label: key, id: seg.speaker ?? key, name: seg.name || '', seconds: 0, segments: [] })
    const row = rows.get(key)
    row.seconds += Math.max(0, num(seg.end) - num(seg.start))
    if (!row.name && seg.name) row.name = seg.name
    row.segments.push(seg)
  }
  for (const s of result?.speakers || []) {
    const key = String(s.label)
    if (!rows.has(key)) rows.set(key, { label: key, id: s.id ?? key, name: s.name || '', seconds: 0, segments: [] })
    else if (s.name && !rows.get(key).name) rows.get(key).name = s.name
  }
  const total = [...rows.values()].reduce((sum, r) => sum + r.seconds, 0)
  return [...rows.values()].map((r, index) => ({ ...r, index, share: total > 0 ? r.seconds / total : 0 }))
}

// Tick marks for the timeline axis: about `count` round steps across the run.
export function axisTicks(length, count = 5) {
  if (!(length > 0)) return []
  const steps = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600]
  const raw = length / Math.max(1, count - 1)
  const step = steps.find(s => s >= raw * 0.7) || steps[steps.length - 1]
  const ticks = []
  for (let t = 0; t <= length + 1e-6; t += step) ticks.push({ at: t, pct: (t / length) * 100 })
  return ticks
}

// RTTM, the line format diarization tools exchange.
export function toRttm(result, fileName = 'recording') {
  const id = String(fileName).replace(/\.[^.]*$/, '').replace(/\s+/g, '_') || 'recording'
  const rows = speakerRows(result)
  const names = new Map(rows.map(r => [r.label, r.name || r.id]))
  return (result?.segments || [])
    .map((s) => {
      const who = String(names.get(String(s.label ?? s.speaker)) || s.speaker).replace(/\s+/g, '_')
      const dur = Math.max(0, num(s.end) - num(s.start))
      return `SPEAKER ${id} 1 ${num(s.start).toFixed(3)} ${dur.toFixed(3)} <NA> <NA> ${who} <NA> <NA>`
    })
    .join('\n') + '\n'
}

function srtTime(seconds) {
  const ms = Math.round(Math.max(0, num(seconds)) * 1000)
  const h = Math.floor(ms / 3600000)
  const m = Math.floor((ms % 3600000) / 60000)
  const s = Math.floor((ms % 60000) / 1000)
  const pad = (n, w = 2) => String(n).padStart(w, '0')
  return `${pad(h)}:${pad(m)}:${pad(s)},${pad(ms % 1000, 3)}`
}

// SRT of the segments that have text. Empty when the run carried no text.
export function toSrt(result) {
  const rows = speakerRows(result)
  const names = new Map(rows.map(r => [r.label, r.name || r.id]))
  return (result?.segments || [])
    .filter(s => String(s.text || '').trim())
    .map((s, i) => `${i + 1}\n${srtTime(s.start)} --> ${srtTime(s.end)}\n${names.get(String(s.label ?? s.speaker)) || s.speaker}: ${String(s.text).trim()}\n`)
    .join('\n')
}

export const hasText = (result) => (result?.segments || []).some(s => String(s.text || '').trim())
