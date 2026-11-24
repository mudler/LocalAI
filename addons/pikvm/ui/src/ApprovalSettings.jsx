/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// What the owner is asked before a chat acts on a PiKVM. Power actions and key
// presses can be set to run without a card; typing text cannot. The answer is
// the signed-in person's own (a user setting kept by the host, written from
// the person's browser session only), so no chat, agent or model can change it.
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import { Notice, RadioCards } from '../../../internal/connector-ui/src/controls'
import { ASK, CHOICES, TOOLS, approvalsChanged, declared, keyOf, valuesOf } from './approval'

function Tool({ tool, value, busy, onChoose, t }) {
  return (
    <fieldset className="pk-fieldset" data-testid={`pikvm-approval-${tool}`} disabled={busy}>
      <legend className="pk-approval__legend">{t(`approval.tool.${tool}`)}</legend>
      <RadioCards name={`approval-${tool}`} legend={t(`approval.tool.${tool}`)} value={value} onChange={(v) => onChoose(tool, v)}
        options={CHOICES.map((c) => ({
          value: c,
          label: (<span className="pk-modecard"><span className="pk-modecard__title">{t(`approval.${c}`)}</span><span className="pk-modecard__line">{t(`approval.line.${c}`)}</span></span>),
        }))} />
      <Notice tone="warn" role="note" testId={`pikvm-approval-warn-${tool}`}>
        <strong>{t('approval.warnTitle')}</strong> {t(`approval.warn.${tool}`)}
      </Notice>
    </fieldset>
  )
}

export default function ApprovalSettings() {
  const { t } = useTranslation('pikvm')
  const [state, setState] = useState({ phase: 'loading', values: {}, declared: true })
  const [busy, setBusy] = useState('')
  const [notice, setNotice] = useState('')
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let live = true
    getSdk().userSettings.get().then((out) => {
      if (live) setState({ phase: 'ready', values: valuesOf(out), declared: declared(out) })
    }).catch(() => { if (live) setState({ phase: 'error', values: {}, declared: true }) })
    return () => { live = false }
  }, [])

  const choose = useCallback((tool, value) => {
    if (busy || state.values[tool] === value) return
    setBusy(tool)
    setFailed(false)
    setNotice('')
    getSdk().userSettings.set(keyOf(tool), value).then((out) => {
      setState((s) => ({ ...s, values: valuesOf(out) }))
      setNotice(t('approval.saved'))
      approvalsChanged()
    }).catch(() => setFailed(true)).finally(() => setBusy(''))
  }, [busy, state.values, t])

  if (state.phase === 'loading') return <p className="cn-muted" role="status" data-testid="pikvm-approval-loading">{t('approval.loading')}</p>
  if (state.phase === 'error') return <Notice tone="error" role="alert" testId="pikvm-approval-unreadable">{t('approval.loadFailed')}</Notice>
  if (!state.declared) return null
  return (
    <section className="pk-settings pk-approval" data-testid="pikvm-approval" aria-labelledby="pk-approval-title">
      <h2 className="pk-settings__title" id="pk-approval-title">{t('approval.title')}</h2>
      <p className="cn-muted">{t('approval.intro')}</p>
      {TOOLS.map((tool) => <Tool key={tool} tool={tool} value={state.values[tool] || ASK} busy={busy === tool} onChoose={choose} t={t} />)}
      <Notice tone="info" role="note" testId="pikvm-approval-text">{t('approval.textAlways')}</Notice>
      <p className="cn-muted" data-testid="pikvm-approval-who">{t('approval.who')}</p>
      <p className="cn-muted" data-testid="pikvm-approval-writemode">{t('approval.needsWrite')}</p>
      <p className="cn-muted" data-testid="pikvm-approval-audit">{t('approval.audit')}</p>
      <p className="cn-muted" role="status" aria-live="polite" data-testid="pikvm-approval-status">{notice}</p>
      {failed && <Notice tone="error" role="alert" testId="pikvm-approval-failed">{t('approval.saveFailed')}</Notice>}
    </section>
  )
}
