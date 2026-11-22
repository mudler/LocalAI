/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { Button, Callout, StatusChip } from '../ui'
import ConfirmDialog from '../ConfirmDialog'
import RelayLimitsDialog from '../p2psettings/RelayLimitsDialog'
import SplitServerSetup from './SplitServerSetup'
import { cutLimits, pathOf, relayOf } from './splitWorkersModel'
import { shortPeer } from '../../utils/p2p'

// The pieces of the model-split workers that the Workers roster and the panel
// of a single install share. `m` is useSplitManagement().

// The one callout of the group, the most important first: two stacked warnings
// push the list off a phone screen, and the first one is the one to fix first.
export function SplitCallout({ m }) {
  const { t } = useTranslation('swarm')
  const { callout, split, sum, idle, waiting, refused } = m
  const names = list => list.map(w => w.label || shortPeer(w.peer_id)).join(', ')
  if (callout === 'noServer') {
    return (
      <Callout
        tone="warn" title={t('split.noServerTitle')} testId="split-no-server-callout"
        actions={<Button size="sm" variant="primary" onClick={m.openSetup} data-testid="split-setup-open">{t('splitServer.open')}</Button>}
      >
        {t('split.noServerBody')}
      </Callout>
    )
  }
  if (callout === 'refused') {
    return (
      <Callout tone="warn" title={t('split.relayRefusedTitle')} testId="split-relay-refused-callout">
        {t('split.relayRefusedBody', { names: refused.map(w => w.node_name || w.node_id).filter((v, i, a) => a.indexOf(v) === i).join(', '), reason: relayOf(refused[0]).reason })}
      </Callout>
    )
  }
  if (callout === 'limited') return <Callout tone="warn" title={t('split.limitedTitle')} testId="split-limited-callout">{t('split.limitedBody')}</Callout>
  if (callout === 'cut') return <Callout tone="warn" title={t('split.cutTitle')} testId="split-cut-callout">{t('split.cutBody', { names: names(sum.cut) })}</Callout>
  if (callout === 'offline') return <Callout tone="warn" title={t('split.offlineTitle')} testId="split-offline-callout">{t('split.offlineBody', { names: names(waiting) })}</Callout>
  if (callout === 'unattached') return <Callout tone="warn" title={t('split.unattachedTitle')} testId="split-unattached-callout">{t('split.unattachedBody', { names: names(idle) })}</Callout>
  if (callout === 'slow') return <Callout title={t('split.slowTitle')} testId="split-slow-callout">{t('split.slowBody', { names: names(sum.slow) })}</Callout>
  return null
}

// The dialogs a split worker can open: the quick setup of a split server, the
// limits of a relay, and the confirm of a revoke.
export function SplitDialogs({ m, addToast }) {
  const { t } = useTranslation('swarm')
  const { split, revoking } = m
  return (
    <>
      {m.setup && (
        <SplitServerSetup
          nodes={m.nodeList} workers={split.workers} addToast={addToast}
          onClose={m.closeSetup} onDone={() => { split.refetch(); m.reloadNodes?.() }}
        />
      )}
      {m.limitsOf && <RelayLimitsDialog relay={m.limitsOf} addToast={addToast} onSaved={split.refetch} onClose={m.closeLimits} />}
      <ConfirmDialog
        open={Boolean(revoking)}
        title={t('split.revokeTitle', { name: revoking ? m.nameOf(revoking) : '' })}
        message={t('split.revokeBody')}
        confirmLabel={t('split.revokeConfirm')}
        pendingLabel={t('split.revoking')}
        pending={m.revokeBusy}
        danger
        onConfirm={m.revoke}
        onCancel={m.cancelRevoke}
      />
    </>
  )
}

// The node a worker is attached to, as a choice: this instance, or a backend
// node of the cluster. A node that left the list keeps its entry.
export function NodePicker({ worker, name, choices, busy, onChange, testId }) {
  const { t } = useTranslation('swarm')
  return (
    <select
      className="dk-select sw-split-node" value={worker.node_id || ''} disabled={busy}
      aria-label={t('split.nodeLabel', { name })} onChange={event => onChange(worker, event.target.value)}
      data-testid={testId}
    >
      <option value="">{t('split.thisServer')}</option>
      {worker.node_id && !choices.some(n => n.id === worker.node_id) && (
        <option value={worker.node_id}>{worker.node_name || worker.node_id}</option>
      )}
      {choices.map(n => <option key={n.id} value={n.id}>{n.name}</option>)}
    </select>
  )
}

// Whether the relays of this instance admit the node of a worker, and, for a
// node that is offline, that the worker waits for it. The reason of a refused
// node is in the callout and in the title of the chip.
export function NodeRelay({ worker, testId }) {
  const { t } = useTranslation('swarm')
  const relay = relayOf(worker)
  if (!worker.node_id) return null
  return (
    <span className="sw-split-relay" data-testid={testId}>
      {worker.node_offline && <StatusChip tone="warn" dot>{t('split.nodeOffline')}</StatusChip>}
      {relay && <StatusChip tone={relay.tone} dot title={relay.reason || undefined}>{t(`split.relay.${relay.key}`)}</StatusChip>}
    </span>
  )
}

// How the server reaches a worker now, with the round trip.
export function PathChip({ worker }) {
  const { t } = useTranslation('swarm')
  const path = pathOf(worker)
  if (path.key === 'none') return <span className="sw-unknown">-</span>
  return (
    <span className="sw-split-path">
      <StatusChip tone={worker.path?.kind === 'relayed' ? 'warn' : 'ok'} dot>{t(`split.path.${path.key}`, { relay: path.relay })}</StatusChip>
      {path.rtt && <span className="sw-hint">{t('split.path.rtt', { ms: path.rtt })}</span>}
      {cutLimits(worker).length > 0 && <span className="sw-hint">{t('split.cutHint', { limits: cutLimits(worker).join(', ') })}</span>}
    </span>
  )
}
