/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'
import { hostGrantsApi } from '../../utils/addonsClient'
import { approvalRequest } from './approvalRequest'
import { UntrustedNotice } from './AddonBits'
import { signedBy } from './addonState'
import AddonOpStatus from './AddonOpStatus'
import HostAccessList from './HostAccessList'
import HostDialog from './HostDialog'
import { useHostPreview } from './useHostPreview'
import { approvalBody, installBlock } from './hostGrantsState'
import { describeError } from './errors'
import { upgradeFailureWords } from './updateReview'

// The approval of an upgrade that asks for more than the old version holds. The
// old version keeps running until the administrator approves here. Only the
// operations that are new are highlighted; the rest is context.
export default function ApproveUpgradeDialog({ addon, sourceId, upgrade, onClose, onDone }) {
  const { t } = useTranslation('addons')
  const entry = { sourceId: sourceId || addon.sourceId, id: addon.id, version: addon.pendingUpgrade.version }
  const host = useHostPreview(entry)
  const [sending, setSending] = useState(false)
  const [failure, setFailure] = useState(null)
  // The approval runs as the operation of the add-on in the upgrade store, not
  // in this dialog: closing the dialog leaves it running, and opening it again
  // shows it queued or running, with Cancel, and sends nothing a second time.
  const op = upgrade.opOf(addon.id)
  const failedOp = op && op.state === 'failed' ? op : null
  const running = sending || Boolean(op && !failedOp)
  const busy = running
  const ready = host.status === 'ready'
  const block = ready ? installBlock(host.preview) : null
  const added = ready ? (host.preview.added.length > 0 ? host.preview.added : addon.pendingUpgrade.delta) : addon.pendingUpgrade.delta
  const approve = async () => {
    setSending(true); setFailure(null)
    const body = approvalBody(host.preview, host.ticked, { action: 'upgrade', sourceId: entry.sourceId, addonId: addon.id, version: entry.version })
    const out = await upgrade.approve(addon, approvalRequest(addon, body, hostGrantsApi.queue))
    setSending(false)
    if (out && out.ok) { onDone?.(addon); return }
    if (out && out.message && out.outcome !== 'cancelled' && out.outcome !== 'detached') setFailure(upgradeFailureWords(out.message, t))
  }
  const note = upgrade.noteOf(addon.id)
  return (
    <HostDialog
      testId="approve-dialog"
      busy={busy}
      dismissWhenBusy
      closeLabel={t('catalog.close')}
      title={t('hostAccess.approve.title', { name: addon.title, version: entry.version })}
      onClose={onClose}
      actions={(
        <>
          <Button onClick={onClose} data-testid="approve-dismiss">{running ? t('catalog.close') : t('catalog.cancel')}</Button>
          <Button variant="primary" busy={busy} disabled={!ready || busy || Boolean(block)} onClick={approve} data-autofocus="true" data-testid="approve-confirm">{t('hostAccess.approve.action')}</Button>
        </>
      )}
    >
      <p>{t('hostAccess.approve.body', { name: addon.title, current: addon.version })}</p>
      {running && <Callout tone="info" role="status" testId="approve-running">{t('hostAccess.approve.running')}</Callout>}
      {op && !failedOp && <AddonOpStatus op={op} name={addon.title} version={addon.version} onCancel={upgrade.cancel} />}
      {!op && note && <AddonOpStatus note={note} name={addon.title} version={addon.version} onDismissNote={upgrade.closeNote} />}
      {host.status === 'loading' && <p className="ad-quiet" role="status" data-testid="approve-loading">{t(host.slow ? 'hostAccess.loadingSlow' : 'hostAccess.loading')}</p>}
      {host.status === 'error' && (
        <Callout tone="error" role="alert" testId="approve-preview-error" actions={<Button size="sm" icon="refresh" onClick={host.retry}>{t('catalog.retry')}</Button>}>
          {describeError(host.error, t)}
        </Callout>
      )}
      {ready && host.preview.official !== true && <UntrustedNotice library={entry.sourceId} signer={signedBy(null, host.preview)} testId="approve-untrusted-source" />}
      {ready && <HostAccessList ops={host.preview.ops} ticked={host.ticked} onTick={host.setTick} added={added} testId="approve" locked={host.preview.locked} />}
      {ready && host.preview.locked && <Callout tone="info" testId="approve-locked">{t(host.preview.lockedReason === 'no_auth' ? 'hostAccess.lockedNoAuth' : 'hostAccess.locked')}</Callout>}
      {block && <Callout tone="error" role="alert" testId="approve-blocked">{t(block.kind === 'blocked' ? 'hostAccess.cannotInstallBlocked' : 'hostAccess.cannotInstallUnsupported', { ops: block.ops.join(', ') })}</Callout>}
      {failedOp && !running && <Callout tone="error" role="alert" testId="approve-failed">{upgradeFailureWords(failedOp.errorKey, t)}</Callout>}
      {!failedOp && failure && <Callout tone="error" role="alert" testId="approve-failed">{failure}</Callout>}
    </HostDialog>
  )
}

// The banner for an upgrade that waits: the old version keeps running. While
// the approved upgrade runs it says so; `full` (a page with no row of the
// add-on to show it) adds the state with its Cancel, and the note of a
// canceled one.
export function WaitingBanner({ addon, onReview, busy = false, op = null, note = null, full = false, upgrade = null }) {
  const { t } = useTranslation('addons')
  const active = op && op.state !== 'failed' ? op : null
  const failed = op && op.state === 'failed' ? op : null
  return (
    <Callout
      tone="warn"
      icon="shield"
      title={t('hostAccess.waiting.title', { name: addon.title, version: addon.pendingUpgrade.version })}
      testId="host-waiting"
      data-app={addon.id}
      actions={<Button size="sm" disabled={busy} onClick={onReview} aria-label={t('hostAccess.waiting.reviewNamed', { name: addon.title })} data-testid="host-waiting-review">{t('hostAccess.waiting.review')}</Button>}
    >
      {t('hostAccess.waiting.body', { current: addon.version, count: addon.pendingUpgrade.delta.length })}
      {active && <p className="ad-entry__note" role="status" data-testid="host-waiting-progress">{t('hostAccess.waiting.progress', { name: addon.title })}</p>}
      {failed && <p className="ad-entry__note" data-tone="error" role="alert" data-testid="host-waiting-failed">{upgradeFailureWords(failed.errorKey, t)}</p>}
      {full && upgrade && <AddonOpStatus op={active} note={active ? null : note} name={addon.title} version={addon.version} onCancel={upgrade.cancel} onDismissNote={upgrade.closeNote} />}
    </Callout>
  )
}
