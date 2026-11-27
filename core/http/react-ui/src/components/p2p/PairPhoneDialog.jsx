/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import { Button, Callout } from '../ui'
import Segmented from '../ui/Segmented'
import { InviteQr, InviteWarning, LinkBox } from './InviteLink'
import { myP2PApi } from '../../utils/api'
import { clock, inviteWarnings, myMintFailure, newDevice, pairBlocked, secondsLeft, ttlWords, userTtlChoices } from '../../utils/p2p'

// How often the open dialog asks whether the phone has paired.
const POLL_MS = 3000

// "Pair a phone": a user makes a one-time invite for their own phone, shows
// its QR code, and waits for the phone to pair. The phone then signs in as
// the user. The kind, the relays and the rest come from the policy of the
// administrator, so the dialog asks only for a name and a life.
//
//   me         GET /api/p2p/me, for the limits
//   isAdmin    the user is an administrator: the phone gets an admin session
//   onChanged  after an invite is made, canceled or used, or a device revoked
//   onClose    closes the dialog
//   addToast   the shell's toast
export default function PairPhoneDialog({ me, isAdmin, onChanged, onClose, addToast }) {
  const { t } = useTranslation('p2p')
  const labelId = useId()
  const choices = userTtlChoices(me?.limits?.max_ttl_seconds)
  const [label, setLabel] = useState('')
  const [ttl, setTtl] = useState(choices[0].id)
  const [busy, setBusy] = useState('')
  const [failure, setFailure] = useState(null)
  const [minted, setMinted] = useState(null)
  const [paired, setPaired] = useState(null)
  const [left, setLeft] = useState(0)
  // The devices the user had when the invite was made: a new one is the phone.
  const known = useRef(new Set())

  const ttlLabel = (seconds) => { const w = ttlWords(seconds); return t(w.key, w.values) }

  const mint = async () => {
    setBusy('mint'); setFailure(null)
    try {
      const before = await myP2PApi.devices().catch(() => null)
      known.current = new Set((before?.devices || []).map(d => d.id))
      const seconds = Number(ttl)
      const res = await myP2PApi.mintInvite(label.trim(), seconds)
      setMinted({ ...res, label: label.trim(), ttl: seconds })
      setPaired(null)
      onChanged?.()
    } catch (err) {
      setFailure({ kind: myMintFailure(err), message: err?.message || '' })
    } finally {
      setBusy('')
    }
  }

  useEffect(() => {
    if (!minted || paired) return undefined
    const tick = () => setLeft(secondsLeft(minted.expires_at))
    tick()
    const id = setInterval(tick, 1000)
    return () => clearInterval(id)
  }, [minted, paired])

  // Waits for the phone: when the invite is no longer pending, the device it
  // made is the one the user did not have before.
  const changed = useRef(onChanged)
  changed.current = onChanged
  useEffect(() => {
    if (!minted || paired) return undefined
    let stop = false
    const poll = async () => {
      try {
        const inv = await myP2PApi.invites()
        if ((inv?.invites || []).some(i => i.id === minted.id)) return
        const res = await myP2PApi.devices()
        const dev = newDevice(res?.devices, known.current)
        if (!stop && dev) {
          setPaired(dev)
          changed.current?.()
        }
      } catch {
        // The next poll tries again; the dialog keeps the link on screen.
      }
    }
    const id = setInterval(poll, POLL_MS)
    return () => { stop = true; clearInterval(id) }
  }, [minted, paired])

  const cancelInvite = async () => {
    setBusy('cancel'); setFailure(null)
    try {
      await myP2PApi.cancelInvite(minted.id)
      onChanged?.()
      addToast?.(t('share.canceled'), 'success')
      onClose()
    } catch (err) {
      setFailure({ kind: 'other', message: err?.message || '' })
      setBusy('')
    }
  }

  const revoke = async () => {
    setBusy('revoke'); setFailure(null)
    try {
      await myP2PApi.revokeDevice(paired.id)
      onChanged?.()
      addToast?.(t('my.revoked', { label: paired.label || t('my.phone') }), 'success')
      onClose()
    } catch (err) {
      setFailure({ kind: 'other', message: err?.message || '' })
      setBusy('')
    }
  }

  const again = () => { setMinted(null); setPaired(null); setFailure(null) }
  const close = () => { if (!busy) onClose() }
  const adminNote = isAdmin && (
    <Callout tone="warn" title={t('my.admin.title')} testId="pair-admin-warning">{t('my.admin.body')}</Callout>
  )
  const failureNote = failure && <PairFailure failure={failure} me={me} maxTtl={ttlLabel(me?.limits?.max_ttl_seconds || 0)} />

  if (minted && paired) {
    const name = paired.label || minted.label || t('my.phone')
    return (
      <Dialog
        title={t('my.paired.title', { label: name })}
        description={t('my.paired.sub')}
        onClose={close} pending={busy !== ''} closeLabel={t('share.close')} labelId={labelId} testId="pair-done"
        foot={(
          <div className="p2p-foot">
            <span className="p2p-foot__actions">
              <Button variant="danger-ghost" busy={busy === 'revoke'} onClick={revoke} data-testid="pair-revoke">{t('my.paired.notMine')}</Button>
              <Button variant="primary" onClick={onClose} data-autofocus data-testid="pair-ok">{t('share.result.done')}</Button>
            </span>
          </div>
        )}
      >
        <div className="p2p-dialog">
          <p className="p2p-hint">{t('my.paired.body')}</p>
          {failureNote}
        </div>
      </Dialog>
    )
  }

  if (minted) {
    const warnings = inviteWarnings(minted)
    const expired = left === 0
    const name = minted.label || t('my.phone')
    return (
      <Dialog
        wide
        title={t('my.result.title', { label: name })}
        description={t('my.result.sub', { ttl: ttlLabel(minted.ttl) })}
        onClose={close} pending={busy !== ''} closeLabel={t('share.close')} labelId={labelId} testId="pair-result"
        foot={(
          <div className="p2p-foot">
            <span className="p2p-foot__note" role="status">{expired ? t('share.result.expired') : t('my.result.waiting')}</span>
            <span className="p2p-foot__actions">
              {!expired && <Button variant="ghost" busy={busy === 'cancel'} onClick={cancelInvite} data-testid="pair-cancel-invite">{t('share.result.cancel')}</Button>}
              <Button variant="primary" onClick={onClose} data-testid="pair-close">{t('share.result.done')}</Button>
            </span>
          </div>
        )}
      >
        <div className="p2p-dialog">
          <div className="p2p-invite p2p-invite--qr">
            <InviteQr link={minted.link} />
            <div className="p2p-invite__main">
              <LinkBox link={minted.link} addToast={addToast} />
              <p className="p2p-hint">{t('my.result.how')}</p>
              <dl className="p2p-facts" data-testid="pair-facts">
                <dt>{t('share.facts.expires')}</dt>
                <dd><span className="dk-mono" data-testid="pair-countdown">{clock(left)}</span> · {t('share.facts.singleUse')}</dd>
                <dt>{t('my.result.signsIn')}</dt>
                <dd>{t('my.result.asYou')}</dd>
              </dl>
            </div>
          </div>
          {expired && (
            <Callout tone="warn" title={t('share.result.expiredTitle')} testId="pair-expired"
              actions={<Button size="sm" variant="primary" onClick={again}>{t('share.result.again')}</Button>}>
              {t('share.result.expiredBody')}
            </Callout>
          )}
          {!expired && warnings.map(w => (
            <InviteWarning key={w} warning={w} relay={minted.relay} punch={warnings.includes('punch_only')} dht onAgain={again} canFix={false} />
          ))}
          {adminNote}
          {failureNote}
        </div>
      </Dialog>
    )
  }

  return (
    <Dialog
      title={t('my.pair.title')}
      description={t('my.lede')}
      onClose={close} pending={busy !== ''} closeLabel={t('share.close')} labelId={labelId} testId="pair-dialog"
      foot={(
        <div className="p2p-foot">
          <span className="p2p-foot__note">{t('share.nothingYet')}</span>
          <span className="p2p-foot__actions">
            <Button onClick={onClose} disabled={busy !== ''}>{t('share.cancel')}</Button>
            <Button variant="primary" busy={busy === 'mint'} onClick={mint} data-testid="pair-mint">{t('my.pair.mint')}</Button>
          </span>
        </div>
      )}
    >
      <div className="p2p-dialog">
        <div className="p2p-form">
          <div className="dk-field">
            <label className="dk-label" htmlFor={`${labelId}-label`}>{t('share.label')}</label>
            <input
              id={`${labelId}-label`} className="dk-input" autoComplete="off" maxLength={80}
              value={label} placeholder={t('my.pair.labelPlaceholder')}
              onChange={e => setLabel(e.target.value)} aria-describedby={`${labelId}-label-hint`} data-testid="pair-label"
            />
            <p className="dk-hint" id={`${labelId}-label-hint`}>{t('my.pair.labelHint')}</p>
          </div>
          <div className="dk-field">
            <span className="dk-label" id={`${labelId}-ttl`}>{t('share.expires')}</span>
            <Segmented
              mode="radio" labelledBy={`${labelId}-ttl`} value={ttl} onChange={setTtl} testId="pair-ttl" itemTestId="pair-ttl-"
              options={choices.map(c => ({ id: c.id, label: ttlLabel(c.seconds) }))}
            />
            <p className="dk-hint">{t('share.singleUseHint')}</p>
          </div>
        </div>
        {adminNote}
        {failureNote}
      </div>
    </Dialog>
  )
}

// Why the server refused the invite, in the words of the person. A limit is
// named from the counts of GET /api/p2p/me, which the page reads again after
// a refusal; when neither count is at its limit, the hourly or the server-wide
// limit was reached.
function PairFailure({ failure, me, maxTtl }) {
  const { t } = useTranslation('p2p')
  const { kind, message } = failure
  if (kind === 'other') return <Callout tone="error" role="alert" testId="pair-error">{message || t('share.mintFailed')}</Callout>
  const limits = me?.limits || {}
  const body = kind === 'limit'
    ? t(`my.fail.limit.${{ devices: 'devices', pending: 'pending' }[pairBlocked(me)] || 'rate'}`, { max: limits.max_devices, pending: limits.max_pending, mints: limits.max_mints_per_hour })
    : t(`my.fail.${kind}.body`, { max: maxTtl })
  return (
    <Callout tone={kind === 'limit' ? 'warn' : 'error'} role="alert" title={t(`my.fail.${kind}.title`)} testId={`pair-fail-${kind}`}>
      {body}
    </Callout>
  )
}
