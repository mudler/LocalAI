/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Fragment, useState, useEffect, useCallback, useMemo } from 'react'
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatAgentsApi, chatTriggersApi } from '../utils/api'
import { browserZone } from '../utils/chatTrigger'
import {
  failingTasks, groupByDay, jobDurationMs, jobOfRun, jobsOf, statusCounts, weekSummary,
} from '../utils/agentJobs'
import { formatDuration } from '../utils/agentRuns'
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
import ActionMenu from '../components/ActionMenu'
import { JobMark, JobStrip } from '../components/agents/JobBits'
import { jobSentence } from '../utils/agentJobText'
import { AgentViews, StatusMark } from '../components/agents/AgentBits'
import { ClockTime, OutputBlock, RelTime, RunItem } from '../components/agents/RunBits'
import Icon from '../components/Icon'
import { Button, EmptyState, LoadError, Page, PageHeader, Segmented, SkeletonRows, Switch } from '../components/ui'
import './agents.css'
import './agent-jobs.css'

const STATUS_ORDER = ['done', 'failed', 'running', 'queued', 'skipped']
const PAGE = 30

// One sentence about the week, built from the jobs the server returned.
function WeekSentence({ jobs, tasks }) {
  const { t } = useTranslation('agents')
  if (jobs.length === 0) return <p className="aj-sum" data-testid="jobs-summary">{t('jobs.summary.none')}</p>
  const w = weekSummary(jobs)
  if (w.total === 0) return <p className="aj-sum" data-testid="jobs-summary">{t('jobs.summary.quiet')}</p>
  const parts = []
  if (w.finished) parts.push(t('jobs.summary.finished', { count: w.finished }))
  if (w.failed) parts.push(t('jobs.summary.failed', { count: w.failed }))
  if (w.stopped) parts.push(t('jobs.summary.stopped', { count: w.stopped }))
  if (w.active) parts.push(t('jobs.summary.running', { count: w.active }))
  const failing = failingTasks(tasks, jobs).map(x => x.name || x.id)
  const names = failing.length > 3
    ? t('jobs.summary.andMore', { names: failing.slice(0, 3).join(', '), count: failing.length - 3 })
    : failing.join(', ')
  return (
    <p className="aj-sum" data-testid="jobs-summary">
      <strong>{t('jobs.summary.lead', { count: w.total })}</strong>{' '}
      {parts.join(', ')}.
      {failing.length > 0 && <> {t('jobs.summary.lastFailed', { count: failing.length, names })}</>}
    </p>
  )
}

export default function AgentJobs() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation(['agents', 'chats'])
  const navigate = useNavigate()
  const [tasks, setTasks] = useState([])
  const [jobs, setJobs] = useState([])
  const [proposals, setProposals] = useState([])
  const [agents, setAgents] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [off, setOff] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [taskFilter, setTaskFilter] = useState('all')
  const [expanded, setExpanded] = useState(() => new Set())
  const [limit, setLimit] = useState(PAGE)
  const [toDelete, setToDelete] = useState(null)

  const fetchData = useCallback(async () => {
    try {
      const [list, props] = await Promise.all([chatTriggersApi.list(), chatTriggersApi.proposals().catch(() => ({ triggers: [] }))])
      const triggers = list.triggers || []
      setTasks(triggers)
      setProposals(props.triggers || [])
      setOff('')
      // The runs of every trigger make the history: the server keeps them per trigger.
      const runs = await Promise.all(triggers.map(x => chatTriggersApi.runs(x.id).then(r => (r.runs || []).map(run => jobOfRun(run, x.id))).catch(() => [])))
      setJobs(runs.flat())
      setError(null)
    } catch (err) {
      // Said in the page; a failed poll keeps the rows already on screen.
      if (err?.status === 409 && err.body?.error?.type === 'triggers_off') setOff(err.body.error.message)
      else setError(err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 5000)
    return () => clearInterval(interval)
  }, [fetchData])

  // Names of the agents, for the line under a trigger. Best effort.
  useEffect(() => {
    chatAgentsApi.list().then(r => setAgents(r.agents || [])).catch(() => {})
  }, [])
  const agentName = (id) => agents.find(a => a.id === id)?.name || ''

  const nameOf = useMemo(() => {
    const map = new Map()
    for (const task of tasks) map.set(task.id, task.name || task.id)
    return (job) => map.get(job.task_id) || job.task_id || t('jobs.history.unknownTask')
  }, [tasks, t])

  const act = async (fn, done) => {
    try {
      await fn()
      if (done) addToast(done, 'success')
      fetchData()
    } catch (err) {
      addToast(t('jobs.actionFailed', { message: err.message }), 'error')
    }
  }

  const toggleEnabled = async (task) => {
    const enabled = !task.enabled
    setTasks(prev => prev.map(x => (x.id === task.id ? { ...x, enabled } : x)))
    try {
      await (enabled ? chatTriggersApi.enable(task.id) : chatTriggersApi.disable(task.id))
      addToast(t(enabled ? 'jobs.tasks.turnedOn' : 'jobs.tasks.turnedOff', { name: task.name }), 'success')
    } catch (err) {
      setTasks(prev => prev.map(x => (x.id === task.id ? { ...x, enabled: !enabled } : x)))
      addToast(t('jobs.tasks.toggleFailed', { message: err.message }), 'error')
    }
  }

  const remove = async (task) => {
    setToDelete(null)
    await act(() => chatTriggersApi.remove(task.id), t('jobs.tasks.deleted'))
  }

  const toggleRow = (id) => setExpanded(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  // History: by trigger first, so the status counts follow the trigger chosen.
  const byTask = taskFilter === 'all' ? jobs : jobs.filter(j => j.task_id === taskFilter)
  const counts = statusCounts(byTask)
  const shown = statusFilter === 'all' ? byTask : byTask.filter(j => j.status === statusFilter)
  const visibleGroups = groupByDay(shown.slice(0, limit))
  const filtered = statusFilter !== 'all' || taskFilter !== 'all'

  const emptyTasks = !loading && !error && tasks.length === 0 && proposals.length === 0
  // On an empty page the empty state carries the primary action.
  const header = (
    <>
      <PageHeader
        title={t('jobs.title')}
        supporting={t('jobs.subtitle')}
        actions={!off && !emptyTasks && (
          <Button variant="primary" icon="plus" onClick={() => navigate('/app/triggers/new')}>{t('jobs.newTask')}</Button>
        )}
      />
      <AgentViews />
    </>
  )

  if (off) {
    return (
      <Page className="ag-page aj-page" data-testid="jobs-page">
        {header}
        <div role="note"><EmptyState variant="unavailable" title={off} /></div>
      </Page>
    )
  }

  return (
    <Page width="full" className="ag-page aj-page" data-testid="jobs-page">
      {header}

      {loading ? (
        <div aria-label={t('jobs.loading')}><SkeletonRows count={3} /></div>
      ) : error && tasks.length === 0 ? (
        <LoadError title={t('load.triggers')} error={error} onRetry={fetchData} />
      ) : (
        <>
          {error && <LoadError title={t('load.triggersStale')} error={error} onRetry={fetchData} />}
          {proposals.length > 0 && (
            <section className="aj-sec" aria-labelledby="aj-prop-h" data-testid="proposals">
              <div className="aj-sec__head"><h2 className="ag-eyebrow" id="aj-prop-h">{t('chats:triggersPage.proposals')}</h2></div>
              <p className="ag-note">{t('chats:triggersPage.proposalsNote')}</p>
              <div className="ag-runs">
                {proposals.map(p => (
                  <div key={p.id} className="aj-proposal" data-testid="proposal-row">
                    <div className="aj-proposal__text">
                      <span className="aj-name">{p.name}</span>
                      <span className="aj-sub__text">{p.summary}</span>
                      <span className="ag-muted ag-small">{p.prompt}</span>
                    </div>
                    <div className="aj-acts">
                      <Button variant="primary" size="sm" onClick={() => act(() => chatTriggersApi.confirm(p.id, browserZone()))}>{t('chats:triggersPage.confirm')}</Button>
                      <Button variant="ghost" size="sm" onClick={() => act(() => chatTriggersApi.dismiss(p.id))}>{t('chats:triggersPage.dismiss')}</Button>
                    </div>
                  </div>
                ))}
              </div>
            </section>
          )}

          {emptyTasks ? (
            <EmptyState
              testId="jobs-empty"
              title={t('jobs.empty.title')}
              body={t('jobs.empty.text')}
              steps={[t('jobs.empty.step1'), t('jobs.empty.step2'), t('jobs.empty.step3')]}
              actions={<Button variant="primary" icon="plus" onClick={() => navigate('/app/triggers/new')}>{t('jobs.createTask')}</Button>}
            />
          ) : (
            <>
              <section className="aj-sec" aria-labelledby="aj-tasks-h">
                <h2 className="dk-sr-only" id="aj-tasks-h">{t('jobs.tasks.title', { count: tasks.length })}</h2>
                {tasks.length === 0 ? (
                  <p className="ag-note">{t('jobs.tasks.noneYet')}</p>
                ) : (
                  <ul className="ag-list aj-tasks" data-testid="jobs-tasks" aria-label={t('jobs.tasks.caption')}>
                    {tasks.map(task => (
                      <TriggerRow
                        key={task.id}
                        task={task}
                        jobs={jobsOf(jobs, task.id)}
                        agent={agentName(task.agent_id)}
                        onToggle={() => toggleEnabled(task)}
                        onRun={() => act(() => chatTriggersApi.run(task.id), t('jobs.tasks.started', { name: task.name || task.id }))}
                        onOpen={() => navigate(`/app/triggers/${encodeURIComponent(task.id)}`)}
                        onEdit={() => navigate(`/app/triggers/${encodeURIComponent(task.id)}/edit`)}
                        onDelete={() => setToDelete(task)}
                      />
                    ))}
                  </ul>
                )}
              </section>

              <section className="aj-sec" aria-labelledby="aj-hist-h">
                <div className="aj-sec__head aj-sec__head--stack">
                  <h2 className="ag-h2" id="aj-hist-h">{t('jobs.history.title')}</h2>
                  <WeekSentence jobs={jobs} tasks={tasks} />
                </div>
                <div className="aj-toolbar">
                  <Segmented
                    label={t('jobs.history.filterStatus')}
                    value={statusFilter}
                    onChange={(s) => setStatusFilter(s)}
                    options={[{ id: 'all', label: t('jobs.history.all'), count: counts.all }, ...STATUS_ORDER.filter(s => counts[s] || statusFilter === s).map(s => ({ id: s, label: t(`jobs.status.${s}`), count: counts[s] || 0 }))]}
                  />
                  <span className="dk-select-wrap aj-taskfilter">
                    <select className="dk-select" value={taskFilter} onChange={(e) => setTaskFilter(e.target.value)} aria-label={t('jobs.history.filterTask')}>
                      <option value="all">{t('jobs.history.allTasks')}</option>
                      {tasks.map(task => <option key={task.id} value={task.id}>{task.name || task.id}</option>)}
                    </select>
                  </span>
                </div>

                {shown.length === 0 ? (
                  <EmptyState
                    testId="jobs-history-empty"
                    level={3}
                    variant={filtered ? 'no-results' : 'first-run'}
                    title={filtered ? t('jobs.history.emptyFiltered') : t('jobs.history.emptyTitle')}
                    body={filtered ? undefined : t('jobs.history.emptyText')}
                    actions={filtered && (
                      <Button variant="ghost" size="sm" onClick={() => { setStatusFilter('all'); setTaskFilter('all') }}>
                        {t('jobs.history.clearFilters')}
                      </Button>
                    )}
                  />
                ) : (
                  <div className="aj-runs" data-testid="jobs-history" aria-label={t('jobs.history.caption')}>
                    {visibleGroups.map(g => (
                      <Fragment key={g.key}>
                        <h3 className="aj-day-head" data-day={g.key}>
                          {g.day ? t(`jobs.history.${g.day}`) : (g.date ? new Date(g.date).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric', year: new Date(g.date).getFullYear() === new Date().getFullYear() ? undefined : 'numeric' }) : '')}
                        </h3>
                        <ul className="aj-runs__list">
                          {g.jobs.map(job => {
                            const name = nameOf(job)
                            const took = jobDurationMs(job)
                            return (
                              <RunItem
                                key={job.id}
                                open={expanded.has(job.id)}
                                onToggle={() => toggleRow(job.id)}
                                toggleLabel={t('jobs.history.details', { name })}
                                error={job.status === 'failed'}
                                data={{ 'data-job': job.id, 'data-status': job.status }}
                                status={<JobMark status={job.status} />}
                                title={<Link to={`/app/triggers/${encodeURIComponent(job.task_id)}`}>{name}</Link>}
                                line={jobSentence(job, t)}
                                when={<ClockTime iso={job.started_at || job.created_at} />}
                                took={took != null ? formatDuration(took) : ''}
                              >
                                <JobDetail job={job} t={t} />
                              </RunItem>
                            )
                          })}
                        </ul>
                      </Fragment>
                    ))}
                    {shown.length > limit && (
                      <div className="aj-more">
                        <Button variant="ghost" size="sm" icon="chevron-down" onClick={() => setLimit(l => l + PAGE)}>
                          {t('tasks.history.more', { count: Math.min(PAGE, shown.length - limit) })}
                        </Button>
                      </div>
                    )}
                  </div>
                )}
              </section>
            </>
          )}
        </>
      )}

      <ChatDeleteConfirm item={toDelete} kind="trigger" onConfirm={remove} onCancel={() => setToDelete(null)} />
    </Page>
  )
}

// The open row of the history: why it failed or was skipped, the file that
// started it, and the way into the chat it started.
function JobDetail({ job, t }) {
  const failed = job.status === 'failed'
  const why = job.reason || job.error
  return (
    <div className={`aj-detail${failed ? ' aj-detail--failed' : ''}`} data-testid="job-detail">
      {why ? (
        <OutputBlock label={failed ? t('jobs.history.error') : t('jobs.history.reason')} text={why} tone={failed ? 'error' : undefined} testId="job-excerpt" />
      ) : (
        <p className="ag-note">{jobSentence(job, t)}</p>
      )}
      {job.file && <p className="ag-note">{t('jobs.history.file', { file: job.file })}</p>}
      {job.chat_id && (
        <div className="aj-detail__acts">
          <Link className="ag-link" to={`/app/chats/${encodeURIComponent(job.chat_id)}`}>{t('jobs.history.openRun')}</Link>
        </div>
      )}
    </div>
  )
}

// One trigger: a tile for its kind, its name, the agent or model that runs
// it and when it runs in words, how its last runs went, and on/off, Run now
// and a menu. The words of the schedule are the server's ("Every weekday at
// 08:00").
function TriggerRow({ task, jobs, agent, onToggle, onRun, onOpen, onEdit, onDelete }) {
  const { t } = useTranslation('agents')
  const last = jobs[0]
  const off = !task.enabled
  const label = task.name || task.id
  const file = task.kind === 'file'
  return (
    <li className="ag-row aj-task" data-row data-task={label} data-off={off || undefined}>
      <span className="ag-tile" aria-hidden="true"><Icon name={file ? 'folder' : 'clock'} /></span>
      <div className="ag-row__main">
        <Link className="ag-row__name" to={`/app/triggers/${encodeURIComponent(task.id)}`}>{label}</Link>
        <span className="aj-task__desc aj-task__when">{task.summary || t('jobs.tasks.noSchedule')}</span>
        <span className="ag-meta">
          {agent
            ? <Link to={`/app/agents/${encodeURIComponent(task.agent_id)}`}><Icon name="robot" />{agent}</Link>
            : <span className="ag-mono"><Icon name="cube" />{task.model || t('jobs.tasks.noAgent')}</span>}
          {!off && Date.parse(task.next_run || '') > Date.now() && <span><Icon name="calendar" />{t('jobs.tasks.next')} <RelTime iso={task.next_run} /></span>}
          {off && <span>{t('jobs.task.disabled')}</span>}
        </span>
      </div>
      <div className="ag-row__record">
        {last ? (
          <>
            <span className="aj-last">
              {last.status === 'failed'
                ? <StatusMark status="failed" label={t('jobs.tasks.lastRunFailedSentence')} />
                : <JobMark status={last.status} />}
              <RelTime className="ag-muted" iso={last.started_at || last.created_at} />
            </span>
            <JobStrip jobs={jobs} />
          </>
        ) : <span className="ag-muted">{t('jobs.tasks.neverRun')}</span>}
      </div>
      <div className="ag-row__end">
        <Switch checked={!off} onChange={onToggle} label={t('jobs.tasks.toggleLabel', { name: label })} />
        <Button size="sm" icon="play" className="aj-task__run" onClick={onRun} aria-label={t('jobs.tasks.runNow')}><span className="aj-acts__word">{t('jobs.tasks.runNow')}</span></Button>
        <ActionMenu
          ariaLabel={t('jobs.tasks.moreFor', { name: label })}
          items={[
            { key: 'run', icon: 'play', label: t('jobs.tasks.runNow'), onClick: onRun },
            { key: 'open', icon: 'eye', label: t('jobs.tasks.open'), onClick: onOpen },
            { key: 'edit', icon: 'edit', label: t('jobs.tasks.edit'), onClick: onEdit },
            { divider: true },
            { key: 'delete', icon: 'trash', label: t('jobs.tasks.delete'), danger: true, onClick: onDelete },
          ]}
        />
      </div>
    </li>
  )
}
