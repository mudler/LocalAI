import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { StatusMark } from './AgentBits'
import { jobMark, stripOfJobs, STRIP } from '../../utils/agentJobs'

// A job's state in the words the server uses, with the Agents area's dot.
export function JobMark({ status }) {
  const { t } = useTranslation('agents')
  return <StatusMark status={jobMark(status)} label={t(`jobs.status.${status}`, { defaultValue: status || '?' })} />
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
