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

// --- Waits ------------------------------------------------------------------
// A wait is a loop whose check is code: the server polls a source and wakes
// the model once. The server sends it as kind "wait" with a watch object
// (core/schema/chats.go, ChatsWait); these helpers turn that into the lines
// the page shows. They are pure, with the clock passed in, so tests can fix it.

export function isWait(loop) {
  return !!loop && loop.kind === 'wait' && !!loop.watch
}

// A span of time for a sentence: "45 s", "12 min", "1 h 20 min".
export function spanText(t, ms) {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return t('loops.unitShort.seconds', { count: s })
  if (s < 3600) return t('loops.unitShort.minutes', { count: Math.round(s / 60) })
  const h = Math.floor(s / 3600)
  const m = Math.round((s % 3600) / 60)
  const hours = t('loops.unitShort.hours', { count: h })
  return m > 0 ? `${hours} ${t('loops.unitShort.minutes', { count: m })}` : hours
}

function ago(t, iso, now) {
  const at = Date.parse(iso)
  if (Number.isNaN(at)) return ''
  if (now - at < 5000) return t('loops.wait.justNow')
  return t('loops.wait.ago', { span: spanText(t, now - at) })
}

function inSpan(t, iso, now) {
  const at = Date.parse(iso)
  if (Number.isNaN(at)) return ''
  if (at - now < 1000) return t('loops.wait.now')
  return t('loops.wait.in', { span: spanText(t, at - now) })
}

function hhmm(iso) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

// waitView describes a wait for the page:
//   phase   waiting | waking | paused | ended | pending
//   tone    run (mint), warn (amber), error (red), ok (green), idle (grey)
//   icon    kit icon name; the title says the same in words
//   title   the headline, with the reason the model gave in `reason`
//   facts   [{ key, text }] lines, in order: source, until, checked, error, next, deadline
export function waitView(t, loop, now = Date.now()) {
  const w = loop.watch
  const phase = w.phase || 'waiting'
  const source = t(`loops.wait.sources.${w.source_kind === 'url' ? 'url' : 'tool'}`, { name: w.source })
  const facts = [
    { key: 'source', text: t('loops.wait.watching', { source }) },
    { key: 'until', text: t('loops.wait.until', { condition: w.condition }) },
  ]
  const checks = w.checks || 0
  if (checks === 0) {
    facts.push({ key: 'checked', text: t('loops.wait.notChecked') })
  } else {
    const outcome = w.outcome ? t(`loops.wait.outcome.${w.outcome}`) : ''
    const head = w.last_check
      ? t('loops.wait.checkedAgo', { count: checks, ago: ago(t, w.last_check, now) })
      : t('loops.wait.checked', { count: checks })
    facts.push({ key: 'checked', text: outcome ? `${head} · ${outcome}` : head })
  }
  const streak = (w.errors || 0) > 0 && phase !== 'ended'
  if (streak) {
    const key = w.last_error ? 'errorStreakMsg' : 'errorStreak'
    facts.push({ key: 'error', text: t(`loops.wait.${key}`, { count: w.errors, max: w.max_errors, message: w.last_error }) })
  } else if (phase === 'ended' && w.last_error && w.end !== 'condition_met') {
    facts.push({ key: 'error', text: t('loops.wait.lastError', { message: w.last_error }) })
  }
  const live = phase === 'waiting' || phase === 'paused'
  if (phase === 'waiting' && w.next_check) facts.push({ key: 'next', text: t('loops.wait.next', { when: inSpan(t, w.next_check, now) }) })
  if (live && w.deadline) {
    facts.push({ key: 'deadline', text: t('loops.wait.deadline', { time: hhmm(w.deadline), when: inSpan(t, w.deadline, now) }) })
  }

  let title; let tone; let icon
  if (phase === 'ended') {
    switch (w.end) {
      case 'condition_met': title = t('loops.wait.end.met'); tone = 'ok'; icon = 'check-circle'; break
      case 'source_failing': title = t('loops.wait.end.failing', { count: w.max_errors || w.errors }); tone = 'error'; icon = 'close-circle'; break
      case 'timed_out': title = t('loops.wait.end.timedOut'); tone = 'warn'; icon = 'warning'; break
      default: title = t('loops.wait.end.stopped'); tone = 'idle'; icon = 'stop'
    }
  } else if (phase === 'waking') {
    const key = { condition_met: 'met', source_failing: 'failing', timed_out: 'timedOut' }[w.end] || 'met'
    title = t(`loops.wait.waking.${key}`)
    tone = key === 'met' ? 'ok' : (key === 'failing' ? 'error' : 'warn')
    icon = 'clock'
  } else if (phase === 'paused') {
    title = t('loops.wait.paused'); tone = 'warn'; icon = 'pause'
  } else if (phase === 'pending') {
    title = t('loops.wait.pending'); tone = 'warn'; icon = 'hourglass'
  } else {
    title = t('loops.wait.name'); tone = streak ? 'warn' : 'run'; icon = 'hourglass'
  }
  // Only a wait that goes on starts its title with the reason it waits.
  const reason = phase === 'waiting' || phase === 'paused' || phase === 'pending' ? (w.reason || '') : ''
  return { phase, tone, icon, title, reason, facts, end: w.end || '' }
}

// From this many waits that still wait, the page folds them into one chip.
export const WAIT_GROUP_MIN = 3

// A wait that still waits: it checks, or it is paused. One that woke the model
// or ended has a result of its own to show, so it never hides in a group.
export function isLiveWait(loop) {
  if (!isWait(loop)) return false
  const phase = loop.watch.phase || 'waiting'
  return loop.state !== 'ended' && (phase === 'waiting' || phase === 'paused')
}

// barItems turns the chips of a chat into what the bar draws, in order:
//   { kind: 'loop' | 'wait', loop }       one chip
//   { kind: 'group', waits: [loop, ...] } the live waits, when there are
//                                          WAIT_GROUP_MIN or more of them; the
//                                          group sits where the first one was
export function barItems(loops, min = WAIT_GROUP_MIN) {
  const chips = chipLoops(loops)
  const live = chips.filter(isLiveWait)
  const fold = live.length >= min
  const items = []
  for (const l of chips) {
    if (fold && isLiveWait(l)) {
      if (!items.some(i => i.kind === 'group')) items.push({ kind: 'group', waits: live })
    } else {
      items.push({ kind: isWait(l) ? 'wait' : 'loop', loop: l })
    }
  }
  return items
}

// groupView is the line of a folded group: how many wait and the soonest time
// that matters: the nearest deadline, else the latest check.
//   { count, paused, facts: [{ key, text }] }
export function groupView(t, waits, now = Date.now()) {
  const list = Array.isArray(waits) ? waits : []
  const time = iso => { const n = Date.parse(iso); return Number.isNaN(n) ? null : n }
  const facts = []
  const deadlines = list.map(l => l.watch.deadline).filter(d => time(d) !== null).sort((a, b) => time(a) - time(b))
  const checks = list.map(l => l.watch.last_check).filter(d => time(d) !== null).sort((a, b) => time(b) - time(a))
  const paused = list.filter(l => (l.watch.phase || 'waiting') === 'paused').length
  if (paused > 0) facts.push({ key: 'paused', text: t('loops.wait.group.paused', { count: paused }) })
  if (deadlines.length > 0) {
    facts.push({ key: 'deadline', text: t('loops.wait.group.deadline', { time: hhmm(deadlines[0]), when: inSpan(t, deadlines[0], now) }) })
  }
  if (checks.length > 0) facts.push({ key: 'checked', text: t('loops.wait.group.lastCheck', { ago: ago(t, checks[0], now) }) })
  return { count: list.length, paused, facts }
}

// A short line for a list: a looping sub-agent's row, the answer to /loop list.
export function loopSummary(t, loop, now = Date.now(), withPhase = true) {
  if (isWait(loop)) {
    const w = loop.watch
    const parts = withPhase ? [t(`loops.wait.phase.${w.phase || 'waiting'}`)] : []
    if (w.phase === 'waiting' && w.next_check) parts.push(t('loops.wait.next', { when: inSpan(t, w.next_check, now) }))
    if (w.phase === 'ended' && w.end) parts.push(t(`loops.wait.endShort.${w.end}`))
    return parts.join(' · ')
  }
  const parts = [loop.interval_seconds > 0 ? t('loops.chip.every', { every: loopEvery(t, loop.interval_seconds, true) }) : t('loops.chip.selfPaced')]
  if (loop.runs > 0) parts.push(t('loops.chip.run', { count: loop.runs }))
  if (loop.state === 'paused') parts.push(t('loops.chip.paused'))
  return parts.join(' · ')
}
