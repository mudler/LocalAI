/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { addonsApi } from '../utils/addonsClient'
import { RegistryContext } from './registryContext'
import { localizedText } from '../components/addons/addonState'
import { approvalState, cancelCall, confirmCall, rendererProps } from './approval'
import { rendererFor, resultText } from './slots'
import SlotBoundary from './SlotBoundary'

// The host's frame around an add-on proposal. It loads the stored proposal
// from the server and draws the facts the host knows (who, how many changes,
// when it expires, the state). The add-on's approval-card component, when the
// manifest declares one for the tool, draws the details inside the body.
//
// Confirm and Cancel belong to the frame. The card gets plain data: no
// callback, no digest, nothing that could decide. A confirmation names the
// proposal in X-LocalAI-Confirm and carries the digest that was displayed; the
// server accepts it only from the proposer's own browser session.
export default function ApprovalFrame({ addonId, proposalId, onClose }) {
  const { t, i18n } = useTranslation('addons')
  const registry = useContext(RegistryContext)
  const [proposal, setProposal] = useState(null)
  const [phase, setPhase] = useState('loading')
  const [busy, setBusy] = useState(false)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const controller = new AbortController()
    setPhase('loading')
    addonsApi.call(`/api/addons/proposals/${encodeURIComponent(proposalId)}`, { signal: controller.signal })
      .then((p) => {
        // A proposal of another add-on is never shown in this add-on's frame.
        if (!p || p.addonId !== addonId) { setPhase('failed'); return }
        setProposal(p)
        setPhase('ready')
      })
      .catch((e) => { if (!(e && e.name === 'AbortError')) setPhase('failed') })
    return () => controller.abort()
  }, [addonId, proposalId])

  const state = useMemo(() => approvalState(proposal, now), [proposal, now])
  useEffect(() => {
    if (!state.expiresAt || state.expired) return undefined
    const timer = setTimeout(() => setNow(Date.now()), Math.max(250, state.expiresAt - Date.now() + 50))
    return () => clearTimeout(timer)
  }, [state.expiresAt, state.expired])

  const send = useCallback(async (call) => {
    if (!call || busy) return
    setBusy(true)
    try {
      const next = await addonsApi.call(call.path, { method: call.method, headers: call.headers, body: call.body })
      if (next && next.id === proposalId) setProposal(next)
      setPhase('ready')
    } catch {
      setPhase('rejected')
    } finally {
      setBusy(false)
    }
  }, [busy, proposalId])

  const app = registry ? registry.apps.find(a => a.id === addonId) : null
  const instance = registry ? registry.instances.find(i => i.id === addonId) : null
  const Card = app && instance ? rendererFor(app, instance, 'approval-card', state.tool) : null
  const title = localizedText(app, 'title', i18n.language) || addonId
  const stateKey = ['pending', 'applied', 'cancelled', 'expired', 'executing', 'rejected', 'outcome-unknown', 'abandoned-unknown'].includes(state.state)
    ? state.state : 'unknown'

  return (
    <div className="ad-approval" data-testid="approval-frame" data-state={state.state || phase}>
      <p className="ad-approval__from">{t('approval.from', { addon: title })}</p>
      {phase === 'loading' && <p role="status">{t('approval.loading')}</p>}
      {phase === 'failed' && <p role="alert">{t('approval.failed')}</p>}
      {phase === 'rejected' && <p role="alert">{t('approval.rejected')}</p>}
      {proposal && (
        <>
          <dl className="ad-approval__facts">
            <div><dt>{t('approval.changes')}</dt><dd data-testid="approval-count">{state.count}</dd></div>
            {state.expiresAt && <div><dt>{t('approval.expires')}</dt><dd>{new Date(state.expiresAt).toLocaleTimeString(i18n.language)}</dd></div>}
            <div><dt>{t('approval.state')}</dt><dd data-testid="approval-state">{t(`approval.states.${stateKey}`)}</dd></div>
          </dl>
          <div className="ad-approval__body" data-addon={addonId} data-testid="approval-body">
            {Card ? (
              <SlotBoundary label={t('slots.unavailable')}>
                <Card {...rendererProps(proposal)} />
              </SlotBoundary>
            ) : (
              <pre className="ad-result" data-testid="approval-generic">{resultText(proposal.effects && proposal.effects.sample)}</pre>
            )}
          </div>
          {state.expired && state.state === 'pending' && <p role="status">{t('approval.expired')}</p>}
          <div className="ad-approval__foot">
            <button type="button" className="dk-btn dk-btn--secondary" disabled={busy || !state.canDecide} data-testid="approval-cancel"
              onClick={() => send(cancelCall(proposal))}>{t('approval.cancel')}</button>
            <button type="button" className="dk-btn dk-btn--primary" disabled={busy || !state.canDecide} data-testid="approval-confirm"
              onClick={() => send(confirmCall(proposal))}>{t('approval.confirm')}</button>
          </div>
        </>
      )}
      {onClose && !proposal && (
        <div className="ad-approval__foot">
          <button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>{t('approval.close')}</button>
        </div>
      )}
    </div>
  )
}
