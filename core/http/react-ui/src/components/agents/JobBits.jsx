import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { StatusMark } from './AgentBits'
import { jobMark, stripOfJobs, STRIP } from '../../utils/agentJobs'

// A job's state in the words the server uses, with the Agents area's dot.
export function JobMark({ status }) {
  const { t } = useTranslation('agents')
  return <StatusMark status={jobMark(status)} label={t(`jobs.status.${status}`, { defaultValue: status || '?' })} />
}

// The schedule of a trigger in the words the server wrote ("Every weekday at
// 08:00"), or the folder it watches.
export function ScheduleText({ trigger, off = false }) {
  const { t } = useTranslation('agents')
  if (!trigger?.summary) return <span className="ag-muted">{t('jobs.tasks.noSchedule')}</span>
  return (
    <span className="aj-sched" data-off={off || undefined}>
      <span className="aj-sched__words">{trigger.summary}</span>
      {trigger.kind === 'schedule' && trigger.time_zone && <code className="aj-sched__cron">{trigger.time_zone}</code>}
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
