/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'
import QrCode from './QrCode'
import { copyToClipboard } from '../../utils/clipboard'
import { P2P_SETTINGS_PATH } from './P2PTabs'

// The parts of a minted invite that the admin "Share this instance" dialog
// and a user's "Pair a phone" dialog both show: the QR code, the link with
// its Copy button, and the warnings of the mint.

// The QR code of an invite link, with its caption.
export function InviteQr({ link }) {
  const { t } = useTranslation('p2p')
  return (
    <figure className="p2p-invite__qr">
      <QrCode text={link} label={t('share.result.qrLabel')} />
      <figcaption>{t('share.result.qrCaption')}</figcaption>
    </figure>
  )
}

// The link as text that wraps (it is one token, so it breaks anywhere) and a
// Copy button. The link is shown once: it is in the mint answer only.
export function LinkBox({ link, addToast }) {
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

// A warning of the minted invite and what to do about it. punch says that the
// link names public relays for hole punching; dht that the public DHT, which
// finds them, is on. canFix is false for a user who is not an administrator:
// the relays are not theirs to add, so the callout says whom to ask instead
// of linking to the P2P settings.
export function InviteWarning({ warning, relay, punch, dht, onAgain, canFix = true }) {
  const { t } = useTranslation('p2p')
  const relayName = relay?.label || relay?.id || ''
  const ask = canFix ? '' : ` ${t('share.warn.askAdmin')}`
  if (warning === 'relay_unreachable') {
    return (
      <Callout tone="warn" title={t('share.warn.unreachable.title')} testId="share-warn-relay_unreachable"
        actions={(
          <>
            <Button size="sm" variant="primary" onClick={onAgain}>{t('share.warn.unreachable.again')}</Button>
            {canFix && <Button size="sm" to={P2P_SETTINGS_PATH}>{t('share.warn.unreachable.check')}</Button>}
          </>
        )}>
        {t('share.warn.unreachable.body', { relay: relayName })}
      </Callout>
    )
  }
  if (warning === 'no_relay') {
    return (
      <Callout tone="warn" title={t('share.warn.noRelay.title')} testId="share-warn-no_relay"
        actions={canFix ? <Button size="sm" to={P2P_SETTINGS_PATH}>{t('share.warn.noRelay.add')}</Button> : undefined}>
        {canFix ? t('share.warn.noRelay.body') : `${t('share.warn.noRelay.userBody')}${ask}`}
      </Callout>
    )
  }
  if (warning === 'punch_only') {
    return (
      <Callout tone="warn" title={t('share.warn.punchOnly.title')} testId="share-warn-punch_only"
        actions={canFix ? <Button size="sm" to={P2P_SETTINGS_PATH}>{t('share.warn.punchOnly.add')}</Button> : undefined}>
        {canFix ? t('share.warn.punchOnly.body') : `${t('share.warn.punchOnly.userBody')}${ask}`}
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
      {punch
        ? t('share.warn.noPublic.punch')
        : canFix
          ? `${t('share.warn.noPublic.lan')} ${t(dht ? 'share.warn.noPublic.later' : 'share.warn.noPublic.noDht')}`
          : t('share.warn.noPublic.lan')}
    </Callout>
  )
}
