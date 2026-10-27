// Helpers for the triggers area: the starts of a trigger ("runs" of the server)
// read as jobs, so the lists can group them by day, count them and draw their
// strip. Everything here reads the fields the triggers API returns and nothing
// else.

const DAY_MS = 86400000
const pad = (n) => String(n).padStart(2, '0')

// ---- Runs as jobs ---------------------------------------------------------

// One start of a trigger (GET /api/chats/triggers/:id/runs) as the job the lists
// draw. The server's states stay as they are: queued, running, done, failed
// and skipped. `reason` says why a run failed or was skipped.
export function jobOfRun(run, taskId) {
  return {
    id: run.id,
    task_id: taskId,
    status: run.status,
    created_at: run.due || run.started,
    started_at: run.started,
    completed_at: run.ended,
    error: run.status === 'failed' || run.status === 'skipped' ? run.reason || '' : '',
    reason: run.reason || '',
    chat_id: run.chat_id || '',
    file: run.file || '',
  }
}

// ---- Jobs ---------------------------------------------------------------

export function isActive(job) {
  return job?.status === 'running' || job?.status === 'queued'
}

// The state names the Agents area already draws (StatusMark).
export function jobMark(status) {
  if (status === 'done') return 'done'
  if (status === 'failed') return 'failed'
  if (status === 'running') return 'running'
  if (status === 'skipped') return 'stopped'
  return 'ready'
}

export function jobTime(job) {
  const t = Date.parse(job?.created_at || '')
  return Number.isNaN(t) ? 0 : t
}

// Real time between the two timestamps the server records, or null.
export function jobDurationMs(job) {
  const a = Date.parse(job?.started_at || '')
  const b = Date.parse(job?.completed_at || '')
  if (Number.isNaN(a) || Number.isNaN(b)) return null
  return Math.max(0, b - a)
}

function cut(text, max) {
  if (text.length <= max) return text
  const at = text.lastIndexOf(' ', max - 1)
  return `${text.slice(0, at > max * 0.6 ? at : max - 1).trimEnd()}…`
}

// What happened, in one line: { kind, text }. kind is 'error', 'skipped', 'done',
// 'running' or 'queued'; the page words the kinds that carry no text of their
// own. A run has no result text: the chat it started holds the answer.
export function jobLine(job, max = 110) {
  const why = cut(String(job?.reason || job?.error || '').replace(/\s+/g, ' ').trim(), max)
  switch (job?.status) {
    case 'failed': return { kind: 'error', text: why }
    case 'skipped': return { kind: 'skipped', text: why }
    case 'done': return { kind: 'done', text: '' }
    case 'running': return { kind: 'running', text: '' }
    default: return { kind: 'queued', text: '' }
  }
}

export function jobsOf(jobs, taskId) {
  return jobs.filter(j => j.task_id === taskId).sort((a, b) => jobTime(b) - jobTime(a))
}

// The last 14 outcomes of one task, oldest first, as the run strip draws them.
export const STRIP = 14
export function stripOfJobs(jobs) {
  return jobs.slice(0, STRIP).map(j => ({ id: j.id, status: jobMark(j.status) })).reverse()
}

// Jobs newest first, grouped by the day they were created in the viewer's
// time zone: [{ key: 'YYYY-MM-DD', day: 'today' | 'yesterday' | null, date, jobs }].
export function groupByDay(jobs, now = Date.now()) {
  const sorted = [...jobs].sort((a, b) => jobTime(b) - jobTime(a))
  const key = (ms) => {
    const d = new Date(ms)
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  }
  const today = key(now)
  const yesterday = key(now - DAY_MS)
  const groups = []
  for (const job of sorted) {
    const ms = jobTime(job)
    const k = ms ? key(ms) : 'unknown'
    let g = groups[groups.length - 1]
    if (!g || g.key !== k) {
      g = { key: k, day: k === today ? 'today' : k === yesterday ? 'yesterday' : null, date: ms, jobs: [] }
      groups.push(g)
    }
    g.jobs.push(job)
  }
  return groups
}

// The week in one count: jobs created in the last seven days.
export function weekSummary(jobs, now = Date.now()) {
  const since = now - 7 * DAY_MS
  const week = jobs.filter(j => jobTime(j) >= since)
  const count = (s) => week.filter(j => j.status === s).length
  return {
    total: week.length,
    finished: count('done'),
    failed: count('failed'),
    stopped: count('skipped'),
    active: week.filter(isActive).length,
  }
}

// Tasks whose most recent job failed, in the order of the task list.
export function failingTasks(tasks, jobs) {
  return tasks.filter(task => {
    const last = jobsOf(jobs, task.id)[0]
    return last?.status === 'failed'
  })
}

export function statusCounts(jobs) {
  const counts = { all: jobs.length }
  for (const j of jobs) counts[j.status] = (counts[j.status] || 0) + 1
  return counts
}
