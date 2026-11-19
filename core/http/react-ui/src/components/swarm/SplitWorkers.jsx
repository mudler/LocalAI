/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { nodesApi, p2pNetApi, p2pSplitApi } from '../../utils/api'
import { useSplitWorkers } from '../../hooks/useSplitWorkers'
import { formatBytes } from '../../utils/format'
import { shortPeer } from '../../utils/p2p'
import { Button, Callout, StatusChip, useLoad } from '../ui'
import ConfirmDialog from '../ConfirmDialog'
import RelayLimitsDialog from '../p2psettings/RelayLimitsDialog'
import SplitServerSetup from './SplitServerSetup'
import { cutLimits, limitsRelay, nodeChoices, pathOf, relayOf, slowAction, stateOf, summarize, topCallout, unattached } from './splitWorkersModel'
import './split.css'

// The "Model-split workers" group of the Nodes page: machines that lend their
// memory so one big model runs across machines. They are not cluster nodes:
// no backends and no placement rules. A worker that only a relay reaches is
// slow; the administrator chooses "Use anyway" or leaves it out. In a cluster
// each worker is attached to the backend node whose models it serves.
export default function SplitWorkers({ addToast }) {
  const { t } = useTranslation('swarm')
  const split = useSplitWorkers()
  const nodes = useLoad(() => (split.distributed ? nodesApi.list() : Promise.resolve([])), [split.distributed])
  const choices = nodeChoices(nodes.data)
  const [busy, setBusy] = useState(null)
  const [revoking, setRevoking] = useState(null)
  const [revokeBusy, setRevokeBusy] = useState(false)
  const [limitsOf, setLimitsOf] = useState(null)
  const [setup, setSetup] = useState(false)

  if (split.status !== 'ready' || split.workers.length === 0) return null
  const sum = summarize(split.workers)
  const idle = unattached(split.workers, split.distributed)
  const noServer = split.distributed && nodes.status === 'ready' && choices.length === 0
  const waiting = split.workers.filter(w => w.node_id && w.node_offline)
  const refused = split.workers.filter(w => relayOf(w)?.key === 'refused')
  const callout = topCallout({ noServer, refused: refused.length > 0, limited: sum.limited.length > 0, cut: sum.cut.length > 0, offline: waiting.length > 0, unattached: idle.length > 0, slow: sum.slow.length > 0 })

  const setSlow = async (worker, allow) => {
    setBusy(worker.id)
    try {
      await p2pSplitApi.setSlowPath(worker.id, allow)
      addToast?.(t(allow ? 'split.toast.used' : 'split.toast.stopped', { name: worker.label || shortPeer(worker.peer_id) }), 'success')
      await split.refetch()
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setBusy(null)
    }
  }
  const attach = async (worker, nodeId) => {
    setBusy(worker.id)
    const name = worker.label || shortPeer(worker.peer_id)
    const node = choices.find(n => n.id === nodeId)?.name
    try {
      await p2pSplitApi.setNode(worker.id, nodeId)
      addToast?.(nodeId ? t('split.toast.attached', { name, node }) : t('split.toast.detached', { name }), 'success')
      await split.refetch()
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setBusy(null)
    }
  }
  const revoke = async () => {
    setRevokeBusy(true)
    try {
      await p2pNetApi.revokeDevice(revoking.id)
      addToast?.(t('split.toast.revoked', { name: revoking.label || shortPeer(revoking.peer_id) }), 'success')
      setRevoking(null)
      await split.refetch()
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setRevokeBusy(false)
    }
  }

  return (
    <section className="sw-section sw-split-workers" aria-labelledby="sw-split-title" data-testid="split-workers">
      <div className="sw-section__head">
        <h2 className="sw-h2" id="sw-split-title">
          {t('split.title')} <span className="sw-count">{t('split.online', { online: sum.online, total: sum.total })}</span>
        </h2>
        <p className="sw-note sw-note--quiet">{t(split.distributed ? 'split.noteCluster' : 'split.note')}</p>
      </div>
      {callout === 'noServer' && (
        <Callout
          tone="warn" title={t('split.noServerTitle')} testId="split-no-server-callout"
          actions={<Button size="sm" variant="primary" onClick={() => setSetup(true)} data-testid="split-setup-open">{t('splitServer.open')}</Button>}
        >
          {t('split.noServerBody')}
        </Callout>
      )}
      {callout === 'refused' && (
        <Callout tone="warn" title={t('split.relayRefusedTitle')} testId="split-relay-refused-callout">
          {t('split.relayRefusedBody', { names: refused.map(w => w.node_name || w.node_id).filter((v, i, a) => a.indexOf(v) === i).join(', '), reason: relayOf(refused[0]).reason })}
        </Callout>
      )}
      {callout === 'limited' && (
        <Callout tone="warn" title={t('split.limitedTitle')} testId="split-limited-callout">{t('split.limitedBody')}</Callout>
      )}
      {callout === 'cut' && (
        <Callout tone="warn" title={t('split.cutTitle')} testId="split-cut-callout">
          {t('split.cutBody', { names: sum.cut.map(w => w.label || shortPeer(w.peer_id)).join(', ') })}
        </Callout>
      )}
      {callout === 'offline' && (
        <Callout tone="warn" title={t('split.offlineTitle')} testId="split-offline-callout">
          {t('split.offlineBody', { names: waiting.map(w => w.label || shortPeer(w.peer_id)).join(', ') })}
        </Callout>
      )}
      {callout === 'unattached' && (
        <Callout tone="warn" title={t('split.unattachedTitle')} testId="split-unattached-callout">
          {t('split.unattachedBody', { names: idle.map(w => w.label || shortPeer(w.peer_id)).join(', ') })}
        </Callout>
      )}
      {callout === 'slow' && (
        <Callout title={t('split.slowTitle')} testId="split-slow-callout">
          {t('split.slowBody', { names: sum.slow.map(w => w.label || shortPeer(w.peer_id)).join(', ') })}
        </Callout>
      )}
      <div className="dk-card sw-roster">
        <div className="dk-table-wrap">
          <table className="dk-table sw-split-table">
            <caption className="dk-sr-only">{t('split.title')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('split.col.worker')}</th>
                <th scope="col" className="dk-hide-phone">{t('split.col.engine')}</th>
                {split.distributed && <th scope="col" className="dk-hide-phone">{t('split.col.node')}</th>}
                <th scope="col" className="dk-hide-phone">{t('split.col.path')}</th>
                <th scope="col" className="dk-hide-phone">{t('split.col.memory')}</th>
                <th scope="col" className="dk-hide-phone">{t('split.col.status')}</th>
                <th scope="col"><span className="dk-sr-only">{t('split.col.actions')}</span></th>
              </tr>
            </thead>
            <tbody>
              {split.workers.map(worker => {
                const state = stateOf(worker)
                const action = slowAction(worker)
                const name = worker.label || worker.name || shortPeer(worker.peer_id)
                return (
                  <tr key={worker.id} data-testid={`split-worker-${worker.id}`}>
                    <td>
                      <span className="dk-table-name dk-mono">{name}</span>
                      <span className="sw-hint sw-split-phone">{t(`split.engine.${worker.engine === 'mlx' ? 'mlx' : 'llama'}`)}</span>
                      <span className="sw-split-phone sw-split-phone--chip"><StatusChip tone={state.tone} dot>{t(`split.state.${state.key}`)}</StatusChip></span>
                      <span className="sw-split-phone sw-split-phone--chip"><PathChip worker={worker} /></span>
                      {split.distributed && (
                        <span className="sw-split-phone sw-split-phone--chip">
                          <NodePicker worker={worker} name={name} choices={choices} busy={busy === worker.id} onChange={attach} testId={`split-node-phone-${worker.id}`} />
                        </span>
                      )}
                      {split.distributed && <span className="sw-split-phone sw-split-phone--chip"><NodeRelay worker={worker} testId={`split-relay-phone-${worker.id}`} /></span>}
                      {worker.node_note && !worker.node_id && <span className="sw-hint" data-testid={`split-note-${worker.id}`}>{worker.node_note}</span>}
                      {worker.error && <span className="sw-hint">{worker.error}</span>}
                    </td>
                    <td className="dk-hide-phone">{t(`split.engine.${worker.engine === 'mlx' ? 'mlx' : 'llama'}`)}</td>
                    {split.distributed && (
                      <td className="dk-hide-phone">
                        <NodePicker worker={worker} name={name} choices={choices} busy={busy === worker.id} onChange={attach} testId={`split-node-${worker.id}`} />
                        <NodeRelay worker={worker} testId={`split-relay-${worker.id}`} />
                      </td>
                    )}
                    <td className="dk-hide-phone"><PathChip worker={worker} /></td>
                    <td className="dk-hide-phone">{worker.memory_bytes > 0 ? formatBytes(worker.memory_bytes) : <span className="sw-unknown">-</span>}</td>
                    <td className="dk-hide-phone"><StatusChip tone={state.tone} dot>{t(`split.state.${state.key}`)}</StatusChip></td>
                    <td>
                      <div className="sw-split-actions">
                        {action === 'useAnyway' && (
                          <Button size="sm" variant="primary" busy={busy === worker.id} onClick={() => setSlow(worker, true)} data-testid={`split-use-${worker.id}`}>{t('split.useAnyway')}</Button>
                        )}
                        {action === 'stopUsing' && (
                          <Button size="sm" busy={busy === worker.id} onClick={() => setSlow(worker, false)} data-testid={`split-stop-${worker.id}`}>{t('split.stopUsing')}</Button>
                        )}
                        {limitsRelay(worker) && (
                          <Button size="sm" variant="ghost" icon="sliders" onClick={() => setLimitsOf(limitsRelay(worker))} data-testid={`split-limits-${worker.id}`}>{t('split.relayLimits')}</Button>
                        )}
                        <Button size="sm" variant="danger-ghost" onClick={() => setRevoking(worker)} data-testid={`split-revoke-${worker.id}`}>{t('split.revoke')}</Button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>
      <div className="sw-split-foot">
        <span className="sw-note sw-note--quiet">
          {t('split.memoryInUse', { memory: formatBytes(split.memory) })}
          {split.distributed && ` ${t('split.nextLoad')}`}
        </span>
        <div className="sw-split-actions">
          {split.distributed && !noServer && <Button onClick={() => setSetup(true)} data-testid="split-setup-open">{t('splitServer.open')}</Button>}
          <Button icon="plus" to="/app/nodes/add?join=shard">{t('split.addWorker')}</Button>
        </div>
      </div>
      {setup && (
        <SplitServerSetup
          nodes={nodes.data} workers={split.workers} addToast={addToast}
          onClose={() => setSetup(false)} onDone={() => { split.refetch(); nodes.reload() }}
        />
      )}
      {limitsOf && <RelayLimitsDialog relay={limitsOf} addToast={addToast} onSaved={split.refetch} onClose={() => setLimitsOf(null)} />}
      <ConfirmDialog
        open={Boolean(revoking)}
        title={t('split.revokeTitle', { name: revoking ? revoking.label || shortPeer(revoking.peer_id) : '' })}
        message={t('split.revokeBody')}
        confirmLabel={t('split.revokeConfirm')}
        pendingLabel={t('split.revoking')}
        pending={revokeBusy}
        danger
        onConfirm={revoke}
        onCancel={() => setRevoking(null)}
      />
    </section>
  )
}

// The node a worker is attached to, as a choice: this instance, or a backend
// node of the cluster. A node that left the list keeps its entry.
function NodePicker({ worker, name, choices, busy, onChange, testId }) {
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
// node that is offline, that the worker waits for it.
// The reason of a refused node is in the callout above the table and in the
// title of the chip.
function NodeRelay({ worker, testId }) {
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
function PathChip({ worker }) {
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
