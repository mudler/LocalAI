// eslint-disable-next-line no-unused-vars
import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { StatusMark } from './AgentBits'
import { jobMark, stripOfJobs, STRIP } from '../../utils/agentJobs'
import { scheduleWords } from '../../utils/agentJobText'

// A job's state in the words the server uses, with the Agents area's dot.
export function JobMark({ status }) {
  const { t } = useTranslation('agents')
  return <StatusMark status={jobMark(status)} label={t(`jobs.status.${status}`, { defaultValue: status || '?' })} />
}

// The schedule of a task: words first, the expression under them in mono.
export function ScheduleText({ expr, off = false }) {
  const { t, i18n } = useTranslation('agents')
  if (!expr) return <span className="ag-muted">{t('jobs.tasks.noSchedule')}</span>
  const words = scheduleWords(expr, t, i18n.language)
  return (
    <span className="aj-sched" data-off={off || undefined}>
      <span className="aj-sched__words">{words || t('jobs.cron.custom')}</span>
      <code className="aj-sched__cron">{expr}</code>
    </span>
  )
}

// The last 14 jobs of a task, oldest first: round finished, square failed,
// hollow cancelled. Empty places keep every task's strip the same width.
export function JobStrip({ jobs }) {
  const { t } = useTranslation('agents')
  const strip = stripOfJobs(jobs)
  const pad = Math.max(0, STRIP - strip.length)
  const label = strip.length
    ? t('strip.label', { count: strip.length, done: strip.filter(s => s.status === 'done').length, failed: strip.filter(s => s.status === 'failed').length })
    : t('strip.none')
  return (
    <span className="ag-strip" role="img" aria-label={label}>
      {Array.from({ length: pad }).map((_, i) => <i key={`e${i}`} className="ag-pip" data-state="empty" />)}
      {strip.map(s => <i key={s.id} className="ag-pip" data-state={s.status} />)}
    </span>
  )
}

// A prompt template with its gaps marked. A gap with a value shows the value;
// a gap without one stays as written, in the warning colour.
export function GappedPrompt({ prompt, params }) {
  const parts = String(prompt || '').split(/(\{\{\s*\.[A-Za-z_]\w*\s*\}\})/g)
  return (
    <pre className="aj-prompt">
      {parts.map((part, i) => {
        const m = part.match(/^\{\{\s*\.([A-Za-z_]\w*)\s*\}\}$/)
        if (!m) return <Fragment key={i}>{part}</Fragment>
        const has = params && Object.prototype.hasOwnProperty.call(params, m[1]) && params[m[1]] !== ''
        return <mark key={i} className="aj-gap" data-filled={has || undefined}>{has ? params[m[1]] : part}</mark>
      })}
    </pre>
  )
}
