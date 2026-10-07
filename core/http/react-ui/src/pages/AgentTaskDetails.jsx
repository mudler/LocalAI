import { useState, useEffect, useMemo, useRef } from 'react'
import { createPortal } from 'react-dom'
// eslint-disable-next-line no-unused-vars
import { Link, useParams, useNavigate, useOutletContext, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentJobsApi } from '../utils/api'
import { basePath } from '../utils/basePath'
// eslint-disable-next-line no-unused-vars
import ModelSelector from '../components/ModelSelector'
// eslint-disable-next-line no-unused-vars
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
// eslint-disable-next-line no-unused-vars
import RunTaskDialog from '../components/agents/RunTaskDialog'
// eslint-disable-next-line no-unused-vars
import { JobMark, JobStrip, ScheduleText, GappedPrompt } from '../components/agents/JobBits'
import { scheduleWords, cronErrorText, jobSentence } from '../utils/agentJobText'
// eslint-disable-next-line no-unused-vars
import { StatusMark, formatWhen } from '../components/agents/AgentBits'
import { CAP_CHAT } from '../utils/capabilities'
import {
  buildPreset, formatKeyValues, jobDurationMs, jobsOf, parseKeyValues, presetOf, promptParams, validateCron, STRIP,
} from '../utils/agentJobs'
import { firstLine, formatDuration } from '../utils/agentRuns'
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

function startedBy(job, t) {
  const who = job.triggered_by || 'manual'
  return t('jobs.history.startedBy', { who: t(`jobs.trigger.${who}`, { defaultValue: who }) })
}

// ---- A task, read --------------------------------------------------------

// eslint-disable-next-line no-unused-vars
function TaskView({ id }) {
  const { t } = useTranslation('agents')
  const { addToast } = useOutletContext()
  const [task, setTask] = useState(null)
  const [jobs, setJobs] = useState([])
  const [loading, setLoading] = useState(true)
  const [missing, setMissing] = useState(false)
  const [running, setRunning] = useState(false)

  const loadJobs = () => agentJobsApi.listJobs().then(list => {
    if (Array.isArray(list)) setJobs(list)
  }).catch(() => {})

  useEffect(() => {
    let alive = true
    agentJobsApi.getTask(id).then(data => {
      if (!alive) return
      if (data) setTask(data)
      else setMissing(true)
      setLoading(false)
    }).catch(err => {
      if (!alive) return
      if (err?.status === 404 || /not found/i.test(err?.message || '')) setMissing(true)
      else addToast(t('jobs.task.loadFailed', { message: err.message }), 'error')
      setLoading(false)
    })
    loadJobs()
    return () => { alive = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  const mine = useMemo(() => jobsOf(jobs, task?.id || id).slice(0, 20), [jobs, task, id])

  const toggle = async () => {
    const enabled = task.enabled === false
    const before = task
    setTask({ ...task, enabled })
    try {
      await agentJobsApi.updateTask(task.id || id, { ...task, enabled })
      addToast(t(enabled ? 'jobs.tasks.turnedOn' : 'jobs.tasks.turnedOff', { name: task.name }), 'success')
    } catch (err) {
      setTask(before)
      addToast(t('jobs.tasks.toggleFailed', { message: err.message }), 'error')
    }
  }

  if (loading) return <div className="page page--medium loading-center"><LoadingSpinner size="lg" /></div>

  if (missing || !task) {
    return (
      <div className="page page--medium ag-page aj-page">
        <div className="ag-home">
          <Link className="ag-back" to="/app/agent-jobs"><Icon name="arrow-left" /> {t('jobs.task.back')}</Link>
          <div className="ag-empty" data-testid="task-missing">
            <h1 className="ag-title">{t('jobs.task.missingTitle')}</h1>
            <p>{t('jobs.task.missingText')}</p>
          </div>
        </div>
      </div>
    )
  }

  const off = task.enabled === false
  const params = task.cron_parameters && typeof task.cron_parameters === 'object' ? task.cron_parameters : {}
  const hasParams = Object.keys(params).length > 0
  const webhooks = Array.isArray(task.webhooks) ? task.webhooks : []
  const sources = Array.isArray(task.multimedia_sources) ? task.multimedia_sources : []
  const origin = `${window.location.origin}${basePath}`
  const byName = encodeURIComponent(task.name)

  return (
    <div className="page page--medium ag-page aj-page" data-testid="task-page">
      <div className="ag-home">
        <div className="ag-bar">
          <Link className="ag-back" to="/app/agent-jobs"><Icon name="arrow-left" /> {t('jobs.task.back')}</Link>
          <div className="ag-bar__acts">
            <Link className="dk-btn dk-btn--secondary dk-btn--sm" to={`/app/agent-jobs/tasks/${id}/edit`}>
              <Icon name="edit" /> {t('jobs.task.edit')}
            </Link>
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => setRunning(true)}>
              <Icon name="play" /> {t('jobs.task.runNow')}
            </button>
          </div>
        </div>

        <header>
          <div className="ag-title">
            <h1>{task.name}</h1>
            <StatusMark status={off ? 'stopped' : 'done'} label={off ? t('jobs.task.disabled') : t('jobs.task.enabled')} />
          </div>
          {task.description && <p className="ag-lede">{task.description}</p>}
        </header>

        <dl className="ag-facts" data-testid="task-facts">
          <dt>{t('jobs.task.model')}</dt>
          <dd className="ag-facts__model">{task.model || <span className="ag-muted">{t('jobs.tasks.noModel')}</span>}</dd>
          <dt>{t('jobs.task.schedule')}</dt>
          <dd><ScheduleText expr={task.cron} off={off} /></dd>
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
          <dt>{t('jobs.task.webhooks')}</dt>
          <dd>{webhooks.length ? t('jobs.edit.summary.webhooks', { count: webhooks.length }) : <span className="ag-muted">{t('jobs.task.none')}</span>}</dd>
          <dt>{t('jobs.task.media')}</dt>
          <dd>{sources.length ? t('jobs.edit.summary.media', { count: sources.length }) : <span className="ag-muted">{t('jobs.task.none')}</span>}</dd>
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
                return (
                  <Link key={job.id} className="ag-run-row" to={`/app/agent-jobs/jobs/${job.id}`} data-job={job.id}>
                    <JobMark status={job.status} />
                    <span className="ag-run-row__main">
                      <span className="ag-run-row__title">{jobSentence(job, t)}</span>
                      <span className="ag-run-row__line">{took != null ? t('jobs.task.runMeta', { who: startedBy(job, t), time: formatDuration(took) }) : startedBy(job, t)}</span>
                    </span>
                    <span className="ag-run-row__when">{formatWhen(job.created_at)}</span>
                    <Icon name="chevron-right" />
                  </Link>
                )
              })}
            </div>
          )}
        </section>

        <section className="ag-sec" aria-labelledby="aj-prompt-h">
          <div className="ag-sec__head"><h2 id="aj-prompt-h">{t('jobs.task.prompt')}</h2></div>
          {task.prompt ? <GappedPrompt prompt={task.prompt} params={params} /> : <p className="ag-note">{t('jobs.edit.summary.promptEmpty')}</p>}
          {promptParams(task.prompt).length > 0 && <p className="ag-note">{t('jobs.task.promptNote')}</p>}
          {task.context && (
            <div className="aj-context">
              <span className="ag-eyebrow">{t('jobs.task.context')}</span>
              <p className="ag-sec__text">{task.context}</p>
            </div>
          )}
        </section>

        <section className="ag-sec" aria-labelledby="aj-sched-h">
          <div className="ag-sec__head"><h2 id="aj-sched-h">{t('jobs.task.scheduleSection')}</h2></div>
          {task.cron ? (
            <>
              <ScheduleText expr={task.cron} off={off} />
              {hasParams && (
                <div className="aj-context">
                  <span className="ag-eyebrow">{t('jobs.task.scheduleParams')}</span>
                  <div className="ag-chips">
                    {Object.entries(params).map(([k, v]) => <span key={k} className="ag-chip ag-chip--mono"><span className="ag-chip__text">{k}={String(v)}</span></span>)}
                  </div>
                </div>
              )}
              <p className="ag-note">{t('jobs.cron.zone')}</p>
            </>
          ) : <p className="ag-note">{t('jobs.task.noScheduleText')}</p>}
        </section>

        {webhooks.length > 0 && (
          <section className="ag-sec" aria-labelledby="aj-wh-h">
            <div className="ag-sec__head"><h2 id="aj-wh-h">{t('jobs.task.webhooks')}</h2></div>
            <ul className="aj-list">
              {webhooks.map((wh, i) => (
                <li key={i}><span className="ag-chip ag-chip--mono"><span className="ag-chip__text">{wh.method || 'POST'}</span></span> <code className="aj-url">{wh.url}</code></li>
              ))}
            </ul>
          </section>
        )}

        {sources.length > 0 && (
          <section className="ag-sec" aria-labelledby="aj-ms-h">
            <div className="ag-sec__head"><h2 id="aj-ms-h">{t('jobs.task.media')}</h2></div>
            <ul className="aj-list">
              {sources.map((ms, i) => (
                <li key={i}><span className="ag-chip ag-chip--mono"><span className="ag-chip__text">{ms.type}</span></span> <code className="aj-url">{ms.url}</code></li>
              ))}
            </ul>
          </section>
        )}

        <details className="aj-api">
          <summary>{t('jobs.task.apiTitle')}</summary>
          <span className="ag-eyebrow">{t('jobs.task.apiExecute')}</span>
          <pre className="ag-pre">{`curl -X POST ${origin}/api/agent/tasks/${byName}/execute`}</pre>
          <span className="ag-eyebrow">{t('jobs.task.apiMedia')}</span>
          <pre className="ag-pre">{`curl -X POST ${origin}/api/agent/tasks/${byName}/execute \\
  -H "Content-Type: application/json" \\
  -d '{"multimedia": {"images": [{"url": "https://example.com/image.jpg"}]}}'`}</pre>
          <span className="ag-eyebrow">{t('jobs.task.apiStatus')}</span>
          <pre className="ag-pre">{`curl ${origin}/api/agent/jobs/<job-id>`}</pre>
        </details>
      </div>

      {running && (
        <RunTaskDialog
          task={{ ...task, id: task.id || id }}
          addToast={addToast}
          onClose={() => setRunning(false)}
          onStarted={() => loadJobs()}
        />
      )}
    </div>
  )
}

// ---- A task, written -----------------------------------------------------

const BLANK = {
  name: '', description: '', model: '', prompt: '', context: '',
  enabled: true, cron: '', cron_parameters: '',
  webhooks: [], multimedia_sources: [],
}

// The API sends headers as an object; the form edits them as JSON text.
const headersText = (h) => (h && typeof h === 'object' && Object.keys(h).length ? JSON.stringify(h) : typeof h === 'string' && h ? h : '{}')

const SECTIONS = [
  { id: 'basics', keys: ['name', 'model', 'description', 'enabled'] },
  { id: 'prompt', keys: ['prompt', 'context'] },
  { id: 'schedule', keys: ['cron', 'cron_parameters'] },
  { id: 'media', keys: ['multimedia_sources'] },
  { id: 'webhooks', keys: ['webhooks'] },
]

const show = (v) => (typeof v === 'object' ? JSON.stringify(v, null, 2) : typeof v === 'boolean' ? String(v) : String(v ?? ''))

// eslint-disable-next-line no-unused-vars
function TaskForm({ id, isNew }) {
  const { t, i18n } = useTranslation('agents')
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [task, setTask] = useState(BLANK)
  const [loading, setLoading] = useState(!isNew)
  const [saving, setSaving] = useState(false)
  const [custom, setCustom] = useState(false)
  const [open, setOpen] = useState(() => new Set(isNew ? ['basics', 'prompt', 'schedule'] : ['prompt', 'schedule']))
  const [preview, setPreview] = useState(null)
  const initial = useRef(isNew ? BLANK : null)

  useEffect(() => {
    if (isNew) return undefined
    let alive = true
    agentJobsApi.getTask(id).then(data => {
      if (!alive) return
      if (data) {
        const loaded = {
          name: data.name || '',
          description: data.description || '',
          model: data.model || '',
          prompt: data.prompt || '',
          context: data.context || '',
          enabled: data.enabled !== false,
          cron: data.cron || '',
          cron_parameters: formatKeyValues(data.cron_parameters),
          webhooks: (Array.isArray(data.webhooks) ? data.webhooks : []).map(w => ({ ...w, method: w.method || 'POST', headers: headersText(w.headers), payload_template: w.payload_template || '' })),
          multimedia_sources: (Array.isArray(data.multimedia_sources) ? data.multimedia_sources : []).map(m => ({ ...m, headers: headersText(m.headers) })),
        }
        initial.current = loaded
        setTask(loaded)
        setCustom(presetOf(loaded.cron).kind === 'custom')
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
  const setItem = (field, i, key, value) => setTask(prev => ({ ...prev, [field]: prev[field].map((x, j) => (j === i ? { ...x, [key]: value } : x)) }))
  const removeItem = (field, i) => setTask(prev => ({ ...prev, [field]: prev[field].filter((_, j) => j !== i) }))

  const cronError = validateCron(task.cron)
  const preset = custom ? { kind: 'custom' } : presetOf(task.cron)
  const timeOf = presetOf(task.cron).time || '09:00'
  const gaps = promptParams(task.prompt)
  const scheduleParams = parseKeyValues(task.cron_parameters)
  const unset = gaps.filter(g => !(g in scheduleParams))

  const changed = (section) => {
    if (!initial.current) return []
    return section.keys.filter(k => JSON.stringify(task[k]) !== JSON.stringify(initial.current[k]))
  }
  const diff = SECTIONS.flatMap(s => changed(s)).map(key => ({ key, before: show(initial.current?.[key]), after: show(task[key]) }))
  const dirty = !!initial.current && diff.length > 0

  const toggleSection = (sid) => setOpen(prev => {
    const next = new Set(prev)
    if (next.has(sid)) next.delete(sid)
    else next.add(sid)
    return next
  })

  const pickPreset = (kind) => {
    if (kind === 'custom') { setCustom(true); return }
    setCustom(false)
    if (kind === 'none') set('cron', '')
    else set('cron', buildPreset(kind, timeOf))
  }

  const summary = (sid) => {
    if (sid === 'basics') {
      if (!task.name.trim()) return { state: 'needs', text: t('jobs.edit.summary.basicsEmpty') }
      return { state: 'ready', text: task.model ? t('jobs.edit.summary.basics', { name: task.name, model: task.model }) : t('jobs.edit.summary.basicsNoModel', { name: task.name }) }
    }
    if (sid === 'prompt') return { state: 'ready', text: task.prompt.trim() ? firstLine(task.prompt, 70) : t('jobs.edit.summary.promptEmpty') }
    if (sid === 'schedule') {
      if (cronError) return { state: 'needs', text: cronErrorText(cronError, t) }
      if (!task.cron.trim()) return { state: 'ready', text: t('jobs.edit.summary.scheduleNone') }
      return { state: 'ready', text: `${scheduleWords(task.cron, t, i18n.language) || t('jobs.cron.custom')}, ${task.cron.trim()}` }
    }
    if (sid === 'media') return { state: 'ready', text: task.multimedia_sources.length ? t('jobs.edit.summary.media', { count: task.multimedia_sources.length }) : t('jobs.edit.summary.mediaNone') }
    return { state: 'ready', text: task.webhooks.length ? t('jobs.edit.summary.webhooks', { count: task.webhooks.length }) : t('jobs.edit.summary.webhooksNone') }
  }

  const save = async (e) => {
    e?.preventDefault?.()
    if (!task.name?.trim()) { addToast(t('jobs.edit.nameRequired'), 'warning'); setOpen(prev => new Set([...prev, 'basics'])); return }
    if (cronError) { addToast(t('jobs.edit.cronFix'), 'warning'); setOpen(prev => new Set([...prev, 'schedule'])); return }

    setSaving(true)
    try {
      const body = { ...task }
      if (body.cron_parameters && typeof body.cron_parameters === 'string') body.cron_parameters = parseKeyValues(body.cron_parameters)
      else delete body.cron_parameters

      const parseHeaders = (text, where) => {
        try { return JSON.parse(text || '{}') } catch { throw new Error(t('jobs.edit.headerJson', { where })) }
      }
      body.webhooks = body.webhooks.map((wh, i) => ({ ...wh, headers: typeof wh.headers === 'string' ? parseHeaders(wh.headers, `${t('jobs.task.webhooks')} ${i + 1}`) : wh.headers }))
      body.multimedia_sources = body.multimedia_sources.map((ms, i) => ({ ...ms, headers: typeof ms.headers === 'string' ? parseHeaders(ms.headers, `${t('jobs.task.media')} ${i + 1}`) : ms.headers }))

      if (isNew) {
        await agentJobsApi.createTask(body)
        addToast(t('jobs.edit.created'), 'success')
      } else {
        await agentJobsApi.updateTask(id, body)
        addToast(t('jobs.edit.updated'), 'success')
      }
      navigate('/app/agent-jobs')
    } catch (err) {
      addToast(t('jobs.edit.saveFailed', { message: err.message }), 'error')
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <div className="page page--medium loading-center" aria-label={t('jobs.edit.loadingTask')}><LoadingSpinner size="lg" /></div>

  const leave = () => navigate('/app/agent-jobs')
  const saveLabel = isNew ? t('jobs.edit.create') : t('jobs.edit.save')

  const renderSection = (sid) => {
    if (sid === 'basics') {
      return (
        <div className="aj-form">
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-name">{t('jobs.edit.name')}</label>
            <input id="aj-name" className="dk-input" value={task.name} onChange={(e) => set('name', e.target.value)} placeholder={t('jobs.edit.namePlaceholder')} required />
          </div>
          <div className="dk-field">
            <span className="dk-label" id="aj-model-l">{t('jobs.edit.model')}</span>
            <ModelSelector value={task.model} onChange={(model) => set('model', model)} capability={CAP_CHAT} />
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-desc">{t('jobs.edit.description')}</label>
            <input id="aj-desc" className="dk-input" value={task.description} onChange={(e) => set('description', e.target.value)} placeholder={t('jobs.edit.descriptionPlaceholder')} />
          </div>
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
              placeholder={t('jobs.edit.promptPlaceholder', { topic: '{{.topic}}', format: '{{.format}}' })}
            />
            <p className="dk-hint">{t('jobs.edit.promptHint', { gap: '{{.name}}' })}</p>
            <p className="aj-found" data-testid="prompt-params">
              {gaps.length === 0
                ? <span className="ag-muted">{t('jobs.edit.foundNone')}</span>
                : <>{t('jobs.edit.found')} {gaps.map(g => <mark key={g} className="aj-gap" data-filled>{`{{.${g}}}`}</mark>)}</>}
            </p>
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-context">{t('jobs.edit.context')}</label>
            <textarea id="aj-context" className="dk-textarea" rows={3} value={task.context} onChange={(e) => set('context', e.target.value)} placeholder={t('jobs.edit.contextPlaceholder')} />
          </div>
        </div>
      )
    }
    if (sid === 'schedule') {
      const kinds = ['none', 'hourly', 'daily', 'weekdays', 'custom']
      const words = !cronError && task.cron.trim() ? (scheduleWords(task.cron, t, i18n.language) || t('jobs.cron.custom')) : ''
      return (
        <div className="aj-form">
          <div className="dk-field">
            <span className="dk-label" id="aj-when-l">{t('jobs.edit.when')}</span>
            <div className="aj-presets" role="radiogroup" aria-labelledby="aj-when-l" data-testid="schedule-presets">
              {kinds.map(k => (
                <button
                  key={k}
                  type="button"
                  role="radio"
                  aria-checked={preset.kind === k}
                  className="dk-chip"
                  aria-pressed={preset.kind === k}
                  data-preset={k}
                  onClick={() => pickPreset(k)}
                >
                  {t(`jobs.edit.preset${k[0].toUpperCase()}${k.slice(1)}`)}
                </button>
              ))}
            </div>
          </div>
          {(preset.kind === 'daily' || preset.kind === 'weekdays') && (
            <div className="dk-field aj-time">
              <label className="dk-label" htmlFor="aj-time">{t('jobs.edit.time')}</label>
              <input
                id="aj-time"
                type="time"
                className="dk-input"
                value={preset.time || timeOf}
                onChange={(e) => { if (e.target.value) set('cron', buildPreset(preset.kind, e.target.value)) }}
              />
            </div>
          )}
          {preset.kind === 'custom' && (
            <div className="dk-field">
              <label className="dk-label" htmlFor="aj-cron">{t('jobs.edit.cron')}</label>
              <input
                id="aj-cron"
                className="dk-input dk-input--mono"
                value={task.cron}
                onChange={(e) => set('cron', e.target.value)}
                placeholder="0 */6 * * *"
                aria-invalid={cronError ? 'true' : undefined}
                aria-describedby={cronError ? 'aj-cron-e' : 'aj-cron-h'}
                spellCheck={false}
              />
              {cronError
                ? <p className="dk-field-error" id="aj-cron-e" role="alert">{cronErrorText(cronError, t)}</p>
                : <p className="dk-hint" id="aj-cron-h">{t('jobs.edit.cronHint')}</p>}
            </div>
          )}
          {words && (
            <p className="aj-words" data-testid="schedule-words">
              <strong>{words}.</strong> {t('jobs.cron.zone')}
            </p>
          )}
          {task.cron.trim() && !cronError && (
            <div className="dk-field">
              <label className="dk-label" htmlFor="aj-cparams">{t('jobs.edit.scheduleParams')}</label>
              <textarea
                id="aj-cparams"
                className="dk-textarea aj-mono"
                rows={3}
                value={task.cron_parameters}
                onChange={(e) => set('cron_parameters', e.target.value)}
                placeholder={'topic=daily news\nformat=bullet points'}
              />
              <p className="dk-hint">{t('jobs.edit.scheduleParamsHint')}</p>
            </div>
          )}
          {task.cron.trim() && !cronError && unset.length > 0 && (
            <p className="aj-warn" role="note" data-testid="schedule-gaps">
              <Icon name="warning" /> {t('jobs.edit.gapNoValue', { names: unset.map(g => `{{.${g}}}`).join(', ') })}
            </p>
          )}
        </div>
      )
    }
    if (sid === 'media') {
      return (
        <div className="aj-form">
          <p className="ag-note">{t('jobs.edit.mediaNote')}</p>
          {task.multimedia_sources.length === 0 && <p className="ag-note">{t('jobs.edit.mediaEmpty')}</p>}
          {task.multimedia_sources.map((ms, i) => (
            <div key={i} className="aj-item" data-testid="media-source">
              <div className="aj-item__row">
                <div className="dk-field aj-item__type">
                  <label className="dk-label" htmlFor={`aj-ms-t-${i}`}>{t('jobs.edit.sourceType')}</label>
                  <span className="dk-select-wrap">
                    <select id={`aj-ms-t-${i}`} className="dk-select" value={ms.type} onChange={(e) => setItem('multimedia_sources', i, 'type', e.target.value)}>
                      {['image', 'video', 'audio', 'file'].map(k => <option key={k} value={k}>{t(`jobs.edit.${k}`)}</option>)}
                    </select>
                  </span>
                </div>
                <div className="dk-field aj-item__grow">
                  <label className="dk-label" htmlFor={`aj-ms-u-${i}`}>{t('jobs.edit.url')}</label>
                  <input id={`aj-ms-u-${i}`} className="dk-input" value={ms.url} onChange={(e) => setItem('multimedia_sources', i, 'url', e.target.value)} placeholder="https://example.com/media.jpg" />
                </div>
                <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm aj-item__x" aria-label={t('jobs.edit.removeSource')} onClick={() => removeItem('multimedia_sources', i)}>
                  <Icon name="trash" />
                </button>
              </div>
              <div className="dk-field">
                <label className="dk-label" htmlFor={`aj-ms-h-${i}`}>{t('jobs.edit.headers')}</label>
                <input id={`aj-ms-h-${i}`} className="dk-input dk-input--mono" value={ms.headers} onChange={(e) => setItem('multimedia_sources', i, 'headers', e.target.value)} placeholder='{"Authorization": "Bearer ..."}' />
              </div>
            </div>
          ))}
          <div>
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => set('multimedia_sources', [...task.multimedia_sources, { type: 'image', url: '', headers: '{}' }])}>
              <Icon name="plus" /> {t('jobs.edit.addSource')}
            </button>
          </div>
        </div>
      )
    }
    return (
      <div className="aj-form">
        <p className="ag-note">{t('jobs.edit.webhooksNote')}</p>
        {task.webhooks.length === 0 && <p className="ag-note">{t('jobs.edit.webhooksEmpty')}</p>}
        {task.webhooks.map((wh, i) => (
          <div key={i} className="aj-item" data-testid="webhook">
            <div className="aj-item__row">
              <div className="dk-field aj-item__type">
                <label className="dk-label" htmlFor={`aj-wh-m-${i}`}>{t('jobs.edit.method')}</label>
                <span className="dk-select-wrap">
                  <select id={`aj-wh-m-${i}`} className="dk-select" value={wh.method} onChange={(e) => setItem('webhooks', i, 'method', e.target.value)}>
                    {['POST', 'PUT', 'PATCH'].map(m => <option key={m} value={m}>{m}</option>)}
                  </select>
                </span>
              </div>
              <div className="dk-field aj-item__grow">
                <label className="dk-label" htmlFor={`aj-wh-u-${i}`}>{t('jobs.edit.url')}</label>
                <input id={`aj-wh-u-${i}`} className="dk-input" value={wh.url} onChange={(e) => setItem('webhooks', i, 'url', e.target.value)} placeholder="https://hooks.slack.com/..." />
              </div>
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm aj-item__x" aria-label={t('jobs.edit.removeWebhook')} onClick={() => removeItem('webhooks', i)}>
                <Icon name="trash" />
              </button>
            </div>
            <div className="dk-field">
              <label className="dk-label" htmlFor={`aj-wh-h-${i}`}>{t('jobs.edit.headers')}</label>
              <input id={`aj-wh-h-${i}`} className="dk-input dk-input--mono" value={wh.headers} onChange={(e) => setItem('webhooks', i, 'headers', e.target.value)} placeholder='{"Content-Type": "application/json"}' />
            </div>
            <div className="dk-field">
              <label className="dk-label" htmlFor={`aj-wh-p-${i}`}>{t('jobs.edit.payload')}</label>
              <textarea
                id={`aj-wh-p-${i}`}
                className="dk-textarea aj-mono"
                rows={3}
                value={wh.payload_template}
                onChange={(e) => setItem('webhooks', i, 'payload_template', e.target.value)}
                placeholder={'{"text": "Job {{.Status}}: {{if .Error}}Error: {{.Error}}{{else}}{{.Result}}{{end}}"}'}
              />
              <p className="dk-hint">{t('jobs.edit.payloadHint', { vars: '{{.Job}} {{.Task}} {{.Result}} {{.Error}} {{.Status}}' })}</p>
            </div>
          </div>
        ))}
        <div>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => set('webhooks', [...task.webhooks, { url: '', method: 'POST', headers: '{}', payload_template: '' }])}>
            <Icon name="plus" /> {t('jobs.edit.addWebhook')}
          </button>
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
            <h1>{isNew ? t('jobs.edit.titleNew') : t('jobs.edit.titleEdit', { name: initial.current?.name || task.name })}</h1>
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
            const n = changed(s).length
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
          params={scheduleParams}
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

// What the next scheduled run would send, and what differs from the saved task.
// eslint-disable-next-line no-unused-vars
function PreviewSheet({ tab, onTab, task, params, diff, isNew, saving, onSave, onClose }) {
  const { t } = useTranslation('agents')
  const sheetRef = useRef(null)
  const closeRef = useRef(null)

  useEffect(() => {
    const opener = document.activeElement
    closeRef.current?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose(); return }
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
  }, [onClose])

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
              {task.prompt.trim() ? <GappedPrompt prompt={task.prompt} params={params} /> : <p className="ag-note">{t('jobs.edit.emptyPrompt')}</p>}
              {task.context.trim() && (
                <div className="aj-context">
                  <span className="ag-eyebrow">{t('jobs.task.context')}</span>
                  <p className="ag-sec__text">{task.context}</p>
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
