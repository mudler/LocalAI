/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'
import { requestFingerprint } from './actionBook'
import { hostGrantsApi } from '../../utils/addonsClient'
import { normalizeOperation } from '../../utils/addonsApi'
import { UntrustedNotice } from './AddonBits'
import { signedBy } from './addonState'
import HostAccessList from './HostAccessList'
import HostDialog from './HostDialog'
import { useHostPreview } from './useHostPreview'
import { approvalBody, installBlock } from './hostGrantsState'
import { describeError, describeOutcome } from './errors'

// One identity per distinct approval: the same ticks of the same preview are a
// retry and keep it; other ticks or a fresh preview are a new request.
export function approveKey(addon, body) {
  return `approve:${addon.id}:${addon.pendingUpgrade.version}:${requestFingerprint(body)}`
}

// Where the approval of an add-on stands, read from the actions of the page,
// which outlive the dialog: { working } while it runs, { failed: outcome } when
// the last one did not complete, or null. Closing the dialog never changes it.
export function approvalProgress(actions, addon) {
  const prefix = `approve:${addon.id}:${addon.pendingUpgrade.version}:`
  const mine = Object.entries(actions.state || {}).filter(([key]) => key.startsWith(prefix)).map(([, value]) => value)
  if (mine.some(v => v && v.phase === 'working')) return { working: true }
  const failed = mine.find(v => v && v.outcome && v.outcome !== 'completed')
  return failed ? { failed } : null
}

// Runs the approval of a held upgrade as one action of the administrator, with
// its own operation identity and the unknown-outcome rule of every lifecycle.
export function approveUpgrade(actions, api, addon, body) {
  // An earlier failure of another request is not this one's answer.
  for (const key of Object.keys(actions.state || {})) if (key.startsWith(`approve:${addon.id}:${addon.pendingUpgrade.version}:`)) actions.clear?.(key)
  return actions.run(approveKey(addon, body), {
    addonId: addon.id,
    submit: async (operationId, signal) => {
      const out = await hostGrantsApi.install({ ...body, operationId }, signal)
      return normalizeOperation(out && out.payload !== undefined ? out.payload : out)
    },
    verify: async (signal) => (await api.installed(signal)).some(a => a.id === addon.id && a.version === addon.pendingUpgrade.version),
  })
}

// The approval of an upgrade that asks for more than the old version holds. The
// old version keeps running until the administrator approves here. Only the
// operations that are new are highlighted; the rest is context.
export default function ApproveUpgradeDialog({ addon, sourceId, api, actions, onClose, onDone }) {
  const { t } = useTranslation('addons')
  const entry = { sourceId: sourceId || addon.sourceId, id: addon.id, version: addon.pendingUpgrade.version }
  const host = useHostPreview(entry)
  const [sending, setSending] = useState(false)
  const [failure, setFailure] = useState(null)
  // The approval runs in the page's actions, not in this dialog: closing the
  // dialog leaves it running, and opening it again shows it still running.
  const progress = approvalProgress(actions, addon)
  const running = sending || Boolean(progress && progress.working)
  const busy = running
  const ready = host.status === 'ready'
  const block = ready ? installBlock(host.preview) : null
  const added = ready ? (host.preview.added.length > 0 ? host.preview.added : addon.pendingUpgrade.delta) : addon.pendingUpgrade.delta
  const approve = async () => {
    setSending(true); setFailure(null)
    const body = approvalBody(host.preview, host.ticked, { action: 'upgrade', sourceId: entry.sourceId, addonId: addon.id, version: entry.version })
    const out = await approveUpgrade(actions, api, addon, body)
    setSending(false)
    if (out && out.outcome === 'completed') { onDone?.(); return }
    if (out) setFailure(describeOutcome(out.outcome, t, out.error))
  }
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
      {!failure && !running && progress && progress.failed && <Callout tone="error" role="alert" testId="approve-failed">{describeOutcome(progress.failed.outcome, t, progress.failed.error)}</Callout>}
      {failure && <Callout tone="error" role="alert" testId="approve-failed">{failure}</Callout>}
    </HostDialog>
  )
}

// The banner for an upgrade that waits: the old version keeps running.
export function WaitingBanner({ addon, onReview, busy = false, progress = null }) {
  const { t } = useTranslation('addons')
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
      {progress && progress.working && <p className="ad-entry__note" role="status" data-testid="host-waiting-progress">{t('hostAccess.waiting.progress', { name: addon.title })}</p>}
      {progress && progress.failed && <p className="ad-entry__note" data-tone="error" role="alert" data-testid="host-waiting-failed">{describeOutcome(progress.failed.outcome, t, progress.failed.error)}</p>}
    </Callout>
  )
}
