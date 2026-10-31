/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ActionMenu from '../ActionMenu'
import { Button } from '../ui'
import { StatusMark } from './AgentBits'
import { ClockTime, OutputBlock, RelTime, RunItem } from './RunBits'
import { jobActive, jobState, startedBy } from '../../utils/agentTasks'
import { jobDurationMs } from '../../utils/agentJobs'
import { firstLine, formatDuration } from '../../utils/agentRuns'

// One job of an agent task as a row of the run history: its state, the task,
// what came out of it in one line, how it started, when and for how long.
// Open, it shows the values it ran with, the answer or the error with Copy,
// its steps, the webhook outcome and the chat that holds it.
export function JobRow({ job, taskName, owner, open, onToggle, onCancel, onRerun, onDelete, showTask = true }) {
  const { t } = useTranslation('agents')
  const navigate = useNavigate()
  const active = jobActive(job)
  const ms = jobDurationMs(job)
  const by = startedBy(job)
  const line = job.status === 'failed'
    ? firstLine(job.error || '', 110) || t('jobs.history.noError')
    : job.status === 'completed'
      ? firstLine(job.result || '', 110) || t('jobs.history.noOutput')
      : t(`tasks.line.${job.status}`, { defaultValue: '' })
  const meta = [by && t(`tasks.startedBy.${by}`), owner].filter(Boolean).join(' · ')
  const mine = !owner
  return (
    <RunItem
      open={open}
      onToggle={onToggle}
      toggleLabel={t('tasks.detailsFor', { name: taskName })}
      error={job.status === 'failed'}
      data={{ 'data-job': job.id, 'data-status': job.status, 'data-testid': 'agent-job-row' }}
      detailTestId="agent-job-detail"
      status={<StatusMark status={jobState(job.status)} label={t(`tasks.state.${job.status}`, { defaultValue: job.status })} />}
      title={showTask ? taskName : line}
      line={showTask ? <>{line}{meta && <span className="aj-run__meta"> · {meta}</span>}</> : meta}
      when={showTask ? <ClockTime iso={job.started_at || job.created_at} /> : <RelTime iso={job.started_at || job.created_at} />}
      took={ms !== null ? formatDuration(ms) : ''}
      actions={mine && (
        <>
          {active
            ? <Button size="sm" onClick={onCancel}>{t('tasks.cancelJob')}</Button>
            : <Button size="sm" variant="ghost" icon="refresh" onClick={onRerun} aria-label={t('tasks.rerunFor', { name: taskName })} title={t('tasks.rerun')} />}
          <ActionMenu
            ariaLabel={t('tasks.jobMoreFor', { name: taskName })}
            items={[
              { key: 'chat', icon: 'chat', label: t('tasks.openChat'), hidden: active, onClick: () => navigate(`/app/chats/${encodeURIComponent(job.id)}`) },
              { key: 'delete', icon: 'trash', label: t('tasks.deleteJob'), danger: true, onClick: onDelete },
            ]}
          />
        </>
      )}
    >
      <JobDetail job={job} />
    </RunItem>
  )
}

// The open row of a job.
export function JobDetail({ job }) {
  const { t } = useTranslation('agents')
  const params = Object.entries(job.parameters || {})
  const active = jobActive(job)
  return (
    <div className={`aj-detail${job.status === 'failed' ? ' aj-detail--failed' : ''}`}>
      {params.length > 0 && (
        <dl className="aj-params" aria-label={t('tasks.output.values')}>
          {params.map(([k, v]) => (
            <div key={k} className="aj-param"><dt>{k}</dt><dd>{v}</dd></div>
          ))}
        </dl>
      )}
      {job.error && <OutputBlock label={t('tasks.output.error')} text={job.error} tone="error" />}
      {job.result && <OutputBlock label={t('tasks.output.answer')} text={job.result} />}
      {!job.result && !job.error && <p className="ag-note">{active ? t('tasks.stillRunning') : t('tasks.noResult')}</p>}
      {(job.traces || []).length > 0 && (
        <details className="aj-steps-fold">
          <summary>{t('tasks.traces', { count: job.traces.length })}</summary>
          <ol className="aj-trace">
            {job.traces.map((tr, i) => (
              <li key={i} className="aj-trace__item">
                <span className="aj-trace__kind">{tr.tool_name || t(`tasks.traceKind.${tr.type}`, { defaultValue: tr.type })}</span>
                <span className="aj-trace__text">{tr.content}</span>
              </li>
            ))}
          </ol>
        </details>
      )}
      {(job.webhook_sent || job.webhook_error) && (
        <p className="ag-note" data-tone={job.webhook_error ? 'error' : undefined}>
          {job.webhook_error ? t('tasks.webhookFailed', { message: job.webhook_error }) : t('tasks.webhookSent')}
        </p>
      )}
      {!active && (
        <div className="aj-detail__acts">
          <Link className="ag-link" to={`/app/chats/${encodeURIComponent(job.id)}`}>{t('tasks.openChat')}</Link>
        </div>
      )}
    </div>
  )
}
