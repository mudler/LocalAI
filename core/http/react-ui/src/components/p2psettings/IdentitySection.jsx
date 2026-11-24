/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Icon from '../Icon'
import { Button } from '../ui'
import { copyToClipboard } from '../../utils/clipboard'
import { p2pRelaysApi } from '../../utils/api'

// Regenerate the peer ID. Every paired device and remote pinned the current
// ID and refuses a new one, so the person ticks that they understand before
// the button works.
function RegenerateDialog({ devices, onClose, onDone, addToast }) {
  const { t } = useTranslation('p2psettings')
  const [sure, setSure] = useState(false)
  const [busy, setBusy] = useState(false)
  const run = async () => {
    setBusy(true)
    try {
      await p2pRelaysApi.regenerateIdentity()
      addToast?.(t('identity.regenerated'), 'success')
      onDone()
    } catch (err) {
      addToast?.(err.message, 'error')
      setBusy(false)
    }
  }
  return (
    <Dialog
      role="alertdialog" title={<><Icon name="warning" /> {t('identity.confirmTitle')}</>}
      onClose={onClose} pending={busy} closeLabel={t('actions.close')} testId="regenerate-dialog" labelId="regenerate-title"
      foot={(
        <>
          <Button onClick={onClose} disabled={busy} data-autofocus>{t('actions.cancel')}</Button>
          <Button variant="danger" disabled={!sure || busy} busy={busy} onClick={run} data-testid="regenerate-confirm">{t('identity.regenerate')}</Button>
        </>
      )}
    >
      <div className="p2ps-section">
        <p>{t('identity.confirmBody')}</p>
        <p>{t('identity.confirmOnly')}</p>
        <label className="p2ps-confirm">
          <input type="checkbox" className="dk-check" checked={sure} onChange={(e) => setSure(e.target.checked)} data-testid="regenerate-sure" />
          <span>{t('identity.confirmCheck', { count: devices })}</span>
        </label>
      </div>
    </Dialog>
  )
}

// The peer ID of this server: clients pin it when they redeem an invite and
// the relays name it as their admin.
export default function IdentitySection({ status, onChanged, addToast }) {
  const { t } = useTranslation('p2psettings')
  const [asking, setAsking] = useState(false)
  const peerID = status?.peer_id || ''
  const copy = async () => {
    const ok = await copyToClipboard(peerID)
    addToast?.(ok ? t('identity.copied') : t('identity.copyFailed'), ok ? 'success' : 'error', 2000)
  }
  return (
    <section className="p2ps-section" aria-labelledby="p2ps-identity-title" data-testid="p2p-identity">
      <h2 className="st-h2" id="p2ps-identity-title">{t('identity.title')}</h2>
      <p className="st-note">{t('identity.lede')}</p>
      {peerID
        ? (
          <div className="p2ps-id">
            <code className="p2ps-id__value" data-testid="p2p-peer-id">{peerID}</code>
            <Button size="sm" icon="copy" onClick={copy}>{t('actions.copy')}</Button>
          </div>
        )
        : <p className="st-note">{t('identity.notRunning')}</p>}
      <div className="st-row">
        <div className="st-row__main">
          <div className="st-row__title"><span className="st-row__label">{t('identity.regenerateTitle')}</span></div>
          <p className="st-row__desc">{t('identity.regenerateDesc')}</p>
        </div>
        <div className="st-row__control">
          <Button variant="danger-ghost" size="sm" disabled={!status?.running} onClick={() => setAsking(true)} data-testid="regenerate-open">{t('identity.regenerateButton')}</Button>
        </div>
      </div>
      {asking && <RegenerateDialog devices={status?.devices || 0} addToast={addToast} onClose={() => setAsking(false)} onDone={() => { setAsking(false); onChanged() }} />}
    </section>
  )
}
