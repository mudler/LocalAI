// The goal of a chat, as the server gives it on the chat ({ text, state,
// paused_by, note, turns, max_turns, steps, max_steps }), and the goal lines of
// the transcript (role goal, goal_event, goal_turn). Pure helpers: the
// components translate the keys they return.

export const GOAL_STATES = ['active', 'paused', 'done', 'blocked']

const PAUSED_BY = ['user', 'turn_limit', 'step_limit', 'error', 'interrupted']

// The key of the sentence that says why a goal paused.
export function pausedWhyKey(by) {
  return `goal.pausedBy.${PAUSED_BY.includes(by) ? by : 'user'}`
}

// What the bar above the composer shows for a goal, or null without one.
//   tone     ok (active), warn (paused, blocked) or done
//   action   resume (paused, blocked), pause (active) or null (done)
//   detailKey + detail  the second line: why it paused, what it needs, what
//            it did
export function goalBar(goal) {
  if (!goal || !GOAL_STATES.includes(goal.state)) return null
  const view = { state: goal.state, text: goal.text || '', tone: 'ok', action: 'pause', detailKey: '', detail: {} }
  switch (goal.state) {
    case 'paused':
      view.tone = 'warn'
      view.action = 'resume'
      view.detailKey = pausedWhyKey(goal.paused_by)
      break
    case 'blocked':
      view.tone = 'warn'
      view.action = 'resume'
      view.detailKey = 'goal.blockedNote'
      view.detail = { note: goal.note || '' }
      break
    case 'done':
      view.tone = 'done'
      view.action = null
      view.detailKey = goal.note ? 'goal.doneNote' : ''
      view.detail = { note: goal.note || '' }
      break
    default:
  }
  return view
}

// The progress of an active goal for the status line: { key, turn, max }, or
// null when the goal is not active. A goal set while one of your turns runs
// has no automatic turn yet.
export function goalProgress(goal) {
  if (!goal || goal.state !== 'active') return null
  const turn = Math.max(0, Number(goal.turns) || 0)
  const max = Math.max(0, Number(goal.max_turns) || 0)
  if (turn === 0 || max === 0) return { key: 'goal.progressStart', turn, max }
  return { key: 'goal.progress', turn: Math.min(turn, max), max }
}

const MARKERS = {
  set: { icon: 'target', tone: 'accent' },
  replaced: { icon: 'target', tone: 'accent' },
  continue: { icon: 'repeat', tone: 'muted' },
  paused: { icon: 'pause', tone: 'warn' },
  resumed: { icon: 'play', tone: 'accent' },
  done: { icon: 'check-circle', tone: 'ok' },
  blocked: { icon: 'alert-circle', tone: 'warn' },
  cleared: { icon: 'close', tone: 'muted' },
}

// How a goal line of the transcript reads: an icon, a tone, the key of its
// label and the text after it. A line of a kind this page does not know shows
// as a plain goal line.
export function goalMarker(msg) {
  const kind = MARKERS[msg?.goal_event] ? msg.goal_event : 'set'
  const m = MARKERS[kind]
  const text = String(msg?.text || '')
  const out = { kind, icon: m.icon, tone: m.tone, labelKey: `goal.markers.${kind}`, params: {}, text: '', textKey: '' }
  switch (kind) {
    case 'continue':
      out.params = { turn: Number(msg.goal_turn) || 0 }
      if (!out.params.turn) out.labelKey = 'goal.markers.continueStart'
      break
    case 'paused':
      out.textKey = pausedWhyKey(text)
      break
    default:
      out.text = text
  }
  return out
}

// The sentence a screen reader hears when the goal changes from prev to next,
// or '' when nothing worth saying changed. t is the chat namespace.
export function goalAnnouncement(prev, next, t) {
  const was = prev?.state || ''
  const now = next?.state || ''
  if (was === now && (prev?.text || '') === (next?.text || '')) return ''
  if (!now) return was ? t('goal.announce.cleared') : ''
  if (now === 'active' && (was === 'paused' || was === 'blocked') && prev?.text === next?.text) return t('goal.announce.resumed')
  switch (now) {
    case 'active': return t('goal.announce.set', { text: next.text })
    case 'paused': return t('goal.announce.paused', { why: t(pausedWhyKey(next.paused_by)) })
    case 'done': return t('goal.announce.done', { note: next.note || '' })
    case 'blocked': return t('goal.announce.blocked', { note: next.note || '' })
    default: return ''
  }
}
