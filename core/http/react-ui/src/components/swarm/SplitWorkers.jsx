/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { useSplitManagement } from '../../hooks/useSplitManagement'
import { formatBytes } from '../../utils/format'
import { Button, StatusChip } from '../ui'
import { NodePicker, NodeRelay, PathChip, SplitCallout, SplitDialogs } from './SplitParts'
import { limitsRelay, slowAction, stateOf } from './splitWorkersModel'
import { shortPeer } from '../../utils/p2p'
import './split.css'

// The "Model-split workers" group of the Nodes page: machines that lend their
// memory so one big model runs across machines. They are not cluster nodes:
// no backends and no placement rules. A worker that only a relay reaches is
// slow; the administrator chooses "Use anyway" or leaves it out. In a cluster
// each worker is attached to the backend node whose models it serves.
export default function SplitWorkers({ addToast }) {
  const { t } = useTranslation('swarm')
  const m = useSplitManagement({ addToast })
  const { split, choices, sum, busy, noServer } = m

  if (split.status !== 'ready' || split.workers.length === 0) return null

  return (
    <section className="sw-section sw-split-workers" aria-labelledby="sw-split-title" data-testid="split-workers">
      <div className="sw-section__head">
        <h2 className="sw-h2" id="sw-split-title">
          {t('split.title')} <span className="sw-count">{t('split.online', { online: sum.online, total: sum.total })}</span>
        </h2>
        <p className="sw-note sw-note--quiet">{t(split.distributed ? 'split.noteCluster' : 'split.note')}</p>
      </div>
      <SplitCallout m={m} />
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
                          <NodePicker worker={worker} name={name} choices={choices} busy={busy === worker.id} onChange={m.attach} testId={`split-node-phone-${worker.id}`} />
                        </span>
                      )}
                      {split.distributed && <span className="sw-split-phone sw-split-phone--chip"><NodeRelay worker={worker} testId={`split-relay-phone-${worker.id}`} /></span>}
                      {worker.node_note && !worker.node_id && <span className="sw-hint" data-testid={`split-note-${worker.id}`}>{worker.node_note}</span>}
                      {worker.error && <span className="sw-hint">{worker.error}</span>}
                    </td>
                    <td className="dk-hide-phone">{t(`split.engine.${worker.engine === 'mlx' ? 'mlx' : 'llama'}`)}</td>
                    {split.distributed && (
                      <td className="dk-hide-phone">
                        <NodePicker worker={worker} name={name} choices={choices} busy={busy === worker.id} onChange={m.attach} testId={`split-node-${worker.id}`} />
                        <NodeRelay worker={worker} testId={`split-relay-${worker.id}`} />
                      </td>
                    )}
                    <td className="dk-hide-phone"><PathChip worker={worker} /></td>
                    <td className="dk-hide-phone">{worker.memory_bytes > 0 ? formatBytes(worker.memory_bytes) : <span className="sw-unknown">-</span>}</td>
                    <td className="dk-hide-phone"><StatusChip tone={state.tone} dot>{t(`split.state.${state.key}`)}</StatusChip></td>
                    <td>
                      <div className="sw-split-actions">
                        {action === 'useAnyway' && (
                          <Button size="sm" variant="primary" busy={busy === worker.id} onClick={() => m.setSlow(worker, true)} data-testid={`split-use-${worker.id}`}>{t('split.useAnyway')}</Button>
                        )}
                        {action === 'stopUsing' && (
                          <Button size="sm" busy={busy === worker.id} onClick={() => m.setSlow(worker, false)} data-testid={`split-stop-${worker.id}`}>{t('split.stopUsing')}</Button>
                        )}
                        {limitsRelay(worker) && (
                          <Button size="sm" variant="ghost" icon="sliders" onClick={() => m.openLimits(limitsRelay(worker))} data-testid={`split-limits-${worker.id}`}>{t('split.relayLimits')}</Button>
                        )}
                        <Button size="sm" variant="danger-ghost" onClick={() => m.askRevoke(worker)} data-testid={`split-revoke-${worker.id}`}>{t('split.revoke')}</Button>
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
          {split.distributed && !noServer && <Button onClick={m.openSetup} data-testid="split-setup-open">{t('splitServer.open')}</Button>}
          <Button icon="plus" to="/app/nodes/add?join=shard">{t('split.addWorker')}</Button>
        </div>
      </div>
      <SplitDialogs m={m} addToast={addToast} />
    </section>
  )
}
