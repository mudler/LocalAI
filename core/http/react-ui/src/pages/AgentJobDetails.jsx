import { useState, useEffect, useRef } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentJobsApi } from '../utils/api'
import { copyToClipboard } from '../utils/clipboard'
import { isActive, jobDurationMs } from '../utils/agentJobs'
import { firstLine, formatDuration } from '../utils/agentRuns'
// eslint-disable-next-line no-unused-vars
import { JobMark, GappedPrompt } from '../components/agents/JobBits'
import { dayWhen } from '../utils/agentJobText'
// eslint-disable-next-line no-unused-vars
import { Chip } from '../components/agents/AgentBits'
// eslint-disable-next-line no-unused-vars
import Prose from '../components/agents/Prose'
// eslint-disable-next-line no-unused-vars
import LoadingSpinner from '../components/LoadingSpinner'
import Icon from '../components/Icon'
import './chat.css'
import './agents.css'
import './agent-jobs.css'

const TRACE_ICON = {
  reasoning: 'brain', tool_call: 'wrench', tool_result: 'check', status: 'info',
  stream_reasoning: 'lightbulb', stream_content: 'pencil', stream_tool_call: 'bolt',
}

function clock(ts) {
  const d = new Date(ts)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

// One entry of what the server recorded: a row that opens into what it holds.
// eslint-disable-next-line no-unused-vars
function TraceRow({ trace, index }) {
  const { t } = useTranslation('agents')
  const [open, setOpen] = useState(false)
  const args = trace.arguments == null ? '' : (typeof trace.arguments === 'string' ? trace.arguments : JSON.stringify(trace.arguments, null, 2))
  const title = trace.tool_name || t(`jobs.job.traceType.${trace.type}`, { defaultValue: trace.type || '?' })
  const sub = trace.tool_name ? t(`jobs.job.traceType.${trace.type}`, { defaultValue: trace.type || '' }) : firstLine(trace.content, 110)
  const has = !!(trace.content || args)
  return (
    <li className="aj-trace" data-open={open || undefined} data-type={trace.type}>
      <button type="button" className="aj-trace__head" aria-expanded={open} disabled={!has} onClick={() => setOpen(v => !v)}>
        <span className="aj-trace__icon" aria-hidden="true"><Icon name={TRACE_ICON[trace.type] || 'info'} /></span>
        <span className="aj-trace__what">
          <span className="aj-trace__title">{trace.tool_name ? <code>{title}</code> : title}</span>
          {sub && <small>{sub}</small>}
        </span>
        <span className="ag-time">{clock(trace.timestamp)}</span>
        <Icon name="chevron-right" className="aj-trace__chev" />
        <span className="dk-sr-only">{index + 1}</span>
      </button>
      {open && (
        <div className="aj-trace__body">
          {trace.content && <pre className="ag-pre">{trace.content}</pre>}
          {args && (
            <>
              <span className="ag-eyebrow">{t('jobs.job.arguments')}</span>
              <pre className="ag-pre">{args}</pre>
            </>
          )}
        </div>
      )}
    </li>
  )
}

export default function AgentJobDetails() {
  const { id } = useParams()
  return <JobDocument key={id} id={id} />
}

// eslint-disable-next-line no-unused-vars
function JobDocument({ id }) {
  const { t } = useTranslation('agents')
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [job, setJob] = useState(null)
  const [task, setTask] = useState(null)
  const [loading, setLoading] = useState(true)
  const [again, setAgain] = useState(false)
  const intervalRef = useRef(null)
  const taskRef = useRef(null)

  useEffect(() => {
    if (!id) return undefined

    const fetchJob = async () => {
      try {
        const data = await agentJobsApi.getJob(id)
        setJob(data)

        // The task the job belongs to, for its name and prompt.
        if (data?.task_id && !taskRef.current) {
          taskRef.current = true
          agentJobsApi.getTask(data.task_id).then(setTask).catch(() => {})
        }

        // Stop polling when the job is done
        if (data && !isActive(data)) {
          if (intervalRef.current) {
            clearInterval(intervalRef.current)
            intervalRef.current = null
          }
        }
      } catch (err) {
        addToast(t('jobs.job.loadFailed', { message: err.message }), 'error')
      } finally {
        setLoading(false)
      }
    }

    fetchJob()
    intervalRef.current = setInterval(fetchJob, 2000)
    return () => { if (intervalRef.current) clearInterval(intervalRef.current) }
  }, [id, addToast, t])

  const cancel = async () => {
    try {
      await agentJobsApi.cancelJob(id)
      addToast(t('jobs.job.cancelled'), 'success')
    } catch (err) {
      addToast(t('jobs.job.cancelFailed', { message: err.message }), 'error')
    }
  }

  const runAgain = async () => {
    setAgain(true)
    try {
      const res = await agentJobsApi.rerunJob(job)
      addToast(t('jobs.job.again'), 'success')
      if (res?.job_id) navigate(`/app/agent-jobs/jobs/${res.job_id}`)
    } catch (err) {
      addToast(t('jobs.job.againFailed', { message: err.message }), 'error')
    } finally {
      setAgain(false)
    }
  }

  const copyLink = async () => {
    const ok = await copyToClipboard(`${window.location.origin}${window.location.pathname}`)
    addToast(t(ok ? 'jobs.job.linkCopied' : 'jobs.job.linkNotCopied'), ok ? 'success' : 'error', 2000)
  }

  if (loading) return <div className="page page--medium loading-center"><LoadingSpinner size="lg" /></div>
  if (!job) {
    return (
      <div className="page page--medium ag-page aj-page">
        <div className="ag-home">
          <Link className="ag-back" to="/app/agent-jobs"><Icon name="arrow-left" /> {t('jobs.job.back')}</Link>
          <div className="ag-empty" data-testid="job-missing">
            <h1 className="ag-title">{t('jobs.job.notFoundTitle')}</h1>
            <p>{t('jobs.job.notFoundText')}</p>
          </div>
        </div>
      </div>
    )
  }

  const active = isActive(job)
  const failed = job.status === 'failed'
  const took = jobDurationMs(job)
  const params = job.parameters && typeof job.parameters === 'object' ? job.parameters : {}
  const paramCount = Object.keys(params).length
  const traces = Array.isArray(job.traces) ? job.traces : []
  const taskName = task?.name || job.task_id
  const title = taskName ? t('jobs.job.title', { task: taskName, when: dayWhen(job.created_at) }) : t('jobs.job.titleUnknown', { id: job.id.slice(0, 8) })
  const who = job.triggered_by || 'manual'
  const media = [['images', job.images], ['videos', job.videos], ['audios', job.audios], ['files', job.files]].filter(([, list]) => list?.length)
  const resultText = typeof job.result === 'string' ? job.result : job.result ? JSON.stringify(job.result, null, 2) : ''
  const errorText = typeof job.error === 'string' ? job.error : job.error ? JSON.stringify(job.error, null, 2) : ''

  return (
    <div className="page page--medium ag-page aj-page" data-testid="job-page" data-status={job.status}>
      <article className="aj-doc">
        <div className="ag-bar">
          <Link className="ag-back" to="/app/agent-jobs"><Icon name="arrow-left" /> {t('jobs.job.back')}</Link>
          <div className="ag-bar__acts">
            {active
              ? <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={cancel}><Icon name="ban" /> {t('jobs.job.cancel')}</button>
              : <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={again || !job.task_id} onClick={runAgain}><Icon name="refresh" /> {t('jobs.job.runAgain')}</button>}
            {job.task_id && <Link className="dk-btn dk-btn--ghost dk-btn--sm" to={`/app/agent-jobs/tasks/${job.task_id}`}><Icon name="edit" /> {t('jobs.job.openTask')}</Link>}
          </div>
        </div>

        <header>
          <h1 className="ag-doc__title">{title}</h1>
          <div className="ag-doc__meta">
            <JobMark status={job.status} />
            <span>{t('jobs.job.startedBy', { who: t(`jobs.trigger.${who}`, { defaultValue: who }) })}</span>
            {took != null && <span>{t('jobs.job.tookFor', { time: formatDuration(took) })}</span>}
            <span className="ag-mono">{job.id}</span>
          </div>
          <div className="ag-doc__chips">
            <span className="ag-addr">
              <span className="ag-addr__text">{window.location.pathname}</span>
              <button type="button" className="ag-btn-quiet" onClick={copyLink}>{t('jobs.job.copyLink')}</button>
            </span>
            {task?.model && <Chip mono title={t('jobs.task.model')}>{task.model}</Chip>}
          </div>
        </header>

        <section className="ag-sec" aria-labelledby="aj-j-task">
          <div className="ag-sec__head"><h2 id="aj-j-task">{t('jobs.job.task')}</h2></div>
          {task?.prompt ? (
            <>
              <GappedPrompt prompt={task.prompt} params={params} />
              <p className="ag-note">{paramCount > 0 ? t('jobs.job.taskNoteParams', { count: paramCount }) : t('jobs.job.taskNote')}</p>
            </>
          ) : (
            <p className="ag-note">{t('jobs.job.taskUnknown')}</p>
          )}
          {paramCount > 0 && !task?.prompt && (
            <div className="ag-chips">
              {Object.entries(params).map(([k, v]) => <span key={k} className="ag-chip ag-chip--mono"><span className="ag-chip__text">{k}={String(v)}</span></span>)}
            </div>
          )}
        </section>

        <section className="ag-sec" aria-labelledby="aj-j-out">
          <div className="ag-sec__head"><h2 id="aj-j-out">{t('jobs.job.outcome')}</h2></div>
          {job.status === 'completed' && (resultText ? <Prose text={resultText} /> : <p className="ag-sec__text">{t('jobs.job.outcomeNone')}</p>)}
          {failed && (
            <div className="ag-fail" role="alert" data-testid="job-failed">
              <h3><Icon name="warning" /> {t('jobs.job.failTitle')}</h3>
              <p>{t('jobs.job.failText')}</p>
              <p className="ag-fail__why">{errorText || t('jobs.job.failNoMessage')}</p>
              <div className="ag-fail__acts">
                <button type="button" className="dk-btn dk-btn--primary" disabled={again || !job.task_id} onClick={runAgain}><Icon name="refresh" /> {t('jobs.job.runAgainNow')}</button>
                {job.task_id && <Link className="ag-link" to={`/app/agent-jobs/tasks/${job.task_id}`}>{t('jobs.job.openTask')}</Link>}
              </div>
            </div>
          )}
          {active && (
            <div className="ag-working" data-testid="job-working">
              <Icon name="spinner" spin />
              <span>{job.status === 'pending' ? t('jobs.job.outcomeWaiting') : t('jobs.job.outcomeWorking')}</span>
            </div>
          )}
          {job.status === 'cancelled' && <p className="ag-sec__text">{t('jobs.job.outcomeCancelled')}</p>}
        </section>

        {media.length > 0 && (
          <section className="ag-sec" aria-labelledby="aj-j-media">
            <div className="ag-sec__head"><h2 id="aj-j-media">{t('jobs.job.media')}</h2></div>
            <div className="ag-chips">
              {media.map(([kind, list]) => <span key={kind} className="ag-chip"><span className="ag-chip__text">{`${list.length} ${t(`jobs.run.${kind}`).toLowerCase()}`}</span></span>)}
            </div>
          </section>
        )}

        {(job.webhook_sent || job.webhook_error) && (
          <section className="ag-sec" aria-labelledby="aj-j-del">
            <div className="ag-sec__head"><h2 id="aj-j-del">{t('jobs.job.delivery')}</h2></div>
            {job.webhook_sent ? (
              <p className="aj-note aj-note--ok" data-testid="job-webhook"><Icon name="check" /> {job.webhook_sent_at ? t('jobs.job.deliverySentAt', { when: dayWhen(job.webhook_sent_at) }) : t('jobs.job.deliverySent')}</p>
            ) : (
              <p className="aj-note aj-note--warn" data-testid="job-webhook"><Icon name="warning" /> {t('jobs.job.deliveryFailed', { message: job.webhook_error })}</p>
            )}
          </section>
        )}

        <section className="ag-sec" aria-labelledby="aj-j-steps">
          <div className="ag-sec__head"><h2 id="aj-j-steps">{t('jobs.job.trace')}</h2></div>
          {traces.length === 0 ? (
            <p className="ag-note">{t('jobs.job.noTrace')}</p>
          ) : (
            <>
              <p className="ag-note">{t('jobs.job.traceNote', { count: traces.length })}</p>
              <ol className="aj-traces" data-testid="job-traces">
                {traces.map((trace, i) => <TraceRow key={i} trace={trace} index={i} />)}
              </ol>
            </>
          )}
        </section>
      </article>
    </div>
  )
}
