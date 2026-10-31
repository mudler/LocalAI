// The loops of a chat: a prompt the chat sends to itself again, at an interval
// or, for a self-paced loop, when the model asks. The server keeps them
// (GET /api/chats/:id/loops, and the loops field of the chat); these helpers
// shape them for the page.

// An interval as the server writes it: digits and s, m or h, one or more
// times (30s, 5m, 1h30m). The first word of "/loop 5m check" is one; the first
// word of "/loop check the build" is not, so that loop is self-paced.
const INTERVAL = /^(?:\d+[smh])+$/

export function looksLikeInterval(word) {
  return typeof word === 'string' && INTERVAL.test(word)
}

// parseLoopCommand reads what follows "/loop":
//   ""                    { kind: 'usage' }
//   "list"                { kind: 'list' }
//   "stop" / "stop <id>"  { kind: 'stop', id }   (id is '' for every loop)
//   "5m <prompt>"         { kind: 'start', interval: '5m', prompt }
//   "<prompt>"            { kind: 'start', interval: '', prompt }  self-paced
//   "5m"                  { kind: 'usage' }      an interval with no prompt
export function parseLoopCommand(rest) {
  const text = String(rest || '').trim()
  if (!text) return { kind: 'usage' }
  const [first, ...more] = text.split(/\s+/)
  const after = text.slice(first.length).trim()
  if (first === 'list' && more.length === 0) return { kind: 'list' }
  if (first === 'stop' && more.length <= 1) return { kind: 'stop', id: more[0] || '' }
  if (looksLikeInterval(first)) {
    if (!after) return { kind: 'usage' }
    return { kind: 'start', interval: first, prompt: after }
  }
  return { kind: 'start', interval: '', prompt: text }
}

// findLoop finds a loop by its full ID or by the start of it, as /loop list
// shows it. It answers null when no loop or more than one loop matches.
export function findLoop(loops, ref) {
  const id = String(ref || '').trim()
  if (!id) return null
  const exact = loops.find(l => l.id === id)
  if (exact) return exact
  const hits = loops.filter(l => l.id.startsWith(id))
  return hits.length === 1 ? hits[0] : null
}

// shortId is the start of a loop ID that /loop list shows and /loop stop takes.
export function shortId(id) {
  return String(id || '').slice(0, 6)
}

// intervalParts splits a number of seconds into one unit: hours when it is
// whole hours, else minutes when it is whole minutes, else seconds.
export function intervalParts(seconds) {
  const s = Math.max(0, Math.round(Number(seconds) || 0))
  if (s > 0 && s % 3600 === 0) return { unit: 'hours', count: s / 3600 }
  if (s > 0 && s % 60 === 0) return { unit: 'minutes', count: s / 60 }
  return { unit: 'seconds', count: s }
}

// loopEvery writes an interval for a sentence ("5 minutes") or, short, for a
// chip ("5 min"), with the chats translations.
export function loopEvery(t, seconds, short = false) {
  const { unit, count } = intervalParts(seconds)
  return t(`loops.${short ? 'unitShort' : 'unit'}.${unit}`, { count })
}

// Loops the chip area shows, in order: active and paused first, then the ended
// ones. A pending loop shows as its confirmation card instead.
export function chipLoops(loops) {
  const list = Array.isArray(loops) ? loops : []
  return [
    ...list.filter(l => l.state === 'active' || l.state === 'paused'),
    ...list.filter(l => l.state === 'ended'),
  ]
}

export function pendingLoops(loops) {
  return (Array.isArray(loops) ? loops : []).filter(l => l.state === 'pending')
}
