// eslint-disable-next-line no-unused-vars
import { Fragment, useState, useEffect, useCallback, useMemo } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useNavigate, useOutletContext, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentJobsApi, modelsApi } from '../utils/api'
import { fromState } from '../utils/editorNav'
import { useModels } from '../hooks/useModels'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import { useModelRemoval } from '../hooks/useModelRemoval'
import { UNDO_MS } from '../utils/cleanupPlan'
import {
  failingTasks, groupByDay, isActive, jobDurationMs, jobLine, jobsOf, statusCounts, weekSummary,
} from '../utils/agentJobs'
import { formatDuration } from '../utils/agentRuns'
// eslint-disable-next-line no-unused-vars
import LoadingSpinner from '../components/LoadingSpinner'
// eslint-disable-next-line no-unused-vars
import UserGroupSection from '../components/UserGroupSection'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
// eslint-disable-next-line no-unused-vars
import HomeUndoToast from '../components/home/HomeUndoToast'
// eslint-disable-next-line no-unused-vars
import RunTaskDialog from '../components/agents/RunTaskDialog'
// eslint-disable-next-line no-unused-vars
import { JobMark, JobStrip, ScheduleText } from '../components/agents/JobBits'
import { jobSentence, scheduleWords } from '../utils/agentJobText'
import { formatWhen } from '../components/agents/AgentBits'
import Icon from '../components/Icon'
import './agents.css'
import './agent-jobs.css'

const STATUS_ORDER = ['completed', 'failed', 'running', 'pending', 'cancelled']

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
  const { t, i18n } = useTranslation('agents')
  const navigate = useNavigate()
  const location = useLocation()
  const { models } = useModels()
  const { isAdmin, authEnabled, user } = useAuth()
  const userMap = useUserMap()
  const [tasks, setTasks] = useState([])
  const [jobs, setJobs] = useState([])
  const [loading, setLoading] = useState(true)
  const [statusFilter, setStatusFilter] = useState('all')
  const [taskFilter, setTaskFilter] = useState('all')
  const [tasksOpen, setTasksOpen] = useState(true)
  const [expanded, setExpanded] = useState(() => new Set())
  const [hasMCPModels, setHasMCPModels] = useState(false)
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [taskUserGroups, setTaskUserGroups] = useState(null)
  const [jobUserGroups, setJobUserGroups] = useState(null)
  const [runTask, setRunTask] = useState(null)

  const fetchData = useCallback(async () => {
    const allUsers = isAdmin && authEnabled
    try {
      const [tr, jr] = await Promise.allSettled([
        agentJobsApi.listTasks(allUsers),
        agentJobsApi.listJobs(allUsers),
      ])
      if (tr.status === 'fulfilled') {
        const tv = tr.value
        // The admin response wraps the list; a plain response is the list.
        if (Array.isArray(tv)) {
          setTasks(tv)
          setTaskUserGroups(null)
        } else if (tv && tv.tasks) {
          setTasks(Array.isArray(tv.tasks) ? tv.tasks : [])
          setTaskUserGroups(tv.user_groups || null)
        } else {
          setTasks([])
          setTaskUserGroups(null)
        }
      }
      if (jr.status === 'fulfilled') {
        const jv = jr.value
        if (Array.isArray(jv)) {
          setJobs(jv)
          setJobUserGroups(null)
        } else if (jv && jv.jobs) {
          setJobs(Array.isArray(jv.jobs) ? jv.jobs : [])
          setJobUserGroups(jv.user_groups || null)
        } else {
          setJobs([])
          setJobUserGroups(null)
        }
      }
    } catch (err) {
      addToast(t('jobs.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast, isAdmin, authEnabled, t])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 5000)
    return () => clearInterval(interval)
  }, [fetchData])

  // Check for MCP-enabled models
  useEffect(() => {
    if (models.length === 0) { setHasMCPModels(false); return }
    let cancelled = false
    Promise.all(
      models.map(m => modelsApi.getConfigJson(m.id).catch(() => null))
    ).then(configs => {
      if (cancelled) return
      const hasMcp = configs.some(cfg => cfg && (cfg.mcp?.remote || cfg.mcp?.stdio))
      setHasMCPModels(hasMcp)
    })
    return () => { cancelled = true }
  }, [models])

  // Deleting a task waits in the browser for the undo window; the delete call is
  // the one the page always used (see useModelRemoval for the hold).
  const removal = useModelRemoval({
    deleteModel: id => agentJobsApi.deleteTask(id),
    loadRunning: async () => new Set(),
    onSettled: ({ removed, failed }) => {
      fetchData()
      if (removed.length > 0) addToast(t('jobs.tasks.deleted'), 'success')
      for (const f of failed) addToast(t('jobs.tasks.deleteFailed', { message: f.message }), 'error')
    },
    onAbandoned: () => addToast(t('jobs.tasks.deleteAbandoned'), 'info'),
  })
  const hidden = new Set(removal.pending?.ids || [])
  const keyOf = (task) => task.id || task.name
  const visibleTasks = tasks.filter(task => !hidden.has(keyOf(task)))
  const nameOf = useMemo(() => {
    const map = new Map()
    for (const task of tasks) map.set(task.id, task.name || task.id)
    return (job) => map.get(job.task_id) || job.task_id || t('jobs.history.unknownTask')
  }, [tasks, t])

  const askDelete = (task) => setConfirmDialog({
    title: t('jobs.tasks.deleteTitle'),
    message: t('jobs.tasks.deleteMessage', { name: task.name || task.id }),
    confirmLabel: t('jobs.tasks.deleteConfirm'),
    danger: true,
    onConfirm: () => {
      setConfirmDialog(null)
      removal.start([{ id: keyOf(task), name: task.name || task.id }])
    },
  })

  const toggleEnabled = async (task) => {
    const enabled = task.enabled === false
    setTasks(prev => prev.map(x => (keyOf(x) === keyOf(task) ? { ...x, enabled } : x)))
    try {
      await agentJobsApi.updateTask(keyOf(task), { ...task, enabled })
      addToast(t(enabled ? 'jobs.tasks.turnedOn' : 'jobs.tasks.turnedOff', { name: task.name || task.id }), 'success')
    } catch (err) {
      setTasks(prev => prev.map(x => (keyOf(x) === keyOf(task) ? { ...x, enabled: !enabled } : x)))
      addToast(t('jobs.tasks.toggleFailed', { message: err.message }), 'error')
    }
  }

  const cancelJob = async (id) => {
    try {
      await agentJobsApi.cancelJob(id)
      addToast(t('jobs.history.cancelled'), 'success')
      fetchData()
    } catch (err) {
      addToast(t('jobs.history.cancelFailed', { message: err.message }), 'error')
    }
  }

  const runAgain = async (job) => {
    try {
      await agentJobsApi.rerunJob(job)
      addToast(t('jobs.job.again'), 'success')
      fetchData()
    } catch (err) {
      addToast(t('jobs.job.againFailed', { message: err.message }), 'error')
    }
  }

  // What this page's old "Clear History" did: cancel the jobs still going. The
  // finished ones were never removed, so the label now says what happens.
  const askStopRunning = () => setConfirmDialog({
    title: t('jobs.stopTitle'),
    message: t('jobs.stopMessage'),
    confirmLabel: t('jobs.stopConfirm'),
    danger: true,
    onConfirm: async () => {
      setConfirmDialog(null)
      try {
        const active = jobs.filter(isActive)
        await Promise.all(active.map(j => agentJobsApi.cancelJob(j.id).catch(() => {})))
        addToast(t('jobs.stopped'), 'success')
        fetchData()
      } catch (err) {
        addToast(t('jobs.stopFailed', { message: err.message }), 'error')
      }
    },
  })

  const toggleRow = (id) => setExpanded(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  // History: by task first, so the status counts follow the task chosen.
  const byTask = taskFilter === 'all' ? jobs : jobs.filter(j => j.task_id === taskFilter)
  const counts = statusCounts(byTask)
  const shown = statusFilter === 'all' ? byTask : byTask.filter(j => j.status === statusFilter)
  const groups = groupByDay(shown)
  const filtered = statusFilter !== 'all' || taskFilter !== 'all'

  const header = (actions = true) => (
    <header className="aj-head">
      <h1 className="aj-title">{t('jobs.title')}</h1>
      {actions && (
        <div className="aj-head__acts">
          <ActionMenu
            ariaLabel={t('jobs.moreJobs')}
            items={[
              { key: 'stop', icon: 'stop', label: t('jobs.stopRunning'), onClick: askStopRunning, disabled: !jobs.some(isActive) },
            ]}
          />
          <button type="button" className="dk-btn dk-btn--primary" onClick={() => navigate('/app/agent-jobs/tasks/new')}>
            <Icon name="plus" /> {t('jobs.newTask')}
          </button>
        </div>
      )}
    </header>
  )

  // Wizard: no models installed
  if (!loading && models.length === 0) {
    return (
      <div className="page page--medium ag-page aj-page">
        {header(false)}
        <div className="ag-empty" data-testid="jobs-no-models">
          <h3>{t('jobs.wizard.noModelsTitle')}</h3>
          <p>{t('jobs.wizard.noModelsText')}</p>
          <div className="ag-empty__acts">
            <button className="dk-btn dk-btn--primary" onClick={() => navigate('/app/models')}>
              <Icon name="store" /> {t('jobs.wizard.browseModels')}
            </button>
            <a className="dk-btn dk-btn--secondary" href="https://localai.io/features/agents/" target="_blank" rel="noopener noreferrer">
              <Icon name="book" /> {t('jobs.wizard.docs')}
            </a>
          </div>
        </div>
      </div>
    )
  }

  // Wizard: models but no MCP
  if (!loading && models.length > 0 && !hasMCPModels && tasks.length === 0) {
    return (
      <div className="page page--medium ag-page aj-page">
        {header(false)}
        <div className="ag-empty" data-testid="jobs-no-mcp">
          <h3>{t('jobs.wizard.noMcpTitle')}</h3>
          <p>{t('jobs.wizard.noMcpText')}</p>
          <p className="ag-note ag-note--bottom">{t('jobs.wizard.mcpExample')}</p>
          <pre className="ag-pre">{`mcp:
  stdio:
    - name: my-tool
      command: /path/to/tool
      args: ["--flag"]`}</pre>
          <div className="ag-empty__acts ag-empty__acts--gap">
            <button className="dk-btn dk-btn--primary" onClick={() => navigate('/app/models?view=installed')}>
              <Icon name="settings" /> {t('jobs.wizard.manageModels')}
            </button>
            <a className="dk-btn dk-btn--secondary" href="https://localai.io/features/agents/" target="_blank" rel="noopener noreferrer">
              <Icon name="book" /> {t('jobs.wizard.docs')}
            </a>
          </div>
        </div>
      </div>
    )
  }

  const emptyTasks = visibleTasks.length === 0 && !taskUserGroups && !removal.pending

  return (
    <div className="page page--wide ag-page aj-page" data-testid="jobs-page">
      {header()}

      {loading ? (
        <div className="loading-center" aria-label={t('jobs.loading')}><LoadingSpinner size="lg" /></div>
      ) : (
        <>
          {emptyTasks ? (
            <div className="aj-empty" data-testid="jobs-empty">
              <h2>{t('jobs.empty.title')}</h2>
              <p>{t('jobs.empty.text')}</p>
              <ol className="aj-steps">
                <li>{t('jobs.empty.step1', { gap: '{{.name}}' })}</li>
                <li>{t('jobs.empty.step2')}</li>
                <li>{t('jobs.empty.step3')}</li>
              </ol>
              <div className="ag-empty__acts">
                <button type="button" className="dk-btn dk-btn--primary" onClick={() => navigate('/app/agent-jobs/tasks/new')}>
                  <Icon name="plus" /> {t('jobs.createTask')}
                </button>
                <a className="dk-btn dk-btn--ghost" href="https://localai.io/features/agents/" target="_blank" rel="noopener noreferrer">
                  <Icon name="book" /> {t('jobs.empty.docs')}
                </a>
              </div>
            </div>
          ) : (
            <>
              <WeekSentence jobs={jobs} tasks={tasks} />

              <section className="aj-sec" aria-labelledby="aj-tasks-h">
                <div className="aj-sec__head">
                  <h2 className="ag-eyebrow" id="aj-tasks-h">{t('jobs.tasks.title', { count: visibleTasks.length })}</h2>
                  <button type="button" className="ag-btn-quiet" aria-expanded={tasksOpen} aria-controls="aj-tasks" onClick={() => setTasksOpen(v => !v)}>
                    <Icon name={tasksOpen ? 'chevron-up' : 'chevron-down'} /> {tasksOpen ? t('jobs.tasks.hide') : t('jobs.tasks.show')}
                  </button>
                </div>
                {tasksOpen && (visibleTasks.length === 0 ? (
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
                        {visibleTasks.map(task => {
                          const mine = jobsOf(jobs, task.id)
                          const last = mine[0]
                          const off = task.enabled === false
                          const label = task.name || task.id
                          return (
                            <tr key={keyOf(task)} data-row data-task={label}>
                              <td>
                                <Link className="aj-name" to={`/app/agent-jobs/tasks/${keyOf(task)}`}>{label}</Link>
                                <span className="aj-sub">
                                  {task.description && <span className="aj-sub__text">{task.description}</span>}
                                  {task.model
                                    ? <Link className="aj-model" to={`/app/model-editor/${encodeURIComponent(task.model)}`} state={fromState(location, t('jobs.title'))}>{task.model}</Link>
                                    : <span className="ag-muted">{t('jobs.tasks.noModel')}</span>}
                                </span>
                                <span className="aj-phone-line">{task.cron ? (scheduleWords(task.cron, t, i18n.language) || t('jobs.cron.custom')) : t('jobs.tasks.noSchedule')}{last ? `. ${last.status === 'failed' ? t('jobs.tasks.lastRunFailedSentence') : t('jobs.tasks.lastRun', { when: formatWhen(last.created_at) })}` : ''}</span>
                              </td>
                              <td className="dk-hide-phone"><ScheduleText expr={task.cron} off={off} /></td>
                              <td className="dk-hide-phone">
                                <div className="aj-record">
                                  <JobStrip jobs={mine} />
                                  <span className="ag-muted ag-small">
                                    {last
                                      ? (last.status === 'failed'
                                        ? <>{t('jobs.tasks.lastRun', { when: formatWhen(last.created_at) })}, {t('jobs.tasks.lastRunFailed')}</>
                                        : t('jobs.tasks.lastRun', { when: formatWhen(last.created_at) }))
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
                                  <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setRunTask(task)} title={t('jobs.tasks.runNow')}>
                                    <Icon name="play" /> <span className="aj-acts__word">{t('jobs.tasks.runNow')}</span>
                                  </button>
                                  <ActionMenu
                                    ariaLabel={t('jobs.tasks.moreFor', { name: label })}
                                    items={[
                                      { key: 'edit', icon: 'edit', label: t('jobs.tasks.edit'), onClick: () => navigate(`/app/agent-jobs/tasks/${keyOf(task)}/edit`) },
                                      { divider: true },
                                      { key: 'delete', icon: 'trash', label: t('jobs.tasks.delete'), danger: true, onClick: () => askDelete(task) },
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
                      {tasks.map(task => <option key={keyOf(task)} value={task.id}>{task.name || task.id}</option>)}
                    </select>
                  </span>
                </div>

                {shown.length === 0 ? (
                  <div className="ag-empty" data-testid="jobs-history-empty">
                    <h3>{filtered ? t('jobs.history.emptyFiltered') : t('jobs.history.emptyTitle')}</h3>
                    {!filtered && <p>{t('jobs.history.emptyText')}</p>}
                    {filtered && (
                      <button type="button" className="dk-btn dk-btn--secondary" onClick={() => { setStatusFilter('all'); setTaskFilter('all') }}>
                        {t('jobs.history.clearFilters')}
                      </button>
                    )}
                  </div>
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
                                      <Link className="aj-name" to={`/app/agent-jobs/jobs/${job.id}`}>{name}</Link>
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
                                            {open && <JobDetail job={job} t={t} onAgain={() => runAgain(job)} onCancel={() => cancelJob(job.id)} />}
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

          {taskUserGroups && (
            <UserGroupSection
              title={t('jobs.tasks.others')}
              userGroups={taskUserGroups}
              userMap={userMap}
              currentUserId={user?.id}
              itemKey="tasks"
              renderGroup={(items) => (
                <div className="dk-table-wrap aj-wrap">
                  <table className="dk-table dk-table--compact">
                    <thead>
                      <tr><th scope="col">{t('jobs.tasks.task')}</th><th scope="col">{t('jobs.tasks.model')}</th></tr>
                    </thead>
                    <tbody>
                      {(items || []).map(task => (
                        <tr key={task.id || task.name} data-row>
                          <td><span className="dk-table-name">{task.name || task.id}</span>{task.description && <span className="dk-table-sub">{task.description}</span>}</td>
                          <td className="ag-mono">{task.model || ''}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            />
          )}

          {jobUserGroups && (
            <UserGroupSection
              title={t('jobs.history.others')}
              userGroups={jobUserGroups}
              userMap={userMap}
              currentUserId={user?.id}
              itemKey="jobs"
              renderGroup={(items) => (
                <div className="dk-table-wrap aj-wrap">
                  <table className="dk-table dk-table--compact">
                    <thead>
                      <tr>
                        <th scope="col">{t('jobs.history.outcome')}</th>
                        <th scope="col">{t('jobs.history.task')}</th>
                        <th scope="col" className="dk-num">{t('jobs.history.started')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(items || []).map(job => (
                        <tr key={job.id} data-row>
                          <td><JobMark status={job.status} /></td>
                          <td className="ag-mono">{job.task_id || ''}</td>
                          <td className="dk-num">{formatWhen(job.created_at)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            />
          )}
        </>
      )}

      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger={confirmDialog?.danger}
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => setConfirmDialog(null)}
      />

      {runTask && (
        <RunTaskDialog task={runTask} addToast={addToast} onClose={() => setRunTask(null)} onStarted={() => fetchData()} />
      )}

      {removal.pending && (
        <HomeUndoToast
          key={removal.pending.ids.join('|')}
          testId="task-undo-toast"
          message={removal.pending.phase === 'removing'
            ? t('jobs.tasks.deleting', { name: removal.pending.items[0].name })
            : t('jobs.tasks.undoMessage', { name: removal.pending.items[0].name })}
          undoLabel={t('jobs.tasks.undoLabel')}
          dismissible={false}
          duration={UNDO_MS}
          onUndo={removal.undo}
          onExpire={removal.commit}
        />
      )}
    </div>
  )
}

// The open row of the history: why it failed with one next step, or the start
// of what it returned, and the way into the whole run.
// eslint-disable-next-line no-unused-vars
function JobDetail({ job, t, onAgain, onCancel }) {
  const failed = job.status === 'failed'
  const line = jobLine(job, 1000)
  const body = failed ? line.text : (typeof job.result === 'string' ? job.result : job.result ? JSON.stringify(job.result, null, 2) : '')
  const active = isActive(job)
  return (
    <div className={`aj-detail${failed ? ' aj-detail--failed' : ''}`} data-testid="job-detail">
      {body ? (
        <>
          <span className="ag-eyebrow">{failed ? t('jobs.history.error') : t('jobs.history.result')}</span>
          <pre className="aj-excerpt" data-testid="job-excerpt">{body.length > 900 ? `${body.slice(0, 900)}…` : body}</pre>
        </>
      ) : (
        <p className="ag-note">{active ? jobSentence(job, t) : failed ? t('jobs.history.noError') : t('jobs.history.noResult')}</p>
      )}
      <div className="aj-detail__acts">
        {active
          ? <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={onCancel}><Icon name="stop" /> {t('jobs.history.cancel')}</button>
          : <button type="button" className={`dk-btn dk-btn--sm ${failed ? 'dk-btn--primary' : 'dk-btn--secondary'}`} onClick={onAgain}><Icon name="refresh" /> {t('jobs.history.runAgain')}</button>}
        <Link className="ag-link" to={`/app/agent-jobs/jobs/${job.id}`}>{t('jobs.history.openRun')}</Link>
      </div>
    </div>
  )
}
