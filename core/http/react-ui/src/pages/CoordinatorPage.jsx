/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatConnectorsApi, chatCoordinatorsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useAgentTools } from '../hooks/useAgentTools'
import { useAgentAbilities } from '../hooks/useAgentAbilities'
import { useModels } from '../hooks/useModels'
import { useToolGroups } from '../hooks/useToolGroups'
import { describeChatModel } from '../utils/describeModel'
import { CAP_CHAT } from '../utils/capabilities'
import {
  CAPS, MAX_CONTACTS, contactsError, coordinatorForm, coordinatorFromApi, coordinatorPayload, fieldOfError, wakeOptions,
} from '../utils/coordinator'
import Icon from '../components/Icon'
import ModelPicker from '../components/ModelPicker'
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
import { SkillsPanel } from '../components/chat/SkillsMenu'
import AgentUsesPanel from '../components/agents/AgentUsesPanel'
import CoordinatorAudit from '../components/agents/CoordinatorAudit'
import CoordinatorContacts from '../components/agents/CoordinatorContacts'
import { CoordinatorStateChip, usageLine, wakeLabel, whenOrDash } from '../components/agents/CoordinatorBits'
import { Button, Callout, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
import './agents.css'
import './coordinator.css'

// The create and edit page of a coordinator: a saved agent with one long chat
// that wakes on a schedule, reaches only its contacts and is held by limits the
// server enforces. Everything the creator writes here is the creator's policy;
// LocalAI decides no topic and no rule.
export default function CoordinatorPage() {
  const { id } = useParams()
  const isEdit = !!id
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t } = useTranslation(['agents', 'home'])
  const { user } = useAuth()

  const [loading, setLoading] = useState(isEdit)
  const [loadError, setLoadError] = useState(null)
  const [attempt, setAttempt] = useState(0)
  const [saved, setSaved] = useState(null) // the coordinator as read
  const [form, setForm] = useState(coordinatorForm)
  const initialRef = useRef(null)
  if (!initialRef.current) initialRef.current = form
  const [saving, setSaving] = useState(false)
  const [tried, setTried] = useState(false)
  const [serverError, setServerError] = useState('')
  const [busy, setBusy] = useState(false)
  const [bindings, setBindings] = useState(null)
  const [bindingsFailed, setBindingsFailed] = useState(false)

  const updateField = useCallback((name, value) => setForm(prev => ({ ...prev, [name]: value })), [])
  const { models: chatModels, loading: modelsLoading } = useModels(CAP_CHAT, { keepBroken: true })
  const tools = useAgentTools(form, updateField)
  const toolGroups = useToolGroups(tools, { commandState: false, forAgent: true })
  const ab = useAgentAbilities({ form, setForm, updateField, tools, initialFolders: [] })

  useEffect(() => {
    chatConnectorsApi.list()
      .then(r => { setBindings(Array.isArray(r?.connectors) ? r.connectors : []); setBindingsFailed(false) })
      .catch(() => { setBindings([]); setBindingsFailed(true) })
  }, [])

  const read = useCallback(async () => {
    const c = await chatCoordinatorsApi.get(id)
    const next = coordinatorFromApi(c)
    setSaved(c)
    initialRef.current = next
    setForm(next)
    return c
  }, [id])

  useEffect(() => {
    if (!isEdit) return undefined
    let live = true
    setLoading(true)
    setLoadError(null)
    read().catch(err => { if (live) setLoadError(err) }).finally(() => { if (live) setLoading(false) })
    return () => { live = false }
  }, [isEdit, read, attempt])

  const payload = useMemo(() => coordinatorPayload(form), [form])
  const dirty = JSON.stringify(payload) !== JSON.stringify(coordinatorPayload(initialRef.current))

  // What blocks a save, from the same rules the server applies. The server
  // checks again, and the operator may set lower ceilings than these.
  const problems = useMemo(() => {
    const out = {}
    if (!form.name.trim()) out.name = 'needsName'
    const c = contactsError(form.contacts)
    if (c) out.contacts = c
    for (const k of Object.keys(CAPS)) {
      const raw = String(form[k] ?? '').trim()
      if (raw === '') continue
      const n = Number(raw)
      if (!Number.isInteger(n) || n < 1 || n > CAPS[k].max) out[k] = 'range'
    }
    return out
  }, [form])
  const shown = tried ? problems : {}

  const focusFirst = (p) => {
    const order = [['name', 'crd-name'], ['contacts', 'coordinator-contact-add'], ...Object.keys(CAPS).map(k => [k, `crd-${k}`])]
    const hit = order.find(([k]) => p[k])
    if (hit) {
      const el = document.getElementById(hit[1]) || document.querySelector(`[data-testid="${hit[1]}"]`)
      el?.scrollIntoView({ block: 'center' })
      el?.focus({ preventScroll: true })
    }
  }

  const submit = async (e) => {
    e?.preventDefault?.()
    setTried(true)
    setServerError('')
    if (Object.keys(problems).length) { focusFirst(problems); return }
    setSaving(true)
    try {
      if (isEdit) {
        await chatCoordinatorsApi.update(id, payload)
        initialRef.current = form
        await read()
        addToast(t('coordinator.updated', { name: payload.name }), 'success')
      } else {
        const made = await chatCoordinatorsApi.create(payload)
        initialRef.current = form
        addToast(t('coordinator.created', { name: payload.name }), 'success')
        // The guard must see the form as saved before the route changes.
        setTimeout(() => navigate(made?.id ? `/app/agents/${encodeURIComponent(made.id)}/coordinator` : '/app/agents'), 0)
      }
    } catch (err) {
      // The 400 says what is wrong in words; say it in place and keep the form.
      setServerError(err.message || '')
      document.getElementById('coordinator-server-error')?.scrollIntoView({ block: 'center' })
    } finally {
      setSaving(false)
    }
  }

  const act = async (fn, done) => {
    setBusy(true)
    try {
      await fn()
      await read()
      addToast(done, 'success')
    } catch (err) {
      addToast(t('coordinator.actionFailed', { message: err.message }), 'error')
    } finally {
      setBusy(false)
    }
  }

  const leave = () => navigate('/app/agents')
  const name = (form.name || '').trim()
  const skillRows = tools.skills.map(sk => (sk.layer === 'addon' && !sk.gone && !(form.addons || []).includes(sk.addon)
    ? { ...sk, unavailable: t('create.uses.skillAddonOff') } : sk))
  const chosenModel = chatModels.find(m => m.id === form.model)
  const describeModel = (n) => describeChatModel(chatModels, n, c => t(`home:picker.cap.${c}`, { defaultValue: c }))
  const errorSection = serverError ? fieldOfError(serverError) : ''

  if (loading || loadError) {
    return (
      <Page className="ag-page crd-page" data-testid="coordinator-page">
        <button type="button" className="ag-back" onClick={leave}><Icon name="arrow-left" /> {t('agent.back')}</button>
        {loadError
          ? <LoadError title={t('coordinator.loadFailed')} error={loadError} onRetry={() => setAttempt(n => n + 1)} />
          : <SkeletonRows count={4} />}
      </Page>
    )
  }

  const caps = (k, label, help) => (
    <div className="dk-field crd-cap" key={k}>
      <label className="dk-label" htmlFor={`crd-${k}`}>{label}</label>
      <input
        id={`crd-${k}`} className="dk-input" type="text" inputMode="numeric" autoComplete="off" value={form[k]}
        placeholder={String(CAPS[k].def)} onChange={(e) => updateField(k, e.target.value)}
        aria-invalid={shown[k] ? true : undefined} aria-describedby={`crd-${k}-msg`}
      />
      {shown[k]
        ? <p className="dk-field-error" id={`crd-${k}-msg`} role="alert">{t('coordinator.limits.range', { max: CAPS[k].max })}</p>
        : <p className="dk-hint" id={`crd-${k}-msg`}>{help}</p>}
    </div>
  )

  return (
    <Page className="ag-page crd-page" data-testid="coordinator-page">
      <UnsavedChangesGuard when={dirty && !saving} />
      <button type="button" className="ag-back" onClick={leave}><Icon name="arrow-left" /> {t('agent.back')}</button>
      <PageHeader
        title={isEdit ? (saved?.name || name) : t('coordinator.titleNew')}
        supporting={isEdit ? t('coordinator.supportingEdit') : t('coordinator.supportingNew')}
        actions={isEdit && saved && <CoordinatorStateChip coordinator={saved} />}
      />

      {isEdit && saved && (
        <section className="crd-card" aria-labelledby="crd-state-h" data-testid="coordinator-card" data-paused={saved.paused || undefined}>
          <div className="crd-card__head">
            <h2 className="crd-sec__title" id="crd-state-h">{t('coordinator.card.title')}</h2>
            <div className="crd-card__acts">
              <Button size="sm" icon="chat" to={`/app/chats/${encodeURIComponent(saved.chat_id)}`} disabled={!saved.chat_id}>{t('coordinator.card.openChat')}</Button>
              {saved.paused
                ? <Button size="sm" variant="primary" icon="play" busy={busy} onClick={() => act(() => chatCoordinatorsApi.resume(id), t('coordinator.resumed', { name: saved.name }))} data-testid="coordinator-resume">{t('coordinator.card.resume')}</Button>
                : <Button size="sm" icon="pause" busy={busy} onClick={() => act(() => chatCoordinatorsApi.pause(id), t('coordinator.paused', { name: saved.name }))} data-testid="coordinator-pause">{t('coordinator.card.pause')}</Button>}
            </div>
          </div>
          {saved.paused && (
            <Callout tone="warn" icon="pause" testId="coordinator-paused-note">
              {saved.paused_by ? t('coordinator.card.pausedBy', { who: saved.paused_by }) : t('coordinator.card.pausedNote')}
            </Callout>
          )}
          <dl className="crd-facts">
            <div><dt>{t('coordinator.card.nextWake')}</dt><dd data-testid="coordinator-next-wake">{saved.paused ? t('coordinator.card.notWhilePaused') : whenOrDash(saved.next_wake)}</dd></div>
            <div><dt>{t('coordinator.card.lastWake')}</dt><dd>{whenOrDash(saved.last_wake)}</dd></div>
            <div><dt>{t('coordinator.card.schedule')}</dt><dd>{wakeLabel(t, saved.wake_every)}</dd></div>
            <div><dt>{t('coordinator.card.runsToday')}</dt><dd data-testid="coordinator-runs">{usageLine(t, saved.usage).runs}</dd></div>
            <div><dt>{t('coordinator.card.sentToday')}</dt><dd data-testid="coordinator-sent">{usageLine(t, saved.usage).sent}</dd></div>
          </dl>
        </section>
      )}

      <form className="crd-form" onSubmit={submit} noValidate aria-label={isEdit ? t('coordinator.titleEdit') : t('coordinator.titleNew')} data-testid="coordinator-form">
        {serverError && (
          <Callout tone="error" role="alert" title={t('coordinator.notSaved')} testId="coordinator-server-error" id="coordinator-server-error" data-section={errorSection || undefined}>
            {serverError}
          </Callout>
        )}

        <section className="crd-sec" aria-labelledby="crd-id-h">
          <h2 className="crd-sec__title" id="crd-id-h">{t('coordinator.identity.title')}</h2>
          <div className="dk-field">
            <label className="dk-label" htmlFor="crd-name">{t('coordinator.identity.name')} <span className="text-error" aria-hidden="true">*</span></label>
            <input id="crd-name" className="dk-input" value={form.name} maxLength={80} autoComplete="off" placeholder="daily-ops"
              onChange={(e) => updateField('name', e.target.value)} aria-required="true"
              aria-invalid={shown.name ? true : undefined} aria-describedby="crd-name-msg" />
            {shown.name && <p className="dk-field-error" id="crd-name-msg" role="alert">{t('coordinator.identity.needsName')}</p>}
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="crd-description">{t('coordinator.identity.description')} <span className="crd-optional">{t('coordinator.optional')}</span></label>
            <input id="crd-description" className="dk-input" value={form.description} maxLength={400} autoComplete="off" onChange={(e) => updateField('description', e.target.value)} />
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="crd-model">{t('coordinator.identity.model')}</label>
            <ModelPicker
              id="crd-model" labels={{ label: t('coordinator.identity.model') }} autoSelect={false} rememberSelection={false}
              defaultChoice={t('create.modelPlaceholder')} defaultSelected={!form.model} onChooseDefault={() => updateField('model', '')}
              value={form.model} onChange={(v) => updateField('model', v)} capability={CAP_CHAT} models={chatModels}
              loading={modelsLoading} describe={describeModel} variant="field" searchable placeholder={t('create.modelPlaceholder')}
            />
            <p className="dk-hint">{t('coordinator.identity.modelHelp')}</p>
          </div>
        </section>

        <section className="crd-sec" aria-labelledby="crd-rules-h">
          <h2 className="crd-sec__title" id="crd-rules-h">{t('coordinator.rules.title')}</h2>
          <Callout tone="info" icon="user-shield" testId="coordinator-policy-note">{t('coordinator.rules.policy')}</Callout>
          <div className="dk-field">
            <label className="dk-label" htmlFor="crd-instructions">{t('coordinator.rules.instructions')}</label>
            <textarea id="crd-instructions" className="dk-input crd-instructions" rows={12} value={form.instructions} onChange={(e) => updateField('instructions', e.target.value)} aria-describedby="crd-instructions-msg" />
            <p className="dk-hint" id="crd-instructions-msg">{t('coordinator.rules.instructionsHelp')}</p>
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="crd-page">{t('coordinator.rules.page')} <span className="crd-optional">{t('coordinator.optional')}</span></label>
            <input id="crd-page" className="dk-input" value={form.page} maxLength={40} autoComplete="off" placeholder="daily-ops"
              onChange={(e) => updateField('page', e.target.value)} aria-describedby="crd-page-msg" aria-invalid={errorSection === 'page' ? true : undefined} />
            <p className="dk-hint" id="crd-page-msg">{t('coordinator.rules.pageHelp')}</p>
          </div>
        </section>

        <section className="crd-sec" aria-labelledby="crd-contacts-h">
          <h2 className="crd-sec__title" id="crd-contacts-h">{t('coordinator.contacts.title')}</h2>
          <p className="ag-note">{t('coordinator.contacts.lead', { max: MAX_CONTACTS })}</p>
          <CoordinatorContacts
            value={form.contacts} onChange={(v) => updateField('contacts', v)}
            bindings={bindings} bindingsFailed={bindingsFailed} me={user?.id} error={shown.contacts}
          />
        </section>

        <section className="crd-sec" aria-labelledby="crd-wake-h">
          <h2 className="crd-sec__title" id="crd-wake-h">{t('coordinator.wake.title')}</h2>
          <p className="ag-note">{t('coordinator.wake.lead')}</p>
          <div className="dk-field">
            <label className="dk-label" htmlFor="crd-wake-every">{t('coordinator.wake.every')}</label>
            <span className="dk-select-wrap crd-narrow">
              <select id="crd-wake-every" className="dk-select" value={form.wake_every} onChange={(e) => updateField('wake_every', Number(e.target.value))} data-testid="coordinator-wake-every" aria-describedby="crd-wake-every-msg">
                {wakeOptions(form.wake_every).map(o => (
                  <option key={o.value} value={o.value}>{o.custom ? t('coordinator.wake.custom', { minutes: Math.round(o.value / 60) }) : t(`coordinator.wake.opt.${o.key}`)}</option>
                ))}
              </select>
            </span>
            <p className="dk-hint" id="crd-wake-every-msg">{t('coordinator.wake.everyHelp')}</p>
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor="crd-wake-note">{t('coordinator.wake.note')} <span className="crd-optional">{t('coordinator.optional')}</span></label>
            <textarea id="crd-wake-note" className="dk-input" rows={3} value={form.wake_note} onChange={(e) => updateField('wake_note', e.target.value)} aria-describedby="crd-wake-note-msg" />
            <p className="dk-hint" id="crd-wake-note-msg">{t('coordinator.wake.noteHelp')}</p>
          </div>
        </section>

        <section className="crd-sec" aria-labelledby="crd-limits-h">
          <h2 className="crd-sec__title" id="crd-limits-h">{t('coordinator.limits.title')}</h2>
          <p className="ag-note">{t('coordinator.limits.lead')}</p>
          <div className="crd-caps">
            {caps('send_per_minute', t('coordinator.limits.perMinute'), t('coordinator.limits.help', CAPS.send_per_minute))}
            {caps('send_per_day', t('coordinator.limits.perDay'), t('coordinator.limits.help', CAPS.send_per_day))}
            {caps('max_helpers', t('coordinator.limits.helpers'), t('coordinator.limits.help', CAPS.max_helpers))}
            {caps('max_depth', t('coordinator.limits.depth'), t('coordinator.limits.help', CAPS.max_depth))}
          </div>
          <p className="ag-note" data-testid="coordinator-ceilings">
            {isEdit && saved ? t('coordinator.limits.runs', { limit: saved.usage?.runs_limit ?? 0 }) : t('coordinator.limits.runsNew')}{' '}
            {t('coordinator.limits.ceilings')}
          </p>
        </section>

        <section className="crd-sec" aria-labelledby="crd-tools-h">
          <h2 className="crd-sec__title" id="crd-tools-h">{t('coordinator.tools.title')}</h2>
          <p className="ag-note">{t('coordinator.tools.lead')}</p>
          <AgentUsesPanel
            abilities={ab} tools={tools} toolGroups={toolGroups} form={form} updateField={updateField}
            notices={<p className="ag-note">{t('create.toolsHelp')}</p>}
          />
          <div className="crd-skills" data-testid="coordinator-skills">
            <p className="ag-note">{t('create.skillsHelp')}</p>
            <SkillsPanel
              skills={skillRows} replaced={tools.replacedSkills} onSet={tools.setSkillsOn} error={tools.skillsError}
              testId="coordinator-skills-list" fromLabel heading={t('create.skillsHeading')} later={t('create.skillsLater')}
            />
          </div>
        </section>

        <div className="crd-foot">
          <Button variant="ghost" onClick={leave}>{t('coordinator.discard')}</Button>
          <Button type="submit" variant="primary" icon="save" busy={saving} data-testid="coordinator-save">{t('coordinator.save')}</Button>
        </div>
      </form>

      {isEdit && saved && (
        <section className="crd-sec crd-sec--audit" aria-labelledby="crd-audit-h" data-testid="coordinator-audit-section">
          <h2 className="crd-sec__title" id="crd-audit-h">{t('coordinator.audit.title')}</h2>
          <p className="ag-note">{t('coordinator.audit.lead')}</p>
          <CoordinatorAudit load={(q) => chatCoordinatorsApi.audit(id, q)} labelId="crd-audit-h" />
        </section>
      )}

    </Page>
  )
}
