import { useState, useEffect, useId, useMemo, useRef } from 'react'
import { Link, useParams, useNavigate, useOutletContext, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatProjectsApi, chatTriggersApi, filesApi } from '../utils/api'
import { useAgentProjects } from '../hooks/useAgentProjects'
// eslint-disable-next-line no-unused-vars
import { ProjectChip } from '../components/projects/ProjectChip'
import {
  agentLabel, allowedFolders, baseOf, defaultFolder, firstProjectAgent, folderProblem, joinFolder, projectOfAgent, subOf,
} from '../utils/triggerProjects'
// eslint-disable-next-line no-unused-vars
import ModelPicker from '../components/ModelPicker'
// eslint-disable-next-line no-unused-vars
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
// eslint-disable-next-line no-unused-vars
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
// eslint-disable-next-line no-unused-vars
import { JobStrip } from '../components/agents/JobBits'
// eslint-disable-next-line no-unused-vars
import { StatusMark, formatWhen } from '../components/agents/AgentBits'
import { CAP_CHAT } from '../utils/capabilities'
import { filesOff } from '../utils/chatAgents'
import { diffConfig, displayValue } from '../utils/agentConfigTools'
import { jobOfRun, STRIP } from '../utils/agentJobs'
import { jobSentence } from '../utils/agentJobText'
import { formatDuration, firstLine } from '../utils/agentRuns'
import { jobDurationMs } from '../utils/agentJobs'
import { actsStatus } from '../utils/triggerActs'
import {
  EVERY_HOURS, EVERY_MINUTES, HOURLY_MINUTES, dayCodes, emptyTrigger, eventSentence, formatNext, joinTimes, matchList, presetOrder,
  scheduleText, triggerForm, triggerPayload,
} from '../utils/chatTrigger'
import { ActsEditor, ActsSummary } from '../components/TriggerActs'
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
import ReportsTo from '../components/agents/ReportsTo'
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
import EventHook, { EventSentence } from '../components/chat/EventHook'
import { useAddonIcons } from '../hooks/useAddonIcons'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import SideSheet from '../components/SideSheet'
// eslint-disable-next-line no-unused-vars
import { Button, LoadError, Page, PageHeader, SkeletonRows, Switch, Tabs } from '../components/ui'
import './agents.css'
import './agent-jobs.css'

// The runs a trigger page shows before Show more.
const RUNS_PAGE = 10

export default function AgentTaskDetails() {
  const { id } = useParams()
  const location = useLocation()
  const isNew = !id || location.pathname.endsWith('/new')
  const isEdit = location.pathname.endsWith('/edit')
  if (!isNew && !isEdit) return <TaskView key={id} id={id} />
  return <TaskForm key={`${id || 'new'}`} id={id} isNew={isNew} />
}

// ---- A trigger, read -----------------------------------------------------

// eslint-disable-next-line no-unused-vars
function TaskView({ id }) {
  const { t } = useTranslation(['agents', 'chats'])
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [task, setTask] = useState(null)
  const [jobs, setJobs] = useState([])
  const { agents, projects } = useAgentProjects()
  const icons = useAddonIcons()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [missing, setMissing] = useState(false)
  const [toDelete, setToDelete] = useState(null)
  const [shownRuns, setShownRuns] = useState(RUNS_PAGE)
  // The secret of an event trigger after a rotate. Held here only: a reload drops it.
  const [secret, setSecret] = useState('')

  const load = async () => {
    try {
      const list = (await chatTriggersApi.list()).triggers || []
      const found = list.find(x => x.id === id)
      if (!found) { setMissing(true); return }
      setTask(found)
      const runs = await chatTriggersApi.runs(id).catch(() => ({ runs: [] }))
      setJobs((runs.runs || []).map(r => jobOfRun(r, id)))
      setError(null)
    } catch (err) {
      setError(err)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    const interval = setInterval(load, 5000)
    return () => clearInterval(interval)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  const mine = useMemo(() => [...jobs].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at)).slice(0, 20), [jobs])

  const toggle = async () => {
    const enabled = !task.enabled
    const before = task
    setTask({ ...task, enabled })
    try {
      await (enabled ? chatTriggersApi.enable(id) : chatTriggersApi.disable(id))
      addToast(t(enabled ? 'jobs.tasks.turnedOn' : 'jobs.tasks.turnedOff', { name: task.name }), 'success')
    } catch (err) {
      setTask(before)
      addToast(t('jobs.tasks.toggleFailed', { message: err.message }), 'error')
    }
  }

  const runNow = async () => {
    try {
      await chatTriggersApi.run(id)
      addToast(t('jobs.tasks.started', { name: task.name }), 'success')
      load()
    } catch (err) {
      addToast(t('jobs.actionFailed', { message: err.message }), 'error')
    }
  }

  const remove = async () => {
    setToDelete(null)
    try {
      await chatTriggersApi.remove(id)
      addToast(t('jobs.tasks.deleted'), 'success')
      navigate('/app/triggers')
    } catch (err) {
      addToast(t('jobs.actionFailed', { message: err.message }), 'error')
    }
  }

  const back = <Link className="ag-back" to="/app/triggers"><Icon name="arrow-left" /> {t('jobs.task.back')}</Link>

  if (loading || (error && !task)) {
    return (
      <Page className="ag-page aj-page">
        <div className="ag-home">
          {back}
          {error ? <LoadError title={t('load.trigger')} error={error} onRetry={load} /> : <SkeletonRows count={4} />}
        </div>
      </Page>
    )
  }

  if (missing || !task) {
    return (
      <Page className="ag-page aj-page">
        <div className="ag-home" data-testid="task-missing">
          {back}
          <PageHeader title={t('jobs.task.missingTitle')} supporting={t('jobs.task.missingText')} />
        </div>
      </Page>
    )
  }

  const off = !task.enabled
  const agent = agents.find(a => a.id === task.agent_id)
  const project = projectOfAgent(agent, projects)

  return (
    <Page className="ag-page aj-page" data-testid="task-page">
      <div className="ag-home">
        {back}
        <PageHeader
          title={task.name}
          supporting={task.summary}
          actions={(
            <div className="ag-bar__acts">
              <StatusMark status={off ? 'paused' : 'done'} label={t(off ? 'jobs.task.disabled' : 'jobs.task.enabled')} />
              <Button icon="edit" to={`/app/triggers/${encodeURIComponent(id)}/edit`}>{t('jobs.task.edit')}</Button>
              <Button variant="primary" icon="play" onClick={runNow} data-testid="task-run">{t('jobs.task.runNow')}</Button>
              <ActionMenu
                ariaLabel={t('jobs.tasks.moreFor', { name: task.name })}
                items={[{ key: 'delete', icon: 'trash', label: t('jobs.tasks.delete'), danger: true, onClick: () => setToDelete(task) }]}
              />
            </div>
          )}
        />

        {error && <LoadError title={t('load.triggerStale')} error={error} onRetry={load} />}

        <dl className="ag-facts" data-testid="task-facts">
          <dt>{t('jobs.task.agent')}</dt>
          <dd>{agent ? <Link className="ag-link" to={`/app/agents/${encodeURIComponent(agent.id)}`}>{agent.name}</Link> : <span className="ag-muted">{t('jobs.tasks.noAgent')}</span>}</dd>
          {project && (
            <>
              <dt>{t('jobs.task.project')}</dt>
              <dd data-testid="task-project"><ProjectChip project={project} testId="task-project-chip" /></dd>
            </>
          )}
          <dt>{t('jobs.task.model')}</dt>
          <dd className="ag-facts__model">{task.model || agent?.model || <span className="ag-muted">{t('jobs.tasks.noModel')}</span>}</dd>
          <dt>{t('jobs.task.schedule')}</dt>
          <dd>
            {task.kind === 'file' ? t('jobs.task.watches', { folder: task.folder }) : task.kind === 'event' ? <EventSentence trigger={task} /> : task.summary}
            {task.next_run && !off ? <span className="ag-muted"> · {t('chats:triggersPage.next', { time: formatNext(task.next_run, task.time_zone) })}</span> : null}
          </dd>
          <dt>{t('jobs.reports.label')}</dt>
          <dd data-testid="task-reports"><ReportsTo trigger={task} icons={icons} /></dd>
          <dt>{t('jobs.task.acts')}</dt>
          <dd data-testid="task-acts"><ActsSummary trigger={task} /></dd>
          <dt>{t('jobs.task.cap')}</dt>
          <dd className="ag-facts__model">{task.daily_cap || '-'}</dd>
          <dt>{t('jobs.task.runLimit')}</dt>
          <dd className="ag-facts__model" data-testid="task-run-limit">{task.effective_run_limit || '-'}</dd>
          <dt>{t('jobs.task.enabledSwitch')}</dt>
          <dd>
            <Switch checked={!off} onChange={toggle} label={t('jobs.tasks.toggleLabel', { name: task.name })} />
          </dd>
        </dl>

        {task.kind === 'event' && task.hook_path && (
          <section className="ag-sec" aria-labelledby="aj-hook-h">
            <div className="ag-section-head"><h2 className="ag-h2" id="aj-hook-h">{t('chats:event.hook.title')}</h2></div>
            <EventHook trigger={task} secret={secret} onSecret={setSecret} />
          </section>
        )}
        <section className="ag-sec" aria-labelledby="aj-prompt-h">
          <div className="ag-section-head"><h2 className="ag-h2" id="aj-prompt-h">{t('jobs.task.prompt')}</h2></div>
          {task.prompt ? <p className="aj-preview aj-preview--doc" data-testid="task-prompt">{task.prompt}</p> : <p className="ag-note">{t('jobs.edit.summary.promptEmpty')}</p>}
        </section>
        <section className="ag-group" aria-labelledby="aj-recent-h">
          <div className="ag-section-head">
            <h2 className="ag-h2" id="aj-recent-h">{t('jobs.task.recent')}</h2>
            {mine.length > 0 && (
              <span className="aj-record">
                <JobStrip jobs={mine} />
                <span className="ag-muted ag-small">{t('jobs.task.recentNote', { count: Math.min(mine.length, STRIP) })}</span>
              </span>
            )}
          </div>
          {mine.length === 0 ? (
            <p className="ag-note">{t('jobs.task.noRuns')}</p>
          ) : (
            <div className="ag-runs" data-testid="task-runs">
              {mine.slice(0, shownRuns).map(job => {
                const took = jobDurationMs(job)
                // eslint-disable-next-line no-unused-vars
                const Row = job.chat_id ? Link : 'div'
                return (
                  <Row key={job.id} className="ag-run-row" {...(job.chat_id ? { to: `/app/chats/${encodeURIComponent(job.chat_id)}` } : {})} data-job={job.id}>
                    <StatusMark status={job.status === 'done' ? 'done' : job.status === 'failed' ? 'failed' : job.status === 'running' ? 'running' : job.status === 'skipped' ? 'stopped' : 'ready'} label={t(`jobs.status.${job.status}`, { defaultValue: job.status })} />
                    <span className="ag-run-row__main">
                      <span className="ag-run-row__title">{jobSentence(job, t)}</span>
                      <span className="ag-run-row__line">{took != null ? t('jobs.task.runMeta', { time: formatDuration(took) }) : ''}</span>
                    </span>
                    <span className="ag-run-row__when">{formatWhen(Date.parse(job.created_at))}</span>
                    {job.chat_id ? <Icon name="chevron-right" /> : <span />}
                  </Row>
                )
              })}
            </div>
          )}
          {mine.length > shownRuns && (
            <div className="aj-more">
              <Button variant="ghost" size="sm" icon="chevron-down" onClick={() => setShownRuns(n => n + RUNS_PAGE)}>
                {t('tasks.history.more', { count: Math.min(RUNS_PAGE, mine.length - shownRuns) })}
              </Button>
            </div>
          )}
        </section>

      </div>
      <ChatDeleteConfirm item={toDelete} kind="trigger" onConfirm={remove} onCancel={() => setToDelete(null)} />
    </Page>
  )
}

// ---- A trigger, written --------------------------------------------------

const SECTIONS = [
  { id: 'basics', keys: ['name', 'agent_id', 'model', 'enabled'] },
  { id: 'prompt', keys: ['prompt'] },
  { id: 'schedule', keys: ['kind', 'schedule', 'time_zone', 'folder', 'quiet_seconds', 'source', 'event_type', 'matches'] },
  { id: 'acts', keys: ['acts'] },
  { id: 'limits', keys: ['daily_cap', 'run_limit'] },
]

// The fields of the schedule: the preset chosen, then what that preset needs.
// eslint-disable-next-line no-unused-vars
function ScheduleFields({ form, setForm, folders, noFiles, allowed, problem, t }) {
  const s = form.schedule
  const set = (patch) => setForm({ ...form, schedule: { ...s, ...patch } })
  const hasTime = ['daily', 'weekdays', 'weekly', 'monthly', 'once'].includes(s.preset)
  const folderList = useId()
  return (
    <>
      <div className="dk-field">
        <label className="dk-label" htmlFor="aj-when">{t('chats:triggersPage.when')}</label>
        <span className="dk-select-wrap">
          <select id="aj-when" className="dk-select" value={s.preset} onChange={(e) => set({ preset: e.target.value })}>
            {presetOrder.map((p) => <option key={p} value={p}>{t(`chats:triggersPage.presets.${p}`)}</option>)}
          </select>
        </span>
      </div>
      <div className="aj-row2">
        {hasTime && (
          <div className="dk-field aj-time">
            <label className="dk-label" htmlFor="aj-time">{t('chats:triggersPage.time')}</label>
            <input id="aj-time" className="dk-input" type="time" value={s.time} required onChange={(e) => set({ time: e.target.value })} />
          </div>
        )}
        {s.preset === 'monthly' && (
          <div className="dk-field aj-time">
            <label className="dk-label" htmlFor="aj-day">{t('chats:triggersPage.dayOfMonth')}</label>
            <input id="aj-day" className="dk-input" type="number" min={1} max={31} value={s.day} onChange={(e) => set({ day: Number(e.target.value) })} />
          </div>
        )}
        {s.preset === 'once' && (
          <div className="dk-field aj-time">
            <label className="dk-label" htmlFor="aj-date">{t('chats:triggersPage.date')}</label>
            <input id="aj-date" className="dk-input" type="date" value={s.date} required onChange={(e) => set({ date: e.target.value })} />
          </div>
        )}
        {s.preset === 'hourly' && (
          <div className="dk-field aj-time">
            <label className="dk-label" htmlFor="aj-minute">{t('chats:triggersPage.minute')}</label>
            <span className="dk-select-wrap">
              <select id="aj-minute" className="dk-select" value={s.minute} onChange={(e) => set({ minute: Number(e.target.value) })}>
                {HOURLY_MINUTES.map((m) => <option key={m} value={m}>{String(m).padStart(2, '0')}</option>)}
              </select>
            </span>
          </div>
        )}
        {s.preset === 'everyHours' && (
          <div className="dk-field aj-time">
            <label className="dk-label" htmlFor="aj-hours">{t('chats:triggersPage.everyHours')}</label>
            <span className="dk-select-wrap">
              <select id="aj-hours" className="dk-select" value={s.hours} onChange={(e) => set({ hours: Number(e.target.value) })}>
                {EVERY_HOURS.map((h) => <option key={h} value={h}>{h}</option>)}
              </select>
            </span>
          </div>
        )}
        {s.preset === 'everyMinutes' && (
          <div className="dk-field aj-time">
            <label className="dk-label" htmlFor="aj-minutes">{t('chats:triggersPage.everyMinutes')}</label>
            <span className="dk-select-wrap">
              <select id="aj-minutes" className="dk-select" value={s.minutes} onChange={(e) => set({ minutes: Number(e.target.value) })}>
                {EVERY_MINUTES.map((m) => <option key={m} value={m}>{m}</option>)}
              </select>
            </span>
          </div>
        )}
        {s.preset === 'cron' && (
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-cron">{t('chats:triggersPage.cron')}</label>
            <input id="aj-cron" className="dk-input dk-input--mono" value={s.cron} spellCheck={false} onChange={(e) => set({ cron: e.target.value })} />
          </div>
        )}
        {s.preset !== 'file' && s.preset !== 'event' && (
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-zone">{t('chats:triggersPage.zone')}</label>
            <input id="aj-zone" className="dk-input" value={form.zone} onChange={(e) => setForm({ ...form, zone: e.target.value })} />
          </div>
        )}
      </div>
      {s.preset === 'weekly' && (
        <fieldset className="dk-field aj-days">
          <legend className="dk-label">{t('chats:triggersPage.days')}</legend>
          <div className="aj-presets">
            {dayCodes.map((d) => (
              <button
                key={d}
                type="button"
                className="dk-chip"
                aria-pressed={s.days.includes(d)}
                onClick={() => set({ days: s.days.includes(d) ? s.days.filter((x) => x !== d) : [...s.days, d] })}
              >
                {t(`chats:triggersPage.day.${d}`)}
              </button>
            ))}
          </div>
        </fieldset>
      )}
      {s.preset === 'event' && (
        <div className="aj-form" data-testid="aj-event-fields">
          <div className="aj-row2">
            <div className="dk-field">
              <label className="dk-label" htmlFor="aj-source">{t('chats:event.form.source')}</label>
              <input id="aj-source" className="dk-input" value={form.source} required placeholder="github" autoComplete="off" spellCheck={false}
                aria-describedby="aj-source-h" onChange={(e) => setForm({ ...form, source: e.target.value })} />
              <p className="dk-hint" id="aj-source-h">{t('chats:event.form.sourceHint')}</p>
            </div>
            <div className="dk-field">
              <label className="dk-label" htmlFor="aj-etype">{t('chats:event.form.type')}</label>
              <input id="aj-etype" className="dk-input" value={form.eventType} placeholder="pull_request" autoComplete="off" spellCheck={false}
                aria-describedby="aj-etype-h" onChange={(e) => setForm({ ...form, eventType: e.target.value })} />
              <p className="dk-hint" id="aj-etype-h">{t('chats:event.form.typeHint')}</p>
            </div>
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-matches">{t('chats:event.form.matches')}</label>
            <textarea id="aj-matches" className="dk-textarea dk-input--mono" rows={3} value={form.matches} placeholder="action=opened" spellCheck={false}
              aria-describedby="aj-matches-h" onChange={(e) => setForm({ ...form, matches: e.target.value })} />
            <p className="dk-hint" id="aj-matches-h">{t('chats:event.form.matchesHint')}</p>
          </div>
        </div>
      )}
      {s.preset === 'file' && noFiles && <p className="dk-hint" role="note" data-testid="aj-folder-nofiles">{t('jobs.edit.watchNoFiles')}</p>}
      {s.preset === 'file' && (
        <div className="aj-row2">
          {allowed ? (
            <FenceFolder form={form} setForm={setForm} allowed={allowed} problem={problem} t={t} />
          ) : (
            <div className="dk-field">
              <label className="dk-label" htmlFor="aj-folder">{t('chats:triggersPage.folder')}</label>
              <input id="aj-folder" className="dk-input" list={folderList} value={form.folder} required placeholder="/Contracts"
                aria-invalid={problem ? true : undefined} aria-describedby={problem ? 'aj-folder-err' : undefined}
                onChange={(e) => setForm({ ...form, folder: e.target.value })} />
              <datalist id={folderList}>{folders.map((f) => <option key={f} value={f} />)}</datalist>
              {problem && <p className="dk-field-error" id="aj-folder-err" role="alert" data-testid="aj-folder-problem">{t(`jobs.edit.folder.${problem}`)}</p>}
            </div>
          )}
          <div className="dk-field aj-time">
            <label className="dk-label" htmlFor="aj-quiet">{t('chats:triggersPage.quiet')}</label>
            <input id="aj-quiet" className="dk-input" type="number" min={0} max={86400} value={form.quiet} placeholder="30"
              onChange={(e) => setForm({ ...form, quiet: e.target.value })} />
          </div>
        </div>
      )}
    </>
  )
}

// The folder of a file trigger of an agent that works inside a fence: one of the
// agent's own folders, and an optional subfolder inside it. The server refuses
// any other folder, so none is offered; a stored folder that is now outside
// shows as a problem and asks for a new choice.
// eslint-disable-next-line no-unused-vars
function FenceFolder({ form, setForm, allowed, problem, t }) {
  const base = baseOf(form.folder, allowed)
  const sub = base ? subOf(form.folder, base) : ''
  const pick = (nextBase, nextSub) => setForm({ ...form, folder: nextBase ? joinFolder(nextBase, nextSub) : '' })
  if (allowed.length === 0) {
    return <p className="dk-field-error" role="alert" data-testid="aj-folder-problem">{t('jobs.edit.folder.noFolders')}</p>
  }
  return (
    <div className="dk-field aj-fence">
      <label className="dk-label" htmlFor="aj-folder">{t('chats:triggersPage.folder')}</label>
      <div className="aj-fence__row">
        <span className="dk-select-wrap aj-fence__base">
          <select id="aj-folder" className="dk-select" value={base} data-testid="aj-folder-base"
            aria-invalid={problem ? true : undefined} aria-describedby={problem ? 'aj-folder-err' : 'aj-folder-hint'}
            onChange={(e) => pick(e.target.value, sub)}>
            {!base && <option value="">{t('jobs.edit.folder.choose')}</option>}
            {allowed.map((f) => <option key={f} value={f}>{f}</option>)}
          </select>
        </span>
        <input className="dk-input aj-fence__sub" value={sub} placeholder={t('jobs.edit.folder.subPlaceholder')} disabled={!base}
          aria-label={t('jobs.edit.folder.sub')} data-testid="aj-folder-sub" spellCheck={false}
          onChange={(e) => pick(base, e.target.value)} />
      </div>
      {problem
        ? <p className="dk-field-error" id="aj-folder-err" role="alert" data-testid="aj-folder-problem">{t(`jobs.edit.folder.${problem}`, { folders: allowed.join(', ') })}</p>
        : <p className="dk-hint" id="aj-folder-hint">{t('jobs.edit.folder.hint')}</p>}
    </div>
  )
}

const show = (v) => displayValue(v === false ? 'false' : v)

// eslint-disable-next-line no-unused-vars
function TaskForm({ id, isNew }) {
  const { t } = useTranslation(['agents', 'chats'])
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [task, setTask] = useState(emptyTrigger)
  const [loading, setLoading] = useState(!isNew)
  const [loadError, setLoadError] = useState(null)
  const [attempt, setAttempt] = useState(0)
  const [saving, setSaving] = useState(false)
  const [open, setOpen] = useState(() => new Set(isNew ? ['basics', 'prompt', 'schedule'] : ['prompt', 'schedule']))
  const [preview, setPreview] = useState(null)
  const { agents, projects, ready } = useAgentProjects()
  const [folders, setFolders] = useState([])
  const location = useLocation()
  const [next, setNext] = useState({ text: '', error: '' })
  const initial = useRef(isNew ? triggerPayload(emptyTrigger()) : null)

  useEffect(() => {
    // Suggestions are best effort.
    filesApi.list('/').then((r) => setFolders((r.entries || []).filter((n) => n.kind === 'dir').map((n) => n.path))).catch(() => {})
  }, [])

  useEffect(() => {
    if (isNew) return undefined
    let alive = true
    chatTriggersApi.list().then(r => {
      if (!alive) return
      const found = (r.triggers || []).find(x => x.id === id)
      if (found) {
        const loaded = triggerForm(found)
        initial.current = triggerPayload(loaded)
        setTask(loaded)
      } else {
        addToast(t('jobs.task.missingTitle'), 'error')
      }
      setLoading(false)
    }).catch(err => {
      if (!alive) return
      // Said in place of the form: an empty form would save over the trigger.
      setLoadError(err)
      setLoading(false)
    })
    return () => { alive = false }
  }, [id, isNew, addToast, t, attempt])

  const set = (field, value) => setTask(prev => ({ ...prev, [field]: value }))

  // A new trigger opened from a project, or with an agent named in the address,
  // starts with that agent and the folder the agent works in. The prefilled
  // form is the base of the changes list, so it shows only what the person adds.
  const prefilled = useRef(false)
  useEffect(() => {
    if (!isNew || !ready || prefilled.current) return
    prefilled.current = true
    const params = new URLSearchParams(location.search)
    const apply = (agentId) => {
      const agent = agents.find(a => a.id === agentId)
      if (!agent) return
      const start = { ...emptyTrigger(), agentId: agent.id, folder: defaultFolder(agent) }
      initial.current = triggerPayload(start)
      setTask(prev => ({ ...prev, agentId: start.agentId, folder: prev.folder || start.folder }))
    }
    if (params.get('agent')) apply(params.get('agent'))
    else if (params.get('project')) {
      chatProjectsApi.agents(params.get('project')).then(r => { const a = firstProjectAgent(r.agents); if (a) apply(a.id) }).catch(() => {})
    }
  }, [isNew, ready, agents, location.search])

  // The next times of the schedule being written, so a mistake shows before saving.
  const previewText = task.schedule.preset !== 'file' ? scheduleText(task.schedule) : ''
  const previewZone = task.zone || ''
  useEffect(() => {
    if (!previewText) { setNext({ text: '', error: '' }); return undefined }
    let live = true
    const timer = setTimeout(() => {
      chatTriggersApi.preview(previewText, previewZone)
        .then((r) => { if (live) setNext({ text: `${r.summary}. ${t('chats:triggersPage.nextRuns', { times: joinTimes(r.times || [], previewZone) })}`, error: '' }) })
        .catch((e) => { if (live) setNext({ text: '', error: e?.message || '' }) })
    }, 300)
    return () => { live = false; clearTimeout(timer) }
  }, [previewText, previewZone, t])

  const payload = useMemo(() => triggerPayload(task), [task])
  const diff = diffConfig(initial.current || {}, payload).map(d => ({ key: d.key, before: show(d.before), after: show(d.after) }))
  const changedKeys = new Set(diff.map(d => d.key))
  const changed = (section) => (initial.current ? section.keys.filter(k => changedKeys.has(k)).length : 0)
  const dirty = !!initial.current && diff.length > 0

  const toggleSection = (sid) => setOpen(prev => {
    const nextSet = new Set(prev)
    if (nextSet.has(sid)) nextSet.delete(sid)
    else nextSet.add(sid)
    return nextSet
  })

  const picked = agents.find(a => a.id === task.agentId)
  const pickedProject = projectOfAgent(picked, projects)
  // An agent that works inside a fence may watch only the folders it has.
  const allowed = useMemo(() => allowedFolders(picked), [picked])
  const fileKind = task.schedule.preset === 'file'
  const [tried, setTried] = useState(false)
  // A trigger just made, with the secret of an event trigger. The secret is shown here once and kept only in this state.
  const [created, setCreated] = useState(null)
  const rawProblem = fileKind ? folderProblem(task.folder, allowed) : ''
  // An empty folder is told after a try to save; a folder outside the fence at once.
  const folderIssue = rawProblem === 'empty' && !tried ? '' : rawProblem
  // The folder of the agent starts a new file trigger, and replaces a folder of
  // another agent that the chosen one does not hold. A saved trigger is never
  // changed behind the person's back: its folder shows as a problem.
  useEffect(() => {
    if (!fileKind || !allowed || allowed.length === 0) return
    if (task.folder.trim() === '' || (isNew && rawProblem === 'outside')) {
      const d = defaultFolder(picked)
      if (d && d !== task.folder) setTask(prev => ({ ...prev, folder: d }))
    }
  }, [fileKind, picked, allowed, task.folder, rawProblem, isNew])
  // The grants of the chosen agent cap what the trigger can hold; with no agent only the list does.
  const agentGrants = picked ? (picked.builtins || []) : null
  const status = actsStatus({ acts: task.acts, agentGrants, writes: task.writes, lock: task.lock })
  const summary = (sid) => {
    if (sid === 'basics') {
      if (!task.name.trim()) return { state: 'needs', text: t('jobs.edit.summary.basicsEmpty') }
      const who = picked?.name || task.model
      return { state: 'ready', text: who ? t('jobs.edit.summary.basics', { name: task.name, model: who }) : t('jobs.edit.summary.basicsNoModel', { name: task.name }) }
    }
    if (sid === 'prompt') return task.prompt.trim() ? { state: 'ready', text: firstLine(task.prompt, 70) } : { state: 'needs', text: t('jobs.edit.summary.promptEmpty') }
    if (sid === 'schedule') {
      if (next.error) return { state: 'needs', text: next.error }
      if (task.schedule.preset === 'file') {
        if (rawProblem && rawProblem !== 'empty') return { state: 'needs', text: t(`jobs.edit.folder.${rawProblem}`, { folders: (allowed || []).join(', ') }) }
        return { state: task.folder.trim() ? 'ready' : 'needs', text: task.folder.trim() ? t('jobs.task.watches', { folder: task.folder }) : t('jobs.edit.summary.folderEmpty') }
      }
      if (task.schedule.preset === 'event') {
        return task.source.trim()
          ? { state: 'ready', text: eventSentence({ source: task.source.trim(), event_type: task.eventType.trim(), matches: matchList(task.matches) }).replaceAll('`', '') }
          : { state: 'needs', text: t('chats:event.form.sourceEmpty') }
      }
      return { state: 'ready', text: scheduleText(task.schedule) }
    }
    if (sid === 'acts') {
      if (status.readOnly) return { state: 'ready', text: t('chats:triggerActs.readOnly') }
      return { state: 'ready', text: t('jobs.edit.summary.acts', { count: status.held.length }) }
    }
    const cap = task.dailyCap ? t('jobs.edit.summary.cap', { count: Number(task.dailyCap) }) : t('jobs.edit.summary.capDefault')
    const limit = task.runLimit.trim()
    return { state: 'ready', text: limit ? `${cap}, ${t('jobs.edit.summary.runLimit', { limit })}` : cap }
  }

  const save = async (e) => {
    e?.preventDefault?.()
    if (!task.name?.trim()) { addToast(t('jobs.edit.nameRequired'), 'warning'); setOpen(prev => new Set([...prev, 'basics'])); return }
    if (!task.prompt.trim()) { addToast(t('jobs.edit.promptRequired'), 'warning'); setOpen(prev => new Set([...prev, 'prompt'])); return }
    // The server refuses a folder outside the agent's folders; say so before the request, in the section.
    if (rawProblem) {
      setTried(true)
      addToast(t(`jobs.edit.folder.${rawProblem}`, { folders: (allowed || []).join(', ') }), 'warning')
      setOpen(prev => new Set([...prev, 'schedule']))
      return
    }
    if (payload.kind === 'event' && !payload.source) {
      addToast(t('chats:event.form.sourceEmpty'), 'warning')
      setOpen(prev => new Set([...prev, 'schedule']))
      return
    }
    setSaving(true)
    try {
      if (isNew) {
        const made = await chatTriggersApi.create(payload)
        addToast(t('jobs.edit.created'), 'success')
        if (made?.secret) {
          // An event trigger: show its secret before leaving the page.
          initial.current = payload
          setCreated({ trigger: made, secret: made.secret })
          return
        }
      } else {
        await chatTriggersApi.update(id, payload)
        addToast(t('jobs.edit.updated'), 'success')
      }
      initial.current = payload
      navigate('/app/triggers')
    } catch (err) {
      addToast(t('jobs.edit.saveFailed', { message: err.message }), 'error')
    } finally {
      setSaving(false)
    }
  }

  if (loading || loadError) {
    return (
      <Page className="ag-page aj-page" data-testid="task-edit">
        <div className="ag-home" aria-label={loading ? t('jobs.edit.loadingTask') : undefined}>
          <button type="button" className="ag-back" onClick={() => navigate('/app/triggers')}><Icon name="arrow-left" /> {t('jobs.edit.back')}</button>
          {loadError
            ? <LoadError title={t('load.trigger')} error={loadError} onRetry={() => { setLoadError(null); setLoading(true); setAttempt(n => n + 1) }} />
            : <SkeletonRows count={4} />}
        </div>
      </Page>
    )
  }

  if (created) {
    return (
      <Page className="ag-page aj-page" data-testid="task-created">
        <div className="ag-home">
          <PageHeader title={t('chats:event.form.createdTitle', { name: created.trigger.name })} supporting={<EventSentence trigger={created.trigger} />} />
          <EventHook trigger={created.trigger} secret={created.secret} onSecret={(v) => setCreated({ ...created, secret: v })} />
          <div className="ag-foot">
            <Button variant="primary" onClick={() => navigate(`/app/triggers/${encodeURIComponent(created.trigger.id)}`)} data-testid="task-created-done">{t('chats:event.form.done')}</Button>
          </div>
        </div>
      </Page>
    )
  }

  const leave = () => navigate(isNew ? '/app/triggers' : `/app/triggers/${encodeURIComponent(id)}`)
  const saveLabel = isNew ? t('jobs.edit.create') : t('jobs.edit.save')

  const renderSection = (sid) => {
    if (sid === 'basics') {
      return (
        <div className="aj-form">
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-name">{t('jobs.edit.name')}</label>
            <input id="aj-name" className="dk-input" value={task.name} maxLength={80} onChange={(e) => set('name', e.target.value)} placeholder={t('jobs.edit.namePlaceholder')} required />
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-agent">{t('chats:triggersPage.agent')}</label>
            <span className="dk-select-wrap">
              <select id="aj-agent" className="dk-select" value={task.agentId} onChange={(e) => set('agentId', e.target.value)}>
                <option value="">{t('jobs.edit.noAgent')}</option>
                {agents.map((a) => <option key={a.id} value={a.id}>{agentLabel(a, projects, (name, project) => t('jobs.edit.agentInProject', { name, project }))}</option>)}
              </select>
            </span>
            {!task.agentId && <p className="dk-hint">{t('chats:triggersPage.noAgentNote')}</p>}
            {pickedProject && (
              <p className="dk-hint aj-agent-project" data-testid="aj-agent-project">
                <ProjectChip project={pickedProject} testId="aj-agent-project-chip" /> {t('jobs.edit.projectAgentHint')}
              </p>
            )}
          </div>
          {!picked?.model && (
            <div className="dk-field">
              <span className="dk-label" id="aj-model-l">{t('jobs.edit.model')}</span>
              <ModelPicker variant="field" searchable value={task.model} onChange={(model) => set('model', model)} capability={CAP_CHAT} />
            </div>
          )}
          <div className="dk-field aj-enabled">
            <button
              type="button"
              id="aj-enabled"
              className="dk-switch"
              role="switch"
              aria-checked={task.enabled}
              aria-describedby="aj-enabled-h"
              aria-label={t('jobs.edit.enabled')}
              onClick={() => set('enabled', !task.enabled)}
            />
            <div>
              <span className="dk-label">{t('jobs.edit.enabled')}</span>
              <p className="dk-hint" id="aj-enabled-h">{t('jobs.edit.enabledHint')}</p>
            </div>
          </div>
        </div>
      )
    }
    if (sid === 'prompt') {
      return (
        <div className="aj-form">
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-prompt">{t('jobs.edit.prompt')}</label>
            <textarea
              id="aj-prompt"
              className="dk-textarea"
              rows={7}
              value={task.prompt}
              onChange={(e) => set('prompt', e.target.value)}
              placeholder={t('jobs.edit.promptPlaceholder')}
              required
            />
            <p className="dk-hint">{t('jobs.edit.promptHint')}</p>
          </div>
        </div>
      )
    }
    if (sid === 'schedule') {
      return (
        <div className="aj-form">
          <ScheduleFields form={task} setForm={setTask} folders={folders} noFiles={!!picked && filesOff(picked)} allowed={allowed} problem={folderIssue} t={t} />
          {next.text && <p className="aj-words" data-testid="trigger-preview">{next.text}</p>}
          {next.error && <p className="dk-field-error" role="alert">{next.error}</p>}
        </div>
      )
    }
    if (sid === 'acts') {
      return (
        <div className="aj-form">
          <ActsEditor value={task.acts} onChange={(acts) => set('acts', acts)} agentGrants={agentGrants} writes={task.writes} lock={task.lock} />
        </div>
      )
    }
    return (
      <div className="aj-form">
        <div className="dk-field aj-time">
          <label className="dk-label" htmlFor="aj-cap">{t('chats:triggersPage.dailyCap')}</label>
          <input id="aj-cap" className="dk-input" type="number" min={0} max={1000} value={task.dailyCap} placeholder="24"
            onChange={(e) => set('dailyCap', e.target.value)} />
          <p className="dk-hint">{t('jobs.edit.capHint')}</p>
        </div>
        <div className="dk-field aj-time">
          <label className="dk-label" htmlFor="aj-run-limit">{t('chats:triggersPage.runLimit')}</label>
          <input id="aj-run-limit" className="dk-input" type="text" value={task.runLimit} placeholder={t('jobs.edit.runLimitPlaceholder')}
            autoComplete="off" spellCheck={false} onChange={(e) => set('runLimit', e.target.value)} />
          <p className="dk-hint">{t('jobs.edit.runLimitHint')}</p>
        </div>
      </div>
    )
  }

  return (
    <Page className="ag-page aj-page" data-testid="task-edit">
      <UnsavedChangesGuard when={dirty && !saving} />
      <form onSubmit={save} noValidate className="ag-edit">
        <div className="ag-edit__head">
          <button type="button" className="ag-back" onClick={leave}><Icon name="arrow-left" /> {t('jobs.edit.back')}</button>
          <PageHeader
            title={isNew ? t('jobs.edit.titleNew') : t('jobs.edit.titleEdit', { name: task.name })}
            supporting={isNew ? t('jobs.edit.subNew') : t('jobs.edit.subEdit')}
            actions={(
              <div className="ag-edit__acts">
                <Button variant="ghost" onClick={leave}>{t('jobs.edit.discard')}</Button>
                <Button icon="eye" onClick={() => setPreview('prompt')} data-testid="task-preview-open">
                  {t('jobs.edit.preview')}
                  {diff.length > 0 && <span className="dk-badge dk-badge--count">{diff.length}</span>}
                </Button>
                <Button type="submit" variant="primary" icon="save" busy={saving}>{saveLabel}</Button>
              </div>
            )}
          />
        </div>

        <div className="ag-folds">
          {SECTIONS.map(s => {
            const isOpen = open.has(s.id)
            const info = summary(s.id)
            const n = changed(s)
            return (
              <section key={s.id} className="ag-fold" data-open={isOpen || undefined} data-section={s.id}>
                <h2 className="ag-fold__h">
                  <button type="button" className="ag-fold__button" aria-expanded={isOpen} aria-controls={`aj-fold-${s.id}`} onClick={() => toggleSection(s.id)}>
                    <span className="ag-ready" data-state={info.state} aria-hidden="true"><Icon name={info.state === 'needs' ? 'warning' : 'check'} /></span>
                    <span className="ag-fold__title">{t(`jobs.edit.sections.${s.id}`)}</span>
                    <span className="ag-fold__sum">{info.text}</span>
                    {n > 0 ? <span className="ag-badge-changed">{t('jobs.edit.changed', { count: n })}</span> : <span />}
                    <Icon name="chevron-down" className="ag-fold__chev" />
                  </button>
                </h2>
                {isOpen && <div className="ag-fold__body" id={`aj-fold-${s.id}`}>{renderSection(s.id)}</div>}
              </section>
            )
          })}
        </div>

        <div className="ag-foot">
          <Button variant="ghost" onClick={leave}>{t('jobs.edit.discard')}</Button>
          <Button type="submit" variant="primary" icon="save" busy={saving}>{saveLabel}</Button>
        </div>
      </form>

      {preview && (
        <PreviewSheet
          tab={preview}
          onTab={setPreview}
          task={task}
          next={next.text}
          diff={diff}
          isNew={isNew}
          saving={saving}
          onSave={() => { setPreview(null); save() }}
          onClose={() => setPreview(null)}
        />
      )}
    </Page>
  )
}

// What the next run would send, and what differs from the saved trigger.
// eslint-disable-next-line no-unused-vars
function PreviewSheet({ tab, onTab, task, next, diff, isNew, saving, onSave, onClose }) {
  const { t } = useTranslation(['agents', 'chats'])

  return (
    <SideSheet
      wide
      title={t('jobs.edit.previewTitle')}
      description={isNew ? t('jobs.edit.previewNew') : t('jobs.edit.previewEdit')}
      closeLabel={t('jobs.edit.close')}
      labelId="aj-preview-title"
      testId="task-preview"
      initialFocus="close"
      onClose={onClose}
      footer={(
        <>
          <button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>{t('jobs.edit.close')}</button>
          <button type="button" className="dk-btn dk-btn--primary" disabled={saving} onClick={onSave}>{isNew ? t('jobs.edit.create') : t('jobs.edit.save')}</button>
        </>
      )}
    >
      <Tabs
        className="ag-sheet-tabs"
        label={t('jobs.edit.previewTitle')}
        idPrefix="task-preview"
        value={tab}
        onChange={onTab}
        items={[
          { id: 'prompt', label: t('jobs.edit.tabPrompt') },
          { id: 'changes', label: t('jobs.edit.tabChanges'), ...(diff.length > 0 ? { count: diff.length } : {}) },
        ]}
      />
      {tab === 'prompt' ? (
        <div className="dk-tabpanel" role="tabpanel" id="task-preview-panel-prompt" aria-labelledby="task-preview-tab-prompt" data-testid="task-preview-prompt">
          <p className="ag-note ag-note--bottom">{t('jobs.edit.promptAsSent')}</p>
          {task.prompt.trim() ? <pre className="aj-prompt">{task.prompt}</pre> : <p className="ag-note">{t('jobs.edit.emptyPrompt')}</p>}
          {next && (
            <div className="aj-context">
              <span className="ag-label">{t('jobs.task.schedule')}</span>
              <p className="ag-sec__text">{next}</p>
            </div>
          )}
        </div>
      ) : (
        <div className="dk-tabpanel" role="tabpanel" id="task-preview-panel-changes" aria-labelledby="task-preview-tab-changes" data-testid="task-preview-changes">
          {diff.length === 0 ? (
            <p className="ag-note">{isNew ? t('jobs.edit.nothingSet') : t('jobs.edit.noChanges')}</p>
          ) : (
            <div className="ag-diff">
              {diff.map(d => (
                <div key={d.key} className="ag-diff__item" data-key={d.key}>
                  <span className="ag-diff__key">{d.key}</span>
                  <div className="ag-diff__pair">
                    <div className="ag-diff__side" data-side="before"><span className="ag-diff__label">{t('jobs.edit.saved')}</span>{d.before || t('jobs.edit.empty')}</div>
                    <div className="ag-diff__side" data-side="after"><span className="ag-diff__label">{t('jobs.edit.new')}</span>{d.after || t('jobs.edit.empty')}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </SideSheet>
  )
}
