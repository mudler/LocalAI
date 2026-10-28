// Job wording that needs the translator: pure functions that take the t
// function of the agents namespace.
import { jobLine } from './agentJobs'
import { formatWhen } from '../components/agents/AgentBits'

// What a run did, in a sentence. A failed or skipped run says why; a finished
// one says that the chat it started holds the answer.
export function jobSentence(job, t, max = 110) {
  const line = jobLine(job, max)
  switch (line.kind) {
    case 'error': return line.text || t('jobs.history.noError')
    case 'skipped': return line.text || t('jobs.history.cancelledLine')
    case 'done': return job.file ? t('jobs.history.doneFile', { file: job.file }) : t('jobs.history.doneLine')
    case 'running': return t('jobs.history.runningLine', { when: formatWhen(Date.parse(job.started_at || job.created_at)) })
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
