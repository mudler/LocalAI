import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { addonsApi } from '../utils/addonsClient'
import { AuthContext } from '../context/authState'
import { cancelCall, confirmCall, proposalFacts } from './approval'
import { resultText, validProposalId } from './slots'

const SAMPLE_CHARS = 1200
const STATE_KEYS = ['pending', 'applied', 'cancelled', 'expired', 'executing', 'rejected', 'outcome-unknown', 'abandoned-unknown']

// The host's card for one stored proposal, drawn inline where a chat tool
// answered with a reference. It loads the authoritative record from the host
// by identifier: the model's text is never the preview. Its only actions are
// the host's authenticated Confirm and Cancel, sent exactly like the approval
// frame sends them (the proposer's own browser session, the proposal named in
// X-LocalAI-Confirm, the digest that was displayed).
//
// The card forgets what it loaded when the person logs out, another person
// signs in, the add-ons feature is withdrawn, or the host stops answering for
// the proposal, so a revoked user is left with a notice and no data.
export default function ProposalCard({ proposalId, onResolved }) {
  const { t, i18n } = useTranslation('addons')
  const auth = useContext(AuthContext)
  const principal = auth && auth.user ? String(auth.user.id || auth.user.email || '') : ''
  const featureOn = !auth || !auth.authEnabled || (typeof auth.hasFeature === 'function' && auth.hasFeature('addons'))
  const signedOut = !!auth && !auth.loading && !!auth.authEnabled && !auth.user
  const withheld = signedOut || !featureOn

  const [proposal, setProposal] = useState(null)
  const [phase, setPhase] = useState('loading')
  const [busy, setBusy] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const resolved = useRef('')
  const onResolvedRef = useRef(onResolved)
  onResolvedRef.current = onResolved

  const load = useCallback((signal) => {
    if (!validProposalId(proposalId)) { setPhase('unavailable'); return Promise.resolve() }
    return addonsApi.call(`/api/addons/proposals/${encodeURIComponent(proposalId)}`, { signal })
      .then((p) => {
        if (!p || p.id !== proposalId) { setProposal(null); setPhase('unavailable'); return }
        setProposal(p)
        setPhase('ready')
      })
      .catch((e) => {
        if (e && e.name === 'AbortError') return
        // A denial or a lost session: drop what was shown.
        setProposal(null)
        setPhase(e && (e.status === 401 || e.status === 403 || e.kind === 'denied' || e.kind === 'unauthenticated') ? 'withdrawn' : 'unavailable')
      })
  }, [proposalId])

  // Everything on screen is dropped when the principal changes or goes away,
  // and loaded again under the new one.
  useEffect(() => {
    setProposal(null)
    resolved.current = ''
    if (withheld) { setPhase('withdrawn'); return undefined }
    setPhase('loading')
    const controller = new AbortController()
    load(controller.signal)
    return () => controller.abort()
  }, [proposalId, principal, withheld, load])

  // Coming back to the page asks the host again: a revocation or an expiry
  // that happened in the meantime shows up here.
  useEffect(() => {
    if (withheld) return undefined
    const refresh = () => { if (document.visibilityState !== 'hidden') load() }
    window.addEventListener('focus', refresh)
    document.addEventListener('visibilitychange', refresh)
    return () => {
      window.removeEventListener('focus', refresh)
      document.removeEventListener('visibilitychange', refresh)
    }
  }, [withheld, load])

  const facts = useMemo(() => proposalFacts(proposal, now), [proposal, now])
  useEffect(() => {
    if (!facts.expiresAt || facts.expired || facts.state !== 'pending') return undefined
    const timer = setTimeout(() => setNow(Date.now()), Math.max(250, facts.expiresAt - Date.now() + 50))
    return () => clearTimeout(timer)
  }, [facts.expiresAt, facts.expired, facts.state])

  // The decision of a person, or a state the host reached, is reported once.
  useEffect(() => {
    if (!proposal || !facts.settled || resolved.current === `${proposal.id}:${facts.state}`) return
    resolved.current = `${proposal.id}:${facts.state}`
    if (typeof onResolvedRef.current === 'function') onResolvedRef.current(proposal)
  }, [proposal, facts.settled, facts.state])

  const send = useCallback(async (call) => {
    if (!call || busy) return
    setBusy(true)
    try {
      const next = await addonsApi.call(call.path, { method: call.method, headers: call.headers, body: call.body })
      if (next && next.id === proposalId) setProposal(next)
      setPhase('ready')
    } catch (e) {
      // The host answered no, or did not answer: read the record again and
      // show what it says, never a guess.
      if (e && (e.status === 401 || e.status === 403) && !proposal) setPhase('withdrawn')
      else setPhase('rejected')
      load()
    } finally {
      setBusy(false)
    }
  }, [busy, proposalId, proposal, load])

  const stateKey = STATE_KEYS.includes(facts.state) ? facts.state : 'unknown'
  const shownState = facts.state === 'pending' && facts.expired ? 'expired' : stateKey
  const sample = facts.sample === undefined ? '' : resultText(facts.sample).slice(0, SAMPLE_CHARS)
  const notice = (() => {
    if (!proposal) return ''
    if (facts.blocked && facts.state === 'pending') return t('proposal.blocked')
    if (facts.state === 'pending' && facts.expired) return t('proposal.expired')
    if (facts.state === 'rejected') return t('proposal.rejectedState')
    if (facts.unknownOutcome) return t('proposal.unknown')
    return ''
  })()

  return (
    <div className="ad-approval ad-pcard" data-testid="proposal-card" data-state={proposal ? shownState : phase} data-proposal={validProposalId(proposalId) ? proposalId : ''}>
      <p className="ad-approval__from">{t('proposal.title')}</p>
      {phase === 'loading' && !proposal && <p role="status" data-testid="proposal-loading">{t('proposal.loading')}</p>}
      {phase === 'withdrawn' && !proposal && <p role="alert" data-testid="proposal-withdrawn">{t('proposal.withdrawn')}</p>}
      {phase === 'unavailable' && !proposal && <p role="alert" data-testid="proposal-unavailable">{t('proposal.unavailable')}</p>}
      {proposal && (
        <>
          <dl className="ad-approval__facts ad-pcard__facts">
            <div><dt>{t('proposal.addon')}</dt><dd data-testid="proposal-addon">{facts.addonId}</dd></div>
            <div><dt>{t('proposal.database')}</dt><dd data-testid="proposal-database">{facts.requestedName || facts.database}</dd></div>
            <div><dt>{t('proposal.operation')}</dt><dd data-testid="proposal-operation"><code>{facts.operation || facts.action}</code></dd></div>
            <div><dt>{t('proposal.effects')}</dt><dd data-testid="proposal-count">{facts.count}</dd></div>
            {facts.expiresAt && <div><dt>{t('approval.expires')}</dt><dd data-testid="proposal-expires">{new Date(facts.expiresAt).toLocaleTimeString(i18n.language)}</dd></div>}
            <div><dt>{t('approval.state')}</dt><dd data-testid="proposal-state">{t(`approval.states.${shownState}`)}</dd></div>
          </dl>
          {sample && (
            <div className="ad-approval__body">
              <p className="ad-approval__from">{t('proposal.sample')}</p>
              <pre className="ad-result" data-testid="proposal-sample">{sample}</pre>
            </div>
          )}
          {notice && <p role="status" data-testid="proposal-notice">{notice}</p>}
          {phase === 'rejected' && <p role="alert" data-testid="proposal-failed">{t('approval.rejected')}</p>}
          <div className="ad-approval__foot">
            {facts.unknownOutcome && (
              <button type="button" className="dk-btn dk-btn--secondary" disabled={busy} data-testid="proposal-refresh" onClick={() => load()}>{t('proposal.refresh')}</button>
            )}
            <button type="button" className="dk-btn dk-btn--secondary" disabled={busy || !facts.canDecide} data-testid="proposal-cancel"
              onClick={() => send(cancelCall(proposal))}>{t('approval.cancel')}</button>
            <button type="button" className="dk-btn dk-btn--primary" disabled={busy || !facts.canDecide} data-testid="proposal-confirm"
              onClick={() => send(confirmCall(proposal))}>{t('approval.confirm')}</button>
          </div>
        </>
      )}
    </div>
  )
}
