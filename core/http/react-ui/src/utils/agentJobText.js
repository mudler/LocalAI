// Job and schedule wording that needs the translator: pure functions that take
// the t function of the agents namespace.
import { cronWords, jobLine } from './agentJobs'
import { formatWhen } from '../components/agents/AgentBits'

function weekdayName(index, lang) {
  // 2023-01-01 was a Sunday.
  return new Intl.DateTimeFormat(lang, { weekday: 'long' }).format(new Date(2023, 0, 1 + index))
}

function list(items, lang) {
  try {
    return new Intl.ListFormat(lang, { style: 'long', type: 'conjunction' }).format(items)
  } catch {
    return items.join(', ')
  }
}

// "Every day at 07:00", or null when the expression has no plain wording.
export function scheduleWords(expr, t, lang) {
  const w = cronWords(expr)
  if (!w) return null
  switch (w.id) {
    case 'everyMinute': return t('jobs.cron.everyMinute')
    case 'everyMinutes': return t('jobs.cron.everyMinutes', { n: w.n })
    case 'hourly': return w.minute === 0 ? t('jobs.cron.hourly') : t('jobs.cron.hourlyAt', { minute: String(w.minute).padStart(2, '0') })
    case 'everyHours': return w.minute === 0
      ? t('jobs.cron.everyHours', { n: w.n })
      : t('jobs.cron.everyHoursAt', { n: w.n, minute: String(w.minute).padStart(2, '0') })
    case 'daily': return t('jobs.cron.daily', { time: w.time })
    case 'weekdays': return t('jobs.cron.weekdays', { time: w.time })
    case 'weekly': return t('jobs.cron.weekly', { days: list(w.days.map(d => weekdayName(d, lang)), lang), time: w.time })
    case 'monthly': return t('jobs.cron.monthly', { day: w.day, time: w.time })
    case 'every': return t(`jobs.cron.each_${w.unit}`, { count: w.n })
    default: return null
  }
}

export function cronErrorText(error, t) {
  if (!error) return ''
  if (error.id === 'fields') return t('jobs.cron.errors.fields', { count: error.count })
  if (error.id === 'field') return t('jobs.cron.errors.field', { field: t(`jobs.cron.fieldNames.${error.field}`) })
  return t('jobs.cron.errors.descriptor')
}

// What a job did, in a sentence. A finished job shows the first words of its
// result, a failed one its error.
export function jobSentence(job, t, max = 110) {
  const line = jobLine(job, max)
  switch (line.kind) {
    case 'result': return line.text
    case 'error': return line.text || t('jobs.history.noError')
    case 'noOutput': return t('jobs.history.noOutput')
    case 'cancelled': return t('jobs.history.cancelledLine')
    case 'running': return t('jobs.history.runningLine', { when: formatWhen(job.started_at || job.created_at) })
    default: return t('jobs.history.pendingLine')
  }
}

// "today 07:00", "yesterday 07:00" or "Oct 5, 07:00": a moment as a title or a
// sentence can carry it.
export function dayWhen(ts, now = Date.now()) {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const today = new Date(now)
  const word = (offset) => {
    try {
      return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(offset, 'day')
    } catch {
      return offset === 0 ? 'today' : 'yesterday'
    }
  }
  if (d.toDateString() === today.toDateString()) return `${word(0)} ${time}`
  if (d.toDateString() === new Date(now - 86400000).toDateString()) return `${word(-1)} ${time}`
  const sameYear = d.getFullYear() === today.getFullYear()
  return `${d.toLocaleDateString([], sameYear ? { month: 'short', day: 'numeric' } : { year: 'numeric', month: 'short', day: 'numeric' })}, ${time}`
}
