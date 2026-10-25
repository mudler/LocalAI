// Helpers for the Jobs area: tasks, their schedules and the jobs they start.
// Everything here reads the fields the agent jobs API returns and nothing else.
// There is no token count, no per-step timing and no next-run time, because the
// API has none (see the notes on cronWords for why the next run is left out).

const DAY_MS = 86400000

// ---- Parameters ---------------------------------------------------------

// "key=value" lines into a map, as the task form and the run dialog send them.
export function parseKeyValues(text) {
  const params = {}
  String(text || '').split('\n').forEach(line => {
    const [key, ...rest] = line.split('=')
    if (key?.trim() && rest.length > 0) params[key.trim()] = rest.join('=').trim()
  })
  return params
}

export function formatKeyValues(map) {
  if (!map || typeof map !== 'object') return typeof map === 'string' ? map : ''
  return Object.entries(map).map(([k, v]) => `${k}=${v}`).join('\n')
}

// The gaps a prompt template leaves to fill: {{.topic}} gives "topic".
export function promptParams(prompt) {
  const found = []
  for (const m of String(prompt || '').matchAll(/\{\{\s*\.([A-Za-z_][\w]*)\s*\}\}/g)) {
    if (!found.includes(m[1])) found.push(m[1])
  }
  return found
}

// The prompt as a run sent it: each {{.name}} replaced by its value. A name with
// no value stays as written.
export function fillPrompt(prompt, params) {
  let out = String(prompt || '')
  for (const [key, value] of Object.entries(params || {})) {
    out = out.replace(new RegExp(`\\{\\{\\s*\\.${key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*\\}\\}`, 'g'), () => String(value))
  }
  return out
}

// ---- Cron ---------------------------------------------------------------
//
// LocalAI reads five fields (minute hour day month weekday) and the @hourly
// style shortcuts and "@every 5m". It uses the server's own time zone and the
// browser cannot know it, so the page says what a schedule means in words and
// does not compute a next run.

const MONTHS = ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec']
const WEEKDAYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat']

const FIELDS = [
  { name: 'minute', min: 0, max: 59 },
  { name: 'hour', min: 0, max: 23 },
  { name: 'day', min: 1, max: 31 },
  { name: 'month', min: 1, max: 12, names: MONTHS, base: 1 },
  { name: 'weekday', min: 0, max: 6, names: WEEKDAYS, base: 0 },
]

function toNumber(token, field) {
  if (/^\d+$/.test(token)) return Number(token)
  if (field.names) {
    const i = field.names.indexOf(token.toLowerCase())
    if (i >= 0) return i + field.base
  }
  return NaN
}

// One field. Returns null when it is not valid for the field.
function parseField(text, field) {
  const values = new Set()
  let star = false
  let stepOfStar = null
  for (const part of text.split(',')) {
    if (!part) return null
    const [range, stepText, extra] = part.split('/')
    if (extra !== undefined) return null
    let step = 1
    if (stepText !== undefined) {
      if (!/^\d+$/.test(stepText) || Number(stepText) < 1) return null
      step = Number(stepText)
    }
    let lo
    let hi
    if (range === '*' || (range === '?' && (field.name === 'day' || field.name === 'weekday'))) {
      lo = field.min
      hi = field.max
      star = true
      if (step > 1) stepOfStar = step
    } else if (range.includes('-')) {
      const [a, b, more] = range.split('-')
      if (more !== undefined) return null
      lo = toNumber(a, field)
      hi = toNumber(b, field)
    } else {
      lo = toNumber(range, field)
      hi = stepText !== undefined ? field.max : lo
    }
    if (Number.isNaN(lo) || Number.isNaN(hi) || lo < field.min || hi > field.max || lo > hi) return null
    for (let v = lo; v <= hi; v += step) values.add(v)
  }
  const sorted = [...values].sort((a, b) => a - b)
  return {
    star: star && stepOfStar === null && values.size === field.max - field.min + 1,
    stepOfStar,
    values: sorted,
    single: sorted.length === 1 && !star ? sorted[0] : null,
  }
}

const DESCRIPTORS = ['@yearly', '@annually', '@monthly', '@weekly', '@daily', '@midnight', '@hourly']
const EVERY = /^@every\s+((?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+)$/i

// null when the expression is fine or empty, else { id, field? } for the page
// to turn into a sentence.
export function validateCron(expr) {
  const text = String(expr || '').trim()
  if (!text) return null
  if (text.startsWith('@')) {
    if (DESCRIPTORS.includes(text.toLowerCase()) || EVERY.test(text)) return null
    return { id: 'descriptor' }
  }
  const parts = text.split(/\s+/)
  if (parts.length !== 5) return { id: 'fields', count: parts.length }
  for (let i = 0; i < 5; i++) {
    if (!parseField(parts[i], FIELDS[i])) return { id: 'field', field: FIELDS[i].name }
  }
  return null
}

const pad = (n) => String(n).padStart(2, '0')
export const clock = (hour, minute) => `${pad(hour)}:${pad(minute)}`

// A schedule in words, as data the page turns into a sentence in the user's
// language: { id, ...values }, or null when no plain wording fits (the page then
// shows the expression itself).
export function cronWords(expr) {
  const text = String(expr || '').trim()
  if (!text || validateCron(text)) return null
  const lower = text.toLowerCase()
  if (lower.startsWith('@')) {
    if (lower === '@hourly') return { id: 'hourly', minute: 0 }
    if (lower === '@daily' || lower === '@midnight') return { id: 'daily', time: '00:00' }
    if (lower === '@weekly') return { id: 'weekly', days: [0], time: '00:00' }
    if (lower === '@monthly') return { id: 'monthly', day: 1, time: '00:00' }
    if (lower === '@yearly' || lower === '@annually') return null
    const m = text.match(/^@every\s+(\d+)(s|m|h)$/i)
    if (m && Number(m[1]) > 0) return { id: 'every', n: Number(m[1]), unit: { s: 'second', m: 'minute', h: 'hour' }[m[2].toLowerCase()] }
    return null
  }
  const [mi, ho, da, mo, dw] = text.split(/\s+/).map((p, i) => parseField(p, FIELDS[i]))
  const monthAny = mo.star
  if (mi.star && ho.star && da.star && monthAny && dw.star) return { id: 'everyMinute' }
  if (mi.stepOfStar && mi.values.length > 1 && ho.star && da.star && monthAny && dw.star) return { id: 'everyMinutes', n: mi.stepOfStar }
  if (mi.single !== null && ho.star && da.star && monthAny && dw.star) return { id: 'hourly', minute: mi.single }
  if (mi.single !== null && ho.stepOfStar && ho.values.length > 1 && da.star && monthAny && dw.star) {
    return { id: 'everyHours', n: ho.stepOfStar, minute: mi.single }
  }
  if (mi.single !== null && ho.single !== null && monthAny) {
    const time = clock(ho.single, mi.single)
    if (da.star && dw.star) return { id: 'daily', time }
    if (da.star && dw.values.join() === '1,2,3,4,5') return { id: 'weekdays', time }
    if (da.star && !dw.star) return { id: 'weekly', days: dw.values, time }
    if (da.single !== null && dw.star) return { id: 'monthly', day: da.single, time }
  }
  return null
}

// The presets the form offers. "hourly" is on the hour; daily and weekdays take
// a time of day ("07:00").
export function buildPreset(kind, time = '09:00') {
  if (kind === 'hourly') return '0 * * * *'
  const [h, m] = String(time).split(':').map(n => parseInt(n, 10))
  const hour = Number.isFinite(h) ? Math.min(23, Math.max(0, h)) : 9
  const minute = Number.isFinite(m) ? Math.min(59, Math.max(0, m)) : 0
  if (kind === 'daily') return `${minute} ${hour} * * *`
  if (kind === 'weekdays') return `${minute} ${hour} * * 1-5`
  return ''
}

// Which preset an expression is: 'none', 'hourly', 'daily', 'weekdays' or
// 'custom'. { kind, time } so the form can show the time.
export function presetOf(expr) {
  const text = String(expr || '').trim()
  if (!text) return { kind: 'none' }
  const w = cronWords(text)
  if (w?.id === 'hourly' && w.minute === 0) return { kind: 'hourly' }
  if (w?.id === 'daily' && !text.startsWith('@')) return { kind: 'daily', time: w.time }
  if (w?.id === 'weekdays') return { kind: 'weekdays', time: w.time }
  return { kind: 'custom' }
}

// ---- Jobs ---------------------------------------------------------------

export function isActive(job) {
  return job?.status === 'running' || job?.status === 'pending'
}

// The state names the Agents area already draws (StatusMark).
export function jobMark(status) {
  if (status === 'completed') return 'done'
  if (status === 'failed') return 'failed'
  if (status === 'running') return 'running'
  if (status === 'cancelled') return 'stopped'
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

// The first line of a result that says something: no code fence, table row,
// rule or empty line, and without the markdown marks.
function plain(text) {
  const body = String(text || '').replace(/```[\s\S]*?```/g, '\n')
  for (const raw of body.split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith('|') || /^[-*_=\s]{3,}$/.test(line)) continue
    const clean = line
      .replace(/^[#>\s]+/, '')
      .replace(/^[-*+]\s+/, '')
      .replace(/\[(.*?)\]\(.*?\)/g, '$1')
      .replace(/[*_`]/g, '')
      .replace(/\s+/g, ' ')
      .trim()
    if (clean) return clean
  }
  return ''
}

function cut(text, max) {
  if (text.length <= max) return text
  const at = text.lastIndexOf(' ', max - 1)
  return `${text.slice(0, at > max * 0.6 ? at : max - 1).trimEnd()}…`
}

// What happened, in one line: { kind, text }. kind is 'result', 'error',
// 'noOutput', 'cancelled', 'running' or 'pending'; the page words the kinds
// that carry no text of their own.
export function jobLine(job, max = 110) {
  const status = job?.status
  if (status === 'failed') return { kind: 'error', text: cut(String(typeof job.error === 'string' ? job.error : JSON.stringify(job.error || '')).replace(/\s+/g, ' ').trim(), max) }
  if (status === 'completed') {
    const result = plain(typeof job.result === 'string' ? job.result : JSON.stringify(job.result || ''))
    return result ? { kind: 'result', text: cut(result, max) } : { kind: 'noOutput', text: '' }
  }
  if (status === 'cancelled') return { kind: 'cancelled', text: '' }
  if (status === 'running') return { kind: 'running', text: '' }
  return { kind: 'pending', text: '' }
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
    finished: count('completed'),
    failed: count('failed'),
    stopped: count('cancelled'),
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
