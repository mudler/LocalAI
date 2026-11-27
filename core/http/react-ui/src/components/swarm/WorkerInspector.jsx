/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodeLifecycleAction } from '../../utils/nodeFleet'
import { isDown, memoryOf, nodeState, shortPeer } from '../../utils/swarm'
import { nodeConnections, splitConnections } from '../../utils/workerRoster'
import { weightsInForce } from '../../utils/connectionWeights'
import { formatBytes, timeAgo } from '../nodes/nodeStatus'
import { Button, StatusChip } from '../ui'
import Icon from '../Icon'
import ConnectionChips from './ConnectionChips'
import SplitServerSwitch from './SplitServerSwitch'
import { NodePicker, NodeRelay, PathChip } from './SplitParts'
import { limitsRelay, slowAction, stateOf } from './splitWorkersModel'

const TONE = { ok: 'ok', warn: 'warn', error: 'error', idle: 'idle' }

function Tile({ label, value, note, level }) {
  return (
    <div className="wk-tile">
      <span className="wk-kick">{label}</span>
      <b data-level={level}>{value}</b>
      <span className="wk-tile__note">{note}</span>
    </div>
  )
}

// One route of the worker with its own health: the weight it carries in the
// preference, the round trip or the path it takes, and why it fails when it
// does.
function RouteHealth({ connection, weight, node }) {
  const { t } = useTranslation('cluster')
  const bits = []
  if (weight !== undefined) bits.push(t('inspector.weight', { weight }))
  if (connection.kind === 'p2p' && connection.pathKey) bits.push(t(`conn.path.${connection.pathKey}`, { relay: connection.relay || '' }))
  if (connection.rtt) bits.push(t('conn.rtt', { ms: connection.rtt }))
  if (connection.kind !== 'p2p' && connection.state === 'live' && node?.last_heartbeat) bits.push(t('inspector.heartbeat', { ago: timeAgo(node.last_heartbeat) }))
  if (connection.state !== 'live' && connection.reason) bits.push(connection.reason)
  const tone = connection.state === 'down' && connection.tone === 'error' ? 'warn' : connection.tone
  return (
    <li className="wk-route" data-kind={connection.kind} data-state={connection.state} data-testid="route-health">
      <div className="wk-route__head">
        <b>{t(`conn.name.${connection.kind}`)}</b>
        <StatusChip tone={tone} dot>{t(`conn.state.${connection.state}`)}</StatusChip>
        {connection.limited && <StatusChip tone="warn" title={t('conn.limitedHint')}>{t('conn.limited')}</StatusChip>}
      </div>
      {bits.length > 0 && <p className="wk-route__line">{bits.join(' · ')}</p>}
    </li>
  )
}

// One worker at a glance, beside the roster (or in a sheet on a narrow screen):
// its memory and CPU, the health of each connection it holds, the models it
// runs, the facts about it, and the actions that apply to its state. The full
// page stays one link away.
export default function WorkerInspector({
  node, nodes, rows, reasons, busy, weights, updates, onApprove, onDrain, onResume, onRemove, onUpdate, onChanged, addToast,
}) {
  const { t } = useTranslation('swarm')
  const { t: tc } = useTranslation('cluster')
  const state = nodeState(node)
  const memory = memoryOf(node)
  const down = isDown(node)
  const action = nodeLifecycleAction(node.status)
  const id = encodeURIComponent(node.id)
  const cpu = node.cpu_logical_cores > 0 && Number.isFinite(node.cpu_usage_percent) ? node.cpu_usage_percent : null
  const models = rows.filter(row => !row.state || row.state === 'loaded')
  const memoryPercent = memory ? Math.round(memory.usagePercent) : null
  const labels = Object.entries(node.labels || {})
  const connections = nodeConnections(node)
  const inForce = weightsInForce(weights)

  return (
    <div className="wk-insp" data-testid="node-inspector">
      <span className="wk-kick">{tc('inspector.kicker')}</span>
      <h2 className="wk-insp__name dk-mono">{node.name}</h2>
      <div className="wk-insp__meta dk-mono">{[node.address, node.node_type || 'backend'].filter(Boolean).join(' · ')}</div>
      <div className="wk-insp__pills">
        <StatusChip tone={TONE[state.level]} dot>{t(`state.${state.key}`)}</StatusChip>
        {node.version && <StatusChip tone="idle" dot>{node.version}</StatusChip>}
        {node.split_server && <StatusChip tone="accent" testId="split-server-pill">{t('splitServer.label')}</StatusChip>}
        {(reasons || []).filter(reason => reason.startsWith('low')).map(reason => (
          <StatusChip key={reason} tone="warn">{t(`attention.${reason}`)}</StatusChip>
        ))}
      </div>

      <div className="wk-tiles">
        {memory
          ? (
            <Tile
              label={memory.kind === 'gpu' ? t('inspector.vram') : t('inspector.ram')} value={`${memoryPercent}%`}
              note={`${formatBytes(memory.used)} / ${formatBytes(memory.total)}`} level={memoryPercent >= 90 ? 'error' : memoryPercent >= 75 ? 'warn' : undefined}
            />
          )
          : <Tile label={t('inspector.vram')} value="—" note={t('table.noReading')} />}
        {cpu !== null
          ? <Tile label={t('inspector.cpu')} value={`${Math.round(cpu)}%`} note={t('inspector.cores', { count: Math.round((node.cpu_logical_cores * cpu) / 100) })} />
          : <Tile label={t('inspector.cpu')} value="—" note={t('table.noReading')} />}
      </div>

      <section className="wk-insp__sec" aria-label={tc('inspector.connections')} data-testid="inspector-connections">
        <h3 className="wk-kick">{tc('inspector.connections')}</h3>
        {connections.length === 0
          ? <p className="wk-insp__empty">{tc('inspector.noConnections')}</p>
          : <ul className="wk-routes">{connections.map(connection => <RouteHealth key={connection.key} connection={connection} node={node} weight={inForce[connection.kind]} />)}</ul>}
        {node.peer_id && <p className="wk-route__line dk-mono" title={node.peer_id}>{t('inspector.peer')}: {shortPeer(node.peer_id)}</p>}
      </section>

      {node.node_type !== 'agent' && (
        <section className="wk-insp__sec" aria-label={t('inspector.models')}>
          <h3 className="wk-kick">{t('inspector.models')}</h3>
          {models.length === 0 && <p className="wk-insp__empty">{t('inspector.noModels')}</p>}
          <ul className="wk-kv-list">
            {models.map(row => (
              <li key={`${row.model_name}#${row.replica_index ?? 0}`} className="wk-kv">
                <span className="wk-kv__k">{row.model_name}</span>
                <span className="wk-kv__v dk-mono">{t('inspector.inFlight', { count: row.in_flight || 0 })}</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="wk-insp__sec" aria-label={t('inspector.details')}>
        <h3 className="wk-kick">{t('inspector.details')}</h3>
        <dl className="wk-kv-list">
          {node.gpu_vendor && <div className="wk-kv"><dt className="wk-kv__k">{t('inspector.gpu')}</dt><dd className="wk-kv__v">{node.gpu_vendor}</dd></div>}
          <div className="wk-kv"><dt className="wk-kv__k">{t('inspector.version')}</dt><dd className="wk-kv__v dk-mono">{node.version || '—'}</dd></div>
          {node.status === 'draining' && <div className="wk-kv" data-testid="inspector-leaving"><dt className="wk-kv__k">{t('inspector.leaving')}</dt><dd className="wk-kv__v dk-mono">{node.leaving_replicas || 0}</dd></div>}
          {node.status === 'draining' && node.drain_deadline && <div className="wk-kv"><dt className="wk-kv__k">{t('inspector.drainDeadline')}</dt><dd className="wk-kv__v">{new Date(node.drain_deadline).toLocaleTimeString()}</dd></div>}
          <div className="wk-kv"><dt className="wk-kv__k">{t('inspector.heartbeat')}</dt><dd className="wk-kv__v" data-late={down ? '' : undefined}>{node.last_heartbeat ? timeAgo(node.last_heartbeat) : '—'}</dd></div>
        </dl>
      </section>

      {labels.length > 0 && (
        <section className="wk-insp__sec" aria-label={t('inspector.labels')}>
          <h3 className="wk-kick">{t('inspector.labels')}</h3>
          <div className="wk-insp__labels">{labels.map(([k, v]) => <span key={k} className="dk-chip dk-chip--sm dk-mono">{k}={v}</span>)}</div>
        </section>
      )}

      <div className="wk-insp__acts">
        {action === 'approve' && <Button variant="primary" disabled={busy} onClick={onApprove}>{t('actions.approve')}</Button>}
        {action === 'drain' && <Button icon="pause" disabled={busy} onClick={onDrain}>{t('actions.drain')}</Button>}
        {action === 'resume' && <Button icon="play" disabled={busy} onClick={onResume}>{t('actions.resume')}</Button>}
        {updates > 0 && <Button icon="arrow-up" disabled={busy} onClick={onUpdate} data-testid="inspector-update">{tc('inspector.update')}</Button>}
        <Button icon="terminal" to={`/app/nodes/${id}?tab=logs`}>{t('inspector.logs')}</Button>
        <SplitServerSwitch node={node} nodes={nodes} onChanged={onChanged} addToast={addToast} />
        <Button variant="danger-ghost" className="wk-insp__remove" disabled={busy} onClick={onRemove}>{t('actions.remove')}</Button>
      </div>
      <Link className="dk-link wk-insp__open" to={`/app/nodes/${id}`}>{t('inspector.open')}</Link>
    </div>
  )
}

// A model-split worker beside the roster: how the server reaches it, the node
// it is attached to, and what to do about a slow path. It has no backends and
// no placement rules, so none of the node actions apply.
export function SplitInspector({ worker, m }) {
  const { t } = useTranslation('swarm')
  const { t: tc } = useTranslation('cluster')
  const name = m.nameOf(worker)
  const action = slowAction(worker)
  const connections = splitConnections(worker)
  const busy = m.busy === worker.id
  const { key: state, tone: stateTone } = stateOf(worker)
  return (
    <div className="wk-insp" data-testid="split-inspector">
      <span className="wk-kick">{tc('inspector.splitKicker')}</span>
      <h2 className="wk-insp__name dk-mono">{name}</h2>
      <div className="wk-insp__meta dk-mono">{[t(`split.engine.${worker.engine === 'mlx' ? 'mlx' : 'llama'}`), worker.memory_bytes > 0 ? formatBytes(worker.memory_bytes) : null].filter(Boolean).join(' · ')}</div>
      <div className="wk-insp__pills">
        <StatusChip tone={stateTone} dot>{t(`split.state.${state}`)}</StatusChip>
        <StatusChip tone="accent">{tc('roster.splitMark')}</StatusChip>
      </div>
      <p className="wk-insp__empty">{tc('inspector.splitNote')}</p>

      <section className="wk-insp__sec" aria-label={tc('inspector.connections')}>
        <h3 className="wk-kick">{tc('inspector.connections')}</h3>
        <ConnectionChips connections={connections} />
        <div className="wk-insp__path"><PathChip worker={worker} /></div>
        {worker.peer_id && <p className="wk-route__line dk-mono" title={worker.peer_id}>{t('inspector.peer')}: {shortPeer(worker.peer_id)}</p>}
        {worker.error && <p className="wk-route__line">{worker.error}</p>}
      </section>

      {m.split.distributed && (
        <section className="wk-insp__sec" aria-label={t('split.col.node')}>
          <h3 className="wk-kick">{t('split.col.node')}</h3>
          <NodePicker worker={worker} name={name} choices={m.choices} busy={busy} onChange={m.attach} testId={`split-node-${worker.id}`} />
          <NodeRelay worker={worker} testId={`split-relay-${worker.id}`} />
          {worker.node_note && !worker.node_id && <p className="wk-route__line" data-testid={`split-note-${worker.id}`}>{worker.node_note}</p>}
        </section>
      )}

      <div className="wk-insp__acts">
        {action === 'useAnyway' && <Button variant="primary" busy={busy} onClick={() => m.setSlow(worker, true)} data-testid={`split-use-${worker.id}`}>{t('split.useAnyway')}</Button>}
        {action === 'stopUsing' && <Button busy={busy} onClick={() => m.setSlow(worker, false)} data-testid={`split-stop-${worker.id}`}>{t('split.stopUsing')}</Button>}
        {limitsRelay(worker) && <Button icon="sliders" onClick={() => m.openLimits(limitsRelay(worker))} data-testid={`split-limits-${worker.id}`}>{t('split.relayLimits')}</Button>}
        <Button variant="danger-ghost" className="wk-insp__remove" onClick={() => m.askRevoke(worker)} data-testid={`split-revoke-${worker.id}`}>{t('split.revoke')}</Button>
      </div>
    </div>
  )
}
