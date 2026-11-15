/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodeLifecycleAction } from '../../utils/nodeFleet'
import { isDown, memoryOf, nodeState } from '../../utils/swarm'
import { formatBytes, timeAgo } from '../nodes/nodeStatus'
import Icon from '../Icon'

function Tile({ label, value, note, level }) {
  return (
    <div className="sw-tile">
      <span className="sw-hero__kick">{label}</span>
      <b data-level={level}>{value}</b>
      <span className="sw-tile__note">{note}</span>
    </div>
  )
}

// One node at a glance, beside the roster (or in a sheet on a narrow screen):
// its memory and CPU, the models it runs, the facts about it, and the actions
// that apply to its state. The full page stays one link away.
export default function NodeInspector({ node, rows, reasons, busy, onApprove, onDrain, onResume, onRemove }) {
  const { t } = useTranslation('swarm')
  const state = nodeState(node)
  const memory = memoryOf(node)
  const down = isDown(node)
  const action = nodeLifecycleAction(node.status)
  const id = encodeURIComponent(node.id)
  const cpu = node.cpu_logical_cores > 0 && Number.isFinite(node.cpu_usage_percent) ? node.cpu_usage_percent : null
  const models = rows.filter(row => !row.state || row.state === 'loaded')
  const memoryPercent = memory ? Math.round(memory.usagePercent) : null
  const labels = Object.entries(node.labels || {})

  return (
    <div className="sw-insp" data-testid="node-inspector">
      <span className="sw-hero__kick">{t('inspector.kicker')}</span>
      <h2 className="sw-insp__name dk-mono">{node.name}</h2>
      <div className="sw-insp__meta dk-mono">{[node.address, node.node_type || 'backend'].filter(Boolean).join(' · ')}</div>
      <div className="sw-insp__pills">
        <span className="sw-pill" data-level={state.level}>{t(`state.${state.key}`)}</span>
        {(reasons || []).filter(reason => reason.startsWith('low')).map(reason => (
          <span key={reason} className="sw-pill" data-level="warn">{t(`attention.${reason}`)}</span>
        ))}
      </div>

      <div className="sw-tiles">
        {memory
          ? (
            <Tile
              label={memory.kind === 'gpu' ? t('inspector.vram') : t('inspector.ram')}
              value={`${memoryPercent}%`}
              note={`${formatBytes(memory.used)} / ${formatBytes(memory.total)}`}
              level={memoryPercent >= 90 ? 'error' : memoryPercent >= 75 ? 'warn' : undefined}
            />
          )
          : <Tile label={t('inspector.vram')} value="—" note={t('table.noReading')} />}
        {cpu !== null
          ? <Tile label={t('inspector.cpu')} value={`${Math.round(cpu)}%`} note={t('inspector.cores', { count: Math.round((node.cpu_logical_cores * cpu) / 100) })} />
          : <Tile label={t('inspector.cpu')} value="—" note={t('table.noReading')} />}
      </div>

      {node.node_type !== 'agent' && (
        <section className="sw-insp__sec" aria-label={t('inspector.models')}>
          <h3 className="sw-hero__kick">{t('inspector.models')}</h3>
          {models.length === 0 && <p className="sw-insp__empty">{t('inspector.noModels')}</p>}
          <ul className="sw-kv-list">
            {models.map(row => (
              <li key={`${row.model_name}#${row.replica_index ?? 0}`} className="sw-kv">
                <span className="sw-kv__k">{row.model_name}</span>
                <span className="sw-kv__v dk-mono">{t('inspector.inFlight', { count: row.in_flight || 0 })}</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="sw-insp__sec" aria-label={t('inspector.details')}>
        <h3 className="sw-hero__kick">{t('inspector.details')}</h3>
        <dl className="sw-kv-list">
          {node.gpu_vendor && <div className="sw-kv"><dt className="sw-kv__k">{t('inspector.gpu')}</dt><dd className="sw-kv__v">{node.gpu_vendor}</dd></div>}
          <div className="sw-kv"><dt className="sw-kv__k">{t('inspector.version')}</dt><dd className="sw-kv__v dk-mono">{node.version || '—'}</dd></div>
          <div className="sw-kv"><dt className="sw-kv__k">{t('inspector.heartbeat')}</dt><dd className="sw-kv__v" data-late={down ? '' : undefined}>{node.last_heartbeat ? timeAgo(node.last_heartbeat) : '—'}</dd></div>
          {labels.length > 0 && (
            <div className="sw-kv">
              <dt className="sw-kv__k">{t('inspector.labels')}</dt>
              <dd className="sw-kv__v sw-insp__labels">{labels.map(([k, v]) => <span key={k} className="dk-chip dk-chip--sm dk-mono">{k}={v}</span>)}</dd>
            </div>
          )}
        </dl>
      </section>

      <div className="sw-insp__acts">
        {action === 'approve' && <button type="button" className="dk-btn dk-btn--primary" disabled={busy} onClick={onApprove}>{t('actions.approve')}</button>}
        {action === 'drain' && <button type="button" className="dk-btn dk-btn--secondary" disabled={busy} onClick={onDrain}><Icon name="pause" /> {t('actions.drain')}</button>}
        {action === 'resume' && <button type="button" className="dk-btn dk-btn--secondary" disabled={busy} onClick={onResume}><Icon name="play" /> {t('actions.resume')}</button>}
        <Link className="dk-btn dk-btn--secondary" to={`/app/nodes/${id}?tab=logs`}><Icon name="terminal" /> {t('inspector.logs')}</Link>
        <button type="button" className="dk-btn dk-btn--ghost sw-remove sw-insp__remove" disabled={busy} onClick={onRemove}>{t('actions.remove')}</button>
      </div>
      <Link className="dk-link sw-insp__open" to={`/app/nodes/${id}`}>{t('inspector.open')}</Link>
    </div>
  )
}
