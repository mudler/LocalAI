// Runs of an agent, as this browser knows them.
//
// The server keeps no run history for an agent. What exists is the stream the
// agent page listens to (messages, status lines, tool calls and results) and
// the chats the older Agent chat page stored in this browser. A run is one
// task and everything the agent did until it answered. This module keeps a
// bounded log of runs in browser storage, writes it while the page watches the
// stream, and reads old stored chats as runs too.
//
// Nothing here is measured by the server. Times are the browser's clock
// between the moment the task was sent and the moment the answer arrived.
// The log holds task text, step text and answers: no keys, no headers.

const RUNS_PREFIX = 'localai_agent_runs_'
const CHATS_PREFIX = 'localai_agent_chats_'

export const MAX_RUNS_PER_AGENT = 50
export const STRIP_LENGTH = 14
// A run still marked as running this long after its last event was cut off:
// the page that watched it was closed before the answer was recorded.
export const STALE_MS = 5 * 60 * 1000

const MAX_TASK = 4000
const MAX_OUTCOME = 30000
const MAX_ARGS = 1500
const MAX_RESULT = 4000
const MAX_STEPS = 80

function scope(userId) {
  return userId ? `${userId}:` : ''
}

export function runsKey(agent, userId) {
  return `${RUNS_PREFIX}${scope(userId)}${agent}`
}

function readJSON(key) {
  try {
    const raw = localStorage.getItem(key)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

function cut(text, max) {
  const s = typeof text === 'string' ? text : text == null ? '' : String(text)
  return s.length > max ? `${s.slice(0, max)}…` : s
}

export function newRunId() {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let id = ''
  for (let i = 0; i < 6; i++) id += alphabet[Math.floor(Math.random() * alphabet.length)]
  return `r_${id}`
}

export function newTurn(task, now = Date.now()) {
  return {
    task: cut(task, MAX_TASK),
    startedAt: now,
    endedAt: null,
    status: 'running',
    outcome: '',
    error: '',
    steps: [],
    live: { reasoning: '', content: '' },
    metadata: null,
  }
}

export function newRun(agent, task, { userId, now = Date.now(), id = newRunId() } = {}) {
  return {
    id,
    agent,
    userId: userId || undefined,
    startedAt: now,
    updatedAt: now,
    turns: [newTurn(task, now)],
  }
}

// ---- Reading what the stream says ------------------------------------------

function flushThought(turn, now) {
  const text = (turn.live.reasoning || '').trim()
  const steps = text ? [...turn.steps, { kind: 'thought', text: cut(text, MAX_RESULT), ts: now }] : turn.steps
  return { ...turn, steps: steps.slice(-MAX_STEPS), live: { ...turn.live, reasoning: '' } }
}

// One `stream_event` from the agent's event stream. `done` marks the boundary
// between the internal generations of one turn, so the text gathered so far is
// folded into a step and the live text starts again.
export function applyStreamEvent(turn, data, now = Date.now()) {
  if (!turn || turn.status !== 'running' || !data) return turn
  switch (data.type) {
    case 'reasoning':
      return { ...turn, live: { ...turn.live, reasoning: turn.live.reasoning + (data.content || '') } }
    case 'content':
      return { ...turn, live: { ...turn.live, content: turn.live.content + (data.content || '') } }
    case 'tool_call': {
      const name = data.tool_name || ''
      const args = data.tool_args || ''
      if (name) {
        const t = flushThought(turn, now)
        return { ...t, steps: [...t.steps, { kind: 'tool', name, args: cut(args, MAX_ARGS), ts: now }].slice(-MAX_STEPS) }
      }
      const idx = turn.steps.findLastIndex(s => s.kind === 'tool' && s.result === undefined)
      if (idx < 0) return turn
      const steps = [...turn.steps]
      steps[idx] = { ...steps[idx], args: cut(steps[idx].args + args, MAX_ARGS) }
      return { ...turn, steps }
    }
    case 'tool_result': {
      const name = data.tool_name || ''
      const idx = turn.steps.findLastIndex(s => s.kind === 'tool' && s.name === name && s.result === undefined)
      if (idx < 0) return turn
      const steps = [...turn.steps]
      steps[idx] = { ...steps[idx], result: cut(data.tool_result || 'done', MAX_RESULT), doneAt: now }
      return { ...turn, steps }
    }
    case 'done': {
      const t = flushThought(turn, now)
      return { ...t, live: { reasoning: '', content: '' } }
    }
    default:
      return turn
  }
}

// A `status` line ("Thinking...", "Action taken: ...").
export function applyStatusLine(turn, text, now = Date.now()) {
  if (!turn || turn.status !== 'running' || !text) return turn
  return { ...turn, steps: [...turn.steps, { kind: 'status', text: cut(text, 400), ts: now }].slice(-MAX_STEPS) }
}

export function finishTurn(turn, { content, metadata } = {}, now = Date.now()) {
  const t = flushThought(turn, now)
  return {
    ...t,
    status: 'done',
    endedAt: now,
    outcome: cut(content || t.live.content || '', MAX_OUTCOME),
    metadata: metadata && Object.keys(metadata).length ? metadata : null,
    live: { reasoning: '', content: '' },
  }
}

// kind says where it broke: 'send' when the task never reached the agent,
// 'agent' when the agent reported an error after it started.
export function failTurn(turn, message, now = Date.now(), kind = 'agent') {
  const t = flushThought(turn, now)
  return { ...t, status: 'failed', endedAt: now, error: cut(message || '', 600), errorKind: kind, live: { reasoning: '', content: '' } }
}

// ---- Reading a run -----------------------------------------------------------

export function lastTurn(run) {
  return run?.turns?.[run.turns.length - 1] || null
}

// The state a reader should see. A run that says running but has not been
// touched for a while was cut off and reads as stopped.
export function effectiveStatus(run, now = Date.now()) {
  const turn = lastTurn(run)
  if (!turn) return 'stopped'
  if (turn.status === 'running') return now - (run.updatedAt || turn.startedAt) > STALE_MS ? 'stopped' : 'running'
  return turn.status
}

// A finished run's state is its first turn's: later follow-ups do not turn a
// good run into a failed one in the record strip.
export function recordStatus(run, now = Date.now()) {
  const first = run?.turns?.[0]
  if (!first) return 'stopped'
  if (run.turns.length === 1) return effectiveStatus(run, now)
  if (first.status === 'running') return effectiveStatus({ ...run, turns: [first] }, now)
  return first.status
}

export function durationMs(turn) {
  if (!turn || !turn.endedAt || !turn.startedAt) return null
  return Math.max(0, turn.endedAt - turn.startedAt)
}

export function stepCount(turn) {
  return (turn?.steps || []).filter(s => s.kind === 'tool').length
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

// The earlier turns of a run in the shape POST /api/agents/:name/chat takes as
// `history`, so a follow-up sees the task and the answers before it.
export function historyForRun(run) {
  const history = []
  for (const turn of run?.turns || []) {
    if (turn.task?.trim()) history.push({ role: 'user', content: turn.task })
    if (turn.status === 'done' && turn.outcome?.trim()) history.push({ role: 'assistant', content: turn.outcome })
  }
  return history
}

export function recordStats(runs, now = Date.now()) {
  const statuses = runs.map(r => recordStatus(r, now))
  const finished = statuses.filter(s => s === 'done').length
  const failed = statuses.filter(s => s === 'failed').length
  const stopped = statuses.filter(s => s === 'stopped').length
  // Old chats carry the timestamps of two messages, not of a run: no timing.
  const times = runs
    .filter(r => !r.legacy && r.turns?.[0]?.status === 'done')
    .map(r => durationMs(r.turns[0]))
    .filter(v => v != null)
    .sort((a, b) => a - b)
  const median = times.length ? times[Math.floor(times.length / 2)] : null
  return { total: runs.length, finished, failed, stopped, medianMs: median }
}

// The strip: the last STRIP_LENGTH runs, oldest first.
export function stripOf(runs, now = Date.now()) {
  return runs.slice(0, STRIP_LENGTH).map(r => ({ id: r.id, status: recordStatus(r, now) })).reverse()
}

// ---- Old chats as runs -------------------------------------------------------

// The Agent chat page kept conversations in this browser. A user message and
// the agent's next message are a run: when it started, when it was answered,
// and the status lines between them as steps. Ids are stable so the same
// message is the same run on every read.
export function legacyRunsFromChats(agent, userId, chats) {
  const runs = []
  for (const conv of chats?.conversations || []) {
    const msgs = Array.isArray(conv.messages) ? conv.messages : []
    for (let i = 0; i < msgs.length; i++) {
      const m = msgs[i]
      if (m?.sender !== 'user' || !m.content) continue
      const steps = []
      let answer = null
      let j = i + 1
      for (; j < msgs.length; j++) {
        const n = msgs[j]
        if (n.sender === 'user') break
        if (n.sender === 'agent') { answer = n; break }
        if (n.sender === 'system') steps.push({ kind: 'status', text: cut(n.content, 400), ts: n.timestamp })
      }
      const startedAt = m.timestamp || conv.createdAt || 0
      runs.push({
        id: `c_${String(conv.id).slice(0, 8)}_${i}`,
        agent,
        userId: userId || undefined,
        legacy: true,
        startedAt,
        updatedAt: answer?.timestamp || startedAt,
        turns: [{
          task: cut(m.content, MAX_TASK),
          startedAt,
          endedAt: answer ? (answer.timestamp || startedAt) : null,
          status: answer ? 'done' : 'stopped',
          outcome: answer ? cut(answer.content, MAX_OUTCOME) : '',
          error: '',
          steps: steps.slice(-MAX_STEPS),
          live: { reasoning: '', content: '' },
          metadata: answer?.metadata || null,
        }],
      })
      i = answer ? j : i
    }
  }
  return runs
}

// ---- Storage ----------------------------------------------------------------

function readStored(agent, userId) {
  const data = readJSON(runsKey(agent, userId))
  return Array.isArray(data?.runs) ? data.runs : []
}

// Runs newest first: the stored log and the runs read from old chats.
export function loadRuns(agent, userId) {
  const stored = readStored(agent, userId)
  const ids = new Set(stored.map(r => r.id))
  const chats = userId ? null : readJSON(`${CHATS_PREFIX}${agent}`)
  const legacy = legacyRunsFromChats(agent, userId, chats).filter(r => !ids.has(r.id))
  return [...stored, ...legacy].sort((a, b) => b.startedAt - a.startedAt)
}

export function loadRun(agent, id, userId) {
  return loadRuns(agent, userId).find(r => r.id === id) || null
}

function writeRuns(agent, userId, runs) {
  let list = runs.slice(0, MAX_RUNS_PER_AGENT)
  for (let attempt = 0; attempt < 6; attempt++) {
    try {
      localStorage.setItem(runsKey(agent, userId), JSON.stringify({ version: 1, runs: list }))
      return true
    } catch {
      // Quota: drop the older half and try again.
      if (list.length <= 1) return false
      list = list.slice(0, Math.ceil(list.length / 2))
    }
  }
  return false
}

export function saveRun(run) {
  if (!run || run.legacy) return false
  const others = readStored(run.agent, run.userId).filter(r => r.id !== run.id)
  const next = [{ ...run, updatedAt: Date.now() }, ...others].sort((a, b) => b.startedAt - a.startedAt)
  return writeRuns(run.agent, run.userId, next)
}

// Forget the log of one agent: the stored runs and, so they do not come back
// as runs, the old chats they were read from.
export function clearRuns(agent, userId) {
  try {
    localStorage.removeItem(runsKey(agent, userId))
    if (!userId) localStorage.removeItem(`${CHATS_PREFIX}${agent}`)
    return true
  } catch {
    return false
  }
}
