import { useState, useEffect, useId, useMemo, useRef } from 'react'
import { createPortal } from 'react-dom'
import { Link, useParams, useNavigate, useOutletContext, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatAgentsApi, chatTriggersApi, filesApi } from '../utils/api'
// eslint-disable-next-line no-unused-vars
import ModelSelector from '../components/ModelSelector'
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
import { diffConfig, displayValue } from '../utils/agentConfigTools'
import { jobOfRun, STRIP } from '../utils/agentJobs'
import { jobSentence } from '../utils/agentJobText'
import { formatDuration, firstLine } from '../utils/agentRuns'
import { jobDurationMs } from '../utils/agentJobs'
import {
  EVERY_HOURS, EVERY_MINUTES, HOURLY_MINUTES, dayCodes, emptyTrigger, formatNext, joinTimes, presetOrder,
  scheduleText, triggerForm, triggerPayload,
} from '../utils/chatTrigger'
// eslint-disable-next-line no-unused-vars
import LoadingSpinner from '../components/LoadingSpinner'
import Icon from '../components/Icon'
import './agents.css'
import './agent-jobs.css'

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
  const [agents, setAgents] = useState([])
  const [loading, setLoading] = useState(true)
  const [missing, setMissing] = useState(false)
  const [toDelete, setToDelete] = useState(null)

  const load = async () => {
    try {
      const list = (await chatTriggersApi.list()).triggers || []
      const found = list.find(x => x.id === id)
      if (!found) { setMissing(true); return }
      setTask(found)
      const runs = await chatTriggersApi.runs(id).catch(() => ({ runs: [] }))
      setJobs((runs.runs || []).map(r => jobOfRun(r, id)))
    } catch (err) {
      addToast(t('jobs.task.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    chatAgentsApi.list().then(r => setAgents(r.agents || [])).catch(() => {})
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

  if (loading) return <div className="page page--medium loading-center"><LoadingSpinner size="lg" /></div>

  if (missing || !task) {
    return (
      <div className="page page--medium ag-page aj-page">
        <div className="ag-home">
          <Link className="ag-back" to="/app/triggers"><Icon name="arrow-left" /> {t('jobs.task.back')}</Link>
          <div className="ag-empty" data-testid="task-missing">
            <h1 className="ag-title">{t('jobs.task.missingTitle')}</h1>
            <p>{t('jobs.task.missingText')}</p>
          </div>
        </div>
      </div>
    )
  }

  const off = !task.enabled
  const agent = agents.find(a => a.id === task.agent_id)

  return (
    <div className="page page--medium ag-page aj-page" data-testid="task-page">
      <div className="ag-home">
        <div className="ag-bar">
          <Link className="ag-back" to="/app/triggers"><Icon name="arrow-left" /> {t('jobs.task.back')}</Link>
          <div className="ag-bar__acts">
            <Link className="dk-btn dk-btn--secondary dk-btn--sm" to={`/app/triggers/${encodeURIComponent(id)}/edit`}>
              <Icon name="edit" /> {t('jobs.task.edit')}
            </Link>
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={runNow} data-testid="task-run">
              <Icon name="play" /> {t('jobs.task.runNow')}
            </button>
            <ActionMenu
              ariaLabel={t('jobs.tasks.moreFor', { name: task.name })}
              items={[{ key: 'delete', icon: 'trash', label: t('jobs.tasks.delete'), danger: true, onClick: () => setToDelete(task) }]}
            />
          </div>
        </div>

        <header>
          <div className="ag-title">
            <h1>{task.name}</h1>
            <StatusMark status={off ? 'paused' : 'ready'} label={t(off ? 'jobs.task.disabled' : 'jobs.task.enabled')} />
          </div>
          <p className="ag-lede">{task.summary}</p>
        </header>

        <dl className="ag-facts" data-testid="task-facts">
          <dt>{t('jobs.task.agent')}</dt>
          <dd>{agent ? <Link className="ag-link" to={`/app/agents/${encodeURIComponent(agent.id)}`}>{agent.name}</Link> : <span className="ag-muted">{t('jobs.tasks.noAgent')}</span>}</dd>
          <dt>{t('jobs.task.model')}</dt>
          <dd className="ag-facts__model">{task.model || agent?.model || <span className="ag-muted">{t('jobs.tasks.noModel')}</span>}</dd>
          <dt>{t('jobs.task.schedule')}</dt>
          <dd>
            {task.kind === 'file' ? t('jobs.task.watches', { folder: task.folder }) : task.summary}
            {task.next_run && !off ? <span className="ag-muted"> · {t('chats:triggersPage.next', { time: formatNext(task.next_run, task.time_zone) })}</span> : null}
          </dd>
          <dt>{t('jobs.task.cap')}</dt>
          <dd className="ag-facts__model">{task.daily_cap || '-'}</dd>
          <dt>{t('jobs.task.enabledSwitch')}</dt>
          <dd>
            <button
              type="button"
              className="dk-switch"
              role="switch"
              aria-checked={!off}
              aria-label={t('jobs.tasks.toggleLabel', { name: task.name })}
              onClick={toggle}
            />
          </dd>
        </dl>

        <section aria-labelledby="aj-recent-h">
          <div className="ag-section-head">
            <h2 className="ag-eyebrow" id="aj-recent-h">{t('jobs.task.recent')}</h2>
            {mine.length > 0 && (
              <span className="aj-record">
                <JobStrip jobs={mine} />
                <span className="ag-muted ag-small">{t('jobs.task.recentNote', { count: Math.min(mine.length, STRIP) })}</span>
              </span>
            )}
          </div>
          {mine.length === 0 ? (
            <div className="ag-empty"><p>{t('jobs.task.noRuns')}</p></div>
          ) : (
            <div className="ag-runs" data-testid="task-runs">
              {mine.map(job => {
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
        </section>

        <section className="ag-sec" aria-labelledby="aj-prompt-h">
          <div className="ag-sec__head"><h2 id="aj-prompt-h">{t('jobs.task.prompt')}</h2></div>
          {task.prompt ? <pre className="aj-prompt">{task.prompt}</pre> : <p className="ag-note">{t('jobs.edit.summary.promptEmpty')}</p>}
        </section>
      </div>
      <ChatDeleteConfirm item={toDelete} kind="trigger" onConfirm={remove} onCancel={() => setToDelete(null)} />
    </div>
  )
}

// ---- A trigger, written --------------------------------------------------

const SECTIONS = [
  { id: 'basics', keys: ['name', 'agent_id', 'model', 'enabled'] },
  { id: 'prompt', keys: ['prompt'] },
  { id: 'schedule', keys: ['kind', 'schedule', 'time_zone', 'folder', 'quiet_seconds'] },
  { id: 'limits', keys: ['daily_cap'] },
]

// The fields of the schedule: the preset chosen, then what that preset needs.
// eslint-disable-next-line no-unused-vars
function ScheduleFields({ form, setForm, folders, t }) {
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
        {s.preset !== 'file' && (
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
      {s.preset === 'file' && (
        <div className="aj-row2">
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-folder">{t('chats:triggersPage.folder')}</label>
            <input id="aj-folder" className="dk-input" list={folderList} value={form.folder} required placeholder="/Contracts"
              onChange={(e) => setForm({ ...form, folder: e.target.value })} />
            <datalist id={folderList}>{folders.map((f) => <option key={f} value={f} />)}</datalist>
          </div>
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

const show = (v) => displayValue(v === false ? 'false' : v)

// eslint-disable-next-line no-unused-vars
function TaskForm({ id, isNew }) {
  const { t } = useTranslation(['agents', 'chats'])
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [task, setTask] = useState(emptyTrigger)
  const [loading, setLoading] = useState(!isNew)
  const [saving, setSaving] = useState(false)
  const [open, setOpen] = useState(() => new Set(isNew ? ['basics', 'prompt', 'schedule'] : ['prompt', 'schedule']))
  const [preview, setPreview] = useState(null)
  const [agents, setAgents] = useState([])
  const [folders, setFolders] = useState([])
  const [next, setNext] = useState({ text: '', error: '' })
  const initial = useRef(isNew ? triggerPayload(emptyTrigger()) : null)

  useEffect(() => {
    // Suggestions are best effort.
    chatAgentsApi.list().then((r) => setAgents(r.agents || [])).catch(() => {})
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
      addToast(t('jobs.task.loadFailed', { message: err.message }), 'error')
      setLoading(false)
    })
    return () => { alive = false }
  }, [id, isNew, addToast, t])

  const set = (field, value) => setTask(prev => ({ ...prev, [field]: value }))

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
  const summary = (sid) => {
    if (sid === 'basics') {
      if (!task.name.trim()) return { state: 'needs', text: t('jobs.edit.summary.basicsEmpty') }
      const who = picked?.name || task.model
      return { state: 'ready', text: who ? t('jobs.edit.summary.basics', { name: task.name, model: who }) : t('jobs.edit.summary.basicsNoModel', { name: task.name }) }
    }
    if (sid === 'prompt') return task.prompt.trim() ? { state: 'ready', text: firstLine(task.prompt, 70) } : { state: 'needs', text: t('jobs.edit.summary.promptEmpty') }
    if (sid === 'schedule') {
      if (next.error) return { state: 'needs', text: next.error }
      if (task.schedule.preset === 'file') return { state: task.folder.trim() ? 'ready' : 'needs', text: task.folder.trim() ? t('jobs.task.watches', { folder: task.folder }) : t('jobs.edit.summary.folderEmpty') }
      return { state: 'ready', text: scheduleText(task.schedule) }
    }
    return { state: 'ready', text: task.dailyCap ? t('jobs.edit.summary.cap', { count: Number(task.dailyCap) }) : t('jobs.edit.summary.capDefault') }
  }

  const save = async (e) => {
    e?.preventDefault?.()
    if (!task.name?.trim()) { addToast(t('jobs.edit.nameRequired'), 'warning'); setOpen(prev => new Set([...prev, 'basics'])); return }
    if (!task.prompt.trim()) { addToast(t('jobs.edit.promptRequired'), 'warning'); setOpen(prev => new Set([...prev, 'prompt'])); return }
    setSaving(true)
    try {
      if (isNew) {
        await chatTriggersApi.create(payload)
        addToast(t('jobs.edit.created'), 'success')
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

  if (loading) return <div className="page page--medium loading-center" aria-label={t('jobs.edit.loadingTask')}><LoadingSpinner size="lg" /></div>

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
                {agents.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
              </select>
            </span>
            {!task.agentId && <p className="dk-hint">{t('chats:triggersPage.noAgentNote')}</p>}
          </div>
          {!picked?.model && (
            <div className="dk-field">
              <span className="dk-label" id="aj-model-l">{t('jobs.edit.model')}</span>
              <ModelSelector value={task.model} onChange={(model) => set('model', model)} capability={CAP_CHAT} />
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
              className="dk-textarea aj-mono"
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
          <ScheduleFields form={task} setForm={setTask} folders={folders} t={t} />
          {next.text && <p className="aj-words" data-testid="trigger-preview">{next.text}</p>}
          {next.error && <p className="dk-field-error" role="alert">{next.error}</p>}
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
      </div>
    )
  }

  return (
    <div className="page page--medium ag-page aj-page" data-testid="task-edit">
      <UnsavedChangesGuard when={dirty && !saving} />
      <form onSubmit={save} noValidate className="ag-edit">
        <header className="ag-edit__head">
          <div>
            <button type="button" className="ag-back" onClick={leave}><Icon name="arrow-left" /> {t('jobs.edit.back')}</button>
            <h1>{isNew ? t('jobs.edit.titleNew') : t('jobs.edit.titleEdit', { name: task.name })}</h1>
            <p className="ag-edit__sub">{isNew ? t('jobs.edit.subNew') : t('jobs.edit.subEdit')}</p>
          </div>
          <div className="ag-edit__acts">
            <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setPreview('prompt')} data-testid="task-preview-open">
              <Icon name="eye" /> {t('jobs.edit.preview')}
              {diff.length > 0 && <span className="dk-badge dk-badge--count">{diff.length}</span>}
            </button>
            <button type="button" className="dk-btn dk-btn--secondary" onClick={leave}>{t('jobs.edit.discard')}</button>
            <button type="submit" className="dk-btn dk-btn--primary" disabled={saving}>
              {saving ? <><Icon name="spinner" spin /> {t('jobs.edit.saving')}</> : <><Icon name="save" /> {saveLabel}</>}
            </button>
          </div>
        </header>

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
          <button type="button" className="dk-btn dk-btn--secondary" onClick={leave}>{t('jobs.edit.discard')}</button>
          <button type="submit" className="dk-btn dk-btn--primary" disabled={saving}>
            {saving ? <><Icon name="spinner" spin /> {t('jobs.edit.saving')}</> : <><Icon name="save" /> {saveLabel}</>}
          </button>
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
    </div>
  )
}

// What the next run would send, and what differs from the saved trigger.
// eslint-disable-next-line no-unused-vars
function PreviewSheet({ tab, onTab, task, next, diff, isNew, saving, onSave, onClose }) {
  const { t } = useTranslation(['agents', 'chats'])
  const sheetRef = useRef(null)
  const closeRef = useRef(null)
  // The parent passes a fresh onClose on every render. Read it through a ref
  // so the effect below runs once per open: re-running it would move focus to
  // the opener and back to Close, pulling it out of the control in use.
  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose })

  useEffect(() => {
    const opener = document.activeElement
    closeRef.current?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onCloseRef.current(); return }
      if (e.key !== 'Tab' || !sheetRef.current) return
      const focusable = Array.from(sheetRef.current.querySelectorAll('button:not([disabled]), [tabindex]:not([tabindex="-1"])'))
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', onKey, true)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      if (opener && document.contains(opener)) opener.focus?.()
    }
  }, [])

  return createPortal(
    <div className="dk-sheet-veil" data-state="open" onMouseDown={onClose}>
      <div
        ref={sheetRef}
        className="dk-sheet dk-sheet--wide"
        role="dialog"
        aria-modal="true"
        aria-labelledby="aj-preview-title"
        data-state="open"
        data-testid="task-preview"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id="aj-preview-title">{t('jobs.edit.previewTitle')}</h2>
            <p className="dk-sheet-desc">{isNew ? t('jobs.edit.previewNew') : t('jobs.edit.previewEdit')}</p>
          </div>
          <button ref={closeRef} type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('jobs.edit.close')} onClick={onClose}>
            <Icon name="close" />
          </button>
        </div>
        <div className="dk-sheet-body">
          <div className="dk-tabs ag-sheet-tabs" role="tablist" aria-label={t('jobs.edit.previewTitle')}>
            <button type="button" role="tab" className="dk-tab" aria-selected={tab === 'prompt'} onClick={() => onTab('prompt')}>{t('jobs.edit.tabPrompt')}</button>
            <button type="button" role="tab" className="dk-tab" aria-selected={tab === 'changes'} onClick={() => onTab('changes')}>
              {t('jobs.edit.tabChanges')}{diff.length > 0 && <span className="dk-badge dk-badge--count">{diff.length}</span>}
            </button>
          </div>
          {tab === 'prompt' ? (
            <div className="dk-tabpanel" role="tabpanel" data-testid="task-preview-prompt">
              <p className="ag-note ag-note--bottom">{t('jobs.edit.promptAsSent')}</p>
              {task.prompt.trim() ? <pre className="aj-prompt">{task.prompt}</pre> : <p className="ag-note">{t('jobs.edit.emptyPrompt')}</p>}
              {next && (
                <div className="aj-context">
                  <span className="ag-eyebrow">{t('jobs.task.schedule')}</span>
                  <p className="ag-sec__text">{next}</p>
                </div>
              )}
            </div>
          ) : (
            <div className="dk-tabpanel" role="tabpanel" data-testid="task-preview-changes">
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
        </div>
        <div className="dk-sheet-foot">
          <button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>{t('jobs.edit.close')}</button>
          <button type="button" className="dk-btn dk-btn--primary" disabled={saving} onClick={onSave}>{isNew ? t('jobs.edit.create') : t('jobs.edit.save')}</button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
