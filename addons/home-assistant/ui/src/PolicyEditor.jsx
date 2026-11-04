/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The policy editor of the Home Assistant add-on: which services a model may
// call, which entities the tools never show or touch, how many things one call
// may switch, and the fingerprints of self-signed certificates. The add-on
// keeps the policy and refuses anything its hard rules forbid; an
// administrator saves it. Everyone who opens the page sees it.
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import { Field, Notice, TextInput } from '../../../internal/connector-ui/src/controls'
import { argsOf, classOf, confirmDomains, draftIssue, draftOf, same, splitLines } from './policy'
import './home-assistant.css'

const call = (args) => getSdk().api.read({ arguments: args })

function Area({ id, label, hint, value, onChange, disabled, rows = 4, error, mono = true, placeholder }) {
  return (
    <Field label={label} hint={hint} error={error}>
      {(p) => (
        <textarea {...p} className={`cn-input${mono ? ' cn-input--mono' : ''}`} rows={rows} data-testid={id} value={value}
          disabled={disabled} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
      )}
    </Field>
  )
}

function Entries({ text, t }) {
  const list = splitLines(text).filter(classOf)
  if (list.length === 0) return <p className="cn-muted" data-testid="home-assistant-allow-empty">{t('policy.allowNone')}</p>
  return (
    <ul className="ha-entries" aria-label={t('policy.allowSummary')} data-testid="home-assistant-allow-summary">
      {list.map((e) => (
        <li key={e}>
          <code>{e}</code>
          <span className={`ha-tag ha-tag--${classOf(e)}`}>{t(`policy.class.${classOf(e)}`)}</span>
        </li>
      ))}
    </ul>
  )
}

export default function PolicyEditor() {
  const { t } = useTranslation('home-assistant')
  const [view, setView] = useState(null)
  const [draft, setDraft] = useState(null)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await call({ tool: 'get-policy' })
      setView(res.data)
      setDraft(draftOf(res.data.policy))
      setError('')
    } catch (e) { setError(e?.message || t('policy.loadFailed')) }
  }, [t])
  useEffect(() => { load() }, [load])

  if (!view || !draft) {
    return (
      <section className="ha-policy" data-testid="home-assistant-policy" aria-busy={!error}>
        <h2 className="ha-policy__title">{t('policy.title')}</h2>
        {error ? <Notice tone="error" role="alert">{error}</Notice> : <p className="cn-muted" role="status">{t('policy.loading')}</p>}
      </section>
    )
  }
  const editable = view.admin === true
  const issue = draftIssue(draft, view.hard)
  const dirty = !same(draft, draftOf(view.policy))
  const set = (k) => (v) => { setDraft({ ...draft, [k]: v }); setSaved(false) }
  const problem = (field) => (issue && issue.field === field ? t(`policy.${issue.key}`, { entry: issue.entry }) : undefined)
  const confirmList = confirmDomains(draft.allow)

  const save = async (e) => {
    e.preventDefault()
    if (issue || !editable) return
    setBusy(true)
    setError('')
    try {
      const res = await call({ tool: 'set-policy', ...argsOf(draft) })
      setView({ ...view, policy: res.data.policy, physical: res.data.physical })
      setDraft(draftOf(res.data.policy))
      setSaved(true)
    } catch (err) { setError(err?.message || t('policy.saveFailed')) } finally { setBusy(false) }
  }
  const restore = () => { setDraft(draftOf(view.defaults)); setSaved(false) }

  return (
    <section className="ha-policy" data-testid="home-assistant-policy" aria-labelledby="ha-policy-title">
      <h2 className="ha-policy__title" id="ha-policy-title">{t('policy.title')}</h2>
      <p className="cn-muted">{t('policy.intro')}</p>
      <Notice tone="warn" role="note" testId="home-assistant-physical-warning">
        <strong>{t('policy.physicalTitle')}</strong> {t('policy.physical')}
      </Notice>
      {!editable && <Notice tone="info" role="note" testId="home-assistant-policy-readonly">{t('policy.readOnly')}</Notice>}
      {view.broken && <Notice tone="error" role="alert" testId="home-assistant-policy-broken">{t('policy.broken')}</Notice>}
      {error && <Notice tone="error" role="alert" testId="home-assistant-policy-error">{error}</Notice>}
      {saved && <Notice tone="ok" role="status" testId="home-assistant-policy-saved">{t('policy.saved')}</Notice>}

      <form className="ha-policy__form" onSubmit={save} aria-describedby="ha-policy-title">
        <fieldset className="ha-fieldset" disabled={!editable || busy}>
          <legend className="ha-legend">{t('policy.allowTitle')}</legend>
          <Area id="home-assistant-allow" label={t('policy.allow')} hint={t('policy.allowHint')} value={draft.allow} onChange={set('allow')} rows={8} error={problem('allow')} />
          <Entries text={draft.allow} t={t} />
          {confirmList.length > 0 && <p className="cn-muted" data-testid="home-assistant-confirm-note">{t('policy.confirmNote', { domains: confirmList.join(', ') })}</p>}
          <Field label={t('policy.maxTargets')} hint={t('policy.maxTargetsHint')} error={problem('maxTargets')}>
            {(p) => <TextInput {...p} type="number" min="1" max="20" inputMode="numeric" data-testid="home-assistant-max-targets" value={draft.maxTargets} onChange={(e) => set('maxTargets')(e.target.value)} />}
          </Field>
        </fieldset>

        <fieldset className="ha-fieldset" disabled={!editable || busy}>
          <legend className="ha-legend">{t('policy.excludeTitle')}</legend>
          <p className="cn-muted">{t('policy.excludeIntro')}</p>
          <Area id="home-assistant-exclude-domains" label={t('policy.excludeDomains')} hint={t('policy.excludeDomainsHint')} value={draft.excludeDomains} onChange={set('excludeDomains')} rows={3} />
          <Area id="home-assistant-exclude-areas" label={t('policy.excludeAreas')} hint={t('policy.excludeAreasHint')} value={draft.excludeAreas} onChange={set('excludeAreas')} rows={3} mono={false} />
          <Area id="home-assistant-exclude-patterns" label={t('policy.excludePatterns')} hint={t('policy.excludePatternsHint')} value={draft.excludePatterns} onChange={set('excludePatterns')} rows={3} error={problem('excludePatterns')} placeholder="sensor\..*_bedroom" />
          <Field label={t('policy.excludeLabel')} hint={t('policy.excludeLabelHint')}>
            {(p) => <TextInput {...p} data-testid="home-assistant-exclude-label" maxLength={64} value={draft.excludeLabel} onChange={(e) => set('excludeLabel')(e.target.value)} />}
          </Field>
        </fieldset>

        <fieldset className="ha-fieldset" disabled={!editable || busy}>
          <legend className="ha-legend">{t('policy.pinsTitle')}</legend>
          <Area id="home-assistant-pins" label={t('policy.pins')} hint={t('policy.pinsHint')} value={draft.pins} onChange={set('pins')} rows={3} error={problem('pins')} placeholder="ha.example:8123=ab12..." />
        </fieldset>

        <div className="ha-actions">
          <button type="submit" className="dk-btn dk-btn--primary" data-testid="home-assistant-policy-save" disabled={!editable || busy || !dirty || Boolean(issue)}>{t('policy.save')}</button>
          <button type="button" className="dk-btn dk-btn--ghost" data-testid="home-assistant-policy-restore" onClick={restore} disabled={!editable || busy}>{t('policy.restore')}</button>
        </div>
      </form>

      <div className="ha-hard" data-testid="home-assistant-hard">
        <h3 className="ha-hard__title">{t('policy.hardTitle')}</h3>
        <p className="cn-muted">{t('policy.hardIntro')}</p>
        <ul className="ha-chips" aria-label={t('policy.hardTitle')}>
          {(view.hard?.domains || []).map((d) => <li key={d}><code>{d}.*</code></li>)}
          <li><code>*.reload*</code></li>
          {(view.hard?.serviceNames || []).map((s) => <li key={s}><code>*.{s}</code></li>)}
          <li><span>{t('policy.hardCover')}</span></li>
        </ul>
      </div>
    </section>
  )
}
