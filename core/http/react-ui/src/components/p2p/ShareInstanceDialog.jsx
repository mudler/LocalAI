/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Icon from '../Icon'
import { Button, Callout, StatusChip } from '../ui'
import Segmented from '../ui/Segmented'
import QrCode from './QrCode'
import ShareModelChoice from './ShareModelChoice'
import { p2pNetApi } from '../../utils/api'
import { copyToClipboard } from '../../utils/clipboard'
import {
  QR_KINDS, TTL_CHOICES, clock, mintBlockedReason, mintModelFields, modelGrant, shareKinds, inviteWarnings, phraseWords, secondsLeft, shortPeer,
} from '../../utils/p2p'
import { P2P_SETTINGS_PATH } from './P2PTabs'
import { useDistributedMode } from '../../hooks/useDistributedMode'

const KIND_ICON = { device: 'monitor', remote: 'globe', worker: 'server' }
// How often the open dialog asks whether the device has redeemed the invite.
const POLL_MS = 3000

// "Share this instance": make a one-time invite link, show it with its QR code
// and what it carries, then wait for the device. When the device redeems the
// invite, the dialog shows the confirmation phrase, which the admin compares
// with the one on the device; "It does not match" revokes the device at once.
//
//   status     GET /api/p2p/status, for the peer ID and the public address
//   onChanged  after an invite is made, canceled or used, or a device revoked
//   onClose    closes the dialog
//   addToast   the shell's toast
export default function ShareInstanceDialog({ status, onChanged, onClose, addToast }) {
  const { t } = useTranslation('p2p')
  const labelId = useId()
  const [kind, setKind] = useState('device')
  const { enabled: distributed } = useDistributedMode()
  const [label, setLabel] = useState('')
  const [ttl, setTtl] = useState(TTL_CHOICES[0].id)
  // The models of a remote invite: every model, or the ones picked.
  const [modelScope, setModelScope] = useState('all')
  const [picked, setPicked] = useState(() => new Set())
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  // The server refused a remote invite: the caller has no user account.
  const [needsAccount, setNeedsAccount] = useState(false)
  // The mint answer: the link is in it and nowhere else.
  const [minted, setMinted] = useState(null)
  // The invite row once the device has used it: the phrase and the peer.
  const [used, setUsed] = useState(null)
  const [left, setLeft] = useState(0)

  const mint = async () => {
    setBusy('mint'); setError(''); setNeedsAccount(false)
    try {
      const grant = mintModelFields(kind, modelScope, picked)
      const res = await p2pNetApi.mintInvite({
        kind, label: label.trim(), ttl_seconds: Number(ttl), relays: 'auto', ...grant,
      })
      setMinted({ ...res, label: label.trim(), ttl: Number(ttl), ...grant })
      setUsed(null)
      onChanged?.()
    } catch (err) {
      if (err?.status === 409 && err?.body?.error?.type === 'needs_account') setNeedsAccount(true)
      else setError(err?.message || t('share.mintFailed'))
    } finally {
      setBusy('')
    }
  }

  // The countdown of the invite on screen.
  useEffect(() => {
    if (!minted || used) return undefined
    const tick = () => setLeft(secondsLeft(minted.expires_at))
    tick()
    const id = setInterval(tick, 1000)
    return () => clearInterval(id)
  }, [minted, used])

  // Waits for the device: the invite turns "used" when it redeems.
  const changed = useRef(onChanged)
  changed.current = onChanged
  useEffect(() => {
    if (!minted || used) return undefined
    let stop = false
    const poll = async () => {
      try {
        const res = await p2pNetApi.invites()
        const row = (res?.invites || []).find(i => i.id === minted.id)
        if (!stop && row?.status === 'used') {
          setUsed(row)
          changed.current?.()
        }
      } catch {
        // The next poll tries again; the dialog keeps the link on screen.
      }
    }
    const id = setInterval(poll, POLL_MS)
    return () => { stop = true; clearInterval(id) }
  }, [minted, used])

  const cancelInvite = async () => {
    setBusy('cancel'); setError('')
    try {
      await p2pNetApi.cancelInvite(minted.id)
      onChanged?.()
      addToast?.(t('share.canceled'), 'success')
      onClose()
    } catch (err) {
      setError(err?.message || '')
      setBusy('')
    }
  }

  const revoke = async () => {
    setBusy('revoke'); setError('')
    try {
      await p2pNetApi.revokeDevice(used.device_id)
      onChanged?.()
      addToast?.(t('share.revoked', { label: minted.label || t(`kinds.${minted.kind}.title`) }), 'success')
      onClose()
    } catch (err) {
      setError(err?.message || '')
      setBusy('')
    }
  }

  const blocked = mintBlockedReason(kind, modelScope, picked)
  const again = () => { setMinted(null); setUsed(null); setError('') }

  const name = minted?.label || (minted ? t(`kinds.${minted.kind}.title`) : '')
  const close = () => { if (!busy) onClose() }

  if (minted && used) {
    return (
      <Dialog
        title={t('share.connected.title', { label: name })}
        description={t('share.connected.sub', { kind: t(`kinds.${minted.kind}.title`) })}
        onClose={close} pending={busy !== ''} closeLabel={t('share.close')} labelId={labelId} testId="share-confirm"
        foot={(
          <div className="p2p-foot">
            <span className="p2p-foot__note">{t(minted.kind === 'device' ? 'share.connected.signIn' : minted.kind === 'worker' ? 'share.connected.approve' : 'share.connected.keyNote')}</span>
            <span className="p2p-foot__actions">
              <Button variant="danger-ghost" busy={busy === 'revoke'} onClick={revoke} data-testid="share-mismatch">{t('share.connected.mismatch')}</Button>
              <Button variant="primary" onClick={onClose} data-autofocus data-testid="share-match">{t('share.connected.match')}</Button>
            </span>
          </div>
        )}
      >
        <div className="p2p-dialog">
        <div className="p2p-phrase" data-testid="share-phrase">
          <p className="p2p-phrase__title">{t('share.connected.confirm')}</p>
          <p className="p2p-phrase__words" aria-label={used.phrase}>
            {phraseWords(used.phrase).map((w, i) => <span key={`${w}-${i}`} className="p2p-phrase__word">{w}</span>)}
          </p>
          <p className="p2p-phrase__hint">{t('share.connected.hint', { label: name })}</p>
        </div>
        <dl className="p2p-facts">
          <dt>{t('share.facts.devicePeer')}</dt>
          <dd className="dk-mono" title={used.device_peer_id}>{shortPeer(used.device_peer_id)}</dd>
        </dl>
        {error && <Callout tone="error" role="alert" testId="share-error">{error}</Callout>}
        </div>
      </Dialog>
    )
  }

  if (minted) {
    const warnings = inviteWarnings(minted)
    const grant = modelGrant(minted)
    const expired = left === 0
    const qr = minted.qr && QR_KINDS.includes(minted.kind)
    const relay = minted.relay || {}
    const relayText = relay.id
      ? t(relay.open ? 'share.facts.relayOpen' : 'share.facts.relayGated', { relay: relay.label || relay.id })
      : t('share.facts.none')
    const publicAddr = status?.reachability === 'public'
    return (
      <Dialog
        wide
        title={t('share.result.title', { label: name })}
        description={t('share.result.sub', { kind: t(`kinds.${minted.kind}.title`), ttl: t(`ttl.${minted.ttl}`) })}
        onClose={close} pending={busy !== ''} closeLabel={t('share.close')} labelId={labelId} testId="share-result"
        foot={(
          <div className="p2p-foot">
            <span className="p2p-foot__note" role="status">{expired ? t('share.result.expired') : t(minted.kind === 'worker' ? 'share.result.waitingWorker' : 'share.result.waiting')}</span>
            <span className="p2p-foot__actions">
              {!expired && <Button variant="ghost" busy={busy === 'cancel'} onClick={cancelInvite} data-testid="share-cancel-invite">{t('share.result.cancel')}</Button>}
              <Button variant="primary" onClick={onClose} data-testid="share-done">{t('share.result.done')}</Button>
            </span>
          </div>
        )}
      >
        <div className="p2p-dialog">
        <div className={qr ? 'p2p-invite p2p-invite--qr' : 'p2p-invite'}>
          {qr && (
            <figure className="p2p-invite__qr">
              <QrCode text={minted.link} label={t('share.result.qrLabel')} />
              <figcaption>{t('share.result.qrCaption')}</figcaption>
            </figure>
          )}
          <div className="p2p-invite__main">
            <LinkBox link={minted.link} addToast={addToast} />
            <p className="p2p-hint">{t(`share.result.how.${minted.kind}`)}</p>
            <dl className="p2p-facts" data-testid="share-facts">
              <dt>{t('share.facts.expires')}</dt>
              <dd><span className="dk-mono" data-testid="share-countdown">{clock(left)}</span> · {t('share.facts.singleUse')}</dd>
              <dt>{t('share.facts.serverPeer')}</dt>
              <dd className="dk-mono" title={status?.peer_id}>{shortPeer(status?.peer_id) || '-'}</dd>
              {grant && (
                <>
                  <dt>{t('share.facts.models')}</dt>
                  <dd data-testid="share-granted">
                    {grant.all ? t('share.facts.modelsAll') : t('share.facts.modelsSome', { count: grant.names.length })}
                    {!grant.all && <span className="p2p-grant dk-mono">{grant.names.join(', ')}</span>}
                  </dd>
                </>
              )}
              <dt>{t('share.facts.relay')}</dt>
              <dd>{relayText}</dd>
              <dt>{t('share.facts.pass')}</dt>
              <dd data-testid="share-pass">
                {relay.pass
                  ? <StatusChip tone="ok" dot>{t('share.facts.passOn', { relay: relay.label || relay.id, ttl: t(`ttl.${minted.ttl}`) })}</StatusChip>
                  : relay.reason === 'relay_unreachable'
                    ? <StatusChip tone="warn" dot>{t('share.facts.passUnreachable', { relay: relay.label || relay.id })}</StatusChip>
                    : <span>{t('share.facts.passNone')}</span>}
              </dd>
              <dt>{t('share.facts.public')}</dt>
              <dd>{publicAddr ? t('share.facts.publicYes') : t('share.facts.publicNo')}</dd>
            </dl>
          </div>
        </div>

        {expired && (
          <Callout tone="warn" title={t('share.result.expiredTitle')} testId="share-expired"
            actions={<Button size="sm" variant="primary" onClick={again}>{t('share.result.again')}</Button>}>
            {t('share.result.expiredBody')}
          </Callout>
        )}
        {!expired && warnings.map(w => <InviteWarning key={w} warning={w} relay={relay} onAgain={again} t={t} />)}
        {minted.kind === 'device' && (
          <Callout tone="info" title={t('share.admission.title')} testId="share-admission">{t('share.admission.result')}</Callout>
        )}
        {error && <Callout tone="error" role="alert" testId="share-error">{error}</Callout>}
        </div>
      </Dialog>
    )
  }

  return (
    <Dialog
      wide
      title={t('share.title')}
      description={t('share.intro')}
      onClose={close} pending={busy !== ''} closeLabel={t('share.close')} labelId={labelId} testId="share-dialog"
      foot={(
        <div className="p2p-foot">
          <span className="p2p-foot__note">{t('share.nothingYet')}</span>
          <span className="p2p-foot__actions">
            <Button onClick={onClose} disabled={busy !== ''}>{t('share.cancel')}</Button>
            <Button variant="primary" busy={busy === 'mint'} disabled={blocked !== ''} aria-describedby={blocked ? `${labelId}-models-reason` : undefined} onClick={mint} data-testid="share-mint">{t('share.mint')}</Button>
          </span>
        </div>
      )}
    >
      <div className="p2p-dialog">
      <KindChoice kinds={shareKinds(distributed)} kind={kind} onChange={k => { setKind(k); setNeedsAccount(false) }} t={t} />
      <p className="p2p-hint">{t('share.qrNote')}</p>
      <Callout tone="info" title={t(`share.kindNote.${kind}.title`)} testId="share-kind-note">{t(`share.kindNote.${kind}.body`)}</Callout>
      <div className="p2p-form">
        <div className="dk-field">
          <label className="dk-label" htmlFor={`${labelId}-label`}>{t('share.label')}</label>
          <input
            id={`${labelId}-label`} className="dk-input" autoComplete="off" maxLength={80}
            value={label} placeholder={t(`share.labelPlaceholder.${kind}`)}
            onChange={e => setLabel(e.target.value)} aria-describedby={`${labelId}-label-hint`} data-testid="share-label"
          />
          <p className="dk-hint" id={`${labelId}-label-hint`}>{t('share.labelHint')}</p>
        </div>
        <div className="dk-field">
          <span className="dk-label" id={`${labelId}-ttl`}>{t('share.expires')}</span>
          <Segmented
            mode="radio" labelledBy={`${labelId}-ttl`} value={ttl} onChange={setTtl} testId="share-ttl" itemTestId="share-ttl-"
            options={TTL_CHOICES.map(c => ({ id: c.id, label: t(`ttl.${c.seconds}`) }))}
          />
          <p className="dk-hint">{t('share.singleUseHint')}</p>
        </div>
      </div>
      {kind === 'remote' && (
        <ShareModelChoice scope={modelScope} onScope={setModelScope} picked={picked} onPicked={setPicked} reason={blocked} reasonId={`${labelId}-models-reason`} />
      )}
      {needsAccount && (
        <Callout tone="warn" role="alert" title={t('share.needsAccount.title')} testId="share-needs-account">
          {t('share.needsAccount.body')}
        </Callout>
      )}
      {error && <Callout tone="error" role="alert" testId="share-error">{error}</Callout>}
      </div>
    </Dialog>
  )
}

// The kinds as cards with a radio each, the way the person reads them: a name
// and one line on what the other side gets.
function KindChoice({ kinds, kind, onChange, t }) {
  const id = useId()
  return (
    <fieldset className="p2p-kinds">
      <legend className="dk-label" id={id}>{t('share.kind')}</legend>
      <div className="p2p-kinds__cards" role="radiogroup" aria-labelledby={id} data-testid="share-kind">
        {kinds.map(k => (
          <label key={k} className={kind === k ? 'p2p-kind p2p-kind--on' : 'p2p-kind'} data-testid={`share-kind-${k}`}>
            <input type="radio" name="p2p-share-kind" value={k} checked={kind === k} onChange={() => onChange(k)} />
            <span className="p2p-kind__text">
              <span className="p2p-kind__title"><Icon name={KIND_ICON[k]} /> {t(`kinds.${k}.title`)}</span>
              <span className="p2p-kind__body">{t(`kinds.${k}.body`)}</span>
            </span>
          </label>
        ))}
      </div>
    </fieldset>
  )
}

// The link as text that wraps (it is one token, so it breaks anywhere) and a
// Copy button. The link is shown once: it is in the mint answer only.
function LinkBox({ link, addToast }) {
  const { t } = useTranslation('p2p')
  const [copied, setCopied] = useState(false)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = async () => {
    if (await copyToClipboard(link)) {
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 2000)
    } else {
      addToast?.(t('share.copyFailed'), 'error')
    }
  }
  return (
    <div className="dk-field">
      <span className="dk-label">{t('share.result.link')}</span>
      <div className="p2p-link">
        <code className="p2p-link__text dk-mono" tabIndex={0} data-testid="share-link">{link}</code>
        <span className="p2p-link__copy">
          <Button size="sm" icon={copied ? 'check' : 'copy'} onClick={copy} data-testid="share-copy">
            {copied ? t('share.copied') : t('share.copy')}
          </Button>
        </span>
      </div>
    </div>
  )
}

// A warning of the minted invite and what to do about it.
function InviteWarning({ warning, relay, onAgain, t }) {
  const relayName = relay?.label || relay?.id || ''
  if (warning === 'relay_unreachable') {
    return (
      <Callout tone="warn" title={t('share.warn.unreachable.title')} testId="share-warn-relay_unreachable"
        actions={(
          <>
            <Button size="sm" variant="primary" onClick={onAgain}>{t('share.warn.unreachable.again')}</Button>
            <Button size="sm" to={P2P_SETTINGS_PATH}>{t('share.warn.unreachable.check')}</Button>
          </>
        )}>
        {t('share.warn.unreachable.body', { relay: relayName })}
      </Callout>
    )
  }
  if (warning === 'no_relay') {
    return (
      <Callout tone="warn" title={t('share.warn.noRelay.title')} testId="share-warn-no_relay"
        actions={<Button size="sm" to={P2P_SETTINGS_PATH}>{t('share.warn.noRelay.add')}</Button>}>
        {t('share.warn.noRelay.body')}
      </Callout>
    )
  }
  if (warning === 'open_relay') {
    return (
      <Callout tone="warn" title={t('share.warn.openRelay.title')} testId="share-warn-open_relay">
        {t('share.warn.openRelay.body', { relay: relayName })}
      </Callout>
    )
  }
  return (
    <Callout tone="info" title={t('share.warn.noPublic.title')} testId="share-warn-no_public_addr">
      {t('share.warn.noPublic.body')}
    </Callout>
  )
}
