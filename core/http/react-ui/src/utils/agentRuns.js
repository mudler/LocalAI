// Runs of an agent, as the server knows them.
//
// A run is a chat that ran with the agent: one the owner started from the
// agent, or one a trigger or a connector started by itself. The server keeps
// the title, the dates and how the last turn ended; this module turns that into
// the record the agent pages show (a strip of the last runs, a success line, a
// row per run). A trigger's own run (a start of the trigger) reads the same way.

export const STRIP_LENGTH = 14

// ---- Reading a run -----------------------------------------------------------

export function lastTurn(run) {
  return run?.turns?.[run.turns.length - 1] || null
}

// The state a reader should see. The server says whether a turn runs, so there
// is no guessing about a run that was cut off.
export function effectiveStatus(run) {
  return lastTurn(run)?.status || 'stopped'
}

// A finished run's state is its first turn's: later follow-ups do not turn a
// good run into a failed one in the record strip.
export function recordStatus(run) {
  const first = run?.turns?.[0]
  if (!first) return 'stopped'
  if (run.turns.length === 1) return effectiveStatus(run)
  if (first.status === 'running') return effectiveStatus({ ...run, turns: [first] })
  return first.status
}

export function durationMs(turn) {
  if (!turn || !turn.endedAt || !turn.startedAt) return null
  return Math.max(0, turn.endedAt - turn.startedAt)
}

export function formatDuration(ms) {
  if (ms == null) return ''
  const s = ms / 1000
  if (s < 10) return `${s.toFixed(1)} s`
  if (s < 60) return `${Math.round(s)} s`
  const m = Math.floor(s / 60)
  const r = Math.round(s - m * 60)
  return `${m} min ${String(r).padStart(2, '0')} s`
}

export function firstLine(text, max = 90) {
  const line = (text || '').replace(/\s+/g, ' ').trim()
  if (line.length <= max) return line
  const cutAt = line.lastIndexOf(' ', max - 1)
  const end = cutAt > max * 0.6 ? cutAt : max - 1
  return `${line.slice(0, end).trimEnd()}…`
}

// A run has no title of its own. Its first sentence of the task stands in.
export function titleOf(task, max = 72) {
  const text = (task || '').replace(/\s+/g, ' ').trim()
  const sentence = text.match(/^.*?[.!?](?=\s|$)/)?.[0] || text
  return firstLine(sentence.replace(/[.!?]$/, ''), max)
}

// The one line that says what a finished run produced: the first sentence of
// its answer, or the error.
export function outcomeLine(run, max = 110) {
  const turn = run?.turns?.[0]
  if (!turn) return ''
  if (turn.status === 'failed') return firstLine(turn.error, max)
  const plain = (turn.outcome || '')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/[#*_`>|]/g, '')
    .replace(/\[(.*?)\]\(.*?\)/g, '$1')
  return firstLine(plain, max)
}

export function recordStats(runs) {
  const statuses = runs.map(r => recordStatus(r))
  const finished = statuses.filter(s => s === 'done').length
  const failed = statuses.filter(s => s === 'failed').length
  const stopped = statuses.filter(s => s === 'stopped').length
  const times = runs
    .filter(r => r.turns?.[0]?.status === 'done')
    .map(r => durationMs(r.turns[0]))
    .filter(v => v != null)
    .sort((a, b) => a - b)
  const median = times.length ? times[Math.floor(times.length / 2)] : null
  return { total: runs.length, finished, failed, stopped, medianMs: median }
}

// The strip: the last STRIP_LENGTH runs, oldest first.
export function stripOf(runs) {
  return runs.slice(0, STRIP_LENGTH).map(r => ({ id: r.id, status: recordStatus(r) })).reverse()
}


// ---- Server rows as runs -----------------------------------------------------

const TURN_STATUS = { done: 'done', failed: 'failed', interrupted: 'failed', stopped: 'stopped' }

function stamp(v) {
  const t = v ? Date.parse(v) : NaN
  return Number.isNaN(t) ? 0 : t
}

// A chat of the agent (GET /api/chats, `agent_id`) as a run. The title stands in
// for the task: the list does not carry the messages.
export function runOfChat(chat) {
  const status = chat.running ? 'running' : TURN_STATUS[chat.last_turn] || 'stopped'
  const startedAt = stamp(chat.created)
  return {
    id: chat.id,
    chatId: chat.id,
    origin: chat.origin || '',
    startedAt,
    turns: [{
      task: chat.title || '',
      startedAt,
      endedAt: status === 'running' ? null : stamp(chat.updated) || startedAt,
      status,
      outcome: '',
      error: '',
    }],
  }
}

const RUN_STATUS = { queued: 'running', running: 'running', done: 'done', failed: 'failed', skipped: 'stopped' }

// One start of a trigger (GET /api/chats/triggers/:id/runs) as a run. `reason`
// says why a run failed or was skipped.
export function runOfTriggerRun(run, task = '') {
  const status = RUN_STATUS[run.status] || 'stopped'
  const startedAt = stamp(run.started) || stamp(run.due)
  return {
    id: run.id,
    chatId: run.chat_id || '',
    file: run.file || '',
    startedAt,
    turns: [{
      task,
      startedAt,
      endedAt: stamp(run.ended) || null,
      status,
      outcome: '',
      error: run.reason || '',
    }],
  }
}
