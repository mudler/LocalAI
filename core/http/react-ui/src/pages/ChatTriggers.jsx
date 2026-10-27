/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
import Icon from '../components/Icon'
import ModelSelector from '../components/ModelSelector'
import { chatAgentsApi, chatTriggersApi, filesApi } from '../utils/api'
import { CAP_CHAT } from '../utils/capabilities'
import {
  EVERY_HOURS, EVERY_MINUTES, HOURLY_MINUTES, browserZone, dayCodes, emptyForm, formatNext, joinTimes, presetOf,
  presetOrder, runTone, scheduleText, triggerPayload,
} from '../utils/chatTrigger'
import './chatskills.css'
import './chatauto.css'

const emptyTrigger = () => ({
  id: '', name: '', prompt: '', enabled: true, schedule: emptyForm(), zone: browserZone(),
  folder: '', quiet: '', dailyCap: '', agentId: '', model: '', sourceChatId: '',
})

const fromTrigger = (tr) => ({
  id: tr.id, name: tr.name, prompt: tr.prompt, enabled: tr.enabled, schedule: presetOf(tr), zone: tr.time_zone || browserZone(),
  folder: tr.folder || '', quiet: tr.quiet_seconds ? String(tr.quiet_seconds) : '', dailyCap: String(tr.daily_cap || ''),
  agentId: tr.agent_id || '', model: tr.model || '', sourceChatId: tr.source_chat_id || '',
})

function RunStatus({ run, t }) {
  if (!run) return <span className="cs-muted">{t('triggersPage.neverRan')}</span>
  return (
    <span>
      <span className={`ca-status ca-status--${runTone(run.status)}`}>{t(`triggersPage.status.${run.status}`)}</span>
      {run.reason && <span className="cs-muted"> {run.reason}</span>}
    </span>
  )
}

function RunsList({ id, t }) {
  const [runs, setRuns] = useState(null)
  useEffect(() => {
    let live = true
    chatTriggersApi.runs(id).then((r) => { if (live) setRuns(r.runs || []) }).catch(() => { if (live) setRuns([]) })
    return () => { live = false }
  }, [id])
  if (runs === null) return null
  if (runs.length === 0) return <p className="cs-muted">{t('triggersPage.noRuns')}</p>
  return (
    <ul className="ca-runs" aria-label={t('triggersPage.runs')}>
      {runs.map((r) => (
        <li key={r.id}>
          <span className={`ca-status ca-status--${runTone(r.status)}`}>{t(`triggersPage.status.${r.status}`)}</span>
          <span className="cs-muted">{formatNext(r.started || r.due, '')}</span>
          {r.reason && <span className="cs-muted">{r.reason}</span>}
          {r.chat_id && <Link to={`/app/chats/${r.chat_id}`}>{t('triggersPage.openChat')}</Link>}
        </li>
      ))}
    </ul>
  )
}

function ScheduleFields({ form, setForm, folders, t }) {
  const s = form.schedule
  const set = (patch) => setForm({ ...form, schedule: { ...s, ...patch } })
  const hasTime = ['daily', 'weekdays', 'weekly', 'monthly', 'once'].includes(s.preset)
  const folderList = useId()
  return (
    <>
      <label className="cs-field">
        <span>{t('triggersPage.when')}</span>
        <select className="input" aria-label={t('triggersPage.when')} value={s.preset} onChange={(e) => set({ preset: e.target.value })}>
          {presetOrder.map((p) => <option key={p} value={p}>{t(`triggersPage.presets.${p}`)}</option>)}
        </select>
      </label>
      <div className="ca-row2">
        {hasTime && (
          <label className="cs-field">
            <span>{t('triggersPage.time')}</span>
            <input className="input" type="time" value={s.time} required onChange={(e) => set({ time: e.target.value })} />
          </label>
        )}
        {s.preset === 'monthly' && (
          <label className="cs-field">
            <span>{t('triggersPage.dayOfMonth')}</span>
            <input className="input" type="number" min={1} max={31} value={s.day} onChange={(e) => set({ day: Number(e.target.value) })} />
          </label>
        )}
        {s.preset === 'once' && (
          <label className="cs-field">
            <span>{t('triggersPage.date')}</span>
            <input className="input" type="date" value={s.date} required onChange={(e) => set({ date: e.target.value })} />
          </label>
        )}
        {s.preset === 'hourly' && (
          <label className="cs-field">
            <span>{t('triggersPage.minute')}</span>
            <select className="input" aria-label={t('triggersPage.minute')} value={s.minute} onChange={(e) => set({ minute: Number(e.target.value) })}>
              {HOURLY_MINUTES.map((m) => <option key={m} value={m}>{String(m).padStart(2, '0')}</option>)}
            </select>
          </label>
        )}
        {s.preset === 'everyHours' && (
          <label className="cs-field">
            <span>{t('triggersPage.everyHours')}</span>
            <select className="input" aria-label={t('triggersPage.everyHours')} value={s.hours} onChange={(e) => set({ hours: Number(e.target.value) })}>
              {EVERY_HOURS.map((h) => <option key={h} value={h}>{h}</option>)}
            </select>
          </label>
        )}
        {s.preset === 'everyMinutes' && (
          <label className="cs-field">
            <span>{t('triggersPage.everyMinutes')}</span>
            <select className="input" aria-label={t('triggersPage.everyMinutes')} value={s.minutes} onChange={(e) => set({ minutes: Number(e.target.value) })}>
              {EVERY_MINUTES.map((m) => <option key={m} value={m}>{m}</option>)}
            </select>
          </label>
        )}
        {s.preset === 'cron' && (
          <label className="cs-field">
            <span>{t('triggersPage.cron')}</span>
            <input className="input" value={s.cron} onChange={(e) => set({ cron: e.target.value })} />
          </label>
        )}
        {s.preset !== 'file' && (
          <label className="cs-field">
            <span>{t('triggersPage.zone')}</span>
            <input className="input" value={form.zone} onChange={(e) => setForm({ ...form, zone: e.target.value })} />
          </label>
        )}
      </div>
      {s.preset === 'weekly' && (
        <fieldset className="cs-field">
          <legend>{t('triggersPage.days')}</legend>
          <div className="ca-days">
            {dayCodes.map((d) => (
              <label key={d} className="ca-radio">
                <input type="checkbox" checked={s.days.includes(d)}
                  onChange={(e) => set({ days: e.target.checked ? [...s.days, d] : s.days.filter((x) => x !== d) })} />
                <span>{t(`triggersPage.day.${d}`)}</span>
              </label>
            ))}
          </div>
        </fieldset>
      )}
      {s.preset === 'file' && (
        <div className="ca-row2">
          <label className="cs-field">
            <span>{t('triggersPage.folder')}</span>
            <input className="input" list={folderList} value={form.folder} required placeholder="/Contracts"
              onChange={(e) => setForm({ ...form, folder: e.target.value })} />
            <datalist id={folderList}>{folders.map((f) => <option key={f} value={f} />)}</datalist>
          </label>
          <label className="cs-field">
            <span>{t('triggersPage.quiet')}</span>
            <input className="input" type="number" min={0} max={86400} value={form.quiet} placeholder="30"
              onChange={(e) => setForm({ ...form, quiet: e.target.value })} />
          </label>
        </div>
      )}
    </>
  )
}

export default function ChatTriggers() {
  const { t } = useTranslation('chats')
  const [triggers, setTriggers] = useState([])
  const [proposals, setProposals] = useState([])
  const [agents, setAgents] = useState([])
  const [folders, setFolders] = useState([])
  const [off, setOff] = useState('')
  const [error, setError] = useState('')
  const [form, setForm] = useState(null)
  const [busy, setBusy] = useState(false)
  const [toDelete, setToDelete] = useState(null)
  const [preview, setPreview] = useState({ text: '', error: '' })
  const [openRuns, setOpenRuns] = useState('')

  const fail = useCallback((e) => {
    if (e?.status === 409 && e.body?.error?.type === 'triggers_off') { setOff(e.body.error.message); return }
    setError(e?.message || t('errors.send'))
  }, [t])

  const load = useCallback(async () => {
    try {
      const [list, props] = await Promise.all([chatTriggersApi.list(), chatTriggersApi.proposals()])
      setTriggers(list.triggers || [])
      setProposals(props.triggers || [])
      setOff('')
    } catch (e) { fail(e) }
  }, [fail])

  useEffect(() => { load() }, [load])
  // Suggestions are best effort.
  useEffect(() => {
    chatAgentsApi.list().then((r) => setAgents(r.agents || [])).catch(() => {})
    filesApi.list('/').then((r) => setFolders((r.entries || []).filter((n) => n.kind === 'dir').map((n) => n.path))).catch(() => {})
  }, [])

  // The next times of the schedule being written, so a mistake shows before saving.
  const previewText = form && form.schedule.preset !== 'file' ? scheduleText(form.schedule) : ''
  const previewZone = form?.zone || ''
  useEffect(() => {
    if (!previewText) { setPreview({ text: '', error: '' }); return undefined }
    let live = true
    const timer = setTimeout(() => {
      chatTriggersApi.preview(previewText, previewZone)
        .then((r) => { if (live) setPreview({ text: `${r.summary}. ${t('triggersPage.nextRuns', { times: joinTimes(r.times || [], previewZone) })}`, error: '' }) })
        .catch((e) => { if (live) setPreview({ text: '', error: e?.message || '' }) })
    }, 300)
    return () => { live = false; clearTimeout(timer) }
  }, [previewText, previewZone, t])

  const save = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const body = triggerPayload(form)
      if (form.id) await chatTriggersApi.update(form.id, body)
      else await chatTriggersApi.create(body)
      setForm(null)
      await load()
    } catch (err) { fail(err) } finally { setBusy(false) }
  }

  const act = async (fn) => {
    setError('')
    try { await fn(); await load() } catch (e) { fail(e) }
  }

  const confirmDelete = (tr) => { setToDelete(null); act(() => chatTriggersApi.remove(tr.id)) }

  if (off) {
    return (
      <div className="cs-page" data-testid="chat-triggers-page">
        <h1 className="cs-title">{t('triggersPage.title')}</h1>
        <div className="cs-notice" role="note">{off}</div>
      </div>
    )
  }

  const pickedAgent = agents.find((a) => a.id === form?.agentId)

  return (
    <div className="cs-page" data-testid="chat-triggers-page">
      <header className="cs-head">
        <h1 className="cs-title">{t('triggersPage.title')}</h1>
        <button type="button" className="btn btn-primary btn-sm" onClick={() => setForm(emptyTrigger())}>
          <Icon name="plus" /> {t('triggersPage.new')}
        </button>
      </header>
      <p className="cs-muted">{t('triggersPage.intro')}</p>

      {error && (
        <div className="cs-error" role="alert">
          <span>{error}</span>
          <button type="button" className="cs-link" onClick={() => setError('')}>{t('errors.dismiss')}</button>
        </div>
      )}

      {proposals.length > 0 && (
        <section className="cs-group" aria-label={t('triggersPage.proposals')} data-testid="proposals">
          <h2 className="cs-sub">{t('triggersPage.proposals')}</h2>
          <p className="cs-muted">{t('triggersPage.proposalsNote')}</p>
          <ul className="cs-list">
            {proposals.map((p) => (
              <li key={p.id} className="cs-row" data-testid="proposal-row">
                <div className="cs-row__text">
                  <span className="cs-row__name">{p.name}</span>
                  <span className="cs-muted">{p.summary}</span>
                  <span className="cs-muted">{p.prompt}</span>
                </div>
                <span className="cs-row__actions">
                  <button type="button" className="btn btn-primary btn-sm" onClick={() => act(() => chatTriggersApi.confirm(p.id, browserZone()))}>{t('triggersPage.confirm')}</button>
                  <button type="button" className="btn btn-secondary btn-sm" onClick={() => act(() => chatTriggersApi.dismiss(p.id))}>{t('triggersPage.dismiss')}</button>
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {form && (
        <form className="cs-form" onSubmit={save} aria-label={form.id ? t('triggersPage.edit') : t('triggersPage.new')}>
          <label className="cs-field">
            <span>{t('triggersPage.name')}</span>
            <input className="input" value={form.name} required maxLength={80} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </label>
          <ScheduleFields form={form} setForm={setForm} folders={folders} t={t} />
          {preview.text && <p className="ca-preview" data-testid="trigger-preview">{preview.text}</p>}
          {preview.error && <p className="cs-error" role="alert">{preview.error}</p>}
          <label className="cs-field">
            <span>{t('triggersPage.prompt')}</span>
            <textarea className="input cs-body" rows={5} required value={form.prompt} onChange={(e) => setForm({ ...form, prompt: e.target.value })} />
          </label>
          <div className="ca-row2">
            <label className="cs-field">
              <span>{t('triggersPage.agent')}</span>
              <select className="input" aria-label={t('triggersPage.agent')} value={form.agentId} onChange={(e) => setForm({ ...form, agentId: e.target.value })}>
                <option value="">{t('list.noAgent')}</option>
                {agents.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
              </select>
            </label>
            <label className="cs-field">
              <span>{t('triggersPage.dailyCap')}</span>
              <input className="input" type="number" min={0} max={1000} value={form.dailyCap} placeholder="24"
                onChange={(e) => setForm({ ...form, dailyCap: e.target.value })} />
            </label>
          </div>
          {!form.agentId && <p className="cs-muted">{t('triggersPage.noAgentNote')}</p>}
          {!pickedAgent?.model && (
            <div className="cs-field">
              <span>{t('agentsPage.model')}</span>
              <ModelSelector value={form.model} onChange={(m) => setForm({ ...form, model: m })} capability={CAP_CHAT} />
            </div>
          )}
          <label className="ca-radio">
            <input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
            <span>{t('triggersPage.on')}</span>
          </label>
          <div className="cs-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>{t('agentsPage.save')}</button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => setForm(null)}>{t('agentsPage.cancel')}</button>
          </div>
        </form>
      )}

      <ul className="cs-list">
        {triggers.map((tr) => (
          <li key={tr.id} className="cs-row" data-testid="trigger-row">
            <div className="cs-row__text">
              <span className="cs-row__name">{tr.name}</span>
              <span className="cs-muted">
                {tr.summary}
                {tr.next_run ? ` · ${t('triggersPage.next', { time: formatNext(tr.next_run, tr.time_zone) })}` : ''}
              </span>
              <RunStatus run={tr.last_run} t={t} />
              {openRuns === tr.id && <RunsList id={tr.id} t={t} />}
            </div>
            <span className="cs-row__actions">
              <label className="ca-radio">
                <input type="checkbox" checked={tr.enabled} aria-label={t('triggersPage.onFor', { name: tr.name })}
                  onChange={(e) => {
                    // The switch moves at once; the list is read again after the server answers.
                    const on = e.target.checked
                    setTriggers((list) => list.map((x) => (x.id === tr.id ? { ...x, enabled: on } : x)))
                    act(() => (on ? chatTriggersApi.enable(tr.id) : chatTriggersApi.disable(tr.id)))
                  }} />
                <span>{t('triggersPage.on')}</span>
              </label>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => act(() => chatTriggersApi.run(tr.id))}>{t('triggersPage.runNow')}</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setOpenRuns(openRuns === tr.id ? '' : tr.id)}>{t('triggersPage.runs')}</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setForm(fromTrigger(tr))}>{t('agentsPage.editButton')}</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setToDelete(tr)}>{t('agentsPage.delete')}</button>
            </span>
          </li>
        ))}
      </ul>
      {triggers.length === 0 && !form && <p className="cs-muted">{t('triggersPage.empty')}</p>}
      <p className="cs-muted"><Link to="/app/chats">{t('skillsPage.back')}</Link></p>
      <ChatDeleteConfirm item={toDelete} kind="trigger" onConfirm={confirmDelete} onCancel={() => setToDelete(null)} />
    </div>
  )
}
