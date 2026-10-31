/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentJobsApi, chatAgentsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useModels } from '../hooks/useModels'
import { CAP_CHAT } from '../utils/capabilities'
import {
  JOB_FILTERS, TASK_FILTERS, jobActive, jobFilterCounts, jobInFilter, jobsOfTask, jobState,
  promptParams, taskBody, taskForm, taskInFilter, taskMatches, withOwners,
} from '../utils/agentTasks'
import { groupByDay } from '../utils/agentJobs'
import { AgentViews, StatusMark } from '../components/agents/AgentBits'
import { JobStrip } from '../components/agents/JobBits'
import { RelTime } from '../components/agents/RunBits'
import { JobRow } from '../components/agents/TaskJobs'
import { RunSheet, TaskSheet } from '../components/agents/TaskSheets'
import { TaskPane } from '../components/agents/TaskPane'
import ActionMenu from '../components/ActionMenu'
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
import ConfirmDialog from '../components/ConfirmDialog'
import Icon from '../components/Icon'
import {
  Button, EmptyState, LoadError, Page, PageHeader, PaneLayout, SearchField, Segmented, SkeletonRows, Switch,
} from '../components/ui'
import './agents.css'
import './agent-jobs.css'

const POLL_MS = 3000
// The run strip speaks the trigger states; a job's states map onto them.
const STRIP_STATE = { completed: 'done', failed: 'failed', running: 'running', pending: 'queued', cancelled: 'skipped' }
const PAGE = 30

// Agent tasks: a prompt template that a model or a saved agent runs in the
// background, from this page or over the API (POST /api/agent/jobs/execute or
// /api/agent/tasks/{name}/execute, as CI pipelines do). Each run is a job with
// its status, result, traces and webhook outcome. Webhooks and their headers
// are set over the API; the page keeps them as they are.
export default function AgentTasks() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation(['agents', 'common'])
  const { isAdmin } = useAuth()
  const { models } = useModels(CAP_CHAT)
  const [tasks, setTasks] = useState([])
  const [jobs, setJobs] = useState([])
  const [agents, setAgents] = useState(null) // null until the list is read
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [allUsers, setAllUsers] = useState(false)
  const [editing, setEditing] = useState(null) // { task, form } or null
  const [saving, setSaving] = useState(false)
  const [running, setRunning] = useState(null) // { task, params }
  const [open, setOpen] = useState(() => new Set())
  const [toDelete, setToDelete] = useState(null)
  const [jobToDelete, setJobToDelete] = useState(null)
  const [query, setQuery] = useState('')
  const [taskFilter, setTaskFilter] = useState('all')
  const [jobFilter, setJobFilter] = useState('all')
  const [jobTask, setJobTask] = useState('all')
  const [limit, setLimit] = useState(PAGE)
  const [selected, setSelected] = useState(null) // { id, owner }
  const [paneTab, setPaneTab] = useState('overview')

  const load = useCallback(async () => {
    try {
      const [ts, js] = await Promise.all([agentJobsApi.tasks(allUsers), agentJobsApi.jobs({ limit: 100, allUsers })])
      setTasks(withOwners(ts, 'tasks'))
      setJobs(withOwners(js, 'jobs'))
      setError(null)
    } catch (err) {
      // Said in the page; a failed poll keeps the rows already on screen.
      setError(err)
    } finally {
      setLoading(false)
    }
  }, [allUsers])

  useEffect(() => { load() }, [load])
  const busy = jobs.some(j => jobActive(j.item))
  useEffect(() => {
    if (!busy) return undefined
    const timer = setInterval(load, POLL_MS)
    return () => clearInterval(timer)
  }, [busy, load])
  useEffect(() => {
    chatAgentsApi.list().then(r => setAgents(r.agents || [])).catch(() => setAgents([]))
  }, [])

  const nameOf = useMemo(() => {
    const m = new Map(tasks.map(({ item }) => [item.id, item.name]))
    return (id) => m.get(id) || id
  }, [tasks])
  // Before the agents are read, an agent task says "Agent" rather than flash its id.
  const runnerName = useCallback((task) => {
    if (!task.agent_id) return task.model
    if (agents === null) return t('tasks.agent')
    return agents.find(a => a.id === task.agent_id)?.name || task.agent_id
  }, [agents, t])

  const act = async (fn, done) => {
    try {
      const out = await fn()
      if (done) addToast(done, 'success')
      await load()
      return out ?? true
    } catch (err) {
      addToast(t('tasks.actionFailed', { message: err.message }), 'error')
      return null
    }
  }

  const save = async () => {
    const { task, form } = editing
    const body = taskBody(form, task)
    setSaving(true)
    const ok = await act(() => (task ? agentJobsApi.updateTask(task.id, body) : agentJobsApi.createTask(body)), t('tasks.saved', { name: body.name }))
    setSaving(false)
    if (ok) setEditing(null)
  }

  const run = async (e) => {
    e.preventDefault()
    const { task, params } = running
    setRunning(null)
    await act(() => agentJobsApi.run(task.id, params), t('tasks.started', { name: task.name }))
  }
  // A task that asks for nothing starts at once; one with inputs asks first.
  const startRun = (task) => {
    if (promptParams(task.prompt).length === 0) act(() => agentJobsApi.run(task.id, {}), t('tasks.started', { name: task.name }))
    else setRunning({ task, params: {} })
  }

  // On and off is the enabled field of an update; the rest goes back as is.
  const toggleTask = async (task, enabled) => {
    setTasks(prev => prev.map(r => (r.item.id === task.id ? { ...r, item: { ...r.item, enabled } } : r)))
    const ok = await act(() => agentJobsApi.updateTask(task.id, taskBody({ ...taskForm(task), enabled }, task)), t(enabled ? 'tasks.turnedOn' : 'tasks.turnedOff', { name: task.name }))
    if (!ok) setTasks(prev => prev.map(r => (r.item.id === task.id ? { ...r, item: { ...r.item, enabled: !enabled } } : r)))
  }

  const toggle = (id) => setOpen(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  const setForm = (patch) => setEditing(ed => ({ ...ed, form: { ...ed.form, ...patch } }))

  const visibleTasks = tasks.filter(({ item }) => taskInFilter(item, taskFilter) && taskMatches(item, query, runnerName(item)))
  const taskCounts = Object.fromEntries(TASK_FILTERS.map(f => [f, tasks.filter(({ item }) => taskInFilter(item, f)).length]))
  const plainJobs = jobs.map(j => j.item)
  const byTask = jobTask === 'all' ? jobs : jobs.filter(j => j.item.task_id === jobTask)
  const jobCounts = jobFilterCounts(byTask.map(j => j.item))
  const shownJobs = byTask.filter(j => jobInFilter(j.item, jobFilter))
  const ownerOf = new Map(jobs.map(j => [j.item.id, j.owner]))
  const groups = groupByDay(shownJobs.slice(0, limit).map(j => j.item))
  const jobsFiltered = jobFilter !== 'all' || jobTask !== 'all'
  const tasksFiltered = taskFilter !== 'all' || query.trim() !== ''
  const empty = !loading && !error && tasks.length === 0
  const sel = selected && tasks.find(r => r.item.id === selected.id)
  const newTask = () => setEditing({ task: null, form: taskForm(null) })
  const open1 = (row, tab = 'overview') => { setSelected({ id: row.item.id, owner: row.owner }); setPaneTab(tab) }

  // The pane keeps its own open runs, so a run opened there stays closed in
  // the history below and the other way round.
  const jobProps = (job, scope = '') => ({
    scope,
    open: open.has(scope + job.id),
    onToggle: () => toggle(scope + job.id),
    onCancel: () => act(() => agentJobsApi.cancel(job.id), t('tasks.cancelled')),
    onRerun: () => act(() => agentJobsApi.rerun(job.id), t('tasks.rerunStarted')),
    onDelete: () => setJobToDelete(job),
  })

  return (
    <Page width="full" className="ag-page aj-page" data-testid="agent-tasks-page">
      <PageHeader
        title={t('tasks.title')}
        supporting={t('tasks.lede')}
        actions={!empty && <Button variant="primary" icon="plus" onClick={newTask}>{t('tasks.new')}</Button>}
      />
      <AgentViews />

      {loading ? (
        <SkeletonRows count={4} />
      ) : error && tasks.length === 0 && jobs.length === 0 ? (
        <LoadError title={t('load.tasks')} error={error} onRetry={load} />
      ) : empty ? (
        <EmptyState
          testId="agent-tasks-empty"
          icon="bolt"
          title={t('tasks.empty.title')}
          body={t('tasks.empty.body', { example: '{{.ticket}}' })}
          facts={[{ text: t('tasks.empty.fact1') }, { text: t('tasks.empty.fact2') }, { text: t('tasks.empty.fact3') }]}
          actions={<Button variant="primary" icon="plus" onClick={newTask}>{t('tasks.new')}</Button>}
        />
      ) : (
        <PaneLayout className="aj-layout">
          <div className="aj-main">
            {error && <LoadError title={t('load.tasksStale')} error={error} onRetry={load} />}
            <section className="aj-sec" aria-labelledby="at-tasks-h">
              <h2 className="dk-sr-only" id="at-tasks-h">{t('tasks.tasks')}</h2>
              <div className="aj-toolbar">
                <SearchField value={query} onChange={setQuery} label={t('tasks.searchLabel')} />
                <Segmented
                  label={t('tasks.filter.label')}
                  value={taskFilter}
                  onChange={setTaskFilter}
                  options={TASK_FILTERS.map(f => ({ id: f, label: t(`tasks.filter.${f}`), count: taskCounts[f] }))}
                />
                {isAdmin && (
                  <span className="aj-inline-switch">
                    <span id="at-all-users">{t('tasks.allUsers')}</span>
                    <Switch checked={allUsers} onChange={setAllUsers} labelledBy="at-all-users" />
                  </span>
                )}
              </div>
              {visibleTasks.length === 0 ? (
                <EmptyState
                  variant="no-results"
                  level={3}
                  title={t('tasks.noMatch')}
                  actions={tasksFiltered && <Button variant="ghost" size="sm" onClick={() => { setQuery(''); setTaskFilter('all') }}>{t('jobs.history.clearFilters')}</Button>}
                />
              ) : (
                <ul className="ag-list aj-tasks" data-testid="agent-tasks">
                  {visibleTasks.map(row => (
                    <TaskRow
                      key={`${row.owner}/${row.item.id}`}
                      row={row}
                      runner={runnerName(row.item)}
                      jobs={jobsOfTask(plainJobs, row.item.id)}
                      selected={sel?.item.id === row.item.id}
                      onOpen={(tab) => open1(row, tab)}
                      onRun={() => startRun(row.item)}
                      onEdit={() => setEditing({ task: row.item, form: taskForm(row.item) })}
                      onToggle={(v) => toggleTask(row.item, v)}
                      onDelete={() => setToDelete(row.item)}
                    />
                  ))}
                </ul>
              )}
            </section>

            <section className="aj-sec" aria-labelledby="at-jobs-h">
              <div className="aj-sec__head">
                <h2 className="ag-h2" id="at-jobs-h">{t('tasks.history.title')}</h2>
              </div>
              {jobs.length > 0 && (
                <div className="aj-toolbar">
                  <Segmented
                    label={t('tasks.history.filterLabel')}
                    value={jobFilter}
                    onChange={(f) => { setJobFilter(f); setLimit(PAGE) }}
                    options={JOB_FILTERS.filter(f => f === 'all' || jobCounts[f] > 0 || jobFilter === f).map(f => ({ id: f, label: t(`tasks.history.${f}`), count: jobCounts[f] }))}
                  />
                  <span className="dk-select-wrap aj-taskfilter">
                    <select className="dk-select" value={jobTask} onChange={(e) => { setJobTask(e.target.value); setLimit(PAGE) }} aria-label={t('tasks.task')}>
                      <option value="all">{t('tasks.history.allTasks')}</option>
                      {tasks.map(({ item }) => <option key={item.id} value={item.id}>{item.name}</option>)}
                    </select>
                  </span>
                </div>
              )}
              {shownJobs.length === 0 ? (
                <EmptyState
                  level={3}
                  variant={jobsFiltered ? 'no-results' : 'first-run'}
                  title={jobsFiltered ? t('jobs.history.emptyFiltered') : t('jobs.history.emptyTitle')}
                  body={jobsFiltered ? undefined : t('tasks.history.emptyBody')}
                  actions={jobsFiltered && <Button variant="ghost" size="sm" onClick={() => { setJobFilter('all'); setJobTask('all') }}>{t('jobs.history.clearFilters')}</Button>}
                />
              ) : (
                <div className="aj-runs" data-testid="agent-jobs">
                  {groups.map(g => (
                    <Fragment key={g.key}>
                      <h3 className="aj-day-head">{g.day ? t(`jobs.history.${g.day}`) : new Date(g.date).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })}</h3>
                      <ul className="aj-runs__list">
                        {g.jobs.map(job => (
                          <JobRow key={job.id} job={job} taskName={nameOf(job.task_id)} owner={ownerOf.get(job.id)} {...jobProps(job)} />
                        ))}
                      </ul>
                    </Fragment>
                  ))}
                  {shownJobs.length > limit && (
                    <div className="aj-more">
                      <Button variant="ghost" size="sm" icon="chevron-down" onClick={() => setLimit(l => l + PAGE)}>
                        {t('tasks.history.more', { count: Math.min(PAGE, shownJobs.length - limit) })}
                      </Button>
                    </div>
                  )}
                </div>
              )}
            </section>
          </div>

          <TaskPane
            task={sel?.item}
            owner={sel?.owner}
            runner={sel ? runnerName(sel.item) : ''}
            jobs={sel ? jobsOfTask(plainJobs, sel.item.id) : []}
            tab={paneTab}
            onTab={setPaneTab}
            onClose={() => setSelected(null)}
            onRun={() => startRun(sel.item)}
            onEdit={() => setEditing({ task: sel.item, form: taskForm(sel.item) })}
            onToggle={(v) => toggleTask(sel.item, v)}
            onDelete={() => setToDelete(sel.item)}
            jobProps={jobProps}
          />
        </PaneLayout>
      )}

      {editing && (
        <TaskSheet editing={editing} setForm={setForm} agents={agents || []} models={models} saving={saving} onSave={save} onClose={() => setEditing(null)} />
      )}
      {running && (
        <RunSheet running={running} setParams={(p) => setRunning(r => ({ ...r, params: { ...r.params, ...p } }))} onRun={run} onClose={() => setRunning(null)} />
      )}

      <ChatDeleteConfirm
        item={toDelete}
        kind="task"
        onConfirm={(task) => {
          setToDelete(null)
          if (selected?.id === task.id) setSelected(null)
          act(() => agentJobsApi.deleteTask(task.id), t('tasks.deleted', { name: task.name }))
        }}
        onCancel={() => setToDelete(null)}
      />
      <ConfirmDialog
        open={Boolean(jobToDelete)}
        title={t('tasks.confirmJob.title')}
        message={t('tasks.confirmJob.message')}
        confirmLabel={t('tasks.delete')}
        danger
        onConfirm={() => { const job = jobToDelete; setJobToDelete(null); act(() => agentJobsApi.deleteJob(job.id), t('tasks.jobDeleted')) }}
        onCancel={() => setJobToDelete(null)}
      />
    </Page>
  )
}

// One task: a tile, its name and what it does in two lines, who runs it and
// what it asks for, how its last runs went, and on/off, Run and a menu.
function TaskRow({ row, runner, jobs, selected, onOpen, onRun, onEdit, onToggle, onDelete }) {
  const { t } = useTranslation('agents')
  const { owner, item: task } = row
  const params = promptParams(task.prompt)
  const last = jobs[0]
  const off = task.enabled === false
  const mine = !owner
  return (
    <li className="ag-row aj-task" data-testid="agent-task-row" data-task={task.name} data-off={off || undefined} data-selected={selected || undefined}>
      <span className="ag-tile" aria-hidden="true"><Icon name="bolt" /></span>
      <div className="ag-row__main">
        <button type="button" className="ag-row__name aj-task__open" onClick={() => onOpen('overview')} aria-label={t('tasks.openTask', { name: task.name })}>{task.name}</button>
        <span className="aj-task__desc">{task.description || task.prompt}</span>
        <span className="ag-meta">
          {task.agent_id
            ? <span title={t('tasks.agent')}><Icon name="robot" />{runner}</span>
            : <span className="ag-mono" title={t('tasks.model')}><Icon name="cube" />{runner || t('jobs.tasks.noModel')}</span>}
          <span><Icon name="play-circle" />{t('tasks.onDemand')}</span>
          {params.length > 0 && <span>{t('tasks.asksFor', { list: params.join(', ') })}</span>}
          {task.webhooks?.length > 0 && <span><Icon name="send" />{t('tasks.webhooks', { count: task.webhooks.length })}</span>}
          {owner && <span><Icon name="user" />{owner}</span>}
        </span>
      </div>
      <div className="ag-row__record">
        {last ? (
          <>
            <span className="aj-last">
              <StatusMark status={jobState(last.status)} label={t(`tasks.state.${last.status}`, { defaultValue: last.status })} />
              <RelTime className="ag-muted" iso={last.started_at || last.created_at} />
            </span>
            <button type="button" className="aj-strip-link" onClick={() => onOpen('runs')} aria-label={t('tasks.runsOf', { name: task.name, count: jobs.length })}>
              <JobStrip jobs={jobs.map(j => ({ ...j, status: STRIP_STATE[j.status] }))} />
              <span className="ag-muted">{t('tasks.runsCount', { count: jobs.length })}</span>
            </button>
          </>
        ) : (
          <span className="ag-muted">{off ? t('tasks.offLine') : t('jobs.tasks.neverRun')}</span>
        )}
      </div>
      <div className="ag-row__end">
        {mine ? (
          <>
            <Switch checked={!off} onChange={onToggle} label={t('tasks.toggleLabel', { name: task.name })} />
            <Button size="sm" icon="play" className="aj-task__run" onClick={onRun} disabled={off} aria-label={t('tasks.runFor', { name: task.name })}><span className="aj-acts__word">{t('tasks.run')}</span></Button>
            <ActionMenu
              ariaLabel={t('tasks.moreFor', { name: task.name })}
              items={[
                { key: 'run', icon: 'play', label: t('tasks.run'), disabled: off, onClick: onRun },
                { key: 'open', icon: 'eye', label: t('tasks.open'), onClick: () => onOpen('overview') },
                { key: 'runs', icon: 'history', label: t('tasks.pane.runs'), onClick: () => onOpen('runs') },
                { key: 'edit', icon: 'edit', label: t('tasks.edit'), onClick: onEdit },
                { divider: true },
                { key: 'delete', icon: 'trash', label: t('tasks.delete'), danger: true, onClick: onDelete },
              ]}
            />
          </>
        ) : (
          <Button size="sm" variant="ghost" onClick={() => onOpen('overview')}>{t('tasks.open')}</Button>
        )}
      </div>
    </li>
  )
}
