/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// What the owner is asked before a chat changes a cluster. Each write tool can
// be set to run without a card. The answer is the signed-in person's own (a
// user setting kept by the host, written from the person's browser session
// only), so no chat, agent or model can change it. The policy of the cluster
// still decides which tools work at all.
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import { Notice, RadioCards } from '../../../internal/connector-ui/src/controls'
import { ASK, CHOICES, TOOLS, approvalsChanged, declared, keyOf, valuesOf } from './approval'
import { isPowerful } from './policy'

function Tool({ tool, value, busy, onChoose, t }) {
  return (
    <fieldset className="kube-fieldset" data-testid={`kubernetes-approval-${tool}`} disabled={busy}>
      <legend className="kube-legend">{t(`tool.${tool}`)}</legend>
      <RadioCards name={`approval-${tool}`} legend={t(`tool.${tool}`)} value={value} onChange={(v) => onChoose(tool, v)}
        options={CHOICES.map((c) => ({
          value: c,
          label: (<span className="kube-modecard"><span className="kube-modecard__title">{t(`approval.${c}`)}</span><span className="kube-modecard__line">{t(`approval.line.${c}`)}</span></span>),
        }))} />
      {value !== ASK && (
        <Notice tone="warn" role="note" testId={`kubernetes-approval-warn-${tool}`}>{t(isPowerful(tool) ? 'approval.warnExec' : 'approval.warn')}</Notice>
      )}
    </fieldset>
  )
}

export default function ApprovalSettings() {
  const { t } = useTranslation('kubernetes')
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

  if (state.phase === 'loading') return <p className="cn-muted" role="status" data-testid="kubernetes-approval-loading">{t('approval.loading')}</p>
  if (state.phase === 'error') return <Notice tone="error" role="alert" testId="kubernetes-approval-unreadable">{t('approval.loadFailed')}</Notice>
  if (!state.declared) return null
  return (
    <section className="kube-settings" data-testid="kubernetes-approval" aria-labelledby="kube-approval-title">
      <h2 className="kube-settings__title" id="kube-approval-title">{t('approval.title')}</h2>
      <p className="cn-muted">{t('approval.intro')}</p>
      <div className="kube-approval__grid">
        {TOOLS.map((tool) => <Tool key={tool} tool={tool} value={state.values[tool] || ASK} busy={busy === tool} onChoose={choose} t={t} />)}
      </div>
      <p className="cn-muted" data-testid="kubernetes-approval-who">{t('approval.who')}</p>
      <p className="cn-muted" data-testid="kubernetes-approval-writemode">{t('approval.needsWrite')}</p>
      <p className="cn-muted" role="status" aria-live="polite" data-testid="kubernetes-approval-status">{notice}</p>
      {failed && <Notice tone="error" role="alert" testId="kubernetes-approval-failed">{t('approval.saveFailed')}</Notice>}
    </section>
  )
}
