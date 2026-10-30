import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentJobsApi, chatAgentsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useModels } from '../hooks/useModels'
import { CAP_CHAT } from '../utils/capabilities'
import { jobActive, jobState, promptParams, taskBody, taskForm, taskMissing, withOwners } from '../utils/agentTasks'
import { jobDurationMs } from '../utils/agentJobs'
import { formatDuration } from '../utils/agentRuns'
// eslint-disable-next-line no-unused-vars
import { AgentViews, StatusMark, formatWhen } from '../components/agents/AgentBits'
// eslint-disable-next-line no-unused-vars
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
// eslint-disable-next-line no-unused-vars
import { Button, EmptyState, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
import './agents.css'
import './agent-jobs.css'

const POLL_MS = 3000

// Agent tasks: a prompt template that a model or a saved agent runs in the
// background, from this page or over the API (POST /api/agent/jobs/execute or
// /api/agent/tasks/{name}/execute, as CI pipelines do). Each run is a job with
// its status, result, traces and webhook outcome. Webhooks and their headers
// are set over the API; the page keeps them as they are.
export default function AgentTasks() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('agents')
  const { isAdmin } = useAuth()
  const { models } = useModels(CAP_CHAT)
  const [tasks, setTasks] = useState([])
  const [jobs, setJobs] = useState([])
  const [agents, setAgents] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [allUsers, setAllUsers] = useState(false)
  const [editing, setEditing] = useState(null) // { task, form } or null
  const [running, setRunning] = useState(null) // { task, params }
  const [open, setOpen] = useState(() => new Set())
  const [toDelete, setToDelete] = useState(null)

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

  const act = async (fn, done) => {
    try {
      const out = await fn()
      if (done) addToast(done, 'success')
      await load()
      return out
    } catch (err) {
      addToast(t('tasks.actionFailed', { message: err.message }), 'error')
      return null
    }
  }

  const save = async (e) => {
    e.preventDefault()
    const { task, form } = editing
    const body = taskBody(form, task)
    const ok = await act(() => (task ? agentJobsApi.updateTask(task.id, body) : agentJobsApi.createTask(body)), t('tasks.saved', { name: body.name }))
    if (ok) setEditing(null)
  }

  const run = async (e) => {
    e.preventDefault()
    const { task, params } = running
    setRunning(null)
    await act(() => agentJobsApi.run(task.id, params), t('tasks.started', { name: task.name }))
  }

  const toggle = (id) => setOpen(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  const form = editing?.form
  const setForm = (patch) => setEditing(ed => ({ ...ed, form: { ...ed.form, ...patch } }))
  const missing = form ? taskMissing(form) : []
  const runnerName = (task) => (task.agent_id ? agents.find(a => a.id === task.agent_id)?.name || task.agent_id : task.model)

  return (
    <Page width="full" className="ag-page aj-page" data-testid="agent-tasks-page">
      <PageHeader
        title={t('tasks.title')}
        supporting={t('tasks.lede')}
        actions={(
          <div className="aj-head__acts">
            {isAdmin && (
              <label className="aj-filters">
                <input type="checkbox" checked={allUsers} onChange={(e) => setAllUsers(e.target.checked)} />
                {t('tasks.allUsers')}
              </label>
            )}
            <Button variant="primary" icon="plus" onClick={() => setEditing({ task: null, form: taskForm(null) })}>{t('tasks.new')}</Button>
          </div>
        )}
      />
      <AgentViews />

      {editing && (
        <form className="aj-form aj-sec" onSubmit={save} data-testid="agent-task-form" aria-label={editing.task ? t('tasks.editTitle') : t('tasks.newTitle')}>
          <h2 className="ag-h2">{editing.task ? t('tasks.editTitle') : t('tasks.newTitle')}</h2>
          <div className="dk-field">
            <label className="dk-label" htmlFor="at-name">{t('tasks.name')}</label>
            <input id="at-name" className="dk-input" value={form.name} onChange={(e) => setForm({ name: e.target.value })} />
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="at-desc">{t('tasks.description')}</label>
            <input id="at-desc" className="dk-input" value={form.description} onChange={(e) => setForm({ description: e.target.value })} />
          </div>
          <div className="aj-row2">
            <div className="dk-field">
              <label className="dk-label" htmlFor="at-runner">{t('tasks.runner')}</label>
              <span className="dk-select-wrap">
                <select id="at-runner" className="dk-select" value={form.runner} onChange={(e) => setForm({ runner: e.target.value })}>
                  <option value="model">{t('tasks.byModel')}</option>
                  <option value="agent" disabled={agents.length === 0}>{t('tasks.byAgent')}</option>
                </select>
              </span>
            </div>
            {form.runner === 'model' ? (
              <div className="dk-field">
                <label className="dk-label" htmlFor="at-model">{t('tasks.model')}</label>
                <span className="dk-select-wrap">
                  <select id="at-model" className="dk-select" value={form.model} onChange={(e) => setForm({ model: e.target.value })}>
                    <option value="">{t('tasks.pick')}</option>
                    {models.map(m => <option key={m.id} value={m.id}>{m.id}</option>)}
                  </select>
                </span>
              </div>
            ) : (
              <div className="dk-field">
                <label className="dk-label" htmlFor="at-agent">{t('tasks.agent')}</label>
                <span className="dk-select-wrap">
                  <select id="at-agent" className="dk-select" value={form.agent_id} onChange={(e) => setForm({ agent_id: e.target.value })}>
                    <option value="">{t('tasks.pick')}</option>
                    {agents.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
                  </select>
                </span>
              </div>
            )}
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="at-prompt">{t('tasks.prompt')}</label>
            <textarea id="at-prompt" className="dk-input aj-prompt" rows={5} value={form.prompt} onChange={(e) => setForm({ prompt: e.target.value })} />
            <p className="dk-hint">{t('tasks.promptHint', { example: '{{.name}}' })}</p>
          </div>
          <label className="aj-enabled">
            <input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ enabled: e.target.checked })} />
            <span>{t('tasks.enabled')}</span>
          </label>
          {editing.task?.webhooks?.length > 0 && <p className="ag-note">{t('tasks.webhooksKept', { count: editing.task.webhooks.length })}</p>}
          <div className="aj-head__acts">
            <Button type="submit" variant="primary" disabled={missing.length > 0}>{t('tasks.save')}</Button>
            <Button variant="ghost" onClick={() => setEditing(null)}>{t('tasks.cancel')}</Button>
          </div>
        </form>
      )}

      {running && (
        <form className="aj-form aj-sec" onSubmit={run} data-testid="agent-task-run" aria-label={t('tasks.runTitle', { name: running.task.name })}>
          <h2 className="ag-h2">{t('tasks.runTitle', { name: running.task.name })}</h2>
          {promptParams(running.task.prompt).map(p => (
            <div className="dk-field" key={p}>
              <label className="dk-label" htmlFor={`at-p-${p}`}>{p}</label>
              <input id={`at-p-${p}`} className="dk-input" value={running.params[p] || ''} onChange={(e) => setRunning(r => ({ ...r, params: { ...r.params, [p]: e.target.value } }))} />
            </div>
          ))}
          <div className="aj-head__acts">
            <Button type="submit" variant="primary" icon="play">{t('tasks.runNow')}</Button>
            <Button variant="ghost" onClick={() => setRunning(null)}>{t('tasks.cancel')}</Button>
          </div>
        </form>
      )}

      {loading ? (
        <SkeletonRows count={3} />
      ) : error && tasks.length === 0 && jobs.length === 0 ? (
        <LoadError title={t('load.tasks')} error={error} onRetry={load} />
      ) : (
        <>
          {error && <LoadError title={t('load.tasksStale')} error={error} onRetry={load} />}
          <section className="aj-sec" aria-labelledby="at-tasks-h">
            <div className="aj-sec__head"><h2 className="ag-eyebrow" id="at-tasks-h">{t('tasks.tasks')}</h2></div>
            {tasks.length === 0 ? (
              <EmptyState testId="agent-tasks-empty" level={3} body={t('tasks.none')} />
            ) : (
              <div className="dk-table-wrap aj-wrap" role="region" aria-label={t('tasks.tasks')} tabIndex={0}>
                <table className="dk-table aj-table" data-testid="agent-tasks">
                  <thead>
                    <tr>
                      <th scope="col">{t('tasks.name')}</th>
                      <th scope="col">{t('tasks.runner')}</th>
                      {allUsers && <th scope="col">{t('tasks.owner')}</th>}
                      <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('tasks.actions')}</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {tasks.map(({ owner, item: task }) => (
                      <tr key={task.id} data-testid="agent-task-row">
                        <td>
                          <span className="aj-name">{task.name}</span>
                          <span className="aj-sub"><span className="aj-sub__text">{task.description || task.prompt}</span>{task.enabled === false && <span>{t('tasks.off')}</span>}</span>
                        </td>
                        <td className="aj-mono">{runnerName(task)}</td>
                        {allUsers && <td>{owner || t('tasks.you')}</td>}
                        <td className="dk-table-actions">
                          {!owner && (
                            <span className="aj-acts">
                              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setRunning({ task, params: {} })}>{t('tasks.run')}</button>
                              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setEditing({ task, form: taskForm(task) })}>{t('tasks.edit')}</button>
                              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setToDelete(task)}>{t('tasks.delete')}</button>
                            </span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          <section className="aj-sec" aria-labelledby="at-jobs-h">
            <div className="aj-sec__head"><h2 className="ag-eyebrow" id="at-jobs-h">{t('tasks.jobs')}</h2></div>
            {jobs.length === 0 ? (
              <p className="ag-note">{t('tasks.noJobs')}</p>
            ) : (
              <div className="dk-table-wrap aj-wrap" role="region" aria-label={t('tasks.jobs')} tabIndex={0}>
                <table className="dk-table aj-table" data-testid="agent-jobs">
                  <thead>
                    <tr>
                      <th scope="col">{t('tasks.status')}</th>
                      <th scope="col">{t('tasks.task')}</th>
                      <th scope="col">{t('tasks.when')}</th>
                      <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('tasks.actions')}</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {jobs.map(({ owner, item: job }) => {
                      const ms = jobDurationMs(job)
                      return (
                        <Fragment key={job.id}>
                          <tr data-testid="agent-job-row" data-status={job.status}>
                            <td><StatusMark status={jobState(job.status)} label={t(`tasks.state.${job.status}`, { defaultValue: job.status })} /></td>
                            <td>
                              <span className="aj-name">{nameOf(job.task_id)}</span>
                              <span className="aj-sub">{job.triggered_by && <span>{t('tasks.by', { who: job.triggered_by })}</span>}{owner && <span>{owner}</span>}</span>
                            </td>
                            <td className="aj-mono">{formatWhen(job.created_at)}{ms !== null ? ` · ${formatDuration(ms)}` : ''}</td>
                            <td className="dk-table-actions">
                              <span className="aj-acts">
                                <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" aria-expanded={open.has(job.id)} onClick={() => toggle(job.id)}>{open.has(job.id) ? t('tasks.hide') : t('tasks.details')}</button>
                                {!owner && jobActive(job) && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => act(() => agentJobsApi.cancel(job.id))}>{t('tasks.cancelJob')}</button>}
                                {!owner && !jobActive(job) && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => act(() => agentJobsApi.rerun(job.id), t('tasks.rerunStarted'))}>{t('tasks.rerun')}</button>}
                                {!owner && <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => act(() => agentJobsApi.deleteJob(job.id))}>{t('tasks.delete')}</button>}
                              </span>
                            </td>
                          </tr>
                          {open.has(job.id) && (
                            <tr className="dk-table-detail">
                              <td colSpan={4}>
                                <div className="dk-table-detail-body">
                                <div className={`aj-detail${job.status === 'failed' ? ' aj-detail--failed' : ''}`} data-testid="agent-job-detail">
                                  {Object.keys(job.parameters || {}).length > 0 && <p className="ag-note">{t('tasks.params', { list: Object.entries(job.parameters).map(([k, v]) => `${k}=${v}`).join(', ') })}</p>}
                                  {job.error && <pre className="aj-excerpt">{job.error}</pre>}
                                  {job.result && <pre className="aj-excerpt">{job.result}</pre>}
                                  {!job.result && !job.error && <p className="ag-note">{jobActive(job) ? t('tasks.stillRunning') : t('tasks.noResult')}</p>}
                                  {(job.traces || []).length > 0 && (
                                    <details>
                                      <summary>{t('tasks.traces', { count: job.traces.length })}</summary>
                                      <pre className="aj-excerpt">{job.traces.map(tr => `[${tr.type}${tr.tool_name ? ` ${tr.tool_name}` : ''}] ${tr.content}`).join('\n')}</pre>
                                    </details>
                                  )}
                                  {job.webhook_sent && <p className="ag-note">{t('tasks.webhookSent')}</p>}
                                  {job.webhook_error && <p className="ag-note">{t('tasks.webhookFailed', { message: job.webhook_error })}</p>}
                                </div>
                                </div>
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}

      <ChatDeleteConfirm
        item={toDelete}
        kind="task"
        onConfirm={(task) => { setToDelete(null); act(() => agentJobsApi.deleteTask(task.id), t('tasks.deleted', { name: task.name })) }}
        onCancel={() => setToDelete(null)}
      />
    </Page>
  )
}
