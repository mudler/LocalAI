// eslint-disable-next-line no-unused-vars
import { Fragment, useState, useEffect, useCallback, useMemo } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatAgentsApi, chatTriggersApi } from '../utils/api'
import { browserZone } from '../utils/chatTrigger'
import {
  failingTasks, groupByDay, isActive, jobDurationMs, jobOfRun, jobsOf, statusCounts, weekSummary,
} from '../utils/agentJobs'
import { formatDuration } from '../utils/agentRuns'
// eslint-disable-next-line no-unused-vars
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
// eslint-disable-next-line no-unused-vars
import { JobMark, JobStrip, ScheduleText } from '../components/agents/JobBits'
import { jobSentence } from '../utils/agentJobText'
import { formatWhen } from '../components/agents/AgentBits'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { Button, EmptyState, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import { AgentViews } from '../components/agents/AgentBits'
import './agents.css'
import './agent-jobs.css'

const STATUS_ORDER = ['done', 'failed', 'running', 'queued', 'skipped']

// One sentence about the week, built from the jobs the server returned.
// eslint-disable-next-line no-unused-vars
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

function clockOf(iso) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
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
  const [tasksOpen, setTasksOpen] = useState(true)
  const [expanded, setExpanded] = useState(() => new Set())
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
  const groups = groupByDay(shown)
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
              <WeekSentence jobs={jobs} tasks={tasks} />

              <section className="aj-sec" aria-labelledby="aj-tasks-h">
                <div className="aj-sec__head">
                  <h2 className="ag-eyebrow" id="aj-tasks-h">{t('jobs.tasks.title', { count: tasks.length })}</h2>
                  <Button variant="ghost" size="sm" icon={tasksOpen ? 'chevron-up' : 'chevron-down'} aria-expanded={tasksOpen} aria-controls="aj-tasks" onClick={() => setTasksOpen(v => !v)}>
                    {tasksOpen ? t('jobs.tasks.hide') : t('jobs.tasks.show')}
                  </Button>
                </div>
                {tasksOpen && (tasks.length === 0 ? (
                  <p className="ag-note" id="aj-tasks">{t('jobs.tasks.noneYet')}</p>
                ) : (
                  <div className="dk-table-wrap aj-wrap" role="region" aria-label={t('jobs.tasks.caption')} tabIndex={0} id="aj-tasks">
                    <table className="dk-table aj-table" data-testid="jobs-tasks">
                      <caption className="dk-sr-only">{t('jobs.tasks.caption')}</caption>
                      <thead>
                        <tr>
                          <th scope="col">{t('jobs.tasks.task')}</th>
                          <th scope="col" className="dk-hide-phone">{t('jobs.tasks.schedule')}</th>
                          <th scope="col" className="dk-hide-phone">{t('jobs.tasks.lastRuns')}</th>
                          <th scope="col"><span className="dk-sr-only">{t('jobs.tasks.enabled')}</span></th>
                          <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('jobs.tasks.actions')}</span></th>
                        </tr>
                      </thead>
                      <tbody>
                        {tasks.map(task => {
                          const mine = jobsOf(jobs, task.id)
                          const last = mine[0]
                          const off = !task.enabled
                          const label = task.name || task.id
                          const who = agentName(task.agent_id)
                          return (
                            <tr key={task.id} data-row data-task={label}>
                              <td>
                                <Link className="aj-name" to={`/app/triggers/${encodeURIComponent(task.id)}`}>{label}</Link>
                                <span className="aj-sub">
                                  {who
                                    ? <Link className="aj-model" to={`/app/agents/${encodeURIComponent(task.agent_id)}`}>{who}</Link>
                                    : <span className="ag-muted">{task.model || t('jobs.tasks.noAgent')}</span>}
                                  {task.kind === 'file' && <span className="ag-muted">{t('jobs.tasks.fileKind')}</span>}
                                </span>
                                <span className="aj-phone-line">{task.summary}{last ? `. ${last.status === 'failed' ? t('jobs.tasks.lastRunFailedSentence') : t('jobs.tasks.lastRun', { when: formatWhen(Date.parse(last.created_at)) })}` : ''}</span>
                              </td>
                              <td className="dk-hide-phone"><ScheduleText trigger={task} off={off} /></td>
                              <td className="dk-hide-phone">
                                <div className="aj-record">
                                  <JobStrip jobs={mine} />
                                  <span className="ag-muted ag-small">
                                    {last
                                      ? (last.status === 'failed'
                                        ? <>{t('jobs.tasks.lastRun', { when: formatWhen(Date.parse(last.created_at)) })}, {t('jobs.tasks.lastRunFailed')}</>
                                        : t('jobs.tasks.lastRun', { when: formatWhen(Date.parse(last.created_at)) }))
                                      : t('jobs.tasks.neverRun')}
                                  </span>
                                </div>
                              </td>
                              <td className="dk-table-toggle-cell">
                                <button
                                  type="button"
                                  className="dk-switch"
                                  role="switch"
                                  aria-checked={!off}
                                  aria-label={t('jobs.tasks.toggleLabel', { name: label })}
                                  onClick={() => toggleEnabled(task)}
                                />
                              </td>
                              <td className="dk-table-actions">
                                <div className="aj-acts">
                                  <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => act(() => chatTriggersApi.run(task.id), t('jobs.tasks.started', { name: label }))} title={t('jobs.tasks.runNow')}>
                                    <Icon name="play" className="ui-btn__icon" /><span className="aj-acts__word">{t('jobs.tasks.runNow')}</span>
                                  </button>
                                  <ActionMenu
                                    ariaLabel={t('jobs.tasks.moreFor', { name: label })}
                                    items={[
                                      { key: 'open', icon: 'eye', label: t('jobs.tasks.open'), onClick: () => navigate(`/app/triggers/${encodeURIComponent(task.id)}`) },
                                      { key: 'edit', icon: 'edit', label: t('jobs.tasks.edit'), onClick: () => navigate(`/app/triggers/${encodeURIComponent(task.id)}/edit`) },
                                      { divider: true },
                                      { key: 'delete', icon: 'trash', label: t('jobs.tasks.delete'), danger: true, onClick: () => setToDelete(task) },
                                    ]}
                                  />
                                </div>
                              </td>
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  </div>
                ))}
              </section>

              <section className="aj-sec" aria-labelledby="aj-hist-h">
                <div className="aj-sec__head">
                  <h2 className="ag-eyebrow" id="aj-hist-h">{t('jobs.history.title')}</h2>
                </div>
                <div className="aj-filters" role="group" aria-label={t('jobs.history.filterStatus')}>
                  <button type="button" className="dk-chip" aria-pressed={statusFilter === 'all'} onClick={() => setStatusFilter('all')} data-filter="all">
                    {t('jobs.history.all')} <span className="aj-count">{counts.all}</span>
                  </button>
                  {STATUS_ORDER.filter(s => counts[s] || statusFilter === s).map(s => (
                    <button key={s} type="button" className="dk-chip" aria-pressed={statusFilter === s} onClick={() => setStatusFilter(statusFilter === s ? 'all' : s)} data-filter={s}>
                      {t(`jobs.status.${s}`)} <span className="aj-count">{counts[s] || 0}</span>
                    </button>
                  ))}
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
                  <div className="dk-table-wrap aj-wrap" role="region" aria-label={t('jobs.history.caption')} tabIndex={0}>
                    <table className="dk-table aj-table aj-table--history" data-testid="jobs-history">
                      <caption className="dk-sr-only">{t('jobs.history.caption')}</caption>
                      <thead>
                        <tr>
                          <th scope="col" className="dk-table-toggle-cell"><span className="dk-sr-only">{t('jobs.history.details', { name: '' })}</span></th>
                          <th scope="col">{t('jobs.history.outcome')}</th>
                          <th scope="col">{t('jobs.history.task')}</th>
                          <th scope="col" className="dk-hide-phone">{t('jobs.history.what')}</th>
                          <th scope="col" className="dk-num dk-hide-phone">{t('jobs.history.started')}</th>
                          <th scope="col" className="dk-num dk-hide-phone">{t('jobs.history.took')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {groups.map(g => (
                          <Fragment key={g.key}>
                            <tr className="aj-day" data-day={g.key}>
                              <th scope="colgroup" colSpan={6}>
                                {g.day ? t(`jobs.history.${g.day}`) : (g.date ? new Date(g.date).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric', year: new Date(g.date).getFullYear() === new Date().getFullYear() ? undefined : 'numeric' }) : '')}
                              </th>
                            </tr>
                            {g.jobs.map(job => {
                              const open = expanded.has(job.id)
                              const name = nameOf(job)
                              const took = jobDurationMs(job)
                              const failed = job.status === 'failed'
                              return (
                                <Fragment key={job.id}>
                                  <tr data-row data-job={job.id} data-status={job.status} data-error={failed || undefined}>
                                    <td className="dk-table-toggle-cell">
                                      <button
                                        type="button"
                                        className="dk-table-toggle"
                                        aria-expanded={open}
                                        aria-controls={`aj-d-${job.id}`}
                                        aria-label={t('jobs.history.details', { name })}
                                        onClick={() => toggleRow(job.id)}
                                      >
                                        <Icon name="chevron-right" />
                                      </button>
                                    </td>
                                    <td><JobMark status={job.status} /></td>
                                    <td className="dk-table-name">
                                      <Link className="aj-name" to={`/app/triggers/${encodeURIComponent(job.task_id)}`}>{name}</Link>
                                      <span className="aj-phone-line">{jobSentence(job, t, 70)}{(job.started_at || job.created_at) && !isActive(job) ? `, ${clockOf(job.started_at || job.created_at)}` : ''}</span>
                                    </td>
                                    <td className="dk-hide-phone"><span className="aj-what">{jobSentence(job, t)}</span></td>
                                    <td className="dk-num dk-hide-phone">{clockOf(job.started_at || job.created_at)}</td>
                                    <td className="dk-num dk-hide-phone">{took != null ? formatDuration(took) : ''}</td>
                                  </tr>
                                  <tr className="dk-table-detail" id={`aj-d-${job.id}`}>
                                    <td colSpan={6}>
                                      <div className="dk-collapse" data-open={open}>
                                        <div className="aj-collapse-in">
                                          <div className="dk-table-detail-body">
                                            {open && <JobDetail job={job} t={t} />}
                                          </div>
                                        </div>
                                      </div>
                                    </td>
                                  </tr>
                                </Fragment>
                              )
                            })}
                          </Fragment>
                        ))}
                      </tbody>
                    </table>
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
// eslint-disable-next-line no-unused-vars
function JobDetail({ job, t }) {
  const failed = job.status === 'failed'
  const why = job.reason || job.error
  return (
    <div className={`aj-detail${failed ? ' aj-detail--failed' : ''}`} data-testid="job-detail">
      {why ? (
        <>
          <span className="ag-eyebrow">{failed ? t('jobs.history.error') : t('jobs.history.reason')}</span>
          <pre className="aj-excerpt" data-testid="job-excerpt">{why}</pre>
        </>
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
