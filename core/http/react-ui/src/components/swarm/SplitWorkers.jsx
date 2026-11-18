/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { p2pNetApi, p2pSplitApi } from '../../utils/api'
import { useSplitWorkers } from '../../hooks/useSplitWorkers'
import { formatBytes } from '../../utils/format'
import { shortPeer } from '../../utils/p2p'
import { Button, Callout, StatusChip } from '../ui'
import ConfirmDialog from '../ConfirmDialog'
import { pathOf, slowAction, stateOf, summarize } from './splitWorkersModel'
import './split.css'

// The "Model-split workers" group of the Nodes page: machines that lend their
// memory so one big model runs across machines. They are not cluster nodes:
// no backends and no placement rules. A worker that only a relay reaches is
// slow; the administrator chooses "Use anyway" or leaves it out.
export default function SplitWorkers({ addToast }) {
  const { t } = useTranslation('swarm')
  const split = useSplitWorkers()
  const [busy, setBusy] = useState(null)
  const [revoking, setRevoking] = useState(null)
  const [revokeBusy, setRevokeBusy] = useState(false)

  if (split.status !== 'ready' || split.workers.length === 0) return null
  const sum = summarize(split.workers)

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
        <p className="sw-note sw-note--quiet">{t('split.note')}</p>
      </div>
      {sum.slow.length > 0 && (
        <Callout title={t('split.slowTitle')} testId="split-slow-callout">
          {t('split.slowBody', { names: sum.slow.map(w => w.label || shortPeer(w.peer_id)).join(', ') })}
        </Callout>
      )}
      {sum.limited.length > 0 && (
        <Callout tone="warn" title={t('split.limitedTitle')} testId="split-limited-callout">{t('split.limitedBody')}</Callout>
      )}
      <div className="dk-card sw-roster">
        <div className="dk-table-wrap">
          <table className="dk-table sw-split-table">
            <caption className="dk-sr-only">{t('split.title')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('split.col.worker')}</th>
                <th scope="col" className="dk-hide-phone">{t('split.col.engine')}</th>
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
                      {worker.error && <span className="sw-hint">{worker.error}</span>}
                    </td>
                    <td className="dk-hide-phone">{t(`split.engine.${worker.engine === 'mlx' ? 'mlx' : 'llama'}`)}</td>
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
        <span className="sw-note sw-note--quiet">{t('split.memoryInUse', { memory: formatBytes(split.memory) })}</span>
        <Button icon="plus" to="/app/nodes/add?join=shard">{t('split.addWorker')}</Button>
      </div>
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

// How the server reaches a worker now, with the round trip.
function PathChip({ worker }) {
  const { t } = useTranslation('swarm')
  const path = pathOf(worker)
  if (path.key === 'none') return <span className="sw-unknown">-</span>
  return (
    <span className="sw-split-path">
      <StatusChip tone={worker.path?.kind === 'relayed' ? 'warn' : 'ok'} dot>{t(`split.path.${path.key}`, { relay: path.relay })}</StatusChip>
      {path.rtt && <span className="sw-hint">{t('split.path.rtt', { ms: path.rtt })}</span>}
    </span>
  )
}
